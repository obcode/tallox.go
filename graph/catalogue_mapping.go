package graph

import (
	"errors"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/obcode/tallox.go/graph/model"
	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
)

// What the catalogue resolvers translate with. Separate from catalogue.resolvers.go because
// gqlgen rewrites that file from the schema on every generate.

// programmeModel reshapes a programme for the wire, with its regulations if it carries any.
func programmeModel(p domain.Programme) *model.Programme {
	out := &model.Programme{
		Code:   p.Code,
		Title:  p.Title,
		Active: p.Active,
		Spos:   make([]*model.Spo, 0, len(p.Spos)),
	}
	for _, s := range p.Spos {
		out.Spos = append(out.Spos, spoModel(s, out))
	}
	return out
}

// spoModel reshapes one version of some regulations.
//
// programme is passed in rather than read off the value, so that a version reached through its
// own programme points back at the same object instead of a copy — otherwise a query walking
// programme → spos → programme would render the programme twice, once with its versions and
// once without.
func spoModel(s domain.Spo, programme *model.Programme) *model.Spo {
	out := &model.Spo{
		ID:        s.ID.String(),
		Version:   s.Version,
		Programme: programme,
	}
	if !s.ValidFrom.IsZero() {
		date := model.NewDate(s.ValidFrom)
		out.ValidFrom = &date
	}
	if s.PrimussID != "" {
		id := s.PrimussID
		out.PrimussID = &id
	}
	if out.Programme == nil {
		out.Programme = programmeModel(s.Programme)
	}
	return out
}

// moduleModel reshapes a module, keeping the domain value for the two fields that take an
// argument.
func moduleModel(m domain.Module) *model.Module {
	out := &model.Module{
		ID:                  m.ID.String(),
		Name:                m.Name,
		HomeProgramme:       programmeModel(m.HomeProgramme),
		CourseType:          m.CourseType,
		Frequency:           m.Frequency,
		ContactHoursPerWeek: m.ContactHoursPerWeek,
		Credits:             m.Credits,
		Active:              m.Active,
		Official:            m.Official,
		RetiredAt:           m.RetiredAt,
		Components:          make([]*model.ModuleComponent, 0, len(m.Components)),
		Offerings:           make([]*model.ModuleOffering, 0, len(m.Offerings)),
		Source:              m,
	}

	if m.ZpaID != nil {
		id := strconv.FormatInt(*m.ZpaID, 10)
		out.ZpaID = &id
	}
	if hours, ok := m.ComponentHours(); ok {
		out.ComponentHours = &hours
	}
	for _, c := range m.Components {
		out.Components = append(out.Components, &model.ModuleComponent{
			ID:            c.ID.String(),
			Kind:          c.Kind,
			TeachingHours: c.TeachingHours,
			Position:      c.Position,
		})
	}
	for _, o := range m.Offerings {
		out.Offerings = append(out.Offerings, &model.ModuleOffering{
			Spo:                  spoModel(o.Spo, nil),
			IsDuty:               o.IsDuty,
			ModuleCodes:          o.ModuleCodes,
			Focuses:              o.Focuses,
			MinProgrammeSemester: o.MinProgrammeSemester,
		})
	}
	return out
}

func moduleModels(modules []domain.Module) []*model.Module {
	out := make([]*model.Module, 0, len(modules))
	for _, m := range modules {
		out = append(out, moduleModel(m))
	}
	return out
}

// moduleFilterFrom reads the input, which is optional and may be absent entirely.
func moduleFilterFrom(in *model.ModuleFilter) (domain.ModuleFilter, error) {
	if in == nil {
		return domain.ModuleFilter{}, nil
	}

	filter := domain.ModuleFilter{
		Frequency: in.Frequency,
	}
	if in.Programme != nil {
		filter.Programme = *in.Programme
	}
	if in.Spo != nil {
		id, err := uuid.Parse(*in.Spo)
		if err != nil {
			return filter, badRequest("SPO_ID_INVALID", "Diese Prüfungsordnung gibt es nicht.")
		}
		filter.Spo = id
	}
	if in.Duty != nil {
		filter.Duty = *in.Duty
	}
	if in.Search != nil {
		filter.Search = *in.Search
	}
	if in.IncludeInactive != nil {
		filter.IncludeInactive = *in.IncludeInactive
	}
	if in.WithoutComponents != nil {
		filter.WithoutComponents = *in.WithoutComponents
	}
	return filter, nil
}

// componentsFrom reads the split off the wire. Positions are assigned by the service from the
// order, so the input does not carry them.
func componentsFrom(in []*model.ModuleComponentInput) []domain.ModuleComponent {
	out := make([]domain.ModuleComponent, 0, len(in))
	for _, c := range in {
		if c == nil {
			continue
		}
		out = append(out, domain.ModuleComponent{Kind: c.Kind, TeachingHours: c.TeachingHours})
	}
	return out
}

// catalogueUserFacing turns a service refusal into an error the interface can branch on.
//
// The code is the contract and the German sentence is the part that gets reworded — matching
// prose across a repository boundary breaks the first time somebody improves a message.
func catalogueUserFacing(actor principal.Actor, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, domain.ErrCatalogueForbidden):
		return &gqlerror.Error{
			Message:    domain.ErrCatalogueForbidden.Error(),
			Extensions: map[string]any{"code": "FORBIDDEN"},
		}
	case errors.Is(err, domain.ErrModuleNotFound):
		return &gqlerror.Error{
			Message:    domain.ErrModuleNotFound.Error(),
			Extensions: map[string]any{"code": "MODULE_NOT_FOUND"},
		}
	case errors.Is(err, domain.ErrNotYourProgramme):
		// Two codes behind one refusal, because the two situations need different actions from
		// the person reading it. A lead who has simply not been assigned a programme should not
		// go asking for a role they already hold.
		if policy.HoldsProgrammeLeadWithoutScope(actor) {
			return &gqlerror.Error{
				Message:    policy.ProgrammeScopeMissingReason,
				Extensions: map[string]any{"code": "PROGRAMME_SCOPE_MISSING"},
			}
		}
		return &gqlerror.Error{
			Message:    policy.PlanningReason,
			Extensions: map[string]any{"code": "NOT_YOUR_PROGRAMME"},
		}
	case errors.Is(err, domain.ErrComponentInvalid):
		return &gqlerror.Error{
			Message:    err.Error(),
			Extensions: map[string]any{"code": "COMPONENTS_INVALID"},
		}
	default:
		// Anything else is ours, not the caller's. The sentence is generic on purpose: a
		// database error in the clear says things about rows nobody asked about.
		return &gqlerror.Error{
			Message:    "Das hat nicht geklappt. Bitte später erneut versuchen.",
			Extensions: map[string]any{"code": "INTERNAL"},
		}
	}
}

func badRequest(code, message string) error {
	return &gqlerror.Error{Message: message, Extensions: map[string]any{"code": code}}
}

// normaliseCode accepts what somebody types for a programme code.
//
// The same normalisation the service applies to a filter, applied here too because these two
// fields take the code as an argument and never reach the service. Upper-cased and trimmed:
// `if` and `IF` are the same programme to a person.
func normaliseCode(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
}
