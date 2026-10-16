package policy_test

import (
	"testing"
	"time"

	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
	"github.com/obcode/tallox.go/internal/testdata"
)

// TestSemesterRulesByRoleAndDoor is the whole access surface of the semester workflow, as a
// table.
//
// Written role by role and door by door rather than as a handful of representative cases,
// because the interesting content is the *absences*: ADMIN not being able to switch a phase
// and PROGRAMME_LEAD not being able to either are decisions, and a decision that is not
// asserted anywhere is one that gets quietly reversed by the next person who finds it
// inconvenient.
func TestSemesterRulesByRoleAndDoor(t *testing.T) {
	t.Parallel()

	type expectation struct {
		read       bool
		administer bool
		publish    bool
	}

	tests := []struct {
		name  string
		actor principal.Actor
		want  expectation
	}{
		{
			name:  "anonymous",
			actor: principal.Anonymous,
			want:  expectation{},
		},
		{
			name:  "a lecturer, in the browser",
			actor: testdata.Eins.Actor(principal.KindInteractive, string(policy.RoleLecturer)),
			// Reads, and that is the point: "may I enter my wishes yet" is the phase, and a
			// lecturer who cannot see it gets a tool that refuses writes without saying why.
			want: expectation{read: true},
		},
		{
			name:  "a lecturer, with a token",
			actor: testdata.Eins.Actor(principal.KindToken, string(policy.RoleLecturer)),
			want:  expectation{read: true},
		},
		{
			name: "a programme lead declares demand within a phase, and does not end one",
			actor: testdata.Vier.Actor(principal.KindInteractive,
				string(policy.RoleProgrammeLead)),
			want: expectation{read: true},
		},
		{
			name: "a subject group lead, likewise",
			actor: testdata.Vier.Actor(principal.KindInteractive,
				string(policy.RoleSubjectGroupLead)),
			want: expectation{read: true},
		},
		{
			name: "the dean's office runs the process, in the browser",
			actor: testdata.Vier.Actor(principal.KindInteractive,
				string(policy.RoleDeansOffice)),
			want: expectation{read: true, administer: true, publish: true},
		},
		{
			name: "the dean's office with a token: switches phases, does not publish",
			// The one asymmetry in this file. Publishing cannot be undone and it is the moment
			// the confidentiality rule stops applying, so it wants somebody present rather
			// than a string in a script.
			actor: testdata.Vier.Actor(principal.KindToken, string(policy.RoleDeansOffice)),
			want:  expectation{read: true, administer: true},
		},
		{
			name: "an administrator runs the system and does not plan with it",
			// The same line the wish rule draws. Somebody who genuinely has to advance a phase
			// grants themselves DEANS_OFFICE, visibly and with an expiry.
			actor: testdata.Fuenf.Actor(principal.KindInteractive, string(policy.RoleAdmin)),
			want:  expectation{read: true},
		},
		{
			name: "holding both, the dean's office half is what counts",
			actor: testdata.Fuenf.Actor(principal.KindInteractive,
				string(policy.RoleAdmin), string(policy.RoleDeansOffice)),
			want: expectation{read: true, administer: true, publish: true},
		},
		{
			name:  "a grant this build does not know is not a grant",
			actor: testdata.Eins.Actor(principal.KindInteractive, "DEANS_OFFICE_JUNIOR"),
			want:  expectation{read: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := policy.MayReadSemesters(tt.actor); got != tt.want.read {
				t.Errorf("MayReadSemesters = %v, want %v", got, tt.want.read)
			}
			if got := policy.MayAdministerSemesters(tt.actor); got != tt.want.administer {
				t.Errorf("MayAdministerSemesters = %v, want %v", got, tt.want.administer)
			}
			if got := policy.MayPublishWishes(tt.actor); got != tt.want.publish {
				t.Errorf("MayPublishWishes = %v, want %v", got, tt.want.publish)
			}
		})
	}
}

// TestPublishingIsNeverBroaderThanAdministering pins the containment between the two write
// rules, exhaustively rather than by example.
//
// Publishing is administering plus a door. If a change ever made somebody able to publish who
// cannot switch a phase, the semester workflow would have a back door into its own most
// consequential action — and it would not be visible in the table above, which reads role by
// role rather than rule against rule.
func TestPublishingIsNeverBroaderThanAdministering(t *testing.T) {
	t.Parallel()

	kinds := []principal.Kind{
		principal.KindNone,
		principal.KindInteractive,
		principal.KindToken,
	}

	for _, kind := range kinds {
		for _, role := range policy.AllRoles() {
			actor := testdata.Eins.Actor(kind, string(role))
			if kind == principal.KindNone {
				actor = principal.Anonymous
			}

			if policy.MayPublishWishes(actor) && !policy.MayAdministerSemesters(actor) {
				t.Errorf("%s via %q may publish but may not administer", role, kind)
			}
		}
	}
}

func TestPhaseMayMoveTo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		from  policy.Phase
		to    policy.Phase
		allow bool
	}{
		{
			name:  "forward one step",
			from:  policy.PhaseDemandPlanning,
			to:    policy.PhaseWishes,
			allow: true,
		},
		{
			name: "backward one step, because reopening a plan is a normal thing to do",
			from: policy.PhaseAssignment,
			to:   policy.PhaseWishes, allow: true,
		},
		{
			name:  "out of FINAL, which is a correction and not a corruption of the record",
			from:  policy.PhaseFinal,
			to:    policy.PhaseReview,
			allow: true,
		},
		{
			name:  "from the assignment into the review",
			from:  policy.PhaseAssignment,
			to:    policy.PhaseReview,
			allow: true,
		},
		{
			name: "past the review straight to the close",
			from: policy.PhaseAssignment,
			to:   policy.PhaseFinal,
		},
		{
			name: "skipping the wish phase",
			from: policy.PhaseDemandPlanning,
			to:   policy.PhaseAssignment,
		},
		{
			name: "straight to the end",
			from: policy.PhaseDemandPlanning,
			to:   policy.PhaseFinal,
		},
		{
			name: "to itself is not a step",
			// A no-op that succeeded would appear in the audit log as a decision somebody made.
			from: policy.PhaseWishes,
			to:   policy.PhaseWishes,
		},
		{
			name: "from a phase this build does not know",
			from: policy.Phase("PLANNING_RETREAT"),
			to:   policy.PhaseWishes,
		},
		{
			name: "to a phase this build does not know",
			from: policy.PhaseWishes,
			to:   policy.Phase("PLANNING_RETREAT"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.from.MayMoveTo(tt.to); got != tt.allow {
				t.Errorf("%s.MayMoveTo(%s) = %v, want %v", tt.from, tt.to, got, tt.allow)
			}
		})
	}
}

// TestNeighboursAgreeWithMayMoveTo keeps the buttons and the rule the same thing.
//
// reachablePhases is what an interface renders from, and MayMoveTo is what the mutation
// enforces. They are one function and its derivation here; the test is what stops somebody
// "optimising" Neighbours into its own list.
func TestNeighboursAgreeWithMayMoveTo(t *testing.T) {
	t.Parallel()

	for _, from := range policy.AllPhases() {
		offered := map[policy.Phase]bool{}
		for _, to := range from.Neighbours() {
			offered[to] = true
		}

		for _, to := range policy.AllPhases() {
			if offered[to] != from.MayMoveTo(to) {
				t.Errorf("from %s: Neighbours offers %s = %v, MayMoveTo says %v",
					from, to, offered[to], from.MayMoveTo(to))
			}
		}
	}
}

// TestEveryPhaseIsReachable is a sanity check on the process as a whole: no phase is a
// dead end, and none is unreachable.
//
// A phase nobody can leave would strand a semester in it with no way out but psql on a host
// that is only reachable through a VPN.
func TestEveryPhaseIsReachable(t *testing.T) {
	t.Parallel()

	for _, phase := range policy.AllPhases() {
		if len(phase.Neighbours()) == 0 {
			t.Errorf("%s has no neighbours — a semester in it could not be moved", phase)
		}
	}
}

// TestVisibilityDoesNotDependOnThePhase is asserted in wish_test.go. This one is its
// counterpart for the write side: the phase and the publication are independent, so no
// combination of them is refused here.
//
// The two halves have to stay separable — the wish phase can close without publishing, and
// publication can happen while the assignment runs — and a constraint tying them together is
// the tempting simplification that would make one of those impossible.
func TestPublishingIsIndependentOfThePhase(t *testing.T) {
	t.Parallel()

	actor := testdata.Vier.Actor(principal.KindInteractive, string(policy.RoleDeansOffice))
	if !policy.MayPublishWishes(actor) {
		t.Fatal("the dean's office cannot publish at all — the loop below would prove nothing")
	}

	published := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)

	for _, phase := range policy.AllPhases() {
		// The permission does not consult the phase. Asserted inside the loop rather than
		// once, so that adding a phase argument to the rule makes this stop compiling instead
		// of silently covering one case.
		if !policy.MayPublishWishes(actor) {
			t.Errorf("the dean's office may not publish while the semester is in %s", phase)
		}

		// And neither does the state. Both directions, because the tempting simplification is
		// a constraint tying the two together, and it would break whichever of the two the
		// faculty needs first: closing the wish phase without publishing, or publishing while
		// the assignment already runs.
		if (policy.SemesterState{Phase: phase}).WishesPublished() {
			t.Errorf("a semester in %s with no timestamp reads as published", phase)
		}
		if !(policy.SemesterState{Phase: phase, WishesPublishedAt: published}).WishesPublished() {
			t.Errorf("a semester in %s with a timestamp reads as unpublished", phase)
		}
	}
}
