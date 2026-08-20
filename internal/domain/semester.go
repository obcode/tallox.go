package domain

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
)

// The refusals the semester workflow produces.
var (
	// ErrSemesterCodeInvalid: not four digits, a hyphen and SS or WS.
	ErrSemesterCodeInvalid = errors.New(
		"a semester code is four digits, a hyphen and SS or WS, e.g. 2026-WS")
	// ErrSemesterExists: the code is taken. Not confidential — which semesters exist is
	// visible to everybody signed in — so this may be specific.
	ErrSemesterExists = errors.New("this semester already exists")
	// ErrNoSuchSemester: no row with that id or code.
	ErrNoSuchSemester = errors.New("no such semester")
	// ErrPhaseNotAdjacent: the requested phase is not one step away from the current one.
	ErrPhaseNotAdjacent = errors.New("a semester moves one phase at a time")
	// ErrPhaseUnknown: a phase name this build does not know.
	ErrPhaseUnknown = errors.New("unknown phase")
	// ErrPhaseMovedOn: the semester was switched by somebody else between reading and writing.
	ErrPhaseMovedOn = errors.New("the semester is no longer in the phase this change assumed")
	// ErrForbidden: the caller's roles or door do not permit the operation. The Reason from
	// internal/policy is what the caller is told; this is what the code branches on.
	ErrForbidden = errors.New("not permitted")
)

// semesterCode is the same shape the database enforces in semester_code_is_year_and_term.
//
// Both, deliberately. Here so that the caller gets ErrSemesterCodeInvalid and a sentence they
// can act on, and in the database so that an import, a migration or a future admin command
// cannot write a code that breaks the chronological sort the whole system orders by.
//
// The form is the faculty's own: the ZPA says "WS 2026", the exam planning says 2026-WS, and
// this says 2026-WS too. A tool that invents a third spelling of a name everybody already uses
// makes every export and every colleague's script guess which one it got.
var semesterCode = regexp.MustCompile(`^[0-9]{4}-(SS|WS)$`)

// Semester is a semester as the rest of the system sees it.
//
// Phase is policy.Phase and not a string: this type crosses into the resolvers, and the
// conversion from what the database stores has to happen once, at the edge that read it,
// rather than at every place that compares it.
type Semester struct {
	ID   uuid.UUID
	Code string
	// Phase is where the planning of this semester stands.
	Phase policy.Phase
	// WishesPublishedAt is the moment the confidentiality window closed, or the zero time
	// while it is still open. Same shape as policy.SemesterState, so the two compose without a
	// conversion that could invert the meaning.
	WishesPublishedAt time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// State is the semester in the form the visibility rules take.
func (s Semester) State() policy.SemesterState {
	return policy.SemesterState{Phase: s.Phase, WishesPublishedAt: s.WishesPublishedAt}
}

// SemesterStore is the persistence this service needs, and nothing more.
type SemesterStore interface {
	CreateSemester(ctx context.Context, code string) (Semester, error)
	SemesterByID(ctx context.Context, id uuid.UUID) (Semester, error)
	Semesters(ctx context.Context) ([]Semester, error)
	// AdvanceSemesterPhase writes to only if the row is still in from, and returns
	// ErrPhaseMovedOn when it is not. The comparison belongs in the UPDATE rather than in this
	// package: two people from the dean's office switching at the same moment is the situation
	// a phase change happens in, and a read-then-write here would let the second overwrite the
	// first with a phase nobody chose.
	AdvanceSemesterPhase(ctx context.Context, id uuid.UUID, from, to policy.Phase) (Semester, error)
	// PublishSemesterWishes is idempotent and keeps the first timestamp.
	PublishSemesterWishes(ctx context.Context, id uuid.UUID) (Semester, error)
}

// SemesterService is the semester workflow: create, list, advance, publish.
type SemesterService struct {
	store SemesterStore
}

// NewSemesterService binds the workflow to its storage.
func NewSemesterService(store SemesterStore) *SemesterService {
	return &SemesterService{store: store}
}

// List returns every semester, newest first.
func (s *SemesterService) List(ctx context.Context, actor principal.Actor) ([]Semester, error) {
	if !policy.MayReadSemesters(actor) {
		return nil, ErrForbidden
	}
	return s.store.Semesters(ctx)
}

// ByID returns one semester.
func (s *SemesterService) ByID(ctx context.Context, actor principal.Actor,
	id uuid.UUID,
) (Semester, error) {
	if !policy.MayReadSemesters(actor) {
		return Semester{}, ErrForbidden
	}
	return s.store.SemesterByID(ctx, id)
}

// Create adds a semester, at the start of the process.
//
// The code is upper-cased and trimmed before validation, because "2026-ws" typed into a form
// is the same semester and refusing it teaches nothing. It is not otherwise repaired: the
// ZPA's own "WS 2026" is refused rather than rearranged, because the year in that spelling is
// only unambiguous if one already knows that a winter semester is named after the year it
// starts in — and an import that guesses wrong there creates a semester nobody meant, one year
// out, looking exactly like one somebody chose.
func (s *SemesterService) Create(ctx context.Context, actor principal.Actor,
	code string,
) (Semester, error) {
	if !policy.MayAdministerSemesters(actor) {
		return Semester{}, ErrForbidden
	}

	code = strings.ToUpper(strings.TrimSpace(code))
	if !semesterCode.MatchString(code) {
		return Semester{}, ErrSemesterCodeInvalid
	}

	created, err := s.store.CreateSemester(ctx, code)
	if err != nil {
		return Semester{}, err
	}
	return created, nil
}

// AdvancePhase moves a semester one step through the process.
//
// Reads the current phase and then writes conditionally on it. The read is what produces a
// useful refusal — "from ASSIGNMENT you cannot reach DEMAND_PLANNING" needs to know where the
// semester is — and the condition on the write is what makes the read safe to have used.
func (s *SemesterService) AdvancePhase(ctx context.Context, actor principal.Actor,
	id uuid.UUID, to policy.Phase,
) (Semester, error) {
	if !policy.MayAdministerSemesters(actor) {
		return Semester{}, ErrForbidden
	}
	if !to.Valid() {
		return Semester{}, ErrPhaseUnknown
	}

	current, err := s.store.SemesterByID(ctx, id)
	if err != nil {
		return Semester{}, err
	}
	if !current.Phase.Valid() {
		// The row says something this build cannot act on. Not a caller error, and not
		// something to paper over with a default — the most permissive phase is the one a
		// default would land on.
		return Semester{}, fmt.Errorf("%w: %s", ErrPhaseUnknown, current.Phase)
	}
	if !current.Phase.MayMoveTo(to) {
		return Semester{}, fmt.Errorf("%w: %s to %s", ErrPhaseNotAdjacent, current.Phase, to)
	}

	return s.store.AdvanceSemesterPhase(ctx, id, current.Phase, to)
}

// PublishWishes ends the confidentiality window of a semester.
//
// Idempotent by way of the store, so a second click is not an error and does not move the
// timestamp. There is no counterpart: see policy.MayPublishWishes for why un-publishing is not
// a thing this system can offer.
func (s *SemesterService) PublishWishes(ctx context.Context, actor principal.Actor,
	id uuid.UUID,
) (Semester, error) {
	if !policy.MayPublishWishes(actor) {
		return Semester{}, ErrForbidden
	}
	return s.store.PublishSemesterWishes(ctx, id)
}
