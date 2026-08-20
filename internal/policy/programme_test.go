package policy_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
	"github.com/obcode/tallox.go/internal/testdata"
)

// Two study programmes, fixed so the assertions can name them.
var (
	programmeOne = uuid.MustParse("11111111-1111-4111-8111-111111111111")
	programmeTwo = uuid.MustParse("22222222-2222-4222-8222-222222222222")
)

// leadOf builds a programme lead scoped to the given programmes.
func leadOf(p testdata.Persona, kind principal.Kind, programmes ...uuid.UUID) principal.Actor {
	actor := p.Actor(kind, string(policy.RoleLecturer), string(policy.RoleProgrammeLead))
	for _, id := range programmes {
		actor.RoleScopes = append(actor.RoleScopes, principal.RoleScope{
			Role:        string(policy.RoleProgrammeLead),
			ProgrammeID: id,
		})
	}
	return actor
}

// The rule this whole migration exists to make enforceable. Until now PROGRAMME_LEAD was
// faculty-wide because there was nothing for it to be about.
func TestAProgrammeLeadPlansOnlyTheirOwnProgrammes(t *testing.T) {
	t.Parallel()

	lead := leadOf(testdata.Vier, principal.KindInteractive, programmeOne)

	if !policy.MayPlanProgramme(lead, programmeOne) {
		t.Error("a programme lead may not plan the programme they lead")
	}
	if policy.MayPlanProgramme(lead, programmeTwo) {
		t.Error("a programme lead may plan a programme they do not lead")
	}
}

// The decision that runs against this package's own precedent, and therefore the one most
// likely to be "corrected" by somebody who remembers the precedent and not the reason.
//
// An empty token scope list and an empty role narrowing both mean unrestricted, because both
// are mechanisms that can only ever remove. A programme scope is not a narrowing of the grant;
// it is the grant's subject. Reading it as "all programmes" would have made the deploy of the
// migration the moment every existing lead silently became faculty-wide.
func TestAnUnscopedProgrammeLeadPlansNothing(t *testing.T) {
	t.Parallel()

	lead := leadOf(testdata.Vier, principal.KindInteractive)

	if policy.MayPlanProgramme(lead, programmeOne) {
		t.Error("a programme lead with no programme may plan one anyway. An empty scope is " +
			"not 'unrestricted' here: the role declares the demand of ONE programme, and the " +
			"role that means all of them is DEANS_OFFICE.")
	}
	if !policy.PlanningScope(lead).Empty() {
		t.Error("the scope of an unscoped lead does not read as empty")
	}
	// And the distinction that decides which sentence they are shown.
	if !policy.HoldsProgrammeLeadWithoutScope(lead) {
		t.Error("an unscoped lead is not recognised as waiting for an administrator")
	}
	if policy.PlanningRefusal(lead) != policy.ProgrammeScopeMissingReason {
		t.Error("an unscoped lead is told they are not allowed, rather than that nobody has " +
			"assigned them a programme — so they go and ask for a role they already hold")
	}
}

// The dean's office plans across programmes, because the import/export statistics are its job.
// Expressed as All rather than as every id, which would look the same today and would be a
// snapshot: a programme created tomorrow would fall outside it.
func TestTheDeansOfficePlansEveryProgrammeIncludingOnesThatDoNotExistYet(t *testing.T) {
	t.Parallel()

	dekanat := testdata.Fuenf.Actor(principal.KindInteractive,
		string(policy.RoleLecturer), string(policy.RoleDeansOffice))

	scope := policy.PlanningScope(dekanat)
	if !scope.All {
		t.Fatal("the dean's office does not reach every programme")
	}
	if !scope.Allows(uuid.New()) {
		t.Error("a programme created after the scope was computed falls outside it")
	}
	if scope.Empty() {
		t.Error("the dean's office reads as reaching nothing")
	}
	if policy.HoldsProgrammeLeadWithoutScope(dekanat) {
		t.Error("the dean's office is reported as a lead waiting for a programme")
	}
}

// Running the system is a different job from planning with it. The same decision the wish rule
// makes, and it is on the golden matrix for the same reason.
func TestAdministeringIsNotPlanning(t *testing.T) {
	t.Parallel()

	admin := testdata.Sechs.Actor(principal.KindInteractive,
		string(policy.RoleLecturer), string(policy.RoleAdmin))

	if policy.MayPlanProgramme(admin, programmeOne) {
		t.Error("an administrator may declare demand. Running the system is a different job; " +
			"an administrator who has to plan is granted the role visibly.")
	}
}

// A lecturer plans nothing, and neither does a subject group lead — they fill instances, they
// do not declare them.
func TestOnlyPlanningRolesPlan(t *testing.T) {
	t.Parallel()

	for _, role := range []policy.Role{policy.RoleLecturer, policy.RoleSubjectGroupLead} {
		t.Run(string(role), func(t *testing.T) {
			t.Parallel()
			actor := testdata.Eins.Actor(principal.KindInteractive, string(role))
			// Even carrying a scope row, which cannot happen through the database — the CHECK
			// on person_programme_scope refuses any role but PROGRAMME_LEAD — but could happen
			// through a mistake here.
			actor.RoleScopes = []principal.RoleScope{{
				Role: string(role), ProgrammeID: programmeOne,
			}}
			if policy.MayPlanProgramme(actor, programmeOne) {
				t.Errorf("%s may declare demand", role)
			}
		})
	}
}

// A stray scope for a role somebody does not hold grants nothing. The database forbids the row,
// and the rule does not depend on the database for it.
func TestAScopeWithoutItsRoleGrantsNothing(t *testing.T) {
	t.Parallel()

	actor := testdata.Eins.Actor(principal.KindInteractive, string(policy.RoleLecturer))
	actor.RoleScopes = []principal.RoleScope{{
		Role: string(policy.RoleProgrammeLead), ProgrammeID: programmeOne,
	}}

	if policy.MayPlanProgramme(actor, programmeOne) {
		t.Error("a programme scope granted permission to somebody who does not hold the role")
	}
}

// Narrowing away a role must take its scopes with it. Without this an actor keeps the
// programmes of a grant it no longer holds, and the next rule to ask answers with them — the
// failure in the direction that leaks.
func TestNarrowingDropsTheScopesOfDroppedRoles(t *testing.T) {
	t.Parallel()

	lead := leadOf(testdata.Vier, principal.KindInteractive, programmeOne, programmeTwo)

	narrowed := policy.Narrow(lead, []policy.Role{policy.RoleLecturer})

	if len(narrowed.RoleScopes) != 0 {
		t.Errorf("narrowing to LECTURER left %d programme scope(s) behind", len(narrowed.RoleScopes))
	}
	if policy.MayPlanProgramme(narrowed, programmeOne) {
		t.Error("an actor narrowed away from PROGRAMME_LEAD may still plan its programmes")
	}

	// Keeping the role keeps its scopes, or the preview would be useless for the role it
	// previews.
	kept := policy.Narrow(lead, []policy.Role{policy.RoleProgrammeLead})
	if !policy.MayPlanProgramme(kept, programmeOne) {
		t.Error("narrowing to PROGRAMME_LEAD lost the programmes it is scoped to")
	}
}

// Narrowing can only ever remove — the property the whole mechanism rests on, extended to the
// scopes. It is what makes it safe to drive from an unverified header.
func TestNarrowingNeverAddsAScope(t *testing.T) {
	t.Parallel()

	lead := leadOf(testdata.Vier, principal.KindInteractive, programmeOne)

	for _, selection := range [][]policy.Role{
		{},
		{policy.RoleLecturer},
		{policy.RoleProgrammeLead},
		{policy.RoleDeansOffice},
		policy.AllRoles(),
	} {
		narrowed := policy.Narrow(lead, selection)
		before := policy.PlanningScope(lead)
		after := policy.PlanningScope(narrowed)

		if after.All && !before.All {
			t.Errorf("narrowing to %v produced a scope reaching every programme", selection)
		}
		for _, id := range after.IDs {
			if !before.Allows(id) {
				t.Errorf("narrowing to %v added the programme %s", selection, id)
			}
		}
	}
}

// The guard and the filter are two renderings of one rule — one ends up in a WHERE clause, the
// other checks a row already in hand — and drift between them is the realistic way this design
// fails. Asserted over the full cartesian product, exactly as the wish rule is.
func TestPlanningGuardAndScopeAgree(t *testing.T) {
	t.Parallel()

	programmes := []uuid.UUID{programmeOne, programmeTwo, uuid.New(), uuid.Nil}

	actors := []struct {
		name  string
		actor principal.Actor
	}{
		{"anonymous", principal.Actor{}},
		{"lecturer", testdata.Eins.Actor(principal.KindInteractive, string(policy.RoleLecturer))},
		{"unscoped lead", leadOf(testdata.Vier, principal.KindInteractive)},
		{"lead of one", leadOf(testdata.Vier, principal.KindInteractive, programmeOne)},
		{"lead of both", leadOf(testdata.Vier, principal.KindInteractive, programmeOne, programmeTwo)},
		{"lead via token", leadOf(testdata.Vier, principal.KindToken, programmeOne)},
		{"dean's office", testdata.Fuenf.Actor(principal.KindInteractive, string(policy.RoleDeansOffice))},
		{"admin", testdata.Sechs.Actor(principal.KindInteractive, string(policy.RoleAdmin))},
		{"lead and dean's office", func() principal.Actor {
			a := leadOf(testdata.Vier, principal.KindInteractive, programmeOne)
			a.Roles = append(a.Roles, string(policy.RoleDeansOffice))
			return a
		}()},
	}

	for _, tc := range actors {
		for _, programme := range programmes {
			guard := policy.MayPlanProgramme(tc.actor, programme)
			filter := policy.PlanningScope(tc.actor).Allows(programme)
			if guard != filter {
				t.Errorf("%s / %s: the guard says %v and the filter says %v. One of them ends "+
					"up in a WHERE clause and the other checks a row already read; they cannot "+
					"disagree.", tc.name, programme, guard, filter)
			}
		}
	}
}

// The nil programme is not a programme. Without the guard, an actor with an empty scope and a
// caller passing a zero uuid would meet in the middle.
func TestTheNilProgrammeIsNeverPlannable(t *testing.T) {
	t.Parallel()

	lead := leadOf(testdata.Vier, principal.KindInteractive, uuid.Nil)

	if policy.MayPlanProgramme(lead, uuid.Nil) {
		t.Error("the nil programme is plannable")
	}
	if !policy.PlanningScope(lead).Empty() {
		t.Error("a scope containing only the nil programme does not read as empty")
	}
}

// Planning is not confidential and not personnel data, so it is reachable through both doors —
// unlike reading somebody else's unpublished wish. A colleague evaluating the demand of their
// programme from a script is a use this API exists for.
func TestPlanningWorksThroughBothDoors(t *testing.T) {
	t.Parallel()

	for _, kind := range []principal.Kind{principal.KindInteractive, principal.KindToken} {
		lead := leadOf(testdata.Vier, kind, programmeOne)
		if !policy.MayPlanProgramme(lead, programmeOne) {
			t.Errorf("a programme lead authenticated by %s may not plan their own programme", kind)
		}
	}
}
