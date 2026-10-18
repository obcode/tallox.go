package policy_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
	"github.com/obcode/tallox.go/internal/testdata"
)

// The two forms of the rule over the full cartesian product, like the wish and the competence
// rule: every role combination and door, own and somebody else's, in no group, in the caller's
// group, in another one and in both.
func TestDesiredHoursGuardAndFilterAgree(t *testing.T) {
	t.Parallel()

	owners := []uuid.UUID{testdata.Eins.ID(), testdata.Zwei.ID()}
	memberships := [][]uuid.UUID{nil, {groupOne}, {groupTwo}, {groupOne, groupTwo}}

	checked := 0
	for _, actor := range everyActor() {
		for _, owner := range owners {
			for _, memberOf := range memberships {
				d := policy.DesiredHours{OwnerID: owner, MemberOf: memberOf}
				guard := policy.CanSeeDesiredHours(actor, d)
				filter := policy.DesiredHoursVisibility(actor).Matches(d)
				checked++
				if guard != filter {
					t.Errorf("guard and filter disagree for %s roles=%v scopes=%v on owner=%s "+
						"memberOf=%v: CanSeeDesiredHours=%v, Matches=%v",
						actor, actor.Roles, actor.RoleScopes, owner, memberOf, guard, filter)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("the cartesian product was empty — this test checked nothing")
	}
}

// Who reads somebody else's figure, said as cases rather than derived.
func TestWhoReadsSomebodyElsesDesiredHours(t *testing.T) {
	t.Parallel()

	groupLead := testdata.Eins.Actor(principal.KindInteractive, string(policy.RoleSubjectGroupLead))
	groupLead.RoleScopes = []principal.RoleScope{
		{Role: string(policy.RoleSubjectGroupLead), SubjectGroupID: groupOne},
	}
	groupLeadByToken := groupLead
	groupLeadByToken.Kind = principal.KindToken
	programmeLead := testdata.Eins.Actor(principal.KindInteractive, string(policy.RoleProgrammeLead))
	programmeLead.RoleScopes = []principal.RoleScope{
		{Role: string(policy.RoleProgrammeLead), ProgrammeID: programmeOne},
	}
	unscopedLead := testdata.Eins.Actor(principal.KindInteractive, string(policy.RoleSubjectGroupLead))
	deans := testdata.Eins.Actor(principal.KindInteractive, string(policy.RoleDeansOffice))
	deansByToken := testdata.Eins.Actor(principal.KindToken, string(policy.RoleDeansOffice))
	admin := testdata.Eins.Actor(principal.KindInteractive, string(policy.RoleAdmin))
	colleague := testdata.Eins.Actor(principal.KindInteractive, string(policy.RoleLecturer))

	inGroupOne := policy.DesiredHours{OwnerID: testdata.Zwei.ID(), MemberOf: []uuid.UUID{groupOne}}
	inGroupTwo := policy.DesiredHours{OwnerID: testdata.Zwei.ID(), MemberOf: []uuid.UUID{groupTwo}}

	for _, tc := range []struct {
		name  string
		actor principal.Actor
		d     policy.DesiredHours
		want  bool
	}{
		{"the lead of the person's group", groupLead, inGroupOne, true},
		{"the lead of another group", groupLead, inGroupTwo, false},
		{"the lead of the person's group, through a token", groupLeadByToken, inGroupOne, false},
		{"a lead nobody has given a group yet", unscopedLead, inGroupOne, false},
		{"a programme lead", programmeLead, inGroupOne, false},
		{"the dean's office", deans, inGroupTwo, true},
		{"the dean's office, through a token", deansByToken, inGroupTwo, false},
		{"the administration", admin, inGroupOne, false},
		{"a colleague", colleague, inGroupOne, false},
		{"anonymous", principal.Anonymous, inGroupOne, false},
	} {
		if got := policy.CanSeeDesiredHours(tc.actor, tc.d); got != tc.want {
			t.Errorf("%s: CanSeeDesiredHours = %v, want %v", tc.name, got, tc.want)
		}
	}

	// One's own, through either door, whatever one holds.
	own := policy.DesiredHours{OwnerID: testdata.Eins.ID()}
	for _, actor := range []principal.Actor{colleague, groupLeadByToken, deansByToken} {
		if !policy.CanSeeDesiredHours(actor, own) {
			t.Errorf("%s cannot read their own figure", actor)
		}
	}
}
