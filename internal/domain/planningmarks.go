package domain

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
)

// The two marks that decide when the planning is open, and the service that switches them.
//
// Decided 2026-08-28. The planning used to open and close on semester.phase, one value for the
// whole faculty, and that was wrong about how the faculty works: the study programmes settle their
// demand at different times, and each subject group runs its own wish round. Both marks are
// therefore switched where the responsibility already lies, by people who need no new role for it.
//
// They look alike and are not:
//
//	DemandCompletion  an announcement. Blocks nothing, can be withdrawn, and goes out of date
//	                  rather than becoming false when somebody adds a late instance.
//	WishWindow        a door. Closed means this subject takes no more entries, and it opens again
//	                  the same afternoon if its lead says so.

var (
	// ErrWishWindowClosed is a subject group's wish round refusing an entry.
	//
	// Distinct from ErrWishPhaseClosed, and the difference is who can repair it: a closed window
	// is one switch held by this subject group's lead, a finished semester is the end of the
	// process. A single refusal would send half the callers to the wrong person.
	ErrWishWindowClosed = errors.New(
		"die Wunschphase dieser Fachgruppe ist derzeit geschlossen")

	// ErrNotYourSubjectGroup is somebody trying to switch a window that is not theirs.
	ErrNotYourSubjectGroup = errors.New("für diese Fachgruppe sind Sie nicht zuständig")
)

// DemandCompletion is one study programme saying its demand for a semester is settled.
type DemandCompletion struct {
	SemesterCode string
	Programme    Programme
	// CompletedAt is when it was last said. Moves on re-announcing, because what a reader wants
	// to know is how fresh the statement is — unlike a publication mark, which keeps its first
	// timestamp because it records something irreversible.
	CompletedAt time.Time
	CompletedBy uuid.UUID
}

// WishWindow is one subject group's wish round, for one semester.
//
// A row exists only where somebody decided something. Absent means open — see migration 18 for
// why this one default is the opposite of every other in the schema.
type WishWindow struct {
	SemesterCode     string
	SubjectGroupID   uuid.UUID
	SubjectGroupCode string
	SubjectGroupName string
	Open             bool
	ChangedAt        time.Time
	ChangedBy        uuid.UUID
}

// WishWriteContext is everything the wish write rule needs about one instance, read at once.
//
// Two halves that used to be one: the semester says whether anything may be written at all, and
// the window says whether this particular subject is taking entries. Reading them separately would
// be two round trips and two chances to decide against a state that has since moved.
type WishWriteContext struct {
	// Semester is the semester of the instance. Zero when there is no such instance.
	Semester Semester
	// SubjectGroupID is the subject group of the instance's module, or uuid.Nil while nobody has
	// sorted it. Carried for the refusal, which names who can open the window.
	SubjectGroupID uuid.UUID
	// WindowOpen is whether this subject is taking entries. True when nobody has said otherwise,
	// and true for a module in no subject group at all.
	WindowOpen bool
}

// Found reports whether the instance exists.
func (c WishWriteContext) Found() bool { return c.Semester.Recorded() }

// PlanningMarkStore is what the service needs from persistence.
type PlanningMarkStore interface {
	// DemandCompletions returns the announcements of one semester.
	DemandCompletions(ctx context.Context, semesterCode string) ([]DemandCompletion, error)
	// AnnounceDemandComplete records or refreshes one, by semester and programme code.
	AnnounceDemandComplete(ctx context.Context, semesterCode, programme string, by uuid.UUID,
	) (*DemandCompletion, error)
	// WithdrawDemandComplete removes one. Reports whether there was one.
	WithdrawDemandComplete(ctx context.Context, semesterCode, programme string) (bool, error)

	// WishWindows returns the subject groups somebody has decided something about, for one
	// semester. Not every group: an absent row is open.
	WishWindows(ctx context.Context, semesterCode string) ([]WishWindow, error)
	// SetWishWindow opens or shuts one.
	SetWishWindow(ctx context.Context, semesterCode string, subjectGroupID uuid.UUID, open bool,
		by uuid.UUID) (*WishWindow, error)

	// ProgrammeIDByCode resolves the code a caller names to the id the policy asks about, or
	// uuid.Nil when the faculty has no such programme.
	//
	// Here rather than through the catalogue service, because it is the only thing this service
	// needs from that direction and a whole reader interface for one lookup would be a seam
	// nobody uses.
	ProgrammeIDByCode(ctx context.Context, code string) (uuid.UUID, error)
}

// SemesterRecorder is the part of the semester service this one needs.
//
// Reading a code, and creating the row when this is the first decision about that semester —
// because switching a mark *is* such a decision, exactly as advancing a phase is. A service that
// could only read would refuse the first person to shut a wish round for next year.
type SemesterRecorder interface {
	ByCode(ctx context.Context, actor principal.Actor, code string) (Semester, error)
	EnsureRecorded(ctx context.Context, actor principal.Actor, code string) (Semester, error)
}

// PlanningMarkService is the business logic of the two marks.
type PlanningMarkService struct {
	store     PlanningMarkStore
	semesters SemesterRecorder
}

// NewPlanningMarkService wires one up.
func NewPlanningMarkService(s PlanningMarkStore, semesters SemesterRecorder) *PlanningMarkService {
	return &PlanningMarkService{store: s, semesters: semesters}
}

// DemandCompletions is which study programmes have settled their demand for a semester.
//
// Readable by anybody with an account, and deliberately so: "IF for SS29 is settled" is what a
// colleague needs in order to know that registering interest is worth the effort. Hiding it would
// produce a tool that refuses nothing and explains nothing.
func (s *PlanningMarkService) DemandCompletions(ctx context.Context, actor principal.Actor,
	semesterCode string) ([]DemandCompletion, error) {
	if !actor.Authenticated() {
		return nil, ErrNotAuthenticated
	}
	semester, err := s.semesters.ByCode(ctx, actor, semesterCode)
	if err != nil {
		return nil, err
	}
	return s.store.DemandCompletions(ctx, semester.Code)
}

// SetDemandComplete announces a study programme's demand as settled, or withdraws the
// announcement.
//
// Not bound by the phase, unlike writing the demand itself. Announcing is a statement about work
// somebody did rather than a change to the plan, and a finished semester whose demand was never
// announced is a fact somebody may still want to record.
func (s *PlanningMarkService) SetDemandComplete(ctx context.Context, actor principal.Actor,
	semesterCode, programme string, complete bool) (*DemandCompletion, error) {
	if !actor.Authenticated() {
		return nil, ErrNotAuthenticated
	}

	semester, err := s.semesters.EnsureRecorded(ctx, actor, semesterCode)
	if err != nil {
		return nil, err
	}

	programmeID, err := s.store.ProgrammeIDByCode(ctx, programme)
	if err != nil {
		return nil, err
	}
	if programmeID == uuid.Nil {
		return nil, ErrProgrammeNotFound
	}
	if !policy.MayAnnounceDemandComplete(actor, programmeID) {
		return nil, ErrNotYourProgramme
	}

	if !complete {
		if _, err := s.store.WithdrawDemandComplete(ctx, semester.Code, programme); err != nil {
			return nil, err
		}
		// Nothing to return: not announced and announced-then-withdrawn are the same state.
		return nil, nil
	}
	return s.store.AnnounceDemandComplete(ctx, semester.Code, programme, actor.ID)
}

// WishWindows is which subject groups have shut their wish round, for one semester.
//
// Readable by anybody with an account, for the reason the announcements are: a lecturer who finds
// a subject refusing entries should be able to see that it is shut rather than conclude the tool
// is broken.
func (s *PlanningMarkService) WishWindows(ctx context.Context, actor principal.Actor,
	semesterCode string) ([]WishWindow, error) {
	if !actor.Authenticated() {
		return nil, ErrNotAuthenticated
	}
	semester, err := s.semesters.ByCode(ctx, actor, semesterCode)
	if err != nil {
		return nil, err
	}
	return s.store.WishWindows(ctx, semester.Code)
}

// SetWishWindow opens or shuts one subject group's wish round.
//
// The lead of that group, or the dean's office. Not bound by the phase either: shutting a window
// in a finished semester changes nothing anybody can act on, and refusing it would be a rule
// nobody asked for.
func (s *PlanningMarkService) SetWishWindow(ctx context.Context, actor principal.Actor,
	semesterCode string, subjectGroupID uuid.UUID, open bool) (*WishWindow, error) {
	if !actor.Authenticated() {
		return nil, ErrNotAuthenticated
	}
	if !policy.MaySetWishWindow(actor, subjectGroupID) {
		return nil, ErrNotYourSubjectGroup
	}

	semester, err := s.semesters.EnsureRecorded(ctx, actor, semesterCode)
	if err != nil {
		return nil, err
	}
	return s.store.SetWishWindow(ctx, semester.Code, subjectGroupID, open, actor.ID)
}
