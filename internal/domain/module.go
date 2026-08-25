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

// SubjectGroupRef is a subject group as it is referred to from somewhere else: enough to label
// it and to link to it, and deliberately not the whole thing.
//
// The full SubjectGroup carries its leads and its members, and filling those in for every row of
// a 506-module catalogue would be a statement per module. A reference is what a module row
// actually needs, and having a name for it is what stops a half-populated SubjectGroup being
// passed around as if it were a whole one.
type SubjectGroupRef struct {
	ID     uuid.UUID
	Code   string
	Name   string
	Active bool
}

// Programme is a study programme of the faculty.
type Programme struct {
	ID    uuid.UUID
	Code  string
	Title string
	// Active is false for a programme the examination office publishes no current regulations
	// for. Its modules are still planable.
	Active bool
	// PlanningStatus is whether this faculty plans this programme — its own decision, not the
	// examination office's. See migration 12 for why it cannot be derived.
	PlanningStatus ProgrammeStatus
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

// Teacher is somebody who teaches, as the examination office publishes them.
//
// Not a user of this installation. Who may sign in is `Person`, a separate and curated list;
// importing teachers grants nothing. The two are connected by the mail address where both know
// it, which is why IsUser is derived on every read rather than stored — somebody admitted this
// morning is connected to their own modules now rather than after the next import.
type Teacher struct {
	ID       uuid.UUID
	Name     string
	SortName string
	// Mail is empty for the handful the source gives no address for. Such a person can never be
	// connected to somebody who signs in.
	Mail                 string
	IsProfessor          bool
	IsLecturerOnContract bool
	IsHonoraryProfessor  bool
	IsStaff              bool
	// Active is the source's own flag, not this system's. Roughly four in five are true, and the
	// false ones are kept because some of them are still responsible for a module.
	Active bool
	// Faculty is absent for more than half of them, so it is a hint and not a filter.
	Faculty string
	// LastSemester is in this system's spelling (`2026-WS`), empty where the source says
	// something that is not a semester.
	LastSemester string
	// IsUser is whether somebody with this address may sign in here.
	IsUser bool
}

// TeacherFilter narrows the list of people who teach.
type TeacherFilter struct {
	// Search matches the name or the address, case-insensitively.
	Search string
	// IncludeInactive keeps the ones the source marks as no longer teaching.
	IncludeInactive bool
}

// Module is an entry in the module catalogue.
type Module struct {
	ID uuid.UUID
	// Name is empty for the modules that appear in no set of regulations — the source carries no
	// name field, so there is nothing to borrow one from.
	Name          string
	HomeProgramme Programme
	// SubjectGroup is the faculty's own grouping this module belongs to, or nil while nobody has
	// assigned one — which is the ordinary state until October's work list is finished, and what
	// the withoutSubjectGroup filter finds.
	//
	// Exactly one, or none. "Whose group fills this instance" is the sentence the assignment
	// phase is made of, and it has to have one answer.
	SubjectGroup *SubjectGroupRef
	// Responsible is who the examination office names for this module, or nil. Nil for about one
	// in thirty: the source names a placeholder, or an address the teacher list does not have.
	Responsible *Teacher
	CourseType  CourseType
	Frequency   Frequency
	// ContactHoursPerWeek is what a STUDENT attends. Not teaching load; see Components.
	ContactHoursPerWeek *int
	Credits             *int
	Active              bool
	Official            bool
	// RetiredAt is when a successful import stopped mentioning it. Modules are never deleted.
	RetiredAt *time.Time
	// ZpaID is the examination office's identifier, for cross-referencing only. Always nil for
	// a local row — the schema refuses any other combination.
	ZpaID *int64
	// Source is where this row came from: the import, or the faculty itself.
	Source ModuleSource
	// Kind is what it stands for — an ordinary module, or an FWP placeholder.
	Kind ModuleKind
	// Components is how the module's hours divide between teachable units. Empty means nobody
	// has stated it — see EffectiveComponents, which falls back to the proposal.
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

// ProposedComponents is the split this module would get if nobody stated one.
//
// Derived from the course type and the catalogue's total, on every read, and never written. See
// CourseType.ProposedComponents for the rule and for why the lecture always gets an even number
// of hours.
func (m Module) ProposedComponents() []ModuleComponent {
	hours := 0
	if m.ContactHoursPerWeek != nil {
		hours = *m.ContactHoursPerWeek
	}
	return m.CourseType.ProposedComponents(hours)
}

// EffectiveComponents is what to plan with: the stated split, or the proposal where there is none.
//
// The one place that decides which of the two applies. Two callers need it — the interface, which
// renders it, and the declaration of an instance, which makes the parts from it — and if they
// answered that question separately they would eventually answer it differently, on a screen that
// shows one thing and a database that holds another.
func (m Module) EffectiveComponents() []ModuleComponent {
	if len(m.Components) > 0 {
		return m.Components
	}
	return m.ProposedComponents()
}

// SplitIsEstimated reports whether what EffectiveComponents returns is a guess.
//
// The flag the interface marks a row with. It is deliberately not "the module has no components":
// a module the catalogue gives no hours for has neither a split nor a proposal, and calling that
// an estimate would put a warning triangle on a row where there is nothing to confirm.
func (m Module) SplitIsEstimated() bool {
	return len(m.Components) == 0 && len(m.ProposedComponents()) > 0
}

// Plannable reports whether an instance can be declared for this module.
//
// True as soon as there is something to make parts from — a stated split or a proposal. Only the
// modules the examination office gives no hours at all are left out, twelve of them in the real
// catalogue, and for those the repair is to state the split by hand.
//
// Exposed rather than left to the caller to work out, because the same question is answered in
// three places that must agree: the picker that offers a module, the refusal that turns one away,
// and the transaction that writes the parts.
func (m Module) Plannable() bool {
	return len(m.EffectiveComponents()) > 0
}

// PracticalKind is the kind of part a "how many groups" figure multiplies.
//
// The first unit of the split that is not the lecture: the laboratory of a lecture-plus-laboratory
// module, the exercise of a lecture-plus-exercise one, and the seminar itself where a module is
// nothing but a seminar. A module that is only a lecture has none, and reports false — parallel
// lectures are not what "groups" means to anybody here.
func (m Module) PracticalKind() (InstancePartKind, bool) {
	kind, _, ok := PracticalKindOf(m.EffectiveComponents())
	return kind, ok
}

// PracticalKindOf is the same question asked of a split already in hand, and it also answers with
// the hours one such unit carries.
//
// The store needs both when it adds a group: which kind of part, and how long it is.
func PracticalKindOf(components []ModuleComponent) (InstancePartKind, float64, bool) {
	for _, c := range components {
		if c.Kind != PartKindLecture {
			return c.Kind, c.TeachingHours, true
		}
	}
	return "", 0, false
}

// ProgrammeSemester folds "the earliest semester a student may take this in" over one programme.
//
// The same fold the demand makes when it seeds a new instance, and the same choice of the
// earliest: 23 of 1076 module/programme pairs disagree across versions of the regulations, and
// the earliest is the one that puts the module where somebody looking for it expects it.
//
// Reports false where the regulations do not say, which is nearly half the elective catalogue —
// there the answer is "no restriction" and not a number.
func (m Module) ProgrammeSemester(programmeCode string) (int, bool) {
	earliest := 0
	for _, o := range m.Offerings {
		if o.Spo.Programme.Code != programmeCode || o.MinProgrammeSemester == nil {
			continue
		}
		if earliest == 0 || *o.MinProgrammeSemester < earliest {
			earliest = *o.MinProgrammeSemester
		}
	}
	return earliest, earliest > 0
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
	// WithoutSubjectGroup keeps only the modules nobody has put into a subject group yet — the
	// other half of the same work list, and the one October starts with.
	WithoutSubjectGroup bool
	// SubjectGroup keeps only the modules of one group. uuid.Nil means every group.
	SubjectGroup uuid.UUID
	// Responsible keeps only the modules this teacher is responsible for.
	Responsible uuid.UUID
}

// CatalogueReader is what reading the catalogue needs.
type CatalogueReader interface {
	// Programmes lists the study programmes. includeUnplanned adds the ones this faculty does
	// not plan — for the screen that decides which those are, and for nothing else.
	Programmes(ctx context.Context, includeUnplanned bool) ([]Programme, error)
	// ProgrammesByID resolves a handful of ids, without their regulations.
	ProgrammesByID(ctx context.Context, ids []uuid.UUID) ([]Programme, error)
	ProgrammeByCode(ctx context.Context, code string) (*Programme, error)
	Modules(ctx context.Context, filter ModuleFilter) ([]Module, error)
	Teachers(ctx context.Context, filter TeacherFilter) ([]Teacher, error)
	ModuleByID(ctx context.Context, id uuid.UUID) (*Module, error)
	// SetModuleComponents replaces a module's split, as one statement about a set of rows.
	SetModuleComponents(ctx context.Context, moduleID uuid.UUID, components []ModuleComponent, by uuid.UUID) (*Module, error)
	// SetProgrammePlanningStatus records that this faculty plans a programme, or no longer
	// does. Returns nil for a code that names no programme.
	SetProgrammePlanningStatus(ctx context.Context, code string, status ProgrammeStatus, by uuid.UUID) (*Programme, error)
	// CreateLocalModule adds a catalogue row the faculty enters itself, with its split.
	CreateLocalModule(ctx context.Context, spec NewLocalModule, by uuid.UUID) (*Module, error)
	// UpdateLocalModule corrects one, or takes it out of the lists. Never touches an imported
	// row: the statement names the source.
	UpdateLocalModule(ctx context.Context, id uuid.UUID, spec NewLocalModule, by uuid.UUID) (*Module, error)
}

// NewLocalModule is a course the faculty enters itself, on the way in.
//
// The home programme is here and nowhere in the update: it is what the permission is judged
// against, so allowing it to move would let somebody push a row out of their own reach.
type NewLocalModule struct {
	HomeProgrammeID     uuid.UUID
	Name                string
	Kind                ModuleKind
	CourseType          CourseType
	Frequency           Frequency
	ContactHoursPerWeek *int
	Credits             *int
	Active              bool
	// Components is the split, or empty to leave the proposal standing.
	Components []ModuleComponent
}
