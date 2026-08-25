package policy_test

import (
	"strings"
	"testing"

	"github.com/obcode/tallox.go/internal/golden"
	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
	"github.com/obcode/tallox.go/internal/testdata"
)

// TestAssignmentMatrix renders who may act in which subject group.
//
// A third table beside the wish matrix and the planning matrix, for the reason the planning
// matrix gives for being a second one: the rules answer different questions, and each is one
// page and one slide.
//
// The assignment phase itself does not exist yet, and this is rendered anyway — the same
// argument that put the wish matrix in week one. The scope is already deciding something real,
// namely which unpublished wishes a subject group lead reads, and a scope with no rendered rule
// is a scope nobody has reviewed. It is also the table that answers the support question this
// migration creates: "why can my colleague not see their own subject group".
//
// Re-record with: go test ./internal/policy/ -update-golden — and then read the diff.
func TestAssignmentMatrix(t *testing.T) {
	t.Parallel()

	golden.Assert(t, "assignment_matrix", renderAssignmentMatrix())
}

func renderAssignmentMatrix() string {
	var b strings.Builder

	b.WriteString(assignmentPreamble)

	header := assignmentRow("Role", "Door", "Scoped to", "own", "other", "Query scope")
	b.WriteString(header)
	b.WriteString(rule(header))

	type situation struct {
		label  string
		scoped []string
		actor  principal.Actor
	}

	var rows []situation
	for _, role := range policy.AllRoles() {
		for _, door := range []struct {
			label string
			kind  principal.Kind
		}{
			{"interactive", principal.KindInteractive},
			{"token", principal.KindToken},
		} {
			plain := testdata.Drei.Actor(door.kind, string(role))
			rows = append(rows, situation{label: string(role), actor: plain})

			// Only one role has a subject group dimension. Showing the scoped variant for the
			// others would suggest they have one.
			if role == policy.RoleSubjectGroupLead {
				rows = append(rows, situation{
					label:  string(role),
					scoped: []string{"one"},
					actor:  headOf(testdata.Drei, door.kind, groupOne),
				})
				rows = append(rows, situation{
					label:  string(role),
					scoped: []string{"one, two"},
					actor:  headOf(testdata.Drei, door.kind, groupOne, groupTwo),
				})
			}
		}
	}

	rows = append([]situation{{
		label: "(not signed in)",
		actor: principal.Anonymous,
	}}, rows...)

	previous := ""
	for _, s := range rows {
		if previous != "" && s.label != previous {
			b.WriteString("\n")
		}
		previous = s.label

		door := "—"
		if s.actor.Kind != "" {
			door = string(s.actor.Kind)
		}

		scoped := "—"
		if len(s.scoped) > 0 {
			scoped = strings.Join(s.scoped, ", ")
		}

		b.WriteString(assignmentRow(
			s.label,
			door,
			scoped,
			answer(policy.MayActInSubjectGroup(s.actor, groupOne)),
			answer(policy.MayActInSubjectGroup(s.actor, groupTwo)),
			assignmentScopeLabel(policy.AssignmentScope(s.actor)),
		))
	}

	return b.String()
}

func assignmentScopeLabel(s policy.SubjectGroupScope) string {
	switch {
	case s.All:
		return "every group"
	case len(s.IDs) == 0:
		return "none"
	case len(s.IDs) == 1:
		return "1 group"
	default:
		return "2 groups"
	}
}

const assignmentPreamble = `Subject groups — who may act in which one
=========================================

Generated from internal/policy (TestAssignmentMatrix). Do not edit by hand:

    go test ./internal/policy/ -update-golden

The rule
--------

Somebody may act in a subject group if

  · they are the dean's office — every group, including one created tomorrow, or
  · they lead that subject group, and no others.

Acting means filling the group's instances, and — before publication — reading the wishes on
them. Nobody else, through either door.

Subject groups have no semester
-------------------------------

A subject group is a statement about a subject, not about a plan: mathematics, software,
technical computer science. It is not copied between semesters and it survives every change of
examination regulations. So the subject group of anything planned is derived through the
module — instance part → course instance → module → subject group — and never stored beside
it.

That has a consequence worth stating before somebody meets it: re-cutting a group, which the
faculty already expects to do, moves modules with an UPDATE and therefore changes
**retroactively** who may read the unpublished wishes on them. This is the intended behaviour
— whoever is responsible now is who may look now — and it is the reason the responsibility is
derived rather than copied onto the wish.

Membership is not on this table
-------------------------------

Being in a subject group and leading one are different things. Membership says which subjects
a colleague works in; it is what the wish screen offers first, and it grants nothing here.

The kickoff sentence "jeder in einer Fachgruppe müsste alles lesen können" is about planning
data. It is deliberately not extended to unpublished wishes, because the
first-come-first-served race the confidentiality rule exists to end plays out *inside* a
subject group — the colleague who has taught the subject for years is in it — so a membership
that granted wish visibility would switch the rule off exactly where it is needed.

The one that surprises people
-----------------------------

**A subject group lead who has not been assigned a group may do nothing.** Not everything.

The same reading person_programme_scope settled for study programmes, and it is worth repeating
because it is the reading that is wrong everywhere else in this system. An empty token scope
list and an empty role selection both mean "unrestricted", because both are mechanisms that can
only ever remove. A subject group scope is not a narrowing of the grant; it is the grant's
subject. The role fills the instances of ONE group, and the role that means all of them is the
dean's office.

Read the other way, the release that introduced subject groups would have been the moment every
existing lead silently gained faculty-wide access to other people's unpublished wishes.

What such a person is shown is not "you may not do this" — which would send them to ask for a
role they already hold — but "your subject group leadership has not been assigned to a subject
group yet".

The columns
-----------

  Scoped to     Which subject groups this person has been assigned.
  own / other   May they act in group one / group two?
  Query scope   The same rule as a query restriction, and what the interface reads to decide
                which groups to offer at all. "every group" is not the list of groups that
                exist today — a group created afterwards is in it, which matters here because
                splitting a group creates one.

Notes
-----

  · ADMIN acts in no group, the same decision the wish rule and the planning rule both make:
    running the system is a different job from planning with it.
  · A programme lead declares instances and does not fill them, so it acts in no group here.
  · Combinations of roles are not listed. Somebody holding several gets the union — checked
    over the complete cartesian product in TestAssignmentGuardAndScopeAgree.
  · Both doors reach equally far. What a subject group lead may *see* through a token is a
    different question, decided by the wish rule and not by this one.

`

// widths of its own, because the columns are not the other matrices' columns.
var assignmentWidths = []int{20, 14, 12, 6, 7, 16}

func assignmentRow(cells ...string) string {
	var b strings.Builder
	for i, c := range cells {
		if i == len(cells)-1 {
			b.WriteString(c)
			break
		}
		b.WriteString(pad(c, assignmentWidths[i]))
	}
	b.WriteString("\n")
	return b.String()
}
