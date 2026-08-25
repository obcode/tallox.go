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

// Wishes: one person's interest in one instance part.
//
// The area the confidentiality rule was written for. Nothing in this file decides who may see
// what — internal/policy does, and internal/store applies its filter inside the query — and that
// division is the point: a service that filtered rows it had already read would be a second
// implementation of the rule, and the exports, digests and token paths that do not go through
// this service would not have it.

var (
	// ErrWishPhaseClosed is registering interest in a semester that is finished.
	//
	// The only phase that refuses one. Wishes may be entered and changed for as long as the
	// semester is not closed — through the demand planning, the wish phase and the assignment —
	// because a correction that the tool refuses happens in a mail instead, and then the list the
	// tool holds is the wrong one.
	//
	// Its own sentence rather than the demand's policy.PhaseClosedReason, because the audience
	// and the repair differ: somebody told "the demand can no longer be changed" while trying to
	// register interest goes looking for a demand screen. What this one says is that the semester
	// is over, which is not something the reader can repair at all — and saying so is the point.
	ErrWishPhaseClosed = errors.New(
		"dieses Semester ist abgeschlossen — Wünsche lassen sich nicht mehr ändern")
	// ErrWishNotFound is a wish that is not there — or is not the caller's, which is deliberately
	// the same answer. Whose it is, is the confidential part.
	ErrWishNotFound = errors.New("dieser Wunsch ist nicht (mehr) da")
	// ErrWishNoteTooLong is a note somebody pasted a document into.
	ErrWishNoteTooLong = errors.New("die Notiz zu einem Wunsch ist auf 500 Zeichen begrenzt")
	// ErrWishPriorityInvalid is a priority outside the three levels.
	ErrWishPriorityInvalid = errors.New("diese Priorität gibt es nicht")
)

// MaxWishNote mirrors wish_note_is_short. Checked here as well so that a paste gets a sentence
// rather than a constraint violation.
const MaxWishNote = 500

// WishPriority is how much somebody wants a part.
//
// Three fixed levels rather than a rank per person and semester. A rank is more expressive and
// costs a reordering dance on every insert in the middle, a uniqueness constraint whose violation
// needs a generic message on the write path, and a number nobody can read off a list without a
// legend. Ties are the common case: somebody wants four things equally and would rather say so.
type WishPriority string

const (
	// WishFirstChoice is "unbedingt" — the parts somebody is actually asking for.
	WishFirstChoice WishPriority = "FIRST_CHOICE"
	// WishHappyTo is "gerne". The default, because it is the honest answer to a form somebody is
	// filling in for the first time.
	WishHappyTo WishPriority = "HAPPY_TO"
	// WishIfNeeded is "notfalls" — held to fill a gap, and the level the assignment reads last.
	WishIfNeeded WishPriority = "IF_NEEDED"
)

// AllWishPriorities returns the levels, most wanted first.
func AllWishPriorities() []WishPriority {
	return []WishPriority{WishFirstChoice, WishHappyTo, WishIfNeeded}
}

// wishPriorityLevels maps the levels onto the smallint the schema stores.
//
// Three homes for this list — the CHECK constraint, this map and the GraphQL enum — and they
// cannot import one another. store.TestDatabaseAndDomainAgreeOnWishPriorities compares the first
// two, the way the roles and the phases are kept in step.
var wishPriorityLevels = map[WishPriority]int16{
	WishFirstChoice: 1,
	WishHappyTo:     2,
	WishIfNeeded:    3,
}

// Level is the stored form.
func (p WishPriority) Level() (int16, bool) {
	level, ok := wishPriorityLevels[p]
	return level, ok
}

// WishPriorityFromLevel is the way back. An unknown level reads as HAPPY_TO rather than as an
// error: the constraint makes it impossible, and a row this binary cannot interpret should render
// as the middle of the scale rather than take a screen down.
func WishPriorityFromLevel(level int16) WishPriority {
	for priority, l := range wishPriorityLevels {
		if l == level {
			return priority
		}
	}
	return WishHappyTo
}

// Wish is one person's interest in one instance part.
type Wish struct {
	ID uuid.UUID
	// Person is who registered it. Always the person who made the call — there is no way to
	// register interest on somebody's behalf, and no column that would record one.
	Person Person
	// Part is the assignable unit wanted.
	Part InstancePart
	// Instance is the cohort that part belongs to, with its module and its programme. Carried
	// because a wish is unreadable without it: "Analysis, IF1B, laboratory" is the row somebody
	// recognises, and "part 3f2a…" is not.
	Instance CourseInstance
	// Priority is how much.
	Priority WishPriority
	// Note is the owner's own words, read by whoever may read the wish.
	Note string

	CreatedAt time.Time
	UpdatedAt time.Time
}

// WishQuery is what a caller is asking for, before the visibility rule is applied to it.
//
// The filter is deliberately not in here: it is derived from the actor and the semester by
// internal/policy and handed to the store separately, so that a caller cannot express a query
// without one.
type WishQuery struct {
	// SemesterCode is required. Without it this would be a question about every semester at
	// once, which is not a screen anybody wants.
	SemesterCode string
	// Programme narrows to one study programme's instances, by code. Empty means every one.
	Programme string
	// Module, Part narrow further. uuid.Nil means no narrowing.
	Module uuid.UUID
	Part   uuid.UUID
	// Person narrows to one person's entries. uuid.Nil means everybody the filter allows.
	//
	// Note what this is not: a way around the rule. Asking for somebody else's wishes narrows the
	// same filtered set, so it answers with what was visible anyway — nothing, before publication.
	Person uuid.UUID
}

// WishStore is what the service needs from persistence.
//
// Every read takes a policy.WishFilter, and that is the shape of the whole design: the rule
// travels into the query rather than being applied to its result, so it cannot be forgotten by a
// caller and it cannot come apart from the count — because there is no count that does not go
// through it.
type WishStore interface {
	// Wishes returns the wishes matching the query that the filter allows.
	Wishes(ctx context.Context, q WishQuery, filter policy.WishFilter) ([]Wish, error)
	// WishByID returns one, or (nil, nil) when it does not exist or the filter hides it. The two
	// are deliberately the same answer.
	WishByID(ctx context.Context, id uuid.UUID, filter policy.WishFilter) (*Wish, error)
	// SetWish registers or updates the actor's own interest and returns the row.
	SetWish(ctx context.Context, partID, personID uuid.UUID, priority WishPriority,
		note string) (*Wish, error)
	// WithdrawWish removes the actor's own. Returns ErrWishNotFound for anything else.
	WithdrawWish(ctx context.Context, id, personID uuid.UUID) error
	// SemesterOfPart is which semester a part belongs to, for the phase rule. (nil, nil) when the
	// part is not there.
	SemesterOfPart(ctx context.Context, partID uuid.UUID) (*Semester, error)
}

// SemesterReader is the part of the semester service the wish service needs: turning a code into
// the state the visibility rule reads.
//
// The service and not the store, so that "which semesters may be asked about at all" — the ±10
// year window — is answered in one place. A semester nobody has decided anything about comes back
// as the untouched one rather than as an absence, which is the model the whole area rests on.
type SemesterReader interface {
	ByCode(ctx context.Context, actor principal.Actor, code string) (Semester, error)
}

// WishService is the business logic of the wish phase.
type WishService struct {
	store     WishStore
	semesters SemesterReader
}

// NewWishService wires one up.
func NewWishService(s WishStore, semesters SemesterReader) *WishService {
	return &WishService{store: s, semesters: semesters}
}

// List returns the wishes of a semester that this actor may see.
//
// The refusal that is not here: asking for somebody else's wishes is not an error. It narrows the
// same filtered set and answers with what was visible anyway — which before publication is
// nothing. Making it an error would turn the filter into an oracle, since the difference between
// "refused" and "empty" is exactly the fact being protected.
func (s *WishService) List(ctx context.Context, actor principal.Actor,
	q WishQuery) ([]Wish, error) {
	if !actor.Authenticated() {
		return nil, ErrNotAuthenticated
	}

	// A semester nobody has decided anything about answers as the untouched one — demand
	// planning, wishes unpublished — which is the conservative state and exactly right here: no
	// row means nothing has been published, so nothing of anybody else's is readable.
	semester, err := s.semesters.ByCode(ctx, actor, q.SemesterCode)
	if err != nil {
		return nil, err
	}

	return s.store.Wishes(ctx, q, policy.WishVisibility(actor, semester.State()))
}

// Mine is the caller's own wishes in a semester.
//
// Its own method rather than List with a Person filter, because it is the one question whose
// answer never depends on the confidentiality rule — and because the wish screen asks it on every
// load, so it should not read as a special case of the general query.
func (s *WishService) Mine(ctx context.Context, actor principal.Actor,
	semesterCode string) ([]Wish, error) {
	if !actor.Authenticated() {
		return nil, ErrNotAuthenticated
	}
	return s.List(ctx, actor, WishQuery{SemesterCode: semesterCode, Person: actor.ID})
}

// Set registers the actor's own interest in a part, or changes it.
//
// Only ever their own: there is no argument for whose it is. A wish registered on somebody's
// behalf is not an expression of interest but somebody else's opinion about them, and the process
// has a place for that — the assignment.
func (s *WishService) Set(ctx context.Context, actor principal.Actor, partID uuid.UUID,
	priority WishPriority, note string) (*Wish, error) {
	if !actor.Authenticated() {
		return nil, ErrNotAuthenticated
	}
	if _, ok := priority.Level(); !ok {
		return nil, ErrWishPriorityInvalid
	}

	note = strings.TrimSpace(note)
	if len(note) > MaxWishNote {
		return nil, ErrWishNoteTooLong
	}

	semester, err := s.store.SemesterOfPart(ctx, partID)
	if err != nil {
		return nil, err
	}
	if semester == nil {
		return nil, ErrPartNotFound
	}
	if !policy.MayWriteInPhase(policy.WriteAreaWishes, semester.Phase, actor) {
		return nil, ErrWishPhaseClosed
	}

	return s.store.SetWish(ctx, partID, actor.ID, priority, note)
}

// Withdraw removes the actor's own wish.
//
// Bound by the phase like registering one, and for the same reason: a list that may be added to
// but not corrected is worse than a closed one, and both directions are the same decision about
// when the window is open.
func (s *WishService) Withdraw(ctx context.Context, actor principal.Actor, id uuid.UUID) error {
	if !actor.Authenticated() {
		return ErrNotAuthenticated
	}

	// Read through the filter, so that a wish this actor may not see answers the same way as one
	// that does not exist. Their own is always visible to them, so this never hides a wish they
	// could withdraw.
	existing, err := s.store.WishByID(ctx, id, policy.WishFilter{
		Scope:   policy.WishScopeOwn,
		OwnerID: actor.ID,
	})
	if err != nil {
		return err
	}
	if existing == nil {
		return ErrWishNotFound
	}

	semester, err := s.store.SemesterOfPart(ctx, existing.Part.ID)
	if err != nil {
		return err
	}
	if semester != nil && !policy.MayWriteInPhase(policy.WriteAreaWishes, semester.Phase, actor) {
		return ErrWishPhaseClosed
	}

	return s.store.WithdrawWish(ctx, id, actor.ID)
}
