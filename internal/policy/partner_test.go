package policy_test

import (
	"testing"
	"time"

	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
	"github.com/obcode/tallox.go/internal/testdata"
)

var publishedAt = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

func partner(kind principal.Kind, roles ...string) principal.Actor {
	a := testdata.Eins.Actor(kind, append([]string{string(policy.RolePartnerReader)}, roles...)...)
	a.RoleScopes = []principal.RoleScope{{Role: string(policy.RolePartnerReader), ProgrammeID: programmeOne}}
	return a
}

// Held alone, the role narrows; held beside any other, it changes nothing.
func TestThePartnerRoleNarrowsOnlyWhenHeldAlone(t *testing.T) {
	t.Parallel()

	alone := partner(principal.KindInteractive)
	scope := policy.DemandReadScope(alone)
	if scope.All || !scope.Allows(programmeOne) || scope.Allows(programmeTwo) {
		t.Errorf("a partner of programme one reads %+v, want programme one only", scope)
	}

	unassigned := testdata.Eins.Actor(principal.KindInteractive, string(policy.RolePartnerReader))
	if !policy.DemandReadScope(unassigned).Empty() {
		t.Error("a partner nobody has assigned a programme reads something; the empty set is nothing")
	}

	colleague := partner(principal.KindInteractive, string(policy.RoleLecturer))
	if !policy.DemandReadScope(colleague).All {
		t.Error("a partner who also teaches here is narrowed; the colleague's reach has to win")
	}
	if !policy.DemandReadScope(testdata.Eins.Actor(principal.KindInteractive)).All {
		t.Error("an account without a role is narrowed; only the partner role narrows")
	}
}

// What a partner reads of the assignments: the published ones of its programmes, through either
// door, and nothing before publication.
func TestAPartnerReadsThePublishedAssignmentsOfItsProgrammes(t *testing.T) {
	t.Parallel()

	published := policy.SemesterState{Phase: policy.PhaseAssignment, AssignmentsPublishedAt: publishedAt}
	unpublished := policy.SemesterState{Phase: policy.PhaseAssignment}
	mine := policy.Assignment{ProgrammeID: programmeOne}
	other := policy.Assignment{ProgrammeID: programmeTwo}

	for _, kind := range []principal.Kind{principal.KindInteractive, principal.KindToken} {
		p := partner(kind)
		for _, tc := range []struct {
			name string
			s    policy.SemesterState
			a    policy.Assignment
			want bool
		}{
			{"published, its programme", published, mine, true},
			{"published, another programme", published, other, false},
			{"unpublished, its programme", unpublished, mine, false},
		} {
			if got := policy.CanSeeAssignment(p, tc.s, tc.a); got != tc.want {
				t.Errorf("%s via %s: CanSeeAssignment = %v, want %v", tc.name, kind, got, tc.want)
			}
			if got := policy.AssignmentVisibility(p, tc.s).Matches(tc.a); got != tc.want {
				t.Errorf("%s via %s: AssignmentVisibility.Matches = %v, want %v", tc.name, kind, got, tc.want)
			}
		}
	}
}

// No wishes, not even published ones; no self-service.
func TestAPartnerReadsNoWishesAndServesNothing(t *testing.T) {
	t.Parallel()

	p := partner(principal.KindInteractive)
	published := policy.SemesterState{Phase: policy.PhaseAssignment, WishesPublishedAt: publishedAt}
	if policy.CanSeeWish(p, published, policy.Wish{OwnerID: testdata.Zwei.ID(), ProgrammeID: programmeOne}) {
		t.Error("a partner reads a published wish")
	}
	if policy.MayServeOneself(p) {
		t.Error("a partner may join subject groups or state competences")
	}
	if !policy.MayServeOneself(partner(principal.KindInteractive, string(policy.RoleLecturer))) {
		t.Error("a partner who also teaches here may not serve themselves")
	}
	for _, phase := range policy.AllPhases() {
		for _, area := range policy.AllWriteAreas() {
			if policy.MayWriteInPhase(area, phase, p) {
				t.Errorf("a partner may write %s in %s", area, phase)
			}
		}
	}
}
