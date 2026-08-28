package domain

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/policy"
)

// The refusals this area produces. German, like every message a person reads.
var (
	// ErrAssignmentNotFound is "there is no such assignment" and "that one is not yours to see",
	// deliberately the same answer. A refusal that distinguished them would answer, for anybody
	// with an id, the question the confidentiality rule exists to refuse.
	ErrAssignmentNotFound = errors.New("diese Zuteilung gibt es nicht")

	// ErrNotYourSubject is the responsibility half of the write rule saying no.
	//
	// One sentence for both axes, because from the refused person's side they are one situation:
	// this instance is somebody else's to fill. Which axis would have let them through is not
	// something they can act on. policy.AssignmentWriteRefusal has the four sentences that name a
	// repair, for a caller that wants to show one.
	ErrNotYourSubject = errors.New("für diese Instanz sind Sie nicht zuständig")

	// ErrAssignmentPhaseClosed is the phase half saying no, and it says "not yet" rather than
	// "no longer" — this is the one write area that closes before its phase instead of after it.
	ErrAssignmentPhaseClosed = errors.New("in dieser Phase kann noch nicht zugeteilt werden")

	// ErrPartAlreadyAssigned is what a caller is told who believed a part was free.
	//
	// Not a leak: it is produced only for somebody who may write here, and anybody who may write
	// here may read the assignment they just collided with. bootstrap.TestPartAlreadyAssigned-
	// TellsNobodySomethingNew asserts that rather than trusting it.
	ErrPartAlreadyAssigned = errors.New("dieser Teil ist inzwischen besetzt, bitte neu laden")

	// ErrPartAssigned is a part refusing to be removed because somebody holds it.
	//
	// The counterpart of ErrPartAlreadyAssigned and not the same refusal: that one is "I tried to
	// fill this and it was taken", this one is "I tried to take this away and it is staffed".
	// Both name the fact and neither names the person.
	//
	// It is thrown on the demand path, and it is the reason migration 17 gives instance_part an
	// incoming RESTRICT that migration 16 had just removed. A lecture that is filled must not
	// disappear because somebody edited the number of laboratory groups — and unlike a wish, an
	// assignment is a decision somebody else took.
	ErrPartAssigned = errors.New(
		"dieser Teil ist besetzt und kann nicht entfernt werden")

	// ErrAssignmentMovedOn is the compare-and-set losing: the assignment being replaced is not
	// the one that is there now.
	//
	// Its own refusal rather than a silent overwrite, because the caller is about to take a
	// decision away from somebody — and the whole reason two roles may write this row is that
	// either of them might legitimately be the one deciding.
	ErrAssignmentMovedOn = errors.New(
		"diese Zuteilung wurde inzwischen geändert, bitte neu laden")

	// ErrAssigneeInvalid is neither or both of the two ways to name somebody.
	ErrAssigneeInvalid = errors.New("bitte genau eine Person angeben")

	// ErrAssigneeNotFound is a person or teacher id that does not resolve. Distinct from
	// ErrAssigneeInvalid because the repair differs: one is a malformed request, the other a
	// stale list of candidates.
	ErrAssigneeNotFound = errors.New("diese Person gibt es nicht")

	// ErrAssignmentNoteTooLong mirrors the CHECK. Same bound as the wish note, deliberately: the
	// two are read side by side on the same screen.
	ErrAssignmentNoteTooLong = errors.New("die Notiz ist zu lang")
)

// MaxAssignmentNoteLength mirrors assignment_note_is_short. Validated here as well as in the
// database, for the reason the track regex is: the constraint keeps the table honest, and this
// turns a violation into a sentence instead of a driver error.
const MaxAssignmentNoteLength = 500

// Assignee is the person holding a part, whether or not they hold an account.
//
// Exactly one of the two ids is set. Name and Mail are read from whichever it is, so that a caller
// rendering a row does not have to know which — that is a fact about accounts and not about
// teaching.
type Assignee struct {
	// PersonID is the Tallox account, or uuid.Nil when this is somebody without one.
	PersonID uuid.UUID
	// TeacherID is the examination office's record, or uuid.Nil when the assignment names an
	// account directly.
	//
	// Both being set is impossible — a CHECK refuses it — and the service canonicalises before
	// writing: a teacher who holds an account is stored as the account, so that the same colleague
	// is not two identities in this table.
	TeacherID uuid.UUID
	// Name is what to show: given name, surname, no academic titles, derived from SortName
	// where there is one — see domain.PlainName. It has to be the same spelling either way,
	// because both kinds of assignee stand in one list, under each other.
	//
	// Empty only for a row whose subject has since been emptied of both.
	Name string
	// Mail is the address, empty for the three teachers who carry none.
	Mail string
	// SortName is the examination office's short name where there is one, so that a list sorts the
	// same way whether or not somebody happens to be in the catalogue.
	SortName string
}

// HasAccount reports whether this assignee can sign in — which is also whether the assignment can
// be somebody's "own" for the visibility rule.
func (a Assignee) HasAccount() bool { return a.PersonID != uuid.Nil }

// Assignment is one person holding one part of one course instance.
type Assignment struct {
	ID uuid.UUID
	// Part is what is held: the lecture, the second laboratory group. The assignable unit, and the
	// level below the one a wish points at — see migration 16.
	Part InstancePart
	// Instance is what the part belongs to: one module, in one study programme, for one cohort.
	// Carried in full because an assignment is unreadable without it — "Analysis, IF1B,
	// Praktikum 2" is the row somebody recognises, and an id is not.
	Instance CourseInstance
	// Assignee is who holds it.
	Assignee Assignee
	// Note is what the deciding person wanted recorded: "vertretungsweise", "nur im ersten
	// Halbsemester". Read by whoever may read the assignment.
	Note string
	// AssignedBy is who decided, or uuid.Nil once that person's row is gone.
	//
	// The column a wish deliberately does not have. An assignment is always somebody's decision
	// about somebody else — that is what distinguishes it from a wish — so its provenance belongs
	// on the row.
	AssignedBy uuid.UUID

	CreatedAt time.Time
	UpdatedAt time.Time
}

// AssignmentQuery is what a caller is asking for, before the visibility rule is applied.
//
// The filter is deliberately not in here: it is derived from the actor and the semester by
// internal/policy and handed to the store separately, so that a caller cannot express a query
// without one.
type AssignmentQuery struct {
	// SemesterCode narrows to one semester. Empty means every one — and only Mine may ask that,
	// for the reason WishQuery gives: the rule is per semester, so a filter built without knowing
	// which one is a filter for the wrong one.
	SemesterCode string
	// Programme narrows to one study programme's instances, by code. Empty means every one.
	Programme string
	// Module narrows to one module across its cohorts, Instance to a single cohort of it.
	Module   uuid.UUID
	Instance uuid.UUID
	// Person narrows to one person's assignments. uuid.Nil means everybody the filter allows.
	//
	// Note what this is not: a way around the rule. Asking for somebody else's assignments narrows
	// the same filtered set, so before publication it answers with what was visible anyway.
	//
	// Matches the account only. Somebody without one is reached through their instance, which is
	// the same asymmetry the "own" scope has.
	Person uuid.UUID
}

// PartWriteContext is everything the write rule needs about a part, read in one statement.
//
// Both halves of the rule hang off the instance the part belongs to — the phase from its semester,
// the two responsibility axes from its programme and its module's subject group — so this is read
// once and answers all of them. Reading them separately would be three round trips and three
// chances to decide against a state that has since moved.
type PartWriteContext struct {
	// InstancePartID is the part itself. uuid.Nil means there is no such part.
	InstancePartID uuid.UUID
	// CourseInstanceID is what it belongs to.
	CourseInstanceID uuid.UUID
	// SemesterCode and Semester are the semester and its state, the latter in the form the rules
	// take.
	SemesterCode string
	Semester     policy.SemesterState
	// ProgrammeID is the first responsibility axis: the programme whose demand the instance is.
	ProgrammeID uuid.UUID
	// SubjectGroupID is the second: the subject group of the instance's module, or uuid.Nil while
	// nobody has sorted it. Nil fails closed on this axis and leaves the other one.
	SubjectGroupID uuid.UUID
	// AssignmentID is the assignment currently on this part, or uuid.Nil while it is free. What
	// the compare-and-set compares against.
	AssignmentID uuid.UUID
}

// Found reports whether the part exists.
func (c PartWriteContext) Found() bool { return c.InstancePartID != uuid.Nil }

// AssignmentStore is what the service needs from persistence.
//
// Every read takes a policy.AssignmentFilter, and that is the shape of the whole design: the rule
// travels into the query rather than being applied to its result, so it cannot be forgotten by a
// caller and it cannot come apart from a count — because there is no count that does not go
// through it.
type AssignmentStore interface {
	// Assignments returns the assignments matching the query that the filter allows.
	Assignments(ctx context.Context, q AssignmentQuery, filter policy.AssignmentFilter,
	) ([]Assignment, error)

	// AssignmentByID returns one, or (nil, nil) when it does not exist or the filter hides it.
	// The two are deliberately the same answer.
	AssignmentByID(ctx context.Context, id uuid.UUID, filter policy.AssignmentFilter,
	) (*Assignment, error)

	// PartWriteContext reads what the write rule needs about a part. A part that does not exist
	// comes back with Found() false rather than as an error.
	PartWriteContext(ctx context.Context, partID uuid.UUID) (PartWriteContext, error)

	// AssignmentWriteContext is the same, reached from an assignment rather than from a part.
	AssignmentWriteContext(ctx context.Context, id uuid.UUID) (PartWriteContext, error)

	// FillPart puts somebody on a part that nobody holds. Returns ErrPartAlreadyAssigned if
	// somebody already does — the conditional write, not a check followed by an insert.
	FillPart(ctx context.Context, partID uuid.UUID, who Assignee, note string, by uuid.UUID,
	) (uuid.UUID, error)

	// ReplaceAssignment hands a part to somebody else, but only while `replacing` is still the
	// assignment on it. Returns ErrAssignmentMovedOn otherwise.
	ReplaceAssignment(ctx context.Context, partID, replacing uuid.UUID, who Assignee,
		note string, by uuid.UUID) (uuid.UUID, error)

	// ClearAssignment removes one. Reports whether there was one to remove.
	ClearAssignment(ctx context.Context, id uuid.UUID) (bool, error)

	// AccountOfTeacher returns the person id belonging to a teacher, or uuid.Nil when that
	// teacher has no account. The canonicalisation lookup.
	AccountOfTeacher(ctx context.Context, teacherID uuid.UUID) (uuid.UUID, error)

	// TeacherExists reports whether a teacher id resolves, so that a stale candidate list is
	// refused rather than written.
	TeacherExists(ctx context.Context, teacherID uuid.UUID) (bool, error)

	// PersonExists reports the same for an account.
	PersonExists(ctx context.Context, personID uuid.UUID) (bool, error)
}
