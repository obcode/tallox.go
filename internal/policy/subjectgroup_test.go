package policy_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
	"github.com/obcode/tallox.go/internal/testdata"
)

// Two subject groups, fixed so the assertions can name them. Mathematics split into a classical
// and a machine-learning group is the faculty's own example of why there are two.
var (
	groupOne = uuid.MustParse("33333333-3333-4333-8333-333333333333")
	groupTwo = uuid.MustParse("44444444-4444-4444-8444-444444444444")
)

// headOf builds a subject group lead scoped to the given groups.
func headOf(p testdata.Persona, kind principal.Kind, groups ...uuid.UUID) principal.Actor {
	actor := p.Actor(kind, string(policy.RoleLecturer), string(policy.RoleSubjectGroupLead))
	for _, id := range groups {
		actor.RoleScopes = append(actor.RoleScopes, principal.RoleScope{
			Role:           string(policy.RoleSubjectGroupLead),
			SubjectGroupID: id,
		})
	}
	return actor
}

// memberOf builds somebody who is in a subject group but does not lead it.
//
// Membership is not carried on the actor at all — it is not a grant — so this is simply a
// lecturer. The function exists so that the test below reads as the sentence it is asserting.
func memberOf(p testdata.Persona, kind principal.Kind) principal.Actor {
	return p.Actor(kind, string(policy.RoleLecturer))
}

// The rule migration 14 exists to make enforceable. Until now SUBJECT_GROUP_LEAD was a role
// with no subject, and role.go said in so many words that anything depending on the scope had
// to wait for it rather than approximate it.
func TestASubjectGroupLeadActsOnlyInTheirOwnGroups(t *testing.T) {
	t.Parallel()

	lead := headOf(testdata.Drei, principal.KindInteractive, groupOne)

	if !policy.MayActInSubjectGroup(lead, groupOne) {
		t.Error("the lead of group one may not act in group one")
	}
	if policy.MayActInSubjectGroup(lead, groupTwo) {
		t.Error("the lead of group one may act in group two — the scope is not being applied")
	}
}

// The reading this file had to decide, and the one that is wrong everywhere else in the
// package: an unscoped grant permits nothing, not everything.
//
// Read the other way, the deploy of migration 14 would have been the moment every subject group
// lead silently gained faculty-wide access to other people's unpublished wishes.
func TestAnUnscopedSubjectGroupLeadActsNowhere(t *testing.T) {
	t.Parallel()

	lead := headOf(testdata.Drei, principal.KindInteractive)

	if policy.MayActInSubjectGroup(lead, groupOne) {
		t.Error("an unscoped subject group lead reaches a group")
	}
	if !policy.AssignmentScope(lead).Empty() {
		t.Error("an unscoped subject group lead does not read as empty")
	}
	if !policy.HoldsSubjectGroupLeadWithoutScope(lead) {
		t.Error("an unscoped lead is not recognised as waiting for an administrator")
	}
	if policy.AssignmentRefusal(lead) != policy.SubjectGroupScopeMissingReason {
		t.Error("an unscoped lead is told they may not, rather than what is missing — which " +
			"sends them to ask for a role they already hold")
	}
}

// Membership is not a permission, and this is the test that says so out loud.
//
// The kickoff sentence "jeder in einer Fachgruppe müsste alles lesen können" is about planning
// data. If membership granted what leadership grants, the confidentiality rule would switch
// itself off precisely inside the subject group — which is where the first-come-first-served
// race it exists to end actually happens.
func TestMembershipOfASubjectGroupGrantsNothing(t *testing.T) {
	t.Parallel()

	member := memberOf(testdata.Zwei, principal.KindInteractive)

	if policy.MayActInSubjectGroup(member, groupOne) {
		t.Error("being in a subject group is being allowed to act in it")
	}
	if !policy.AssignmentScope(member).Empty() {
		t.Error("a member's assignment scope is not empty")
	}
	if policy.HoldsSubjectGroupLeadWithoutScope(member) {
		t.Error("a member without the role is offered the sentence meant for an unassigned lead")
	}
}

// The dean's office reaches every group, and deliberately not by enumerating them: a group
// created after the query was built has to be included. The faculty expects to split groups in
// service, so this is not hypothetical.
func TestTheDeansOfficeReachesEverySubjectGroup(t *testing.T) {
	t.Parallel()

	dekanat := testdata.Fuenf.Actor(principal.KindInteractive, string(policy.RoleDeansOffice))
	scope := policy.AssignmentScope(dekanat)

	if !scope.All {
		t.Fatal("the dean's office does not reach every subject group")
	}
	if len(scope.IDs) != 0 {
		t.Error("the dean's office scope enumerates ids, which would be a snapshot")
	}
	if !policy.MayActInSubjectGroup(dekanat, uuid.New()) {
		t.Error("a subject group created after the fact is outside the dean's office's reach")
	}
}

// ADMIN is not on the list, the same decision the wish rule and the planning rule both make:
// running the system is a different job from planning with it.
func TestAdminActsInNoSubjectGroup(t *testing.T) {
	t.Parallel()

	admin := testdata.Sechs.Actor(principal.KindInteractive, string(policy.RoleAdmin))

	if policy.MayActInSubjectGroup(admin, groupOne) {
		t.Error("an administrator reaches a subject group by virtue of being an administrator")
	}
}

// Narrowing can only ever remove, extended to the subject group scopes. It is what makes the
// mechanism safe to drive from an unverified header.
func TestNarrowingNeverAddsASubjectGroupScope(t *testing.T) {
	t.Parallel()

	lead := headOf(testdata.Drei, principal.KindInteractive, groupOne)

	for _, selection := range [][]policy.Role{
		{},
		{policy.RoleLecturer},
		{policy.RoleSubjectGroupLead},
		{policy.RoleDeansOffice},
		policy.AllRoles(),
	} {
		narrowed := policy.Narrow(lead, selection)
		before := policy.AssignmentScope(lead)
		after := policy.AssignmentScope(narrowed)

		if after.All && !before.All {
			t.Errorf("narrowing to %v produced a scope reaching every subject group", selection)
		}
		for _, id := range after.IDs {
			if !before.Allows(id) {
				t.Errorf("narrowing to %v added the subject group %s", selection, id)
			}
		}
	}
}

// The guard and the filter are two renderings of one rule, and drift between them is the
// realistic way this design fails. Asserted over the full cartesian product, exactly as the
// planning rule and the wish rule are.
func TestAssignmentGuardAndScopeAgree(t *testing.T) {
	t.Parallel()

	groups := []uuid.UUID{groupOne, groupTwo, uuid.New(), uuid.Nil}

	actors := []struct {
		name  string
		actor principal.Actor
	}{
		{"anonymous", principal.Actor{}},
		{"lecturer", testdata.Eins.Actor(principal.KindInteractive, string(policy.RoleLecturer))},
		{"unscoped lead", headOf(testdata.Drei, principal.KindInteractive)},
		{"lead of one", headOf(testdata.Drei, principal.KindInteractive, groupOne)},
		{"lead of both", headOf(testdata.Drei, principal.KindInteractive, groupOne, groupTwo)},
		{"lead via token", headOf(testdata.Drei, principal.KindToken, groupOne)},
		{"dean's office", testdata.Fuenf.Actor(principal.KindInteractive, string(policy.RoleDeansOffice))},
		{"admin", testdata.Sechs.Actor(principal.KindInteractive, string(policy.RoleAdmin))},
		{"lead and dean's office", func() principal.Actor {
			a := headOf(testdata.Drei, principal.KindInteractive, groupOne)
			a.Roles = append(a.Roles, string(policy.RoleDeansOffice))
			return a
		}()},
		{"programme lead", leadOf(testdata.Vier, principal.KindInteractive, programmeOne)},
	}

	for _, tc := range actors {
		for _, group := range groups {
			guard := policy.MayActInSubjectGroup(tc.actor, group)
			filter := policy.AssignmentScope(tc.actor).Allows(group)
			if guard != filter {
				t.Errorf("%s / %s: the guard says %v and the filter says %v. One of them ends "+
					"up in a WHERE clause and the other checks a row already read; they cannot "+
					"disagree.", tc.name, group, guard, filter)
			}
		}
	}
}

// The nil subject group is not a subject group. Without the guard, an actor with an empty scope
// and a caller passing a zero uuid would meet in the middle.
func TestTheNilSubjectGroupIsNeverReachable(t *testing.T) {
	t.Parallel()

	lead := headOf(testdata.Drei, principal.KindInteractive, uuid.Nil)

	if policy.MayActInSubjectGroup(lead, uuid.Nil) {
		t.Error("the nil subject group is reachable")
	}
	if !policy.AssignmentScope(lead).Empty() {
		t.Error("a scope containing only the nil subject group does not read as empty")
	}
}

// The two scopes are about different things and must not read each other's targets. A scope row
// whose role string is wrong is malformed, and a malformed row grants nothing — which is the
// whole reason RoleScope names its targets rather than carrying one anonymous id.
func TestTheTwoScopesDoNotReadEachOther(t *testing.T) {
	t.Parallel()

	// A subject group id filed under the programme lead grant, and vice versa: what a row with
	// the wrong role string looks like.
	crossed := testdata.Drei.Actor(principal.KindInteractive,
		string(policy.RoleProgrammeLead), string(policy.RoleSubjectGroupLead))
	crossed.RoleScopes = []principal.RoleScope{
		{Role: string(policy.RoleProgrammeLead), SubjectGroupID: groupOne},
		{Role: string(policy.RoleSubjectGroupLead), ProgrammeID: programmeOne},
	}

	if !policy.PlanningScope(crossed).Empty() {
		t.Error("a programme scope naming a subject group is being read as a programme")
	}
	if !policy.AssignmentScope(crossed).Empty() {
		t.Error("a subject group scope naming a programme is being read as a subject group")
	}
	if policy.MayActInSubjectGroup(crossed, groupOne) {
		t.Error("a malformed scope granted a subject group")
	}
	if policy.MayPlanProgramme(crossed, programmeOne) {
		t.Error("a malformed scope granted a programme")
	}
}

// The act the faculty asked for: a lead files an unsorted module into her own group.
//
// The case that motivated the whole change is a study programme lead creating a local module a
// week before the wish round — it arrives in no subject group, and until now only an
// administrator could sort it. A module in no group reaches no lead at all, so the person who
// would fill its instances could not even see it on her screen.
func TestASubjectGroupLeadFilesAnUnsortedModuleIntoTheirOwnGroup(t *testing.T) {
	t.Parallel()

	lead := headOf(testdata.Drei, principal.KindInteractive, groupOne)

	if !policy.MayFileModule(lead, uuid.Nil, groupOne) {
		t.Error("the lead of group one may not file an unsorted module into group one")
	}
	if !policy.MayFileModule(lead, groupOne, uuid.Nil) {
		t.Error("the lead of group one may not take one of her own modules out again")
	}
	if !policy.MayFileModule(lead, groupOne, groupOne) {
		t.Error("re-filing into the same group is refused — which breaks every batch that " +
			"contains a module already filed correctly")
	}
}

// The half that makes filing safe to hand out: both ends of the move have to be in reach.
//
// Checking only the target would make "move this module into mine" a unilateral act against
// the group it came from. Two directions, and neither of them is allowed.
func TestASubjectGroupLeadCannotFileAcrossSomebodyElsesGroup(t *testing.T) {
	t.Parallel()

	lead := headOf(testdata.Drei, principal.KindInteractive, groupOne)

	if policy.MayFileModule(lead, groupTwo, groupOne) {
		t.Error("the lead of group one takes a module out of group two — filing is checking " +
			"only where the module is going")
	}
	if policy.MayFileModule(lead, groupOne, groupTwo) {
		t.Error("the lead of group one pushes a module into group two")
	}
}

// The same reading as everywhere else in this file, on the new act.
//
// The no-op is in here deliberately. Without the emptiness check, moving a module from no group
// to no group would come back true for everybody — and "yes, you may do nothing" is a row
// somebody later reads as "yes, you may do this".
func TestAnUnscopedSubjectGroupLeadFilesNothing(t *testing.T) {
	t.Parallel()

	lead := headOf(testdata.Drei, principal.KindInteractive)

	if policy.MayFileModule(lead, uuid.Nil, groupOne) {
		t.Error("an unscoped subject group lead files a module into a group")
	}
	if policy.MayFileModule(lead, uuid.Nil, uuid.Nil) {
		t.Error("an unscoped lead may perform the move that changes nothing")
	}
	if policy.ModuleFilingRefusal(lead) != policy.SubjectGroupScopeMissingReason {
		t.Error("an unscoped lead is told they may not file, rather than what is missing")
	}
}

// Filing is @interactiveOnly, and the rule says so rather than leaving it to the directive.
//
// Re-filing a module moves who may read the unpublished wishes on its instances. A long-lived
// token in a script that could do that would decouple "who changed who sees what" from any
// login event — the same argument the wish rule makes for collapsing the token door.
func TestFilingAModuleIsRefusedThroughTheTokenDoor(t *testing.T) {
	t.Parallel()

	for _, actor := range []struct {
		what string
		who  principal.Actor
	}{
		{"a scoped subject group lead", headOf(testdata.Drei, principal.KindToken, groupOne)},
		{"an administrator", testdata.Drei.Actor(principal.KindToken, string(policy.RoleAdmin))},
		{"the dean's office", testdata.Drei.Actor(principal.KindToken, string(policy.RoleDeansOffice))},
	} {
		if policy.MayFileModule(actor.who, uuid.Nil, groupOne) {
			t.Errorf("%s files a module through the token door", actor.what)
		}
	}
}

// The dean's office reaches every group here, as it does everywhere else.
//
// Through AssignmentScope rather than through a special case: it is the role that means "all
// subject groups", and a rule saying otherwise only here would be one nobody could remember.
func TestTheDeansOfficeFilesAcrossGroups(t *testing.T) {
	t.Parallel()

	deans := testdata.Drei.Actor(principal.KindInteractive, string(policy.RoleDeansOffice))

	if !policy.MayFileModule(deans, groupTwo, groupOne) {
		t.Error("the dean's office may not move a module between two groups")
	}
}

// The two forms of the filing rule have to agree, over everything.
//
// The realistic way a guard/filter pair breaks is that somebody changes one of them — the same
// hazard TestGuardAndFilterAgree watches for on the wish rule. Here the filter runs in the
// WHERE clause that decides which modules a batch is allowed to touch, and the guard answers
// for a row already in hand; a drift between them is a batch that writes rows the rule refuses,
// or refuses rows it permits.
func TestFilingGuardAndScopeAgree(t *testing.T) {
	t.Parallel()

	groups := []uuid.UUID{uuid.Nil, groupOne, groupTwo}

	for _, actor := range filingActors() {
		scope := policy.FilingScope(actor.who)
		for _, from := range groups {
			for _, to := range groups {
				guard := policy.MayFileModule(actor.who, from, to)
				filter := !scope.Empty() &&
					(from == uuid.Nil || scope.Allows(from)) &&
					(to == uuid.Nil || scope.Allows(to))

				if guard != filter {
					t.Errorf("%s filing %v→%v: guard says %v, scope says %v",
						actor.what, from, to, guard, filter)
				}
			}
		}
	}
}

// Every shape of actor the filing rule distinguishes, for the agreement test.
func filingActors() []struct {
	what string
	who  principal.Actor
} {
	out := []struct {
		what string
		who  principal.Actor
	}{
		{"anonymous", principal.Anonymous},
		{"a scoped lead", headOf(testdata.Drei, principal.KindInteractive, groupOne)},
		{"a lead scoped to both", headOf(testdata.Drei, principal.KindInteractive, groupOne, groupTwo)},
		{"an unscoped lead", headOf(testdata.Drei, principal.KindInteractive)},
		{"a scoped lead through a token", headOf(testdata.Drei, principal.KindToken, groupOne)},
	}
	for _, role := range policy.AllRoles() {
		for _, kind := range []principal.Kind{principal.KindInteractive, principal.KindToken} {
			out = append(out, struct {
				what string
				who  principal.Actor
			}{
				what: string(role) + " (" + string(kind) + ")",
				who:  testdata.Drei.Actor(kind, string(role)),
			})
		}
	}
	return out
}
