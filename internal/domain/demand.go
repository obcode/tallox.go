package domain

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/policy"
)

// The demand, as the rest of the program sees it.
//
// What gets planned is an instance: one module, offered in one semester, for one study
// programme, to one parallel cohort. What gets assigned later is not the instance but its parts,
// because the faculty's own sentence is "one person holds the lecture, another the laboratory".
//
// The types here carry their module and programme fully rather than as ids. The alternative —
// a field resolver per relation — is a query per instance, and a programme's demand for a
// semester is tens of instances on one screen.

// The refusals of the demand, as sentinels the interface branches on by code.
var (
	// ErrInstanceNotFound is an id that names no course instance.
	ErrInstanceNotFound = errors.New("diese Instanz gibt es nicht")
	// ErrPartNotFound is an id that names no part of an instance.
	ErrPartNotFound = errors.New("diesen Teil gibt es nicht")
	// ErrModuleNotDecomposed is a module there is nothing to make parts from.
	//
	// It used to mean "nobody has stated the split", which was most of the catalogue. Since the
	// split can be proposed from the course type and the catalogue's total, it means the much
	// smaller thing it says: the examination office states no hours at all, so there is no number
	// to divide. Twelve real modules are in that state, and the repair is to enter the split by
	// hand — which is why the sentence names it.
	ErrModuleNotDecomposed = errors.New(
		"für dieses Modul nennt der Modulkatalog keine SWS — bitte die Aufteilung von Hand eintragen")
	// ErrTrackTaken is this exact instance already existing: same semester, module, programme
	// and parallel cohort.
	//
	// Named rather than generic, and that is safe here in a way it will not be for wishes: the
	// demand is not confidential, so saying "IF3A is already declared" reveals nothing that the
	// same person cannot read on the same screen.
	ErrTrackTaken = errors.New("diese Instanz ist für diesen Zug schon angelegt")
	// ErrInstanceInUse is a withdrawal refused because something already hangs off the
	// instance.
	//
	// Deliberately opaque, and this is the first mutation in the system where that matters. The
	// obvious message — "this instance has 3 wishes" — is the confidential fact with the names
	// removed and nothing else: before publication there are no counts, no flags, no sorting by
	// interest. So the refusal says that the instance is in use and stops there, whatever it is
	// that uses it.
	ErrInstanceInUse = errors.New(
		"diese Instanz kann nicht mehr zurückgezogen werden, es hängt bereits etwas daran")
	// ErrTrackInvalid is a parallel cohort that is not a short label.
	ErrTrackInvalid = errors.New("ein Zug ist ein kurzes Kürzel, z. B. A oder B")
	// ErrPartInvalid is a part that does not describe teaching — no hours, too many, or a kind
	// this build does not know.
	ErrPartInvalid = errors.New("dieser Teil ist nicht gültig")
	// ErrTooManyParts is a request that would fill the table.
	ErrTooManyParts = errors.New("so viele Teile kann eine Instanz nicht haben")
	// ErrNotSharedAcrossTracks is asking to undo a sharing that is not there.
	ErrNotSharedAcrossTracks = errors.New("dieser Teil wird nicht für mehrere Züge gehalten")
	// ErrNoSiblingTracks is asking to share a part where there is nobody to share it with.
	ErrNoSiblingTracks = errors.New("für dieses Modul gibt es in diesem Semester nur einen Zug")
	// ErrSameSemester is copying a semester's demand into itself.
	ErrSameSemester = errors.New("das Quell- und das Zielsemester sind dasselbe")
)

// MaxPartsPerInstance bounds one instance.
//
// A guard against a request that would fill a table rather than a rule about teaching: the
// largest thing the faculty runs is a lecture with a handful of laboratory groups, and three
// kinds of eight groups each is already past anything anybody has described.
const MaxPartsPerInstance = 24

// CourseInstance is one module offered in one semester, for one programme, to one cohort.
type CourseInstance struct {
	ID uuid.UUID
	// SemesterCode is the semester in this system's spelling, `2026-WS`.
	SemesterCode string
	// SemesterPhase is where that semester's planning stands. Carried on the instance because
	// every write to it is judged against the phase, and reading the instance is how the rule
	// gets both halves in one go.
	SemesterPhase policy.Phase
	// Module is the catalogue entry this is an offering of, with its split and its offerings
	// attached — the same Module the catalogue serves, so that "is this compulsory here" and
	// "how do its hours divide" are answerable on the demand screen without a second lookup.
	Module Module
	// Programme is whose demand this is. Not necessarily the module's home programme: a module
	// at home in one programme and offered by another is exactly what the import/export figures
	// are about.
	Programme Programme
	// Track is the parallel cohort — the A in IF3A — and empty for a module that runs once.
	Track string
	// ProgrammeSemester is which cohort year this is for, the 3 in IF3A, or nil where nobody
	// has said and the regulations do not either.
	ProgrammeSemester *int
	// Parts are the assignable units this cohort holds itself, in order.
	Parts []InstancePart
	// BorrowedParts are the parts of a sibling cohort that are held for this one as well.
	//
	// Not this instance's rows and never counted in its hours — the point of sharing a lecture
	// is that it happens once and counts once. They are here because a screen that showed this
	// cohort with laboratories and no lecture would look like a planning mistake.
	BorrowedParts []BorrowedPart
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// TeachingHours is what this instance costs the faculty: the sum over the parts it holds.
//
// Borrowed parts are not in it, by construction. A lecture held once for two cohorts is two
// hours of teaching, not four, and the cohort that owns the row is where those two hours are
// counted — summing them in both places is the plausible-looking wrong answer this model is
// arranged to prevent.
//
// A part whose hours nobody has stated yet contributes nothing rather than blocking the sum: an
// instance can be declared before the detail is settled, which is what the demand deadline
// coming before the detail requires.
func (i CourseInstance) TeachingHours() float64 {
	var total float64
	for _, p := range i.Parts {
		if p.TeachingHours != nil {
			total += *p.TeachingHours
		}
	}
	return total
}

// InstancePart is one assignable unit of an instance: a lecture, a laboratory group, a seminar.
type InstancePart struct {
	ID   uuid.UUID
	Kind InstancePartKind
	// Position is the order within the instance.
	Position int
	// TeachingHours is what a LECTURER is credited with for holding this part — not the module's
	// own figure, which is what a student attends. Nil while nobody has stated it.
	TeachingHours *float64
	// SharedAcrossTracks is instance_part.serves_sibling_tracks: this part is held once and
	// serves the other cohorts of the same module too.
	SharedAcrossTracks bool
}

// BorrowedPart is a sibling cohort's shared part, seen from the cohort it is held for.
type BorrowedPart struct {
	Part InstancePart
	// FromTrack is the cohort that owns the row. Empty is possible and means the sibling has no
	// letter, which happens while somebody is in the middle of splitting a single cohort in two.
	FromTrack string
}

// DemandFilter narrows the demand of a semester.
//
// The semester is not optional and has no default. "The demand" without one is a question about
// every semester at once, which is not a screen anybody wants and is a large answer to build by
// accident.
type DemandFilter struct {
	// SemesterCode is the semester in this system's spelling.
	SemesterCode string
	// Programme is a short code, or empty for every programme's demand in that semester — which
	// is what the dean's office looks at.
	Programme string
	// Module keeps only the instances of one module, across cohorts. Zero for every module.
	Module uuid.UUID
}

// NewCourseInstance is a demand about to be declared.
type NewCourseInstance struct {
	SemesterID  uuid.UUID
	ModuleID    uuid.UUID
	ProgrammeID uuid.UUID
	Track       string
	// ProgrammeSemester, or nil to take what the programme's regulations say.
	ProgrammeSemester *int
	// CreatedBy is who declared it. uuid.Nil records nobody.
	CreatedBy uuid.UUID
}

// CopyCounts is what a copy did to the database, and it is the store's whole answer.
//
// The service builds the report around it. Splitting them this way keeps the transaction free of
// anything it would have to look up twice: it holds ids, it counts rows, and the codes and the
// resulting list are assembled outside it by the layer that already has them.
type CopyCounts struct {
	// Created is how many instances the copy declared.
	Created int
	// Skipped is how many were already declared in the target semester and were left exactly as
	// they are. A copy never overwrites work in the semester it copies into.
	Skipped int
	// PartsCreated is how many parts came with them.
	PartsCreated int
}

// CopyReport is what copying a semester's demand did.
//
// Numbers rather than a boolean, and reported even when nothing happened. A copy that silently
// does nothing — because the target already holds the same instances — is indistinguishable from
// a copy that failed, and the person who pressed the button is the one who cannot tell.
type CopyReport struct {
	From      string
	To        string
	Programme Programme
	// Counts is what happened.
	Counts CopyCounts
	// Instances is the demand of the target semester afterwards — the whole list, not only the
	// new rows, because that is what the screen showing it needs.
	Instances []CourseInstance
}

// Planning a whole screen in one act.
//
// The demand of a study programme is read and written as a table — one row per module, a tick, a
// number of cohorts, a number of groups in each — because that is how the faculty has always done
// it. Everything below exists to make that one save a single, reconcilable statement rather than
// forty small mutations that can half-succeed.

// MaxTracksPerModule bounds the cohorts of one module in one semester.
//
// Eight, which is the alphabet the interface offers and four times the largest thing the faculty
// runs. A guard against a number typed into a stepper by holding down a key, not a rule about
// teaching.
const MaxTracksPerModule = 8

// MaxGroupsPerTrack bounds the parallel groups of one cohort.
//
// Twelve, against a real maximum of three. Same purpose as above, and the instance-wide limit
// (MaxPartsPerInstance) still applies on top.
const MaxGroupsPerTrack = 12

// DemandTrack is one cohort of a module, as the table states it: a letter and a number of groups.
type DemandTrack struct {
	// Track is the cohort letter, empty for a module that runs once.
	Track string
	// Groups is how many parallel groups of the practical unit this cohort runs — the
	// laboratory or the exercise. A module that is nothing but a lecture has none, and the
	// figure is then without effect rather than an error.
	Groups int
}

// DemandEntry is one row of the table: this module, in these cohorts.
//
// An entry with no tracks is the row whose tick was taken away, and it means "not offered". That
// is the whole of the difference between "leave it alone" and "withdraw it": a module the caller
// says nothing about at all is untouched.
type DemandEntry struct {
	ModuleID uuid.UUID
	Tracks   []DemandTrack
	// ProgrammeSemester is the cohort year for every cohort of this module, or nil to leave what
	// is there — and, for a new instance, to take what the regulations say.
	ProgrammeSemester *int
}

// DemandChange is one thing a plan did, or would do.
type DemandChange struct {
	ModuleID   uuid.UUID
	ModuleName string
	// Track after the change.
	Track string
	// TrackBefore is set where a cohort was renamed rather than created — IF1 becoming IF1A when
	// a second cohort appears beside it.
	TrackBefore *string
	// GroupsBefore and GroupsAfter are set where the number of groups changed.
	GroupsBefore *int
	GroupsAfter  *int
}

// DemandRefusal is one thing a plan could not do, and it names no reason beyond its code.
type DemandRefusal struct {
	ModuleID   uuid.UUID
	ModuleName string
	Track      string
	// Code is the machine-readable half, e.g. INSTANCE_IN_USE.
	Code string
	// Reason is the sentence to show. It says what happened, never what is in the way — the
	// first thing that will hang off an instance is a confidential wish.
	Reason string
}

// DemandPlan is what a save did, or — with DryRun — what it would do.
type DemandPlan struct {
	// DryRun is true when nothing was written.
	DryRun    bool
	Created   []DemandChange
	Withdrawn []DemandChange
	Changed   []DemandChange
	Refused   []DemandRefusal
	// Instances is the demand afterwards, so that one answer redraws the screen. After a dry run
	// it is what is there now, because that is what is there.
	Instances []CourseInstance
	// TeachingHours is the sum over those instances.
	TeachingHours float64
}

// Empty reports whether a plan would change nothing at all.
//
// What the interface needs in order to skip a confirmation nobody has anything to confirm.
func (p DemandPlan) Empty() bool {
	return len(p.Created) == 0 && len(p.Withdrawn) == 0 && len(p.Changed) == 0
}

// Destructive reports whether a plan would take something away.
//
// The distinction the save hangs on: adding and adjusting happen on one click, withdrawing is
// shown first and confirmed. A tick that is taken away is a statement, and it deserves a sentence
// before it is acted on — the more so from the wish phase onwards, when somebody's entry may be
// behind it.
func (p DemandPlan) Destructive() bool { return len(p.Withdrawn) > 0 }

// DemandStore is the persistence the demand service needs, and nothing more.
type DemandStore interface {
	// CourseInstances lists the demand, with parts, borrowed parts and modules attached.
	CourseInstances(ctx context.Context, filter DemandFilter) ([]CourseInstance, error)
	// CourseInstanceByID returns one instance, or (nil, nil) when there is none.
	CourseInstanceByID(ctx context.Context, id uuid.UUID) (*CourseInstance, error)
	// CourseInstanceByPartID returns the instance a part belongs to, or (nil, nil).
	CourseInstanceByPartID(ctx context.Context, partID uuid.UUID) (*CourseInstance, error)
	// CreateCourseInstance declares an instance and makes its parts from the module's split,
	// in one transaction. Returns ErrModuleNotDecomposed when there is no split, and
	// ErrTrackTaken when this cohort is already declared.
	CreateCourseInstance(ctx context.Context, spec NewCourseInstance) (*CourseInstance, error)
	// DuplicateCourseInstance copies an instance to another cohort, in one transaction.
	//
	// sourceTrack is written back to the source when it is non-empty, so that a single cohort
	// becoming two is one atomic act: IF1 does not exist for a moment as both "no letter" and
	// "B" — a state in which the two rows do not look like siblings and every label is wrong.
	DuplicateCourseInstance(ctx context.Context, id uuid.UUID, track, sourceTrack string,
		by uuid.UUID) (*CourseInstance, error)
	// UpdateCourseInstance writes the two editable things: the cohort and the cohort year.
	UpdateCourseInstance(ctx context.Context, id uuid.UUID, track string,
		programmeSemester *int) (*CourseInstance, error)
	// DeleteCourseInstance withdraws one. Returns ErrInstanceInUse when something hangs off it.
	DeleteCourseInstance(ctx context.Context, id uuid.UUID) error
	// AddInstancePart appends a part — the second laboratory group, the tutorial nobody
	// expected.
	AddInstancePart(ctx context.Context, instanceID uuid.UUID, kind InstancePartKind,
		hours *float64) (*CourseInstance, error)
	// UpdateInstancePart writes a part's kind and hours.
	UpdateInstancePart(ctx context.Context, partID uuid.UUID, kind InstancePartKind,
		hours *float64) (*CourseInstance, error)
	// DeleteInstancePart removes one. Returns ErrInstanceInUse when something hangs off it.
	DeleteInstancePart(ctx context.Context, partID uuid.UUID) (*CourseInstance, error)
	// ShareInstancePartAcrossTracks marks a part as held for the sibling cohorts too and
	// removes their own parts of that kind, in one transaction.
	ShareInstancePartAcrossTracks(ctx context.Context, partID uuid.UUID) (*CourseInstance, error)
	// SplitInstancePartAcrossTracks undoes that: the part stops being shared and every sibling
	// cohort that now has none of that kind gets its own, with the same hours.
	SplitInstancePartAcrossTracks(ctx context.Context, partID uuid.UUID) (*CourseInstance, error)
	// CopyDemand declares in `to` what `from` holds for one programme, in one transaction.
	// Instances already declared in the target are left untouched and counted as skipped.
	CopyDemand(ctx context.Context, from, to Semester, programmeID, by uuid.UUID) (CopyCounts, error)
	// PlanDemand reconciles a whole screen against what is stored, in one transaction.
	//
	// Only the modules named in entries are touched. With dryRun the same reconciliation runs
	// and is rolled back, so that what the interface shows and what the save does cannot be two
	// different computations.
	//
	// Takes the semester by code rather than by id, and records it itself: the row for a
	// semester nobody has touched has to come into being inside this transaction, or a dry run
	// would either create it — recording a decision nobody took — or write instances pointing at
	// a semester that does not exist.
	PlanDemand(ctx context.Context, semesterCode string, programmeID uuid.UUID,
		entries []DemandEntry, by uuid.UUID, dryRun bool) (DemandPlan, error)
}
