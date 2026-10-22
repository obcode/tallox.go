package domain

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
)

// Desired teaching hours: how many hours a week somebody wants to teach in one semester.
//
// On the wish service because it is part of the same statement — "this is what I would like to
// teach, and this much of it" — and because the screen it is entered on is the wish screen. Who
// may read it is internal/policy's DesiredHoursVisibility, applied inside the query.

var (
	// ErrPartnerWritesNothing is any write by an account that only follows the planning as a
	// partner faculty. Its own sentence, because the one the write would otherwise meet — "the
	// semester is finished" — is not true.
	ErrPartnerWritesNothing = errors.New("a partner faculty reads the planning and writes nothing")
	// ErrDesiredHoursOutOfRange is a figure below zero or above any teaching position.
	ErrDesiredHoursOutOfRange = errors.New(
		"die gewünschten SWS müssen zwischen 0 und 40 liegen, in halben Stunden")
	// ErrDesiredHoursRange is a minimum above the maximum.
	ErrDesiredHoursRange = errors.New(
		"die minimalen SWS dürfen nicht über den maximalen liegen")
	// ErrDesiredHoursPhaseClosed is stating a figure for a semester that is finished.
	ErrDesiredHoursPhaseClosed = errors.New(
		"dieses Semester ist abgeschlossen — die gewünschten SWS lassen sich nicht mehr ändern")
	// ErrDesiredHoursFrozen is somebody changing their own figure after the freeze. The interface
	// reads policy.DesiredHoursFrozenReason, which says where to go instead.
	ErrDesiredHoursFrozen = errors.New("die Wunsch-SWS dieses Semesters sind eingefroren")
	// ErrDesiredHoursNotFrozen is publishing, or correcting, figures that are still open.
	ErrDesiredHoursNotFrozen = errors.New(
		"die Wunsch-SWS dieses Semesters sind noch nicht eingefroren")
	// ErrDesiredHoursUnknownPerson is a correction for somebody who has no account.
	ErrDesiredHoursUnknownPerson = errors.New("diese Person hat kein Konto in Tallox")
)

// MaxDesiredHours mirrors desired_teaching_hours_in_range.
const MaxDesiredHours = 40

// DesiredHours is one person's range for one semester.
type DesiredHours struct {
	SemesterCode string
	// Person is whose figure it is.
	Person Person
	// MinHours and MaxHours are multiples of one half, MinHours <= MaxHours. A single figure is
	// both.
	MinHours float64
	MaxHours float64
	// Note is the person's own words, or empty for a reader the note's rule does not reach.
	Note string
	// CorrectedBy is the name of whoever in the dean's office last corrected the figure, empty when
	// it stands as the person stated it — or when the note's rule does not reach the reader.
	CorrectedBy string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// DesiredHoursRow is one colleague in the overview: who, in which subject groups, and the range
// where the reader may see it.
type DesiredHoursRow struct {
	Person        Person
	SubjectGroups []SubjectGroupRef
	// Stated is false both for "nobody stated a figure" and "this reader may not see it" — on
	// purpose, see DesiredHoursOverview.
	Stated   bool
	MinHours float64
	MaxHours float64
}

// DesiredHoursStore is what the service needs from persistence. Part of WishStore.
type DesiredHoursStore interface {
	// DesiredHours returns the figures the filter allows, narrowed to one semester and one person
	// where those are set (empty and uuid.Nil mean no narrowing). The note and who corrected it
	// come through noteFilter.
	DesiredHours(ctx context.Context, semesterCode string, person uuid.UUID,
		filter, noteFilter policy.DesiredHoursFilter) ([]DesiredHours, error)
	// SetDesiredHours states or changes a person's figure, creating the semester row if nobody
	// has decided anything about the semester yet. correctedBy is uuid.Nil when the person writes
	// their own. Answers ErrDesiredHoursUnknownPerson for a person without an account.
	SetDesiredHours(ctx context.Context, semesterCode string, personID uuid.UUID,
		minHours, maxHours float64, note string, correctedBy uuid.UUID) error
	// ClearDesiredHours takes a person's figure back. Reports whether there was one.
	ClearDesiredHours(ctx context.Context, semesterCode string, personID uuid.UUID) (bool, error)
	// DesiredHoursOverview lists the faculty's colleagues with the ranges the filter allows.
	DesiredHoursOverview(ctx context.Context, semesterCode string,
		filter policy.DesiredHoursFilter) ([]DesiredHoursRow, error)
}

// DesiredHours lists the figures of a semester this actor may see — or, with an empty code, the
// actor's own across every semester, for the same reason Mine may span them: own entries depend on
// no semester's state.
//
// Asking for somebody this actor may not see answers with nothing, not with a refusal: whether a
// colleague has stated a figure is itself the thing the rule protects.
func (s *WishService) DesiredHours(ctx context.Context, actor principal.Actor,
	semesterCode string, person uuid.UUID) ([]DesiredHours, error) {
	if !actor.Authenticated() {
		return nil, ErrNotAuthenticated
	}
	if semesterCode == "" {
		own := policy.DesiredHoursFilter{Scope: policy.DesiredHoursScopeOwn, OwnerID: actor.ID}
		return s.store.DesiredHours(ctx, "", actor.ID, own, own)
	}
	// Through the semester service, for the ±10 year window and the code check.
	semester, err := s.semesters.ByCode(ctx, actor, semesterCode)
	if err != nil {
		return nil, err
	}
	return s.store.DesiredHours(ctx, semesterCode, person,
		policy.DesiredHoursVisibility(actor, semester.State()), policy.DesiredHoursNoteVisibility(actor))
}

// DesiredHoursOverview lists every colleague with their subject groups and the range this actor
// may see.
//
// The list of people does not depend on the figures, and a figure this actor may not see is
// indistinguishable from none: before publication a colleague sees a table of empty cells and
// their own, which is the honest picture of what they may know.
func (s *WishService) DesiredHoursOverview(ctx context.Context, actor principal.Actor,
	semesterCode string) ([]DesiredHoursRow, error) {
	if !actor.Authenticated() {
		return nil, ErrNotAuthenticated
	}
	if semesterCode == "" {
		return nil, ErrSemesterRequired
	}
	semester, err := s.semesters.ByCode(ctx, actor, semesterCode)
	if err != nil {
		return nil, err
	}
	return s.store.DesiredHoursOverview(ctx, semester.Code,
		policy.DesiredHoursVisibility(actor, semester.State()))
}

// SetDesiredHours states the actor's own range for a semester, or takes it back when both ends
// are nil. One end alone is a single figure. Returns the stored figure, or nil after taking it
// back.
func (s *WishService) SetDesiredHours(ctx context.Context, actor principal.Actor,
	semesterCode string, minHours, maxHours *float64, note string) (*DesiredHours, error) {
	if !actor.Authenticated() {
		return nil, ErrNotAuthenticated
	}
	if semesterCode == "" {
		return nil, ErrSemesterRequired
	}
	if policy.PartnerOnly(actor) {
		return nil, ErrPartnerWritesNothing
	}
	low, high, clear, err := desiredRange(minHours, maxHours)
	if err != nil {
		return nil, err
	}
	note = strings.TrimSpace(note)
	if len(note) > MaxWishNote {
		return nil, ErrWishNoteTooLong
	}

	semester, err := s.semesters.ByCode(ctx, actor, semesterCode)
	if err != nil {
		return nil, err
	}
	state := semester.State()
	// Both directions, as with the wishes: a figure that may be raised but not withdrawn is worse
	// than a frozen one.
	if !policy.MayStateDesiredHours(actor, state) {
		if state.DesiredHoursFrozen() && policy.MayWriteInPhase(policy.WriteAreaDesiredHours,
			state.Phase, actor) {
			return nil, ErrDesiredHoursFrozen
		}
		return nil, ErrDesiredHoursPhaseClosed
	}

	if clear {
		_, err := s.store.ClearDesiredHours(ctx, semester.Code, actor.ID)
		return nil, err
	}
	if err := s.store.SetDesiredHours(ctx, semester.Code, actor.ID, low, high, note,
		uuid.Nil); err != nil {
		return nil, err
	}
	return s.readBack(ctx, semester.Code, actor.ID, policy.DesiredHoursFilter{
		Scope: policy.DesiredHoursScopeOwn, OwnerID: actor.ID})
}

// CorrectDesiredHours is the dean's office changing somebody's figure after the freeze — what was
// agreed with the person in the plausibility check. Both ends nil takes the figure back. Recorded
// as the dean's office's correction until the person writes again, which the freeze prevents.
//
// A nil note keeps the person's own: correcting a figure in a table is not a reason to erase what
// somebody wrote beside it.
func (s *WishService) CorrectDesiredHours(ctx context.Context, actor principal.Actor,
	semesterCode string, person uuid.UUID, minHours, maxHours *float64,
	note *string) (*DesiredHours, error) {
	if !actor.Authenticated() {
		return nil, ErrNotAuthenticated
	}
	if semesterCode == "" {
		return nil, ErrSemesterRequired
	}
	if !policy.MayReadInteractiveOnly(actor) || !policy.RolesOf(actor).Has(policy.RoleDeansOffice) {
		return nil, ErrForbidden
	}
	low, high, clear, err := desiredRange(minHours, maxHours)
	if err != nil {
		return nil, err
	}
	if note != nil {
		trimmed := strings.TrimSpace(*note)
		if len(trimmed) > MaxWishNote {
			return nil, ErrWishNoteTooLong
		}
		note = &trimmed
	}

	semester, err := s.semesters.ByCode(ctx, actor, semesterCode)
	if err != nil {
		return nil, err
	}
	state := semester.State()
	if !policy.MayCorrectDesiredHours(actor, state) {
		if !state.DesiredHoursFrozen() {
			return nil, ErrDesiredHoursNotFrozen
		}
		return nil, ErrDesiredHoursPhaseClosed
	}

	if clear {
		_, err := s.store.ClearDesiredHours(ctx, semester.Code, person)
		return nil, err
	}
	text := ""
	if note != nil {
		text = *note
	} else {
		existing, err := s.store.DesiredHours(ctx, semester.Code, person,
			policy.DesiredHoursNoteVisibility(actor), policy.DesiredHoursNoteVisibility(actor))
		if err != nil {
			return nil, err
		}
		if len(existing) == 1 {
			text = existing[0].Note
		}
	}
	if err := s.store.SetDesiredHours(ctx, semester.Code, person, low, high, text,
		actor.ID); err != nil {
		return nil, err
	}
	return s.readBack(ctx, semester.Code, person, policy.DesiredHoursNoteVisibility(actor))
}

// readBack reads a figure just stored through the filter that renders a list, so the answer is
// assembled by the same query.
func (s *WishService) readBack(ctx context.Context, semesterCode string, person uuid.UUID,
	filter policy.DesiredHoursFilter) (*DesiredHours, error) {
	stored, err := s.store.DesiredHours(ctx, semesterCode, person, filter, filter)
	if err != nil {
		return nil, err
	}
	if len(stored) != 1 {
		return nil, fmt.Errorf("the desired teaching hours just stored read back as %d rows",
			len(stored))
	}
	return &stored[0], nil
}

// desiredRange checks and completes a range: one end alone is a single figure, neither end is
// taking the figure back.
func desiredRange(minHours, maxHours *float64) (low, high float64, clear bool, err error) {
	switch {
	case minHours == nil && maxHours == nil:
		return 0, 0, true, nil
	case minHours == nil:
		low, high = *maxHours, *maxHours
	case maxHours == nil:
		low, high = *minHours, *minHours
	default:
		low, high = *minHours, *maxHours
	}
	if !validDesiredHours(low) || !validDesiredHours(high) {
		return 0, 0, false, ErrDesiredHoursOutOfRange
	}
	if low > high {
		return 0, 0, false, ErrDesiredHoursRange
	}
	return low, high, false, nil
}

// validDesiredHours is the range of the CHECK constraint, in half hours, so that 7.3 gets a
// sentence rather than being rounded into a figure somebody did not type.
func validDesiredHours(h float64) bool {
	return h >= 0 && h <= MaxDesiredHours && !math.IsNaN(h) && math.Mod(h*2, 1) == 0
}
