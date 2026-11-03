package policy_test

import (
	"testing"

	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
	"github.com/obcode/tallox.go/internal/testdata"
)

// observerActors are the three doors an observer comes through.
func observerActors(roles ...string) map[string]principal.Actor {
	all := append([]string{string(policy.RoleObserver)}, roles...)
	return map[string]principal.Actor{
		"interactive": testdata.Eins.Actor(principal.KindInteractive, all...),
		"token":       testdata.Eins.Actor(principal.KindToken, all...),
		"full reach":  testdata.Eins.FullReachActor(all...),
	}
}

// Held alone, the role reads all programmes' demand — it is not the partner's narrowing.
func TestAnObserverReadsEveryProgramme(t *testing.T) {
	t.Parallel()

	for door, a := range observerActors() {
		if !policy.ObserverOnly(a) || !policy.Onlooker(a) {
			t.Errorf("via %s: an account holding only OBSERVER is not an observer", door)
		}
		if !policy.DemandReadScope(a).All {
			t.Errorf("via %s: an observer is narrowed to programmes; it follows the whole planning", door)
		}
	}
}

// No other people's statements in any state — not even published ones — and one's own stays.
func TestAnObserverReadsNoOtherPeoplesStatements(t *testing.T) {
	t.Parallel()

	everything := policy.SemesterState{
		Phase:                   policy.PhaseReview,
		WishesPublishedAt:       publishedAt,
		AssignmentsPublishedAt:  publishedAt,
		DesiredHoursFrozenAt:    publishedAt,
		DesiredHoursPublishedAt: publishedAt,
	}
	theirs := policy.Wish{OwnerID: testdata.Zwei.ID(), ProgrammeID: programmeOne}
	hours := policy.DesiredHours{OwnerID: testdata.Zwei.ID()}
	load := policy.AssignedLoad{OwnerID: testdata.Zwei.ID()}

	for door, a := range observerActors() {
		if policy.CanSeeWish(a, everything, theirs) || policy.WishVisibility(a, everything).Matches(theirs) {
			t.Errorf("via %s: an observer reads a published wish", door)
		}
		if !policy.CanSeeWish(a, everything, policy.Wish{OwnerID: testdata.Eins.ID()}) {
			t.Errorf("via %s: an observer cannot read its own wish", door)
		}
		if policy.CanSeeDesiredHours(a, everything, hours) || policy.DesiredHoursVisibility(a, everything).Matches(hours) {
			t.Errorf("via %s: an observer reads published desired hours", door)
		}
		if policy.CanSeeAssignedLoad(a, everything, load) || policy.AssignedLoadVisibility(a, everything).Matches(policy.DesiredHours{OwnerID: load.OwnerID}) {
			t.Errorf("via %s: an observer reads somebody's assigned load", door)
		}
		if policy.CanSeeCompetence(a, policy.Competence{OwnerID: testdata.Zwei.ID(), ProgrammeID: programmeOne}) {
			t.Errorf("via %s: an observer reads somebody's competence", door)
		}
	}
}

// The published assignments are what any account reads, and the observer is no exception; before
// publication there is nothing.
func TestAnObserverReadsThePublishedAssignments(t *testing.T) {
	t.Parallel()

	published := policy.SemesterState{Phase: policy.PhaseAssignment, AssignmentsPublishedAt: publishedAt}
	unpublished := policy.SemesterState{Phase: policy.PhaseAssignment}
	theirs := policy.Assignment{AssigneeID: testdata.Zwei.ID(), ProgrammeID: programmeTwo}

	for door, a := range observerActors() {
		if !policy.CanSeeAssignment(a, published, theirs) || !policy.AssignmentVisibility(a, published).Matches(theirs) {
			t.Errorf("via %s: an observer cannot read a published assignment", door)
		}
		if policy.CanSeeAssignment(a, unpublished, theirs) || policy.AssignmentVisibility(a, unpublished).Matches(theirs) {
			t.Errorf("via %s: an observer reads an unpublished assignment", door)
		}
	}
}

// Nothing written: no self-service, no write area, no desired hours, no import run.
func TestAnObserverWritesNothing(t *testing.T) {
	t.Parallel()

	open := policy.SemesterState{Phase: policy.PhaseWishes}
	for door, a := range observerActors() {
		if policy.MayServeOneself(a) {
			t.Errorf("via %s: an observer may join subject groups or state competences", door)
		}
		if policy.MayStateDesiredHours(a, open) {
			t.Errorf("via %s: an observer may state desired hours", door)
		}
		if policy.MayTriggerZPASync(a) {
			t.Errorf("via %s: an observer may start an import run", door)
		}
		for _, phase := range policy.AllPhases() {
			for _, area := range policy.AllWriteAreas() {
				if policy.MayWriteInPhase(area, phase, a) {
					t.Errorf("via %s: an observer may write %s in %s", door, area, phase)
				}
			}
		}
	}
}

// The import report is the one view behind a role that the observer reads — with full reach, like
// everybody who reads it.
func TestAnObserverReadsTheImportWithFullReach(t *testing.T) {
	t.Parallel()

	doors := observerActors()
	if !policy.MayReadZPAImport(doors["interactive"]) || !policy.MayReadZPAImport(doors["full reach"]) {
		t.Error("an observer cannot read the module import through a door with full reach")
	}
	if policy.MayReadZPAImport(doors["token"]) {
		t.Error("an observer reads the module import through a plain token")
	}
	for _, role := range []policy.Role{policy.RoleAdmin, policy.RoleDeansOffice} {
		a := testdata.Eins.Actor(principal.KindInteractive, string(role))
		if !policy.MayReadZPAImport(a) || !policy.MayTriggerZPASync(a) {
			t.Errorf("%s lost the import", role)
		}
	}
}

// Held beside another role it changes nothing: the colleague's reach wins.
func TestTheObserverRoleNarrowsOnlyWhenHeldAlone(t *testing.T) {
	t.Parallel()

	published := policy.SemesterState{Phase: policy.PhaseAssignment, WishesPublishedAt: publishedAt}
	theirs := policy.Wish{OwnerID: testdata.Zwei.ID(), ProgrammeID: programmeOne}
	for door, a := range observerActors(string(policy.RoleLecturer)) {
		if policy.Onlooker(a) {
			t.Errorf("via %s: an observer who also teaches here counts as an onlooker", door)
		}
		if !policy.MayServeOneself(a) {
			t.Errorf("via %s: an observer who also teaches here may not serve themselves", door)
		}
		if !policy.CanSeeWish(a, published, theirs) {
			t.Errorf("via %s: an observer who also teaches here cannot read a published wish", door)
		}
	}
}
