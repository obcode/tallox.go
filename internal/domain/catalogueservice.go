package domain

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
)

// The refusals of the catalogue, as sentinels the interface branches on by code.
var (
	// ErrCatalogueForbidden is the caller not being allowed to read the catalogue at all, which
	// today means having no account.
	ErrCatalogueForbidden = errors.New("kein Zugriff auf den Modulkatalog")
	// ErrModuleNotFound is an id that names no module.
	ErrModuleNotFound = errors.New("dieses Modul gibt es nicht")
	// ErrNotYourProgramme is the scoped refusal: this module is planned somewhere else.
	ErrNotYourProgramme = errors.New("dieses Modul gehört zu einem anderen Studiengang")
	// ErrComponentInvalid is a split that does not describe anything — no hours, or too many.
	ErrComponentInvalid = errors.New("die Aufteilung ist nicht gültig")
	// ErrLocalModuleInvalid is a local course whose description does not hold together — no
	// name, an unknown course type, implausible hours.
	ErrLocalModuleInvalid = errors.New("die Angaben zur Lehrveranstaltung sind nicht gültig")
	// ErrLocalModuleNameTaken is a second course of the same name in the same programme. There
	// is no other identity for a row somebody typed, so the name is the one that has to hold.
	ErrLocalModuleNameTaken = errors.New(
		"in diesem Studiengang gibt es bereits eine eigene Lehrveranstaltung dieses Namens")
)

// CatalogueService answers questions about the module catalogue and takes the one decision that
// belongs to it: how a module's hours divide.
type CatalogueService struct {
	store CatalogueReader
}

// NewCatalogueService wires the service.
func NewCatalogueService(store CatalogueReader) *CatalogueService {
	return &CatalogueService{store: store}
}

// Programmes lists every study programme.
//
// Readable by anybody with an account and no particular role, like the semester list and for the
// same reason: this is what a lecturer needs in order to find a module, and a tool that refuses
// without saying why teaches people to ignore refusals.
func (s *CatalogueService) Programmes(ctx context.Context, actor principal.Actor) ([]Programme, error) {
	if err := mayRead(actor); err != nil {
		return nil, err
	}
	return s.store.Programmes(ctx)
}

// MyProgrammes resolves the study programmes an actor's leadership applies to.
//
// The actor carries their ids and nothing else — internal/principal holds grants without
// interpreting them, and a code is as much an interpretation as a name. This turns them into
// programmes for the one field that renders somebody's own assignments.
//
// Deliberately not a permission check beyond having an account: these are the caller's own
// grants, and a script needs them before it can ask anything useful.
func (s *CatalogueService) MyProgrammes(ctx context.Context, actor principal.Actor) ([]Programme, error) {
	if err := mayRead(actor); err != nil {
		return nil, err
	}

	scope := policy.PlanningScope(actor)
	if scope.All {
		// The dean's office reaches every programme, including ones that do not exist yet, so
		// there is no list to enumerate — and enumerating today's would be a snapshot pretending
		// to be a rule. The field is about assignments, and the dean's office has none.
		return nil, nil
	}
	return s.store.ProgrammesByID(ctx, scope.IDs)
}

// ProgrammeByCode returns one programme, or (nil, nil) when there is none with that code.
func (s *CatalogueService) ProgrammeByCode(ctx context.Context, actor principal.Actor, code string) (*Programme, error) {
	if err := mayRead(actor); err != nil {
		return nil, err
	}
	return s.store.ProgrammeByCode(ctx, normaliseProgrammeCode(code))
}

// Modules lists the catalogue, filtered.
func (s *CatalogueService) Modules(ctx context.Context, actor principal.Actor, filter ModuleFilter) ([]Module, error) {
	if err := mayRead(actor); err != nil {
		return nil, err
	}
	filter.Programme = normaliseProgrammeCode(filter.Programme)
	return s.store.Modules(ctx, filter)
}

// Teachers lists the people who teach.
//
// Readable by anybody with an account and no particular role, like the catalogue itself: it is
// who teaches at the faculty, and a lecturer looking for the person responsible for a module
// needs it. It grants nothing and says nothing about who may sign in beyond the one derived
// flag, which is the same thing the people administration shows anyway.
func (s *CatalogueService) Teachers(ctx context.Context, actor principal.Actor, filter TeacherFilter) ([]Teacher, error) {
	if err := mayRead(actor); err != nil {
		return nil, err
	}
	return s.store.Teachers(ctx, filter)
}

// ModuleByID returns one module, or (nil, nil).
func (s *CatalogueService) ModuleByID(ctx context.Context, actor principal.Actor, id uuid.UUID) (*Module, error) {
	if err := mayRead(actor); err != nil {
		return nil, err
	}
	return s.store.ModuleByID(ctx, id)
}

// MaxComponentsPerModule bounds the split.
//
// Not a rule about teaching, a guard against a request that would fill a table: nothing in the
// catalogue is broken into more than a handful of kinds, and the largest real course type names
// two.
const MaxComponentsPerModule = 12

// SetModuleComponents replaces how a module's hours divide between teachable units.
//
// # Who
//
// The study programme lead of the module's **home** programme, and the dean's office. Not a lead
// in whose catalogue the module merely counts: the module is planned where it is at home — the
// faculty's own words, "the planning of a module should sit with one programme" — and two leads
// editing one split would disagree in the database with no rule to settle it.
//
// # Why the whole list at once
//
// The entries only mean anything together. A mutation that edited one would allow a moment in
// which a module's hours add up to something nobody intended, and somebody reading the
// catalogue during that moment would see it.
//
// # Why an empty list is allowed
//
// It clears the split, and the module falls back to the proposal — marked as a guess again. A
// split entered wrongly has to be removable by the person who entered it, and refusing that would
// leave the only repair to somebody with database access.
func (s *CatalogueService) SetModuleComponents(
	ctx context.Context, actor principal.Actor, moduleID uuid.UUID, components []ModuleComponent,
) (*Module, error) {
	if err := mayRead(actor); err != nil {
		return nil, err
	}

	module, err := s.store.ModuleByID(ctx, moduleID)
	if err != nil {
		return nil, err
	}
	if module == nil {
		return nil, ErrModuleNotFound
	}

	// The scope is checked against the module's home programme, which is the whole reason the
	// module has to be read before the permission is decided.
	if !policy.MayPlanProgramme(actor, module.HomeProgramme.ID) {
		return nil, ErrNotYourProgramme
	}

	if len(components) > MaxComponentsPerModule {
		return nil, fmt.Errorf("%w: höchstens %d Teile", ErrComponentInvalid, MaxComponentsPerModule)
	}
	for i := range components {
		if components[i].TeachingHours <= 0 || components[i].TeachingHours > 20 {
			return nil, fmt.Errorf("%w: die SWS eines Teils müssen zwischen 0 und 20 liegen",
				ErrComponentInvalid)
		}
		if _, ok := ParseInstancePartKind(string(components[i].Kind)); !ok {
			return nil, fmt.Errorf("%w: %q ist keine Art von Lehrveranstaltung",
				ErrComponentInvalid, components[i].Kind)
		}
		// The caller's order is the order. Positions are assigned here rather than accepted, so
		// that a client cannot produce a gap or a collision and the unique constraint is never
		// the thing that reports a bad request.
		components[i].Position = i
	}

	return s.store.SetModuleComponents(ctx, moduleID, components, actor.ID)
}

// MaxLocalModuleName is as long as a course name may be.
//
// Generous — the longest name in the real catalogue is 78 characters — and bounded because
// nothing else bounds it: the column is text, and a name is what a person types into a form.
const MaxLocalModuleName = 200

// CreateLocalModule adds a course the faculty enters itself.
//
// # Why this is a module and not a table of its own
//
// Two cases need it, and both behave exactly like modules once they exist: a course that is not
// in the examination office's catalogue (yet), and an FWP placeholder — "we need three
// electives, ideally something technical" — declared before the subjects are known. As a
// `module` row each inherits the split, the instance, its parts, the hours arithmetic and later
// the wishes; as a table of its own each would be a second branch at every one of those layers.
//
// "Three of them" is three cohorts of one placeholder. That is not a workaround but what the
// identity of an instance already says: three offerings of one module in one programme and
// semester must differ in their track, so the demand table expresses it with the stepper it has.
//
// # Who
//
// The lead of the programme the course is at home in, and the dean's office — the same rule
// SetModuleComponents uses, and the same sentence behind it: a module is planned where it is at
// home. Deliberately **no phase condition**: a module hangs off no semester, so there is no phase
// that could close it. The phase gate applies where it belongs, at the instance.
func (s *CatalogueService) CreateLocalModule(ctx context.Context, actor principal.Actor,
	spec NewLocalModule,
) (*Module, error) {
	if err := mayRead(actor); err != nil {
		return nil, err
	}
	if !policy.MayPlanProgramme(actor, spec.HomeProgrammeID) {
		return nil, ErrNotYourProgramme
	}

	spec, err := validLocalModule(spec)
	if err != nil {
		return nil, err
	}
	return s.store.CreateLocalModule(ctx, spec, actor.ID)
}

// ChangeLocalModule corrects a local course, or takes it out of the lists with Active false.
//
// There is no delete, and that is the same decision the rest of this schema makes: instances
// point at a module, and later wishes will point at their parts. Deactivating removes it from
// every list a planner sees and leaves what was planned with it intact.
//
// The permission is judged against the module's *stored* home programme, which is why the row is
// read first — and why the home programme is not among the editable fields. Letting it move
// would let somebody push a row out of their own reach in the same request.
func (s *CatalogueService) ChangeLocalModule(ctx context.Context, actor principal.Actor,
	moduleID uuid.UUID, spec NewLocalModule,
) (*Module, error) {
	if err := mayRead(actor); err != nil {
		return nil, err
	}

	module, err := s.store.ModuleByID(ctx, moduleID)
	if err != nil {
		return nil, err
	}
	if module == nil || module.Source != ModuleSourceLocal {
		// An imported row answers the same as no row at all. It is not this mutation's to edit,
		// and saying which of the two it is would only invite a second attempt.
		return nil, ErrModuleNotFound
	}
	if !policy.MayPlanProgramme(actor, module.HomeProgramme.ID) {
		return nil, ErrNotYourProgramme
	}

	spec.HomeProgrammeID = module.HomeProgramme.ID
	spec, err = validLocalModule(spec)
	if err != nil {
		return nil, err
	}
	return s.store.UpdateLocalModule(ctx, moduleID, spec, actor.ID)
}

// validLocalModule normalises what somebody typed and refuses what cannot be stored.
//
// The vocabularies are checked here rather than left to the schema's CHECK, for the reason the
// split is: a constraint violation carries a table name and a constraint name, and the habit of
// not forwarding those is what the wish workflow will depend on.
func validLocalModule(spec NewLocalModule) (NewLocalModule, error) {
	spec.Name = strings.TrimSpace(spec.Name)
	switch {
	case spec.Name == "":
		return spec, fmt.Errorf("%w: die Lehrveranstaltung braucht einen Namen",
			ErrLocalModuleInvalid)
	case len(spec.Name) > MaxLocalModuleName:
		return spec, fmt.Errorf("%w: der Name ist zu lang", ErrLocalModuleInvalid)
	}

	if _, ok := ParseCourseType(string(spec.CourseType)); !ok {
		return spec, fmt.Errorf("%w: %q ist keine bekannte Zerlegungsart",
			ErrLocalModuleInvalid, spec.CourseType)
	}
	if _, ok := ParseFrequency(string(spec.Frequency)); !ok {
		return spec, fmt.Errorf("%w: %q ist kein bekannter Turnus",
			ErrLocalModuleInvalid, spec.Frequency)
	}
	if _, ok := ParseModuleKind(string(spec.Kind)); !ok {
		return spec, fmt.Errorf("%w: %q ist keine bekannte Art", ErrLocalModuleInvalid, spec.Kind)
	}

	// The same bounds module_hours_are_plausible and module_credits_are_plausible carry. Zero is
	// allowed for the hours because twelve real modules have it — see the migration.
	if spec.ContactHoursPerWeek != nil && (*spec.ContactHoursPerWeek < 0 ||
		*spec.ContactHoursPerWeek > 30) {
		return spec, fmt.Errorf("%w: die SWS müssen zwischen 0 und 30 liegen", ErrLocalModuleInvalid)
	}
	if spec.Credits != nil && (*spec.Credits < 0 || *spec.Credits > 60) {
		return spec, fmt.Errorf("%w: die ECTS müssen zwischen 0 und 60 liegen",
			ErrLocalModuleInvalid)
	}

	if len(spec.Components) > MaxComponentsPerModule {
		return spec, fmt.Errorf("%w: höchstens %d Teile", ErrComponentInvalid,
			MaxComponentsPerModule)
	}
	for i := range spec.Components {
		if spec.Components[i].TeachingHours <= 0 || spec.Components[i].TeachingHours > 20 {
			return spec, fmt.Errorf("%w: die SWS eines Teils müssen zwischen 0 und 20 liegen",
				ErrComponentInvalid)
		}
		if _, ok := ParseInstancePartKind(string(spec.Components[i].Kind)); !ok {
			return spec, fmt.Errorf("%w: %q ist keine Art von Lehrveranstaltung",
				ErrComponentInvalid, spec.Components[i].Kind)
		}
		spec.Components[i].Position = i
	}
	return spec, nil
}

// mayRead is the one gate on reading the catalogue: an account.
//
// Deliberately not a role check. The catalogue is published by the examination office, and every
// lecturer needs it to answer "which of these could I teach". What is scoped is writing.
func mayRead(actor principal.Actor) error {
	if actor.ID == uuid.Nil {
		return ErrCatalogueForbidden
	}
	return nil
}

// normaliseProgrammeCode accepts what somebody types and stores what the schema holds.
//
// Upper-cased and trimmed, like the semester code, and for the same reason: `if` and `IF` are
// the same programme to a person, and refusing the first would be pedantry. It is not otherwise
// repaired — a code with a space in it is refused by the lookup rather than rearranged.
func normaliseProgrammeCode(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
}
