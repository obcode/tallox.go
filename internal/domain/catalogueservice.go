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
// It clears the split and makes the module undeclarable again. A split entered wrongly has to be
// removable by the person who entered it, and refusing that would leave the only repair to
// somebody with database access.
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
