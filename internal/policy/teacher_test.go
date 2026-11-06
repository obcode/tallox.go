package policy_test

import (
	"testing"

	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
)

// Entering a teacher by hand: the dean's office, or a subject group lead with a group, or a
// programme lead with a programme, and only with full reach.
func TestEnteringATeacherIsForTheLeadsAndTheDeansOffice(t *testing.T) {
	t.Parallel()

	for _, c := range callers() {
		got := policy.MayEnterTeacher(c.actor)

		roles := policy.RolesOf(c.actor)
		want := c.actor.ActsWithFullReach() &&
			(roles.Has(policy.RoleDeansOffice) ||
				((roles.Has(policy.RoleSubjectGroupLead) || roles.Has(policy.RoleProgrammeLead)) &&
					c.scoped != ""))
		if got != want {
			t.Errorf("%s %s scoped=%q: MayEnterTeacher = %v, want %v",
				c.label, c.door, c.scoped, got, want)
		}
	}

	if policy.MayEnterTeacher(principal.Anonymous) {
		t.Error("an anonymous caller may enter a teacher")
	}
}
