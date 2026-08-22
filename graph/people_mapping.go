package graph

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/graph/model"
	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
)

// This file holds what the administration resolvers translate with. Separate from
// *.resolvers.go for the same reason token_mapping.go is: gqlgen rewrites those from the
// schema on every generate and moves anything else out, so a helper left in one survives
// exactly until the next `go generate`.

// personModel reshapes a domain person for the wire.
func personModel(p domain.Person) *model.Person {
	out := &model.Person{
		ID:         p.ID.String(),
		Mail:       p.Mail,
		Name:       p.Name,
		Active:     p.Active,
		Roles:      p.Roles,
		Programmes: make([]*model.Programme, 0, len(p.Programmes)),
	}
	for _, programme := range p.Programmes {
		out.Programmes = append(out.Programmes, programmeModel(programme))
	}
	return out
}

// teacherAccountModel reshapes one row of the admission screen.
//
// The teacher half goes through teacherModel, the same one the catalogue uses, so that a
// teacher reads the same wherever it is asked for.
func teacherAccountModel(a domain.TeacherAccount) *model.TeacherAccount {
	out := &model.TeacherAccount{Teacher: teacherModel(a.Teacher)}
	if a.Person != nil {
		out.Account = personModel(*a.Person)
	}
	return out
}

// personModels reshapes a list, preserving order.
func personModels(people []domain.Person) []*model.Person {
	out := make([]*model.Person, 0, len(people))
	for _, p := range people {
		out = append(out, personModel(p))
	}
	return out
}

// grantModels reshapes role grants, resolving the granter to a person.
//
// The granter is looked up rather than returned as a bare id, because the only use for this
// field is a human reading "granted by whom" — an id would push every caller into a second
// query, and there is no caller for which the id alone is the answer. Grants with no granter
// (the reconciliation from the configuration file, a future import) keep a null there, which
// is the honest rendering of "no human decided this".
//
// A granter who cannot be resolved also yields null rather than an error. That case is real:
// person_role.granted_by is ON DELETE SET NULL, but a lookup can also fail because the caller
// lost the right to administer between two calls. Failing the whole grant list over a missing
// name would make the audit view unavailable exactly when somebody is auditing.
func (r *Resolver) grantModels(ctx context.Context, actor principal.Actor,
	grants []domain.RoleGrant) []*model.RoleGrant {
	// One lookup per distinct granter, not per grant: a person with five grants usually got
	// them from the same administrator in the same sitting.
	granters := make(map[uuid.UUID]*model.Person)

	out := make([]*model.RoleGrant, 0, len(grants))
	for _, g := range grants {
		grant := &model.RoleGrant{
			Role:      g.Role,
			GrantedAt: g.GrantedAt,
		}
		if !g.ExpiresAt.IsZero() {
			expires := g.ExpiresAt
			grant.ExpiresAt = &expires
		}
		if g.GrantedBy != uuid.Nil {
			granter, seen := granters[g.GrantedBy]
			if !seen {
				if p, err := r.People.Get(ctx, actor, g.GrantedBy); err == nil {
					granter = personModel(*p)
				}
				granters[g.GrantedBy] = granter
			}
			grant.GrantedBy = granter
		}
		out = append(out, grant)
	}
	return out
}

// deref reads an optional GraphQL argument, treating "not given" as the zero value.
//
// The zero value is the right default for every optional argument on this surface: an omitted
// search is no search, and an omitted includeInactive is the narrower list. Where that ever
// stops being true, the argument needs a real default in the schema rather than a special
// case here.
func deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}

// isNotFound reports whether a failure means "there is nobody there" rather than "you may not
// ask".
//
// Kept apart from peopleFacing because the two answers differ in kind: a missing person is a
// null the interface renders, a refusal is an error it reports. Collapsing them would make an
// authorization failure look like an empty database, which is the direction that wastes an
// afternoon.
func isNotFound(err error) bool {
	return errors.Is(err, domain.ErrNoSuchPerson)
}

// parseID turns a GraphQL ID into a uuid, with a refusal a caller can act on.
//
// A malformed id and an id that names nobody get different answers on purpose: the first is a
// bug in the caller's code and the second is an ordinary outcome. Collapsing them would send
// somebody looking for a deleted person when they have a typo in a variable.
func parseID(raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, refusal("INVALID_ID", "Das ist keine gültige Kennung.")
	}
	return id, nil
}

// peopleFacing decides what a caller is told about a failure of the administration surface.
//
// Separate from userFacing rather than one growing switch, because the two have different
// audiences and different rules about what may be said. Token refusals are deliberately vague
// where the caller could learn something from them; here the caller is an administrator
// looking at a screen that lists everybody, so "that person already exists" leaks nothing and
// saying it is simply more useful.
//
// The default arm stays generic all the same. A driver error carries table and constraint
// names, and the habit of not forwarding them is worth keeping on every write path rather
// than only on the ones where it currently matters.
func peopleFacing(err error) error {
	switch {
	case errors.Is(err, domain.ErrNotAdministrator):
		return refusal("FORBIDDEN", policy.AdminReason)
	case errors.Is(err, domain.ErrLastAdmin):
		return refusal("LAST_ADMIN", domain.LastAdminReason)
	case errors.Is(err, domain.ErrInvalidMail):
		return refusal("INVALID_MAIL",
			"Das ist keine Adresse, mit der sich jemand anmelden kann.")
	case errors.Is(err, domain.ErrPersonExists):
		return refusal("PERSON_EXISTS",
			"Unter dieser Adresse ist bereits jemand eingetragen.")
	case errors.Is(err, domain.ErrNoSuchPerson):
		return refusal("PERSON_NOT_FOUND", "Diese Person gibt es nicht.")
	case errors.Is(err, domain.ErrNoSuchTeacher):
		return refusal("TEACHER_NOT_FOUND", "Diese Lehrperson gibt es nicht.")
	case errors.Is(err, domain.ErrTeacherHasNoMail):
		// Its own code, because there is nothing the caller can do here and the reason is worth
		// naming: the address is the whole link between the two lists, and this one has none.
		return refusal("TEACHER_HAS_NO_MAIL",
			"Für diese Person liefert das ZPA keine Mailadresse. Ohne Adresse kann sie sich "+
				"nicht anmelden — ein Konto lässt sich erst anlegen, wenn das ZPA eine hat.")
	case errors.Is(err, domain.ErrNameTooLong):
		return refusal("NAME_TOO_LONG",
			fmt.Sprintf("Der Name darf höchstens %d Zeichen lang sein.", domain.MaxNameLength))
	case errors.Is(err, domain.ErrUnknownRole):
		return refusal("UNKNOWN_ROLE", "Diese Rolle gibt es nicht.")
	case errors.Is(err, domain.ErrUnknownProgramme):
		return refusal("UNKNOWN_PROGRAMME", "Diesen Studiengang gibt es nicht.")
	case errors.Is(err, domain.ErrNotAProgrammeLead):
		// Its own code because the repair is specific: grant the role first. Folded into the
		// generic refusal, an administrator would see "that did not work" for a mistake with an
		// obvious next step.
		return refusal("NOT_A_PROGRAMME_LEAD",
			"Diese Person ist keine Studiengangsleitung. Erst die Rolle vergeben, dann die "+
				"Studiengänge zuordnen.")
	case errors.Is(err, domain.ErrGrantExpiryOutOfRange):
		return refusal("GRANT_EXPIRY_OUT_OF_RANGE",
			fmt.Sprintf("Eine befristete Rolle läuft höchstens %d Tage.",
				domain.MaxTemporaryGrantDays))
	default:
		return refusal("INTERNAL", "Die Aktion konnte nicht ausgeführt werden.")
	}
}

// programmeList names the programmes somebody leads, for a sentence a person reads.
//
// Codes rather than long names, because the codes are what the faculty says out loud and what
// the person reading the diagnosis will compare against the list they were given.
func programmeList(programmes []domain.Programme) string {
	if len(programmes) == 0 {
		return "keinen Studiengang"
	}
	codes := make([]string, 0, len(programmes))
	for _, p := range programmes {
		codes = append(codes, p.Code)
	}
	return strings.Join(codes, ", ")
}

// diagnose renders what the rules answer for one person, without reading anything the rules
// protect.
//
// The list is decisions, never content. That is the whole design: the ordinary support
// question — "why can my colleague not see this" — is answerable from which rules let
// somebody past, and answering it that way means the diagnosis cannot become a way around the
// confidentiality it describes. When a rule lands that this list does not mention, it gets an
// entry here; when a rule is asked about here, it is asked by calling the real one rather than
// by restating it.
//
// The two entries about wishes are asked for both doors, because the answer differs by door
// and that difference is exactly the thing somebody is confused about when a script and a
// browser disagree.
func diagnose(p domain.Person) []*model.PolicyDecision {
	asPerson := func(kind principal.Kind) principal.Actor {
		roles := make([]string, 0, len(p.Roles))
		for _, r := range p.Roles {
			roles = append(roles, string(r))
		}
		// The programme assignments travel with the roles, because the rule about planning
		// depends on both and a diagnosis built from the roles alone would report that a
		// correctly assigned lead may plan nothing.
		scopes := make([]principal.RoleScope, 0, len(p.Programmes))
		for _, programme := range p.Programmes {
			scopes = append(scopes, principal.RoleScope{
				Role:        string(policy.RoleProgrammeLead),
				ProgrammeID: programme.ID,
			})
		}

		return principal.Actor{
			ID: p.ID, Mail: p.Mail, Name: p.Name,
			Roles: roles, RoleScopes: scopes, Kind: kind,
		}
	}

	interactive := asPerson(principal.KindInteractive)
	viaToken := asPerson(principal.KindToken)

	decisions := []*model.PolicyDecision{
		{
			Rule:    "policy.MayAdministerPeople",
			Allowed: policy.MayAdministerPeople(interactive),
			Reason: "Personen und Rollen verwalten. Braucht ADMIN und eine angemeldete " +
				"Browser-Sitzung.",
		},
		{
			Rule:    "policy.MayReadInteractiveOnly",
			Allowed: policy.MayReadInteractiveOnly(interactive),
			Reason: "Personalbezogene Felder im Browser lesen. Über ein Personal Access " +
				"Token nie.",
		},
		{
			Rule:    "policy.WishVisibility (Browser, vor der Veröffentlichung)",
			Allowed: policy.WishVisibility(interactive, policy.SemesterState{}).Scope == policy.WishScopeAll,
			Reason: "Fremde Wünsche vor dem Stichtag sehen. Nur Planung und Dekanat, und " +
				"nur im Browser — ADMIN bewusst nicht.",
		},
		{
			Rule:    "policy.WishVisibility (Token, vor der Veröffentlichung)",
			Allowed: policy.WishVisibility(viaToken, policy.SemesterState{}).Scope == policy.WishScopeAll,
			Reason: "Dasselbe über ein Token. Immer nein: ein Token ist langlebig und " +
				"entkoppelt „wer hat das gesehen“ von einer Anmeldung.",
		},
	}

	// The support question this release creates. A programme lead who has been granted the role
	// and not assigned a programme looks, from every other line here, exactly like one who has
	// been set up correctly — and the difference is the whole reason they cannot do their job.
	scope := policy.PlanningScope(interactive)
	switch {
	case scope.All:
		decisions = append(decisions, &model.PolicyDecision{
			Rule:    "policy.PlanningScope",
			Allowed: true,
			Reason: "Bedarf festlegen. Das Dekanat für alle Studiengänge — auch für " +
				"Studiengänge, die es heute noch nicht gibt.",
		})
	case policy.HoldsProgrammeLeadWithoutScope(interactive):
		decisions = append(decisions, &model.PolicyDecision{
			Rule:    "policy.PlanningScope",
			Allowed: false,
			Reason: "Bedarf festlegen. Diese Person ist Studiengangsleitung, ist aber " +
				"keinem Studiengang zugeordnet — und darf deshalb für keinen etwas " +
				"festlegen. Zuordnen in der Personenverwaltung.",
		})
	default:
		decisions = append(decisions, &model.PolicyDecision{
			Rule:    "policy.PlanningScope",
			Allowed: len(scope.IDs) > 0,
			Reason: "Bedarf festlegen, für " + programmeList(p.Programmes) + ". Nur für die " +
				"zugeordneten Studiengänge, nie für andere.",
		})
	}

	if !p.Active {
		// First, because it makes every other line moot. A deactivated person fails
		// authentication before any rule is consulted, and a diagnosis that listed four
		// permissions and left this to a boolean field further down would be read as "she has
		// the rights, so it must be something else".
		return append([]*model.PolicyDecision{{
			Rule:    "auth.ErrInactiveUser",
			Allowed: false,
			Reason: "Dieses Konto ist deaktiviert. Damit scheitert die Anmeldung an beiden " +
				"Türen, unabhängig von allen Rollen unten.",
		}}, decisions...)
	}

	return decisions
}
