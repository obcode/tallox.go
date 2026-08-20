package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// The projection of the cached payloads into the catalogue, as far as the rest of the program
// needs to know about it.
//
// It exists as its own vocabulary rather than as store rows for the ordinary reason — the
// resolvers cannot reach the database — and for one specific to it: the report is the only
// place the nine decisions the projection makes about untidy input become visible, and they are
// decisions rather than statistics. A count of skipped modules that nobody can name is the same
// as no count at all.

// DependsOnSubjectPhrase is what the source writes for a module whose content varies.
//
// It maps to the same value as an unrecognised phrase, which is why the report has to know it:
// 23 modules legitimately say this, and reporting them as unmapped every night would train
// people to ignore the line that exists to be noticed.
const DependsOnSubjectPhrase = "je nach Fach"

// ProjectionStatus is how one projection ended.
//
// Three values rather than the sync's four: there is no PARTIAL, because the whole projection is
// one transaction. Either the catalogue moved or it did not.
type ProjectionStatus string

const (
	// ProjectionRunning is a projection under way, or one whose process did not finish.
	ProjectionRunning ProjectionStatus = "RUNNING"
	// ProjectionSucceeded is a projection that committed.
	ProjectionSucceeded ProjectionStatus = "SUCCEEDED"
	// ProjectionFailed is a projection that rolled back. The catalogue is untouched.
	ProjectionFailed ProjectionStatus = "FAILED"
)

// ProjectionNoteCode names one decision the projection made about input the source left untidy.
//
// Each is a decision with a reason, and the reason is in the migration next to the constraint
// that keeps this list closed. What matters here is that none of them is a silent drop: a
// projection that quietly discarded rows would be indistinguishable from a catalogue that never
// had them, and the first person to notice would be a programme lead who cannot find a module
// they are responsible for.
type ProjectionNoteCode string

const (
	// NoteModuleWithoutHomeProgramme is a module the source gives no owner for. Skipped: the
	// home programme is mandatory by decision.
	NoteModuleWithoutHomeProgramme ProjectionNoteCode = "MODULE_WITHOUT_HOME_PROGRAMME"
	// NoteProgrammeCodeMalformed is a programme code this schema cannot store. Skipped, along
	// with its modules.
	NoteProgrammeCodeMalformed ProjectionNoteCode = "PROGRAMME_CODE_MALFORMED"
	// NoteProgrammeWithoutRegulations is a programme named only by the modules that call it
	// home. Kept and marked inactive.
	NoteProgrammeWithoutRegulations ProjectionNoteCode = "PROGRAMME_WITHOUT_REGULATIONS"
	// NoteModuleWithoutName is a module that appears in no set of regulations and therefore has
	// no name anywhere. Kept, and rendered by its identifier.
	NoteModuleWithoutName ProjectionNoteCode = "MODULE_WITHOUT_NAME"
	// NoteModuleInactive is a module the examination office has retired. Kept and flagged.
	NoteModuleInactive ProjectionNoteCode = "MODULE_INACTIVE"
	// NoteAssociationWithUnknownRegulations is an association pointing at regulations the source
	// no longer returns. Not projected — there is no path from it to a programme.
	NoteAssociationWithUnknownRegulations ProjectionNoteCode = "ASSOCIATION_WITH_UNKNOWN_REGULATIONS"
	// NoteFrequencyUnmapped is a phrase for how often a module runs that this version does not
	// recognise. Became UNKNOWN.
	NoteFrequencyUnmapped ProjectionNoteCode = "FREQUENCY_UNMAPPED"
	// NoteCourseTypeUnmapped is the same for how the teaching is broken up.
	NoteCourseTypeUnmapped ProjectionNoteCode = "COURSE_TYPE_UNMAPPED"
	// NoteMinSemesterConflict is a module whose catalogue slots within one set of regulations
	// disagree about the earliest semester. Folded with the lowest.
	NoteMinSemesterConflict ProjectionNoteCode = "MIN_SEMESTER_CONFLICT"
	// NoteDutyConflict is the alarm rather than a note. The grain of an offering rests on
	// compulsory-or-elective being determined by module and regulations together; if the source
	// ever contradicts that, the fold silently picks an answer and this says so.
	NoteDutyConflict ProjectionNoteCode = "DUTY_CONFLICT"
)

// AllProjectionNoteCodes returns every finding, in the order a report reads best: what was
// dropped, then what was kept with a caveat, then what needs looking at.
func AllProjectionNoteCodes() []ProjectionNoteCode {
	return []ProjectionNoteCode{
		NoteModuleWithoutHomeProgramme,
		NoteProgrammeCodeMalformed,
		NoteAssociationWithUnknownRegulations,
		NoteProgrammeWithoutRegulations,
		NoteModuleWithoutName,
		NoteModuleInactive,
		NoteFrequencyUnmapped,
		NoteCourseTypeUnmapped,
		NoteMinSemesterConflict,
		NoteDutyConflict,
	}
}

// ParseProjectionNoteCode reports whether s is a finding this package knows.
func ParseProjectionNoteCode(s string) (ProjectionNoteCode, bool) {
	for _, c := range AllProjectionNoteCodes() {
		if string(c) == s {
			return c, true
		}
	}
	return "", false
}

// CatalogueProjectionNote is one finding, with a count and a few examples.
//
// Counts and samples rather than a row per object: the useful sentence is "665 associations
// across 12 sets of regulations", and whoever wants to chase one needs a handful of identifiers
// rather than all of them.
type CatalogueProjectionNote struct {
	Code   ProjectionNoteCode
	Count  int
	Sample []string
}

// CatalogueProjection is one run of the projection and what it found.
type CatalogueProjection struct {
	ID uuid.UUID
	// The import run that triggered it, or nil for a projection asked for on its own.
	RunID      *uuid.UUID
	StartedAt  time.Time
	FinishedAt *time.Time
	Status     ProjectionStatus

	ProgrammesWritten int
	ModulesWritten    int
	OfferingsWritten  int
	OfferingsRemoved  int

	Error *string
	Notes []CatalogueProjectionNote
}

// CatalogueStore is the persistence the projection needs.
//
// A narrow hand-written interface rather than a generated mock, like every other seam in this
// repository: what is worth substituting here is nothing, and what is worth testing is the SQL.
type CatalogueStore interface {
	// Project rebuilds the catalogue from whatever the cache holds. runID names the import run
	// that triggered it, or is nil.
	Project(ctx context.Context, runID *uuid.UUID) (CatalogueProjection, error)
	// LatestProjections returns the recent runs, newest first, each with its report.
	LatestProjections(ctx context.Context, limit int) ([]CatalogueProjection, error)
}
