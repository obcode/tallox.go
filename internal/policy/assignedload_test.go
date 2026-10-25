package policy_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
	"github.com/obcode/tallox.go/internal/testdata"
)

// The two forms of the rule over the full cartesian product.
func TestAssignedLoadGuardAndFilterAgree(t *testing.T) {
	t.Parallel()

	now := time.Now()
	states := []policy.SemesterState{
		{Phase: policy.PhaseAssignment},
		{Phase: policy.PhaseAssignment, AssignmentsPublishedAt: now},
		// The desired hours published, the assignments not: the windows are independent.
		{Phase: policy.PhaseAssignment, DesiredHoursFrozenAt: now, DesiredHoursPublishedAt: now},
	}
	owners := []uuid.UUID{testdata.Eins.ID(), testdata.Zwei.ID()}
	memberships := [][]uuid.UUID{nil, {groupOne}, {groupTwo}, {groupOne, groupTwo}}

	checked := 0
	for _, actor := range everyActor() {
		for _, state := range states {
			for _, owner := range owners {
				for _, memberOf := range memberships {
					l := policy.AssignedLoad{OwnerID: owner, MemberOf: memberOf}
					guard := policy.CanSeeAssignedLoad(actor, state, l)
					filter := policy.AssignedLoadVisibility(actor, state).Matches(policy.DesiredHours(l))
					checked++
					if guard != filter {
						t.Errorf("guard and filter disagree for %s roles=%v scopes=%v on owner=%s "+
							"memberOf=%v state=%+v: CanSeeAssignedLoad=%v, Matches=%v",
							actor, actor.Roles, actor.RoleScopes, owner, memberOf, state, guard, filter)
					}
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("the cartesian product was empty — this test checked nothing")
	}
}

// Who reads somebody else's load before the assignments are published, said as cases. The lead of
// one of the person's groups does — that is the exception — and nobody else but the dean's office.
func TestWhoReadsSomebodyElsesAssignedLoad(t *testing.T) {
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
	deans := testdata.Eins.Actor(principal.KindInteractive, string(policy.RoleDeansOffice))
	colleague := testdata.Eins.Actor(principal.KindInteractive, string(policy.RoleLecturer))
	colleagueByToken := testdata.Eins.Actor(principal.KindToken, string(policy.RoleLecturer))
	partner := testdata.Eins.Actor(principal.KindInteractive, string(policy.RolePartnerReader))

	// A member of both groups: the lead of group one reads the load that includes group two.
	inBoth := policy.AssignedLoad{OwnerID: testdata.Zwei.ID(), MemberOf: []uuid.UUID{groupOne, groupTwo}}
	inGroupTwo := policy.AssignedLoad{OwnerID: testdata.Zwei.ID(), MemberOf: []uuid.UUID{groupTwo}}
	planning := policy.SemesterState{Phase: policy.PhaseAssignment}
	published := policy.SemesterState{Phase: policy.PhaseAssignment, AssignmentsPublishedAt: time.Now()}

	for _, tc := range []struct {
		name  string
		actor principal.Actor
		state policy.SemesterState
		l     policy.AssignedLoad
		want  bool
	}{
		{"the lead of one of the person's groups", groupLead, planning, inBoth, true},
		{"the lead of another group", groupLead, planning, inGroupTwo, false},
		{"the lead, through a token", groupLeadByToken, planning, inBoth, false},
		{"a programme lead", programmeLead, planning, inBoth, false},
		{"the dean's office", deans, planning, inGroupTwo, true},
		{"a colleague", colleague, planning, inBoth, false},
		{"a colleague, once published", colleague, published, inBoth, true},
		{"a colleague by token, once published", colleagueByToken, published, inBoth, false},
		{"a partner faculty, once published", partner, published, inBoth, false},
		{"anonymous, once published", principal.Anonymous, published, inBoth, false},
	} {
		if got := policy.CanSeeAssignedLoad(tc.actor, tc.state, tc.l); got != tc.want {
			t.Errorf("%s: CanSeeAssignedLoad = %v, want %v", tc.name, got, tc.want)
		}
	}

	own := policy.AssignedLoad{OwnerID: testdata.Eins.ID()}
	for _, actor := range []principal.Actor{colleague, colleagueByToken, groupLeadByToken} {
		if !policy.CanSeeAssignedLoad(actor, planning, own) {
			t.Errorf("%s (%s) cannot read their own load", actor, actor.Kind)
		}
	}
}
