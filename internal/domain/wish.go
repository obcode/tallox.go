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

// Wishes: one person's interest in one course instance.
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
	// ErrSemesterRequired is asking for other people's wishes without saying which semester.
	//
	// Unreachable through the schema, where the argument is not nullable — and here anyway,
	// because the reason it may not happen is a rule and not a signature: the confidentiality
	// filter is built from one semester's publication date, so a query across all of them would
	// apply one semester's answer to the others.
	ErrSemesterRequired = errors.New("dafür fehlt das Semester")
	// ErrWishTrackLimitInvalid is a bound outside 1 to 9. Zero would be "none of them", which is
	// withdrawing the wishes, not bounding them.
	ErrWishTrackLimitInvalid = errors.New(
		"„höchstens“ geht von einem bis neun Zügen")
	// ErrWishTrackLimitWithoutWishes is a bound on cohorts the caller has not wished for. Refused
	// rather than stored, because a bound on nothing would wait unread and qualify wishes entered
	// much later by somebody who has forgotten it.
	ErrWishTrackLimitWithoutWishes = errors.New(
		"„höchstens“ gilt für eingetragene Wünsche — zuerst einen der Züge wählen")
)

// MaxWishTrackLimit mirrors wish_track_limit_in_range.
const MaxWishTrackLimit = 9

// MaxWishNote mirrors wish_note_is_short. Checked here as well so that a paste gets a sentence
// rather than a constraint violation.
const MaxWishNote = 500

// WishPriority is how much somebody wants an instance.
//
// Three fixed levels rather than a rank per person and semester. A rank is more expressive and
// costs a reordering dance on every insert in the middle, a uniqueness constraint whose violation
// needs a generic message on the write path, and a number nobody can read off a list without a
// legend. Ties are the common case: somebody wants four things equally and would rather say so.
type WishPriority string

const (
	// WishFirstChoice is "unbedingt" — what somebody is actually asking for.
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

// Wish is one person's interest in one course instance.
type Wish struct {
	ID uuid.UUID
	// Person is who registered it. Always the person who made the call — there is no way to
	// register interest on somebody's behalf, and no column that would record one.
	Person Person
	// Instance is what is wanted: one module, in one study programme, for one cohort — with the
	// module and the programme carried, because a wish is unreadable without them. "Analysis,
	// IF1B" is the row somebody recognises, and an id is not.
	//
	// The instance and not one of its parts. Which part somebody ends up holding is settled in the
	// assignment, where it is a decision between people rather than something one of them states
	// alone; until then "ich mache die Vorlesung, das Praktikum jemand anderes" is what Note is
	// for. See migration 16.
	Instance CourseInstance
	// SubjectGroupID is the subject group of the module, uuid.Nil for none — what the rule for the
	// details below is scoped by, beside the programme of the instance.
	SubjectGroupID uuid.UUID
	// Priority is how much.
	Priority WishPriority
	// Note is the owner's own words. Read by whoever could read the wish before publication — see
	// policy.CanReadWishDetails — and the empty string for everybody else.
	Note string
	// TrackLimit is the owner's bound on the cohorts of this module in this programme: "of the
	// cohorts I wished for, at most this many". 0 for none. The same on every wish of the group,
	// because it belongs to the group; visible wherever the wish is, like the priority — it is
	// part of what somebody wants, not their own words.
	TrackLimit int
	// CiE is the owner's offer to give the cohort as a course in English. An offer and nothing
	// more: it lists nothing and is no criterion of the assignment (decided 2026-10-29). Visible
	// wherever the wish is, like the priority — it is part of what somebody offers, not their own
	// words.
	CiE bool

	// CreatedAt and UpdatedAt are the zero time where the reader may not see the details: the order
	// in which people registered is the first-come information the rule exists to make irrelevant.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// withoutDetailsFor blanks the note and the timestamps of every wish this actor could not have
// read before publication. The rows are in hand and already filtered, so this is the guard form of
// the rule, applied once on the way out.
func withoutDetailsFor(actor principal.Actor, wishes []Wish) []Wish {
	for i, w := range wishes {
		if !policy.CanReadWishDetails(actor, policy.Wish{
			OwnerID: w.Person.ID, ProgrammeID: w.Instance.Programme.ID, SubjectGroupID: w.SubjectGroupID,
		}) {
			wishes[i].Note = ""
			wishes[i].CreatedAt = time.Time{}
			wishes[i].UpdatedAt = time.Time{}
		}
	}
	return wishes
}

// WishQuery is what a caller is asking for, before the visibility rule is applied to it.
//
// The filter is deliberately not in here: it is derived from the actor and the semester by
// internal/policy and handed to the store separately, so that a caller cannot express a query
// without one.
type WishQuery struct {
	// SemesterCode narrows to one semester. Empty means every one — and only Mine may ask that.
	//
	// The visibility rule is *per semester*: one may be published and the next not, so a filter
	// built without knowing which semester is a filter for the wrong one. WishService.List
	// therefore always sets this. "My own entries, everywhere" is the exception because it does
	// not consult the rule at all — own rows are visible to their owner in every semester and in
	// every phase.
	SemesterCode string
	// Programme narrows to one study programme's instances, by code. Empty means every one.
	Programme string
	// Module narrows to one module across its cohorts, Instance to a single cohort of it.
	// uuid.Nil means no narrowing.
	Module   uuid.UUID
	Instance uuid.UUID
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
	SetWish(ctx context.Context, instanceID, personID uuid.UUID, priority WishPriority,
		note string, cie bool) (*Wish, error)
	// WithdrawWish removes the actor's own. Returns ErrWishNotFound for anything else.
	WithdrawWish(ctx context.Context, id, personID uuid.UUID) error
	// SetWishTrackLimit bounds the person's own wishes for the cohorts of the instance's module in
	// its programme, or lifts the bound with nil. ErrWishTrackLimitWithoutWishes when the person
	// has wished for none of them.
	SetWishTrackLimit(ctx context.Context, instanceID, personID uuid.UUID, maxTracks *int) error
	// WishWriteContext is what the write rule needs about an instance: which semester it is in,
	// and whether its subject group is taking entries. Both at once, because deciding against two
	// states read a moment apart is how a rule ends up describing neither.
	// (nil, nil) when the instance is not there.
	WishWriteContext(ctx context.Context, instanceID uuid.UUID) (WishWriteContext, error)

	DesiredHoursStore
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
	// Never the every-semester query: the rule this applies belongs to one semester, and reaching
	// here without a code would silently apply that semester's publication state to all of them.
	if q.SemesterCode == "" {
		return nil, ErrSemesterRequired
	}

	semester, err := s.semesters.ByCode(ctx, actor, q.SemesterCode)
	if err != nil {
		return nil, err
	}

	wishes, err := s.store.Wishes(ctx, q, policy.WishVisibility(actor, semester.State()))
	if err != nil {
		return nil, err
	}
	return withoutDetailsFor(actor, wishes), nil
}

// Mine is the caller's own wishes — in one semester, or in every semester when the code is empty.
//
// Its own method rather than List with a Person filter, because it is the one question whose
// answer never depends on the confidentiality rule — and because the wish screen asks it on every
// load, so it should not read as a special case of the general query.
//
// Across every semester it goes straight to the own-only filter rather than through List, and
// that is the point of the distinction: List resolves the semester because the rule it applies is
// the semester's — published or not. There is no such state to resolve for "everywhere", and
// inventing one would mean picking a semester's publication date and applying it to the rest.
//
// With a semester it still goes through List, which is what keeps the ±10 year window and the
// code validation on this path too: a screen asking for `20260-WS` should hear about it.
func (s *WishService) Mine(ctx context.Context, actor principal.Actor,
	semesterCode string) ([]Wish, error) {
	if !actor.Authenticated() {
		return nil, ErrNotAuthenticated
	}
	if semesterCode != "" {
		return s.List(ctx, actor, WishQuery{SemesterCode: semesterCode, Person: actor.ID})
	}
	return s.store.Wishes(ctx,
		WishQuery{Person: actor.ID},
		policy.WishFilter{Scope: policy.WishScopeOwn, OwnerID: actor.ID})
}

// Set registers the actor's own interest in an instance, or changes it.
//
// Only ever their own: there is no argument for whose it is. A wish registered on somebody's
// behalf is not an expression of interest but somebody else's opinion about them, and the process
// has a place for that — the assignment.
func (s *WishService) Set(ctx context.Context, actor principal.Actor, instanceID uuid.UUID,
	priority WishPriority, note string, cie bool) (*Wish, error) {
	if !actor.Authenticated() {
		return nil, ErrNotAuthenticated
	}
	if policy.PartnerOnly(actor) {
		return nil, ErrPartnerWritesNothing
	}
	if _, ok := priority.Level(); !ok {
		return nil, ErrWishPriorityInvalid
	}

	note = strings.TrimSpace(note)
	if len(note) > MaxWishNote {
		return nil, ErrWishNoteTooLong
	}

	where, err := s.store.WishWriteContext(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	if !where.Found() {
		return nil, ErrInstanceNotFound
	}
	if !policy.MayWriteInPhase(policy.WriteAreaWishes, where.Semester.Phase, actor) {
		return nil, ErrWishPhaseClosed
	}
	// The programme's release: demand first, then wishes. Before the subject group's window,
	// because a round cannot have collected anything for a demand that is not settled yet.
	if !where.WishesReleased {
		return nil, ErrWishesNotReleased
	}
	// The subject group's own door, which since 2026-08-28 is what actually ends a wish round.
	// Checked after the phase, because "the semester is finished" is the wider statement and the
	// one whose repair is nobody's.
	if !where.WindowOpen {
		return nil, ErrWishWindowClosed
	}
	// Last, because it is the only refusal about the instance rather than about the moment: the
	// phase and the window say "not now", this one says "not here".
	if where.TaughtElsewhere {
		return nil, ErrInstanceTaughtElsewhere
	}
	// Says something about the module description, which the catalogue shows to everybody, and
	// nothing about anybody's interest.
	if cie && !where.EnglishPossible {
		return nil, ErrEnglishNotOffered
	}

	return s.store.SetWish(ctx, instanceID, actor.ID, priority, note, cie)
}

// SetTrackLimit bounds the actor's own wishes for the cohorts of an instance's module in its
// programme — "einen der beiden Züge, egal welcher" — or lifts the bound with nil.
//
// Named through one of the cohorts because that is what a caller holds; the bound belongs to all
// of them. Bound by the phase, the release and the window like a wish, because it is part of one: a bound
// changed after the round has shut is a wish changed after the round has shut.
func (s *WishService) SetTrackLimit(ctx context.Context, actor principal.Actor,
	instanceID uuid.UUID, maxTracks *int) error {
	if !actor.Authenticated() {
		return ErrNotAuthenticated
	}
	if policy.PartnerOnly(actor) {
		return ErrPartnerWritesNothing
	}
	if maxTracks != nil && (*maxTracks < 1 || *maxTracks > MaxWishTrackLimit) {
		return ErrWishTrackLimitInvalid
	}

	where, err := s.store.WishWriteContext(ctx, instanceID)
	if err != nil {
		return err
	}
	if !where.Found() {
		return ErrInstanceNotFound
	}
	if !policy.MayWriteInPhase(policy.WriteAreaWishes, where.Semester.Phase, actor) {
		return ErrWishPhaseClosed
	}
	if !where.WishesReleased {
		return ErrWishesNotReleased
	}
	if !where.WindowOpen {
		return ErrWishWindowClosed
	}

	return s.store.SetWishTrackLimit(ctx, instanceID, actor.ID, maxTracks)
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

	where, err := s.store.WishWriteContext(ctx, existing.Instance.ID)
	if err != nil {
		return err
	}
	if where.Found() && !policy.MayWriteInPhase(policy.WriteAreaWishes, where.Semester.Phase, actor) {
		return ErrWishPhaseClosed
	}
	// Bound by the release and the window like registering one, and for the same reason the phase binds both: a
	// list that may be added to but not corrected is worse than a closed one, so the two
	// directions are one decision about when the round is running.
	if where.Found() && !where.WishesReleased {
		return ErrWishesNotReleased
	}
	if where.Found() && !where.WindowOpen {
		return ErrWishWindowClosed
	}

	return s.store.WithdrawWish(ctx, id, actor.ID)
}
