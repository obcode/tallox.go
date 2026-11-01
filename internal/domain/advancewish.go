package domain

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
)

// Advance wishes ("Vormerkungen"): interest in a module for a semester whose demand is not settled.
//
// Since 2026-10-31 a programme's instances take no wishes until its lead has released the demand.
// Colleagues plan further ahead than that — "Analysis in IF, summer 2028" — and asked to keep that
// plan in the tool: so that the leads see it without a separate page, and so that they hear when
// the module turns out not to be offered. With no claim attached.
//
// An advance wish is the wish it is going to be. It names what an instance is made of except the
// cohort — semester, module, programme — and the release of that programme's demand turns it into
// a wish on each cohort there is (ConvertAdvanceWishes in internal/store), its bound into a track
// limit. Visibility is the wish rule with the named programme in place of the instance's:
// policy.WishVisibility and policy.CanReadWishDetails apply unchanged.

var (
	// ErrAdvanceWishReleased is noting something down for a demand that is already settled — then
	// there are cohorts, or there are none, and the wish itself is what one enters.
	ErrAdvanceWishReleased = errors.New(
		"der Bedarf dieses Studiengangs ist für das Semester schon freigegeben — " +
			"bitte direkt einen Wunsch eintragen")
	// ErrAdvanceWishConverted is changing one that has already become wishes. From then on the
	// wishes are what one changes.
	ErrAdvanceWishConverted = errors.New(
		"diese Vormerkung ist schon als Wunsch übernommen — bitte dort ändern")
	// ErrAdvanceWishNotFound is one that is not there or not the caller's, deliberately the same.
	ErrAdvanceWishNotFound = errors.New("diese Vormerkung ist nicht (mehr) da")
)

// AdvanceWishStatus is where an advance wish stands. Derived when it is read, never stored, so
// that a cohort declared after the release changes it without anybody touching the row.
type AdvanceWishStatus string

const (
	// AdvanceWishPending is waiting for the programme to release its demand.
	AdvanceWishPending AdvanceWishStatus = "PENDING"
	// AdvanceWishConverted has become wishes.
	AdvanceWishConverted AdvanceWishStatus = "CONVERTED"
	// AdvanceWishNotOffered is the programme having released its demand without a cohort of the
	// module: "you are not needed for this one" — the notification it was asked for.
	AdvanceWishNotOffered AdvanceWishStatus = "NOT_OFFERED"
)

// AdvanceWish is one person's interest in a module, in one programme, for one semester.
type AdvanceWish struct {
	ID           uuid.UUID
	Person       Person
	SemesterCode string
	Module       ModuleRef
	Programme    Programme
	// SubjectGroupID is the subject group of the module, uuid.Nil for none — what the rule is
	// scoped by beside the programme.
	SubjectGroupID uuid.UUID
	Priority       WishPriority
	// Note is the owner's own words; empty where the reader may not see the details, as on a wish.
	Note string
	// MaxTracks is "of the cohorts there will be, at most this many", 0 for no bound.
	MaxTracks   int
	ConvertedAt time.Time
	// Released and Offered are facts about the process the status is derived from.
	Released bool
	Offered  bool
	// CreatedAt and UpdatedAt are zero where the details are hidden, as on a wish.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Status is where it stands.
func (a AdvanceWish) Status() AdvanceWishStatus {
	switch {
	case !a.ConvertedAt.IsZero():
		return AdvanceWishConverted
	case a.Released && !a.Offered:
		return AdvanceWishNotOffered
	default:
		return AdvanceWishPending
	}
}

// policyWish is the minimum the visibility rule asks about.
func (a AdvanceWish) policyWish() policy.Wish {
	return policy.Wish{OwnerID: a.Person.ID, ProgrammeID: a.Programme.ID, SubjectGroupID: a.SubjectGroupID}
}

// AdvanceWishQuery is what a caller asks for, before the visibility rule is applied.
type AdvanceWishQuery struct {
	// SemesterCode narrows to one semester. Empty only for the caller's own, for the reason
	// WishQuery gives.
	SemesterCode string
	Programme    string
	Module       uuid.UUID
	SubjectGroup uuid.UUID
	Person       uuid.UUID
}

// NewAdvanceWish is one being noted down or changed.
type NewAdvanceWish struct {
	SemesterCode string
	ModuleID     uuid.UUID
	Programme    string
	Priority     WishPriority
	Note         string
	// MaxTracks is nil for no bound.
	MaxTracks *int
}

// AdvanceWishStore is what the service needs from persistence.
type AdvanceWishStore interface {
	AdvanceWishes(ctx context.Context, q AdvanceWishQuery, filter policy.WishFilter) ([]AdvanceWish, error)
	// AdvanceWishReleased is whether the programme has released its demand for the semester.
	AdvanceWishReleased(ctx context.Context, semesterCode string, programmeID uuid.UUID) (bool, error)
	// SetAdvanceWish stores the person's own and returns its id; ErrAdvanceWishConverted where it
	// has already become wishes, ErrModuleNotFound for a module that is not there.
	SetAdvanceWish(ctx context.Context, personID, programmeID uuid.UUID, a NewAdvanceWish) (uuid.UUID, error)
	// WithdrawAdvanceWish removes the person's own; ErrAdvanceWishNotFound for anything else.
	WithdrawAdvanceWish(ctx context.Context, id, personID uuid.UUID) error
	ProgrammeIDByCode(ctx context.Context, code string) (uuid.UUID, error)
}

// advanceWithoutDetailsFor blanks note and timestamps the way withoutDetailsFor does for wishes.
func advanceWithoutDetailsFor(actor principal.Actor, rows []AdvanceWish) []AdvanceWish {
	for i, a := range rows {
		if !policy.CanReadWishDetails(actor, a.policyWish()) {
			rows[i].Note = ""
			rows[i].CreatedAt = time.Time{}
			rows[i].UpdatedAt = time.Time{}
		}
	}
	return rows
}

// AdvanceWishes returns the advance wishes of a semester this actor may see. Not an error when
// that is none — for the reason WishService.List gives.
func (s *WishService) AdvanceWishes(ctx context.Context, actor principal.Actor,
	q AdvanceWishQuery) ([]AdvanceWish, error) {
	if !actor.Authenticated() {
		return nil, ErrNotAuthenticated
	}
	if q.SemesterCode == "" {
		return nil, ErrSemesterRequired
	}
	semester, err := s.semesters.ByCode(ctx, actor, q.SemesterCode)
	if err != nil {
		return nil, err
	}
	q.SemesterCode = semester.Code
	rows, err := s.store.AdvanceWishes(ctx, q, policy.WishVisibility(actor, semester.State()))
	if err != nil {
		return nil, err
	}
	return advanceWithoutDetailsFor(actor, rows), nil
}

// MyAdvanceWishes is the caller's own, in every semester — the own-only filter, which needs no
// semester's publication state.
func (s *WishService) MyAdvanceWishes(ctx context.Context, actor principal.Actor) ([]AdvanceWish, error) {
	if !actor.Authenticated() {
		return nil, ErrNotAuthenticated
	}
	return s.store.AdvanceWishes(ctx, AdvanceWishQuery{Person: actor.ID},
		policy.WishFilter{Scope: policy.WishScopeOwn, OwnerID: actor.ID})
}

// SetAdvanceWish notes down the actor's own interest in a module for a coming semester, or changes
// it while it is pending.
//
// Open in every phase but FINAL and regardless of any wish round: being early is the point. Refused
// once the programme has released that semester's demand, because then there is a wish to enter
// instead — and an advance wish entered after the release would reach the cohorts past the subject
// group's wish window.
func (s *WishService) SetAdvanceWish(ctx context.Context, actor principal.Actor,
	a NewAdvanceWish) (*AdvanceWish, error) {
	if !actor.Authenticated() {
		return nil, ErrNotAuthenticated
	}
	if policy.PartnerOnly(actor) {
		return nil, ErrPartnerWritesNothing
	}
	if _, ok := a.Priority.Level(); !ok {
		return nil, ErrWishPriorityInvalid
	}
	a.Note = strings.TrimSpace(a.Note)
	if len(a.Note) > MaxWishNote {
		return nil, ErrWishNoteTooLong
	}
	if a.MaxTracks != nil && (*a.MaxTracks < 1 || *a.MaxTracks > MaxWishTrackLimit) {
		return nil, ErrWishTrackLimitInvalid
	}

	semester, err := s.semesters.ByCode(ctx, actor, a.SemesterCode)
	if err != nil {
		return nil, err
	}
	a.SemesterCode = semester.Code
	if !policy.MayWriteInPhase(policy.WriteAreaWishes, semester.Phase, actor) {
		return nil, ErrWishPhaseClosed
	}

	programmeID, err := s.store.ProgrammeIDByCode(ctx, a.Programme)
	if err != nil {
		return nil, err
	}
	if programmeID == uuid.Nil {
		return nil, ErrProgrammeNotFound
	}
	released, err := s.store.AdvanceWishReleased(ctx, semester.Code, programmeID)
	if err != nil {
		return nil, err
	}
	if released {
		return nil, ErrAdvanceWishReleased
	}

	id, err := s.store.SetAdvanceWish(ctx, actor.ID, programmeID, a)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.AdvanceWishes(ctx, AdvanceWishQuery{SemesterCode: semester.Code, Person: actor.ID},
		policy.WishFilter{Scope: policy.WishScopeOwn, OwnerID: actor.ID})
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.ID == id {
			return &row, nil
		}
	}
	return nil, ErrAdvanceWishNotFound
}

// WithdrawAdvanceWish removes one of the actor's own, pending or converted. The wishes a converted
// one became stay: they are withdrawn as wishes.
func (s *WishService) WithdrawAdvanceWish(ctx context.Context, actor principal.Actor, id uuid.UUID) error {
	if !actor.Authenticated() {
		return ErrNotAuthenticated
	}
	if policy.PartnerOnly(actor) {
		return ErrPartnerWritesNothing
	}
	return s.store.WithdrawAdvanceWish(ctx, id, actor.ID)
}
