package domain

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
)

// Confirming one's assignments: "passt so", once per person and semester (2026-10-29).
//
// The faculty asked for a date by which everybody who teaches checks what they have been given,
// before the plan goes to the examination office. The date is a field of the semester, set by the
// dean's office; the confirmation is a row per person with a fingerprint of what they confirmed.
// Its state is derived on read — see migration 20261029160000 for why it is never stored.

// ErrNothingToConfirm is a confirmation asked for by somebody who holds nothing in the semester.
//
// Refused rather than recorded: an empty confirmation would read as "checked" on the day
// somebody is first given something. Not a leak — it is about the caller's own assignments.
var ErrNothingToConfirm = errors.New(
	"in diesem Semester hast Du keine Zuteilungen zu bestätigen")

// AssignmentConfirmationState is where one person's confirmation stands.
type AssignmentConfirmationState string

const (
	// ConfirmationConfirmed is confirmed, and what they hold is still what they confirmed.
	ConfirmationConfirmed AssignmentConfirmationState = "CONFIRMED"
	// ConfirmationOpen is not confirmed in this semester.
	ConfirmationOpen AssignmentConfirmationState = "OPEN"
	// ConfirmationStale is confirmed, but the assignments have changed since.
	ConfirmationStale AssignmentConfirmationState = "STALE"
)

// AssignmentConfirmation is one person's confirmation in one semester.
type AssignmentConfirmation struct {
	SemesterCode string
	PersonID     uuid.UUID
	State        AssignmentConfirmationState
	// ConfirmedAt is when they last confirmed, or the zero time. Set for a stale one too.
	ConfirmedAt time.Time
}

// ConfirmationRecord is what the store answers per person: the last confirmation and whether it
// still matches. The state is made from it here, once.
type ConfirmationRecord struct {
	PersonID    uuid.UUID
	ConfirmedAt time.Time
	// Current is whether the confirmed fingerprint equals the person's current one.
	Current bool
}

// State turns the record into the three-valued state.
func (r ConfirmationRecord) State() AssignmentConfirmationState {
	switch {
	case r.ConfirmedAt.IsZero():
		return ConfirmationOpen
	case r.Current:
		return ConfirmationConfirmed
	default:
		return ConfirmationStale
	}
}

// AssignmentConfirmationStore is what confirming needs from persistence.
type AssignmentConfirmationStore interface {
	// ConfirmAssignments records a confirmation over what the person holds now. Reports false
	// when they hold nothing in the semester, and then records nothing.
	ConfirmAssignments(ctx context.Context, semesterID, personID uuid.UUID) (bool, error)
	// ConfirmationRecords answers for exactly the persons named, in any order. The caller decides
	// whom it may ask about; this does not filter.
	ConfirmationRecords(ctx context.Context, semesterID uuid.UUID, personIDs []uuid.UUID,
	) ([]ConfirmationRecord, error)
}

// MyConfirmation is the caller's own confirmation. Through every door: it is their own timetable.
func (s *AssignmentService) MyConfirmation(ctx context.Context, actor principal.Actor,
	semesterCode string) (AssignmentConfirmation, error) {
	if !actor.Authenticated() {
		return AssignmentConfirmation{}, ErrNotAuthenticated
	}
	semester, err := s.semesters.ByCode(ctx, actor, semesterCode)
	if err != nil {
		return AssignmentConfirmation{}, err
	}
	out, err := s.confirmations(ctx, semester, []uuid.UUID{actor.ID})
	if err != nil {
		return AssignmentConfirmation{}, err
	}
	return out[0], nil
}

// Confirmations is the confirmation of everybody whose assignments this actor may read in the
// semester: one entry per account holding at least one assignment that passes the filter.
//
// The persons are taken from the filtered assignments rather than asked about directly, so this
// can never answer for somebody whose assignments the caller cannot see — before publication a
// confirmation says that somebody holds something, and it must not reach further than the
// assignment does. A plain token does not get here at all (`@fullReach`).
func (s *AssignmentService) Confirmations(ctx context.Context, actor principal.Actor,
	semesterCode string) ([]AssignmentConfirmation, error) {
	visible, err := s.List(ctx, actor, AssignmentQuery{SemesterCode: semesterCode})
	if err != nil {
		return nil, err
	}
	semester, err := s.semesters.ByCode(ctx, actor, semesterCode)
	if err != nil {
		return nil, err
	}

	seen := map[uuid.UUID]bool{}
	var persons []uuid.UUID
	for _, a := range visible {
		if a.Assignee.HasAccount() && !seen[a.Assignee.PersonID] {
			seen[a.Assignee.PersonID] = true
			persons = append(persons, a.Assignee.PersonID)
		}
	}
	if len(persons) == 0 {
		return []AssignmentConfirmation{}, nil
	}
	return s.confirmations(ctx, semester, persons)
}

// ConfirmMine confirms all of the caller's assignments in a semester at once.
//
// Not tied to the due date or to the publication of the assignments; bound only by FINAL, like
// every other write to the assignment.
func (s *AssignmentService) ConfirmMine(ctx context.Context, actor principal.Actor,
	semesterCode string) (AssignmentConfirmation, error) {
	if !actor.Authenticated() {
		return AssignmentConfirmation{}, ErrNotAuthenticated
	}
	semester, err := s.semesters.ByCode(ctx, actor, semesterCode)
	if err != nil {
		return AssignmentConfirmation{}, err
	}
	if !policy.MayConfirmOwnAssignments(actor, semester.State()) {
		return AssignmentConfirmation{}, ErrAssignmentPhaseClosed
	}
	// No row means nothing was ever decided about the semester, so nobody holds anything there.
	if !semester.Recorded() {
		return AssignmentConfirmation{}, ErrNothingToConfirm
	}
	ok, err := s.store.ConfirmAssignments(ctx, semester.ID, actor.ID)
	if err != nil {
		return AssignmentConfirmation{}, err
	}
	if !ok {
		return AssignmentConfirmation{}, ErrNothingToConfirm
	}
	out, err := s.confirmations(ctx, semester, []uuid.UUID{actor.ID})
	if err != nil {
		return AssignmentConfirmation{}, err
	}
	return out[0], nil
}

// confirmations reads the records of the named persons and returns them in that order, with an
// open entry for anybody the store has nothing on.
func (s *AssignmentService) confirmations(ctx context.Context, semester Semester,
	persons []uuid.UUID) ([]AssignmentConfirmation, error) {
	records := map[uuid.UUID]ConfirmationRecord{}
	if semester.Recorded() {
		list, err := s.store.ConfirmationRecords(ctx, semester.ID, persons)
		if err != nil {
			return nil, err
		}
		for _, r := range list {
			records[r.PersonID] = r
		}
	}
	out := make([]AssignmentConfirmation, 0, len(persons))
	for _, id := range persons {
		r := records[id]
		out = append(out, AssignmentConfirmation{
			SemesterCode: semester.Code,
			PersonID:     id,
			State:        r.State(),
			ConfirmedAt:  r.ConfirmedAt,
		})
	}
	return out, nil
}
