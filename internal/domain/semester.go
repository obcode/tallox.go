package domain

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
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
	// ErrSemesterOutOfRange: a well-formed code too far from now to record a decision about.
	// There is no "no such semester" any more — every plannable code names one.
	ErrSemesterOutOfRange = errors.New("this semester is too far away to plan")
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
var semesterCodePattern = regexp.MustCompile(`^[0-9]{4}-(SS|WS)$`)

// Semester is a semester as the rest of the system sees it.
//
// It may or may not have a row behind it, and that is the point. A semester exists because the
// calendar does; the row appears when the first decision about it is recorded and holds
// nothing but those decisions. A semester with no row is therefore not "missing" — it is one
// nobody has decided anything about yet, which is precisely DEMAND_PLANNING and unpublished.
//
// Phase is policy.Phase and not a string: this type crosses into the resolvers, and the
// conversion from what the database stores has to happen once, at the edge that read it,
// rather than at every place that compares it.
type Semester struct {
	// ID is the row's key, or uuid.Nil while there is no row. Nothing outside this package and
	// internal/store sees it: the API addresses a semester by its code, which is the name it
	// has in the faculty and the one that survives being written down in a script.
	ID   uuid.UUID
	Code string
	// Phase is where the planning of this semester stands.
	Phase policy.Phase
	// WishesPublishedAt is the moment the confidentiality window closed, or the zero time
	// while it is still open. Same shape as policy.SemesterState, so the two compose without a
	// conversion that could invert the meaning.
	WishesPublishedAt time.Time
	CreatedAt         time.Time
	// UpdatedAt is when the last decision about this semester was recorded, or the zero time
	// while none has been.
	UpdatedAt time.Time
}

// Recorded reports whether anything has been decided about this semester yet.
//
// Equivalently: whether there is a row. The two are the same question because the row exists
// for no other purpose, and saying it this way round keeps callers from reading "no row" as
// "not found".
func (s Semester) Recorded() bool { return s.ID != uuid.Nil }

// State is the semester in the form the visibility rules take.
func (s Semester) State() policy.SemesterState {
	return policy.SemesterState{Phase: s.Phase, WishesPublishedAt: s.WishesPublishedAt}
}

// SemesterStore is the persistence this service needs, and nothing more.
type SemesterStore interface {
	// EnsureSemester returns the row for this code, creating it if this is the first decision
	// anybody has recorded about the semester. Idempotent, and safe when two callers arrive
	// together: the uniqueness of the code is what settles it, in the database, rather than a
	// check in Go that two goroutines both pass.
	EnsureSemester(ctx context.Context, code string) (Semester, error)
	// SemesterByCode returns the row, or a zero Semester when there is none. Not an error: no
	// row is an ordinary and expected state — it means nothing has been decided.
	SemesterByCode(ctx context.Context, code string) (Semester, error)
	// Semesters returns the recorded ones, newest first. The ones nobody has touched are not
	// in the database to be returned; List adds them from the calendar.
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

// SemesterService is the semester workflow: list, look up, advance, publish.
//
// There is no Create. A semester is not something anybody makes — it is a name for a stretch
// of time, and it is "there" the way next March is there. What can be created is a *decision*
// about one, and that is what AdvancePhase and PublishWishes do; the row is the record of it.
type SemesterService struct {
	store SemesterStore
	// now is the only clock in the semester workflow, and it is injected for the reason the
	// other services inject theirs: the window this returns moves through the year, and a test
	// that cannot stand still would have to be written around whichever fortnight it runs in.
	now func() time.Time
}

// NewSemesterService binds the workflow to its storage and its clock.
func NewSemesterService(store SemesterStore, now func() time.Time) *SemesterService {
	if now == nil {
		now = time.Now
	}
	return &SemesterService{store: store, now: now}
}

// List returns the semesters worth showing, newest first.
//
// The window from the calendar, plus every semester anybody has already decided something
// about. Two halves, because either alone is wrong: only the recorded ones would leave a fresh
// installation with an empty page and no way to start, and only the window would hide a plan
// somebody made for five years out — which, since it took a deliberate act to record, is
// exactly the one that must not disappear.
//
// The calendar decides which semesters are *offered* here and nothing else. It never decides a
// phase; that stays a decision somebody makes, which is why it lives in a column.
func (s *SemesterService) List(ctx context.Context, actor principal.Actor) ([]Semester, error) {
	if !policy.MayReadSemesters(actor) {
		return nil, ErrForbidden
	}

	recorded, err := s.store.Semesters(ctx)
	if err != nil {
		return nil, err
	}

	byCode := make(map[string]Semester, len(recorded))
	for _, semester := range recorded {
		byCode[semester.Code] = semester
	}
	for _, code := range SemestersAround(s.now(), windowBack, windowForward) {
		if _, ok := byCode[code]; !ok {
			byCode[code] = untouched(code)
		}
	}

	out := make([]Semester, 0, len(byCode))
	for _, semester := range byCode {
		out = append(out, semester)
	}
	// Newest first, by the code — which is chronological, which is the property the code's
	// format exists for. Sorting here rather than relying on the query's ORDER BY because half
	// of these rows never went past the database.
	sort.Slice(out, func(i, j int) bool { return out[i].Code > out[j].Code })
	return out, nil
}

// ByCode returns one semester, whether or not anything has been decided about it.
//
// A plannable code always answers. "No such semester" is not a state this system has any more:
// asking about 2031-SS is asking about a stretch of time that will happen, and answering that
// it does not exist would be false in the ordinary sense of the word.
func (s *SemesterService) ByCode(ctx context.Context, actor principal.Actor,
	code string,
) (Semester, error) {
	if !policy.MayReadSemesters(actor) {
		return Semester{}, ErrForbidden
	}

	code, err := s.plannable(code)
	if err != nil {
		return Semester{}, err
	}

	found, err := s.store.SemesterByCode(ctx, code)
	if err != nil {
		return Semester{}, err
	}
	if !found.Recorded() {
		return untouched(code), nil
	}
	return found, nil
}

// AdvancePhase moves a semester one step through the process.
//
// Reads the current phase and then writes conditionally on it. The read is what produces a
// useful refusal — "from ASSIGNMENT you cannot reach DEMAND_PLANNING" needs to know where the
// semester is — and the condition on the write is what makes the read safe to have used.
//
// This is one of the two places a row comes into existence, and it comes into existence as a
// side effect rather than as its own permitted act: switching the phase of a semester nobody
// has touched is a decision about it, and recording that decision is what the row is for.
func (s *SemesterService) AdvancePhase(ctx context.Context, actor principal.Actor,
	code string, to policy.Phase,
) (Semester, error) {
	if !policy.MayAdministerSemesters(actor) {
		return Semester{}, ErrForbidden
	}
	if !to.Valid() {
		return Semester{}, ErrPhaseUnknown
	}

	code, err := s.plannable(code)
	if err != nil {
		return Semester{}, err
	}

	current, err := s.store.EnsureSemester(ctx, code)
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

	return s.store.AdvanceSemesterPhase(ctx, current.ID, current.Phase, to)
}

// PublishWishes ends the confidentiality window of a semester.
//
// Idempotent by way of the store, so a second click is not an error and does not move the
// timestamp. There is no counterpart: see policy.MayPublishWishes for why un-publishing is not
// a thing this system can offer.
func (s *SemesterService) PublishWishes(ctx context.Context, actor principal.Actor,
	code string,
) (Semester, error) {
	if !policy.MayPublishWishes(actor) {
		return Semester{}, ErrForbidden
	}

	code, err := s.plannable(code)
	if err != nil {
		return Semester{}, err
	}

	// Ensures the row first, like AdvancePhase, and that is deliberate even though publishing
	// the wishes of a semester in which nobody could have entered any is a strange act. It is
	// still a statement somebody made about that semester, it cannot be walked back, and the
	// timestamp is the record of it. Refusing it here would be a second rule about when
	// publication is allowed, which the phase-independence in the schema deliberately avoids.
	existing, err := s.store.EnsureSemester(ctx, code)
	if err != nil {
		return Semester{}, err
	}
	return s.store.PublishSemesterWishes(ctx, existing.ID)
}

// plannable normalises a code and reports whether a decision about it may be recorded.
//
// Upper-cased and trimmed, because "2026-ws" typed into a form is the same semester and
// refusing it teaches nothing. Not otherwise repaired: the ZPA's own "WS 2026" is refused
// rather than rearranged, because the year in that spelling is only unambiguous if one already
// knows that a winter semester is named after the year it starts in — and a wrong guess there
// records a decision about a semester one year out, looking exactly like one somebody chose.
//
// The range check is the other half. It is not about the calendar being finite; it is about
// there being no delete and no un-publishing, so a mistyped year in a script would otherwise
// leave the faculty planning the year 9999 forever.
func (s *SemesterService) plannable(code string) (string, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if !semesterCodePattern.MatchString(code) {
		return "", ErrSemesterCodeInvalid
	}
	if !IsPlannable(s.now(), code) {
		return "", ErrSemesterOutOfRange
	}
	return code, nil
}

// untouched is a semester nobody has decided anything about: the start of the process, and
// confidential.
//
// Written out here rather than left to the zero value, so that the defaults this system shows
// for an untouched semester and the defaults the schema writes for a fresh row are visibly the
// same thing. If they ever part company, the page and the database would disagree about a
// semester in the moment somebody first clicks on it.
func untouched(code string) Semester {
	return Semester{Code: code, Phase: policy.PhaseDemandPlanning}
}

// ensure returns the row for a semester, creating it if this is the first decision recorded
// about it.
//
// Unexported and without a permission check, for the other services in this package: by the
// time one of them calls this, it has already decided that the caller may record the decision
// it is about to record. Exporting it would be exporting "create a semester", which is the one
// thing this service says nobody does.
func (s *SemesterService) ensure(ctx context.Context, code string) (Semester, error) {
	return s.store.EnsureSemester(ctx, code)
}
