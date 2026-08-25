package policy_test

import (
	"testing"

	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
	"github.com/obcode/tallox.go/internal/testdata"
)

// TestAllRolesRoundTrip keeps the list, the parser and the stored strings in one piece.
//
// Role values travel as text: into the database, into the GraphQL enum, into the golden
// matrix. A role that is in AllRoles but not parseable, or vice versa, produces a grant that
// exists in one direction only — stored but never recognised, which reads as "my permissions
// vanished" and debugs as anything but.
func TestAllRolesRoundTrip(t *testing.T) {
	t.Parallel()

	seen := map[policy.Role]bool{}

	for _, r := range policy.AllRoles() {
		if seen[r] {
			t.Errorf("%s appears twice in AllRoles", r)
		}
		seen[r] = true

		parsed, ok := policy.ParseRole(string(r))
		if !ok {
			t.Errorf("%s is in AllRoles but ParseRole rejects it", r)
		}
		if parsed != r {
			t.Errorf("ParseRole(%q) = %q", r, parsed)
		}
		if string(r) == "" {
			t.Error("a role with an empty name would be granted by every empty string in the " +
				"database")
		}
	}

	for _, unknown := range []string{"", "lecturer", "Lecturer", "PLANNER", "ADMIN "} {
		if _, ok := policy.ParseRole(unknown); ok {
			t.Errorf("ParseRole accepted %q — role matching is exact, and case-insensitive "+
				"matching would make a lower-cased typo a grant", unknown)
		}
	}
}

// TestRolesOfDropsWhatItDoesNotKnow: the one place opaque grant strings acquire meaning, and
// the place where an unknown one has to lose it.
func TestRolesOfDropsWhatItDoesNotKnow(t *testing.T) {
	t.Parallel()

	actor := testdata.Drei.Actor(principal.KindInteractive,
		string(policy.RoleLecturer), "PLANNER", string(policy.RoleSubjectGroupLead))

	roles := policy.RolesOf(actor)

	if !roles.Has(policy.RoleLecturer) || !roles.Has(policy.RoleSubjectGroupLead) {
		t.Errorf("known grants were lost: %v", roles.Sorted())
	}
	if len(roles) != 2 {
		t.Errorf("RolesOf returned %v, want exactly the two known grants", roles.Sorted())
	}

	if roles := policy.RolesOf(principal.Anonymous); len(roles) != 0 {
		t.Errorf("the anonymous actor holds %v", roles.Sorted())
	}
}

// There used to be a RoleSet.Plans() here, and its removal is the point of this note.
//
// It answered "do these roles run the planning process" as a faculty-wide boolean, and the wish
// rule read it. That was correct only while there was nothing for a role to be scoped to: an IG
// lead has no business in IF wishes, and a mathematics lead none in the software subjects. Both
// roles are scoped now, so the question is not "does this person plan" but "does this person plan
// *this*" — which is policy.UnpublishedWishScope, and which cannot be a boolean.
//
// It is deleted rather than left unused deliberately. A faculty-wide predicate named after
// planning is exactly what the next rule reaches for, and the next rule would be wrong in the
// direction that leaks.

// TestSortedIsStable: the golden matrix and every log line render role sets, and a map
// iteration order would make both of them differ from run to run.
func TestSortedIsStable(t *testing.T) {
	t.Parallel()

	set := policy.RoleSet{
		policy.RoleAdmin:         true,
		policy.RoleLecturer:      true,
		policy.RoleProgrammeLead: true,
		policy.RoleDeansOffice:   false, // present in the map, not granted
	}

	first := set.Sorted()
	for range 20 {
		got := set.Sorted()
		if len(got) != len(first) {
			t.Fatalf("Sorted returned %v and %v on successive calls", first, got)
		}
		for i := range got {
			if got[i] != first[i] {
				t.Fatalf("Sorted returned %v and %v on successive calls", first, got)
			}
		}
	}

	want := []policy.Role{policy.RoleLecturer, policy.RoleProgrammeLead, policy.RoleAdmin}
	if len(first) != len(want) {
		t.Fatalf("Sorted() = %v, want %v — a false entry is not a grant", first, want)
	}
	for i := range want {
		if first[i] != want[i] {
			t.Errorf("Sorted() = %v, want %v (AllRoles order)", first, want)
		}
	}
}
