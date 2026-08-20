package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// The catalogue as the rest of the program sees it.
//
// Deliberately not the store's row types. Two of these carry things no column holds — a
// module's components arrive with it, and its offerings are already joined to their regulations
// — because the alternative is a field resolver per relation and a query per row. The catalogue
// is 506 modules and 1784 offerings; loading what a list needs in four statements and stitching
// it here is both simpler and faster than a loader framework.

// Programme is a study programme of the faculty.
type Programme struct {
	ID    uuid.UUID
	Code  string
	Title string
	// Active is false for a programme the examination office publishes no current regulations
	// for. Its modules are still planable.
	Active bool
	// Spos are its versions of the regulations, newest first. Empty unless asked for.
	Spos []Spo
}

// Spo is one version of one programme's examination regulations.
type Spo struct {
	ID      uuid.UUID
	Version int
	// ValidFrom is the day this version starts applying, or the zero time if the source does
	// not say.
	ValidFrom time.Time
	// PrimussID is empty while the version is still being entered, which is the only reliable
	// signal that it is unfinished.
	PrimussID string
	Programme Programme
}

// Module is an entry in the module catalogue.
type Module struct {
	ID uuid.UUID
	// Name is empty for the modules that appear in no set of regulations — the source carries no
	// name field, so there is nothing to borrow one from.
	Name          string
	HomeProgramme Programme
	CourseType    CourseType
	Frequency     Frequency
	// ContactHoursPerWeek is what a STUDENT attends. Not teaching load; see Components.
	ContactHoursPerWeek *int
	Credits             *int
	Active              bool
	Official            bool
	// RetiredAt is when a successful import stopped mentioning it. Modules are never deleted.
	RetiredAt *time.Time
	// ZpaID is the examination office's identifier, for cross-referencing only.
	ZpaID *int64
	// Components is how the module's hours divide between teachable units. Empty means nobody
	// has stated it, which is what stops an instance being declared.
	Components []ModuleComponent
	// Offerings is where the module counts, across every programme and version.
	Offerings []ModuleOffering
}

// ComponentHours is the sum of the components, and whether there are any.
//
// Compared with ContactHoursPerWeek for a warning rather than a constraint: twelve modules carry
// no hours in the source and several carry figures that do not match what is taught, and a hard
// rule would make exactly those unplannable — precisely where somebody needs to enter the truth.
func (m Module) ComponentHours() (float64, bool) {
	if len(m.Components) == 0 {
		return 0, false
	}
	var total float64
	for _, c := range m.Components {
		total += c.TeachingHours
	}
	return total, true
}

// DutyStatus folds "compulsory or elective" over one programme's versions of its regulations.
//
// Three-valued because two is not enough: four modules in the catalogue are compulsory under one
// version and elective under another, and picking one silently would be wrong for those four and
// unexplainable to whoever is looking at them.
//
// Reports false when the module does not appear in that programme's catalogue at all, which is
// different from being elective in it.
func (m Module) DutyStatus(programmeCode string) (DutyStatus, bool) {
	var seenDuty, seenElective bool
	for _, o := range m.Offerings {
		if o.Spo.Programme.Code != programmeCode {
			continue
		}
		if o.IsDuty {
			seenDuty = true
		} else {
			seenElective = true
		}
	}

	switch {
	case seenDuty && seenElective:
		return DutyMixed, true
	case seenDuty:
		return DutyCompulsory, true
	case seenElective:
		return DutyElective, true
	default:
		return "", false
	}
}

// InCatalogue reports whether the module counts in any version of this programme's regulations.
//
// False together with a home programme of the same code is a real and common state: the module
// is the programme's own and does not currently appear in its catalogue. Twenty-six active
// modules are in it, which is why a programme's list is the union of the two rather than the
// catalogue alone.
func (m Module) InCatalogue(programmeCode string) bool {
	for _, o := range m.Offerings {
		if o.Spo.Programme.Code == programmeCode {
			return true
		}
	}
	return false
}

// ModuleComponent is one unit of a module's split into teachable parts.
type ModuleComponent struct {
	ID   uuid.UUID
	Kind InstancePartKind
	// TeachingHours is the hours per week of ONE unit of this kind. The multiplicity — three
	// laboratory groups — belongs to the instance.
	TeachingHours float64
	Position      int
}

// ModuleOffering is the statement that a module counts in one version of some regulations.
type ModuleOffering struct {
	ID                   uuid.UUID
	Spo                  Spo
	IsDuty               bool
	ModuleCodes          []string
	Focuses              []string
	MinProgrammeSemester *int
}

// DutyStatus is compulsory, elective, or both depending on the version.
type DutyStatus string

const (
	// DutyCompulsory is compulsory under every version asked about.
	DutyCompulsory DutyStatus = "COMPULSORY"
	// DutyElective is elective under every version asked about.
	DutyElective DutyStatus = "ELECTIVE"
	// DutyMixed is compulsory under some and elective under others.
	DutyMixed DutyStatus = "MIXED"
)

// AllDutyStatuses returns every value, most restrictive first.
func AllDutyStatuses() []DutyStatus {
	return []DutyStatus{DutyCompulsory, DutyElective, DutyMixed}
}

// ParseDutyStatus reports whether s is a value this package knows.
func ParseDutyStatus(s string) (DutyStatus, bool) {
	for _, d := range AllDutyStatuses() {
		if string(d) == s {
			return d, true
		}
	}
	return "", false
}

// ModuleFilter narrows the catalogue.
//
// A struct rather than a long argument list, and it travels unchanged from the GraphQL input to
// the store: every field here is a WHERE clause, so translating it twice would be two places to
// get the same predicate wrong.
type ModuleFilter struct {
	// Programme is a short code. Matches a module that counts in one of the programme's
	// regulations OR is at home in it — the second half is not redundant, see Module.InCatalogue.
	Programme string
	// Spo narrows to one version of the regulations. Zero means every version.
	Spo uuid.UUID
	// Frequency keeps only modules with one of these. Empty means every frequency.
	Frequency []Frequency
	// Duty needs Programme and is ignored without it: compulsory-or-elective is a property of a
	// module in a programme, not of a module.
	Duty DutyStatus
	// Search matches the name or one of the module codes, case-insensitively.
	Search string
	// IncludeInactive keeps the modules the examination office has retired.
	IncludeInactive bool
	// WithoutComponents keeps only the modules whose hours nobody has split yet — the work list.
	WithoutComponents bool
}

// CatalogueReader is what reading the catalogue needs.
type CatalogueReader interface {
	Programmes(ctx context.Context) ([]Programme, error)
	// ProgrammesByID resolves a handful of ids, without their regulations.
	ProgrammesByID(ctx context.Context, ids []uuid.UUID) ([]Programme, error)
	ProgrammeByCode(ctx context.Context, code string) (*Programme, error)
	Modules(ctx context.Context, filter ModuleFilter) ([]Module, error)
	ModuleByID(ctx context.Context, id uuid.UUID) (*Module, error)
	// SetModuleComponents replaces a module's split, as one statement about a set of rows.
	SetModuleComponents(ctx context.Context, moduleID uuid.UUID, components []ModuleComponent, by uuid.UUID) (*Module, error)
}
