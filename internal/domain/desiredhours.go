package domain

import (
	"context"
	"errors"
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
	// ErrDesiredHoursOutOfRange is a figure below zero or above any teaching position.
	ErrDesiredHoursOutOfRange = errors.New(
		"die gewünschten SWS müssen zwischen 0 und 40 liegen, in halben Stunden")
	// ErrDesiredHoursPhaseClosed is stating a figure for a semester that is finished.
	ErrDesiredHoursPhaseClosed = errors.New(
		"dieses Semester ist abgeschlossen — die gewünschten SWS lassen sich nicht mehr ändern")
)

// MaxDesiredHours mirrors desired_teaching_hours_in_range.
const MaxDesiredHours = 40

// DesiredHours is one person's figure for one semester.
type DesiredHours struct {
	SemesterCode string
	// Person is whose figure it is — always the person who stated it.
	Person Person
	// Hours is a multiple of one half.
	Hours     float64
	Note      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// DesiredHoursStore is what the service needs from persistence. Part of WishStore.
type DesiredHoursStore interface {
	// DesiredHours returns the figures the filter allows, narrowed to one semester and one person
	// where those are set (empty and uuid.Nil mean no narrowing).
	DesiredHours(ctx context.Context, semesterCode string, person uuid.UUID,
		filter policy.DesiredHoursFilter) ([]DesiredHours, error)
	// SetDesiredHours states or changes the person's own figure, creating the semester row if
	// nobody has decided anything about the semester yet.
	SetDesiredHours(ctx context.Context, semesterCode string, personID uuid.UUID, hours float64,
		note string) (*DesiredHours, error)
	// ClearDesiredHours takes the person's own figure back. Reports whether there was one.
	ClearDesiredHours(ctx context.Context, semesterCode string, personID uuid.UUID) (bool, error)
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
		return s.store.DesiredHours(ctx, "", actor.ID,
			policy.DesiredHoursFilter{Scope: policy.DesiredHoursScopeOwn, OwnerID: actor.ID})
	}
	// Through the semester service, for the ±10 year window and the code check.
	if _, err := s.semesters.ByCode(ctx, actor, semesterCode); err != nil {
		return nil, err
	}
	return s.store.DesiredHours(ctx, semesterCode, person, policy.DesiredHoursVisibility(actor))
}

// SetDesiredHours states the actor's own figure for a semester, or takes it back when hours is
// nil. Returns the stored figure, or nil after taking it back.
func (s *WishService) SetDesiredHours(ctx context.Context, actor principal.Actor,
	semesterCode string, hours *float64, note string) (*DesiredHours, error) {
	if !actor.Authenticated() {
		return nil, ErrNotAuthenticated
	}
	if semesterCode == "" {
		return nil, ErrSemesterRequired
	}
	if hours != nil && !validDesiredHours(*hours) {
		return nil, ErrDesiredHoursOutOfRange
	}
	note = strings.TrimSpace(note)
	if len(note) > MaxWishNote {
		return nil, ErrWishNoteTooLong
	}

	semester, err := s.semesters.ByCode(ctx, actor, semesterCode)
	if err != nil {
		return nil, err
	}
	// Both directions, as with the wishes: a figure that may be raised but not withdrawn is worse
	// than a frozen one.
	if !policy.MayWriteInPhase(policy.WriteAreaDesiredHours, semester.Phase, actor) {
		return nil, ErrDesiredHoursPhaseClosed
	}

	if hours == nil {
		_, err := s.store.ClearDesiredHours(ctx, semester.Code, actor.ID)
		return nil, err
	}
	return s.store.SetDesiredHours(ctx, semester.Code, actor.ID, *hours, note)
}

// validDesiredHours is the range of the CHECK constraint, in half hours, so that 7.3 gets a
// sentence rather than being rounded into a figure somebody did not type.
func validDesiredHours(h float64) bool {
	return h >= 0 && h <= MaxDesiredHours && !math.IsNaN(h) && math.Mod(h*2, 1) == 0
}
