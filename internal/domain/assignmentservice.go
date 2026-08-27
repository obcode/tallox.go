package domain

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
)

// AssignmentService is the business logic of the assignment phase.
//
// What it does not decide: who may do any of this. Every permission question goes to
// internal/policy, and the two halves of every answer — the phase and the responsibility — are
// read from one row so that they cannot describe different moments.
//
// What it does own: the shape of a request, the order things are read in, and the one piece of
// domain reasoning that is neither policy nor persistence — canonicalising an assignee, so that a
// colleague who holds an account is never stored as a catalogue entry.
type AssignmentService struct {
	store     AssignmentStore
	semesters SemesterReader
}

// NewAssignmentService wires one up.
func NewAssignmentService(s AssignmentStore, semesters SemesterReader) *AssignmentService {
	return &AssignmentService{store: s, semesters: semesters}
}

// List returns the assignments of a semester that this actor may see.
//
// The refusal that is not here, for the reason WishService.List gives: asking about somebody
// else's assignments is not an error. It narrows the same filtered set and answers with what was
// visible anyway. Making it an error would turn the filter into an oracle, because the difference
// between "refused" and "empty" is exactly the fact being protected.
func (s *AssignmentService) List(ctx context.Context, actor principal.Actor,
	q AssignmentQuery) ([]Assignment, error) {
	if !actor.Authenticated() {
		return nil, ErrNotAuthenticated
	}
	// Never the every-semester query: the rule this applies belongs to one semester, and reaching
	// here without a code would silently apply one semester's publication state to all of them.
	if q.SemesterCode == "" {
		return nil, ErrSemesterRequired
	}

	semester, err := s.semesters.ByCode(ctx, actor, q.SemesterCode)
	if err != nil {
		return nil, err
	}

	return s.store.Assignments(ctx, q, policy.AssignmentVisibility(actor, semester.State()))
}

// Mine is what the caller has been given to teach — in one semester, or in every semester when
// the code is empty.
//
// Its own method rather than List with a Person filter, for the reason WishService.Mine gives:
// this is the one question whose answer never depends on the confidentiality rule. Across every
// semester it goes straight to the own-only filter, because there is no publication state to
// resolve for "everywhere" and inventing one would mean applying one semester's date to the rest.
func (s *AssignmentService) Mine(ctx context.Context, actor principal.Actor,
	semesterCode string) ([]Assignment, error) {
	if !actor.Authenticated() {
		return nil, ErrNotAuthenticated
	}
	if semesterCode != "" {
		return s.List(ctx, actor, AssignmentQuery{SemesterCode: semesterCode, Person: actor.ID})
	}
	return s.store.Assignments(ctx,
		AssignmentQuery{Person: actor.ID},
		policy.AssignmentFilter{Scope: policy.AssignmentReadScopeOwn, AssigneeID: actor.ID})
}

// ByID returns one assignment, or ErrAssignmentNotFound when it does not exist or the rule hides
// it — deliberately the same answer.
func (s *AssignmentService) ByID(ctx context.Context, actor principal.Actor, id uuid.UUID,
) (*Assignment, error) {
	if !actor.Authenticated() {
		return nil, ErrNotAuthenticated
	}

	// The state to filter by comes from the row itself rather than from an argument: a detail
	// view is reached by id, and asking the caller which semester it is in would be asking them
	// for half of the answer.
	where, err := s.store.AssignmentWriteContext(ctx, id)
	if err != nil {
		return nil, err
	}
	if !where.Found() {
		return nil, ErrAssignmentNotFound
	}

	found, err := s.store.AssignmentByID(ctx, id, policy.AssignmentVisibility(actor, where.Semester))
	if err != nil {
		return nil, err
	}
	if found == nil {
		return nil, ErrAssignmentNotFound
	}
	return found, nil
}

// Set puts somebody on a part of an instance.
//
// `replacing` is the assignment the caller was looking at, and leaving it empty means "I believe
// this part is free". That is the compare-and-set, and its default direction is the safe one: a
// call that names nothing can only ever fill a part nobody holds, so a caller who has not seen
// the current state cannot take a decision away from somebody who has.
//
// Two roles may write this row — the lead of the module's subject group and the lead of the
// instance's study programme — which is what makes that more than a formality.
func (s *AssignmentService) Set(ctx context.Context, actor principal.Actor,
	partID uuid.UUID, who Assignee, note string, replacing uuid.UUID) (*Assignment, error) {
	if !actor.Authenticated() {
		return nil, ErrNotAuthenticated
	}

	note = strings.TrimSpace(note)
	if len(note) > MaxAssignmentNoteLength {
		return nil, ErrAssignmentNoteTooLong
	}
	if (who.PersonID == uuid.Nil) == (who.TeacherID == uuid.Nil) {
		return nil, ErrAssigneeInvalid
	}

	where, err := s.store.PartWriteContext(ctx, partID)
	if err != nil {
		return nil, err
	}
	if !where.Found() {
		return nil, ErrPartNotFound
	}
	if err := s.mayWrite(actor, where); err != nil {
		return nil, err
	}

	who, err = s.canonical(ctx, who)
	if err != nil {
		return nil, err
	}

	var id uuid.UUID
	if replacing == uuid.Nil {
		id, err = s.store.FillPart(ctx, partID, who, note, actor.ID)
	} else {
		id, err = s.store.ReplaceAssignment(ctx, partID, replacing, who, note, actor.ID)
	}
	if err != nil {
		return nil, err
	}

	// Re-read through the filter rather than assembling an answer here, the same way every write
	// in internal/store ends: otherwise there would be two opinions about what an assignment looks
	// like, and the one built by the writer is the one nobody tests.
	written, err := s.store.AssignmentByID(ctx, id,
		policy.AssignmentVisibility(actor, where.Semester))
	if err != nil {
		return nil, err
	}
	if written == nil {
		// Unreachable by the rule: writing here required responsibility, and responsibility reads.
		// Kept because "unreachable" is a claim about two functions agreeing, and this is the
		// cheap place to stop being wrong about it.
		return nil, ErrAssignmentNotFound
	}
	return written, nil
}

// Clear gives a part back.
//
// Bound by the same phase and the same responsibility as filling one. A list that may be added to
// but not corrected is worse than a closed one — the same argument the wish phase makes about
// withdrawing.
func (s *AssignmentService) Clear(ctx context.Context, actor principal.Actor, id uuid.UUID) error {
	if !actor.Authenticated() {
		return ErrNotAuthenticated
	}

	where, err := s.store.AssignmentWriteContext(ctx, id)
	if err != nil {
		return err
	}
	if !where.Found() {
		return ErrAssignmentNotFound
	}

	// Visibility before the write rule, and the order is the point. The write rule produces
	// ErrNotYourSubject, which says "this exists and is somebody else's" — for a caller holding an
	// id of a confidential assignment, that is the fact the read rule refuses. So anybody who
	// could not have read it is told it does not exist, and only somebody who could gets the
	// refusal that names a repair.
	visible, err := s.store.AssignmentByID(ctx, id,
		policy.AssignmentVisibility(actor, where.Semester))
	if err != nil {
		return err
	}
	if visible == nil {
		return ErrAssignmentNotFound
	}

	if err := s.mayWrite(actor, where); err != nil {
		return err
	}

	removed, err := s.store.ClearAssignment(ctx, id)
	if err != nil {
		return err
	}
	if !removed {
		return ErrAssignmentNotFound
	}
	return nil
}

// mayWrite asks the policy and then picks which half said no.
//
// Two sentences, because the repairs differ: somebody who is not responsible needs the right
// subject or the right programme, and somebody who is too early needs the phase advanced.
// policy.AssignmentWriteRefusal splits the first of those further, for a caller that wants to
// name the repair.
func (s *AssignmentService) mayWrite(actor principal.Actor, where PartWriteContext) error {
	if policy.MayWriteAssignment(actor, where.SubjectGroupID, where.ProgrammeID,
		where.Semester.Phase) {
		return nil
	}
	if !policy.MayActInSubjectGroup(actor, where.SubjectGroupID) &&
		!policy.MayPlanProgramme(actor, where.ProgrammeID) {
		return ErrNotYourSubject
	}
	return ErrAssignmentPhaseClosed
}

// canonical resolves an assignee to the identity this table should store.
//
// A teacher who holds an account is written as the account. Without this the same colleague would
// appear in this table under two identities depending on which list somebody picked them from,
// and "my assignments" — which matches the account — would find only half of what they teach.
//
// The address is what makes the two rows one person, resolved on every write rather than kept in
// a column: a stored link is only as fresh as the last projection, so somebody admitted this
// morning would be connected to their own teaching tonight.
func (s *AssignmentService) canonical(ctx context.Context, who Assignee) (Assignee, error) {
	if who.PersonID != uuid.Nil {
		exists, err := s.store.PersonExists(ctx, who.PersonID)
		if err != nil {
			return Assignee{}, err
		}
		if !exists {
			return Assignee{}, ErrAssigneeNotFound
		}
		return Assignee{PersonID: who.PersonID}, nil
	}

	exists, err := s.store.TeacherExists(ctx, who.TeacherID)
	if err != nil {
		return Assignee{}, err
	}
	if !exists {
		return Assignee{}, ErrAssigneeNotFound
	}

	account, err := s.store.AccountOfTeacher(ctx, who.TeacherID)
	if err != nil {
		return Assignee{}, err
	}
	if account != uuid.Nil {
		return Assignee{PersonID: account}, nil
	}
	return Assignee{TeacherID: who.TeacherID}, nil
}
