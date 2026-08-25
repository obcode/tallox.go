package policy_test

import (
	"strings"
	"testing"

	"github.com/obcode/tallox.go/internal/golden"
	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
	"github.com/obcode/tallox.go/internal/testdata"
)

// TestPlanningMatrix renders who may declare demand for which study programme.
//
// A second table rather than three more columns on the wish matrix, and the separation is
// deliberate. The two rules answer different questions — one is about a person's own entries
// and a date, the other about a person's grants and a thing — and a table that mixed them would
// be read by nobody. Each is one page, and each is one slide.
//
// This is the table that answers "why can my colleague not enter the demand for their own
// programme", which is the support question this migration creates.
//
// Re-record with: go test ./internal/policy/ -update-golden — and then read the diff.
func TestPlanningMatrix(t *testing.T) {
	t.Parallel()

	golden.Assert(t, "planning_matrix", renderPlanningMatrix())
}

func renderPlanningMatrix() string {
	var b strings.Builder

	b.WriteString(planningPreamble)

	header := planningRow("Role", "Door", "Scoped to", "own", "other", "Query scope")
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
			plain := testdata.Vier.Actor(door.kind, string(role))
			rows = append(rows, situation{label: string(role), actor: plain})

			// Only one role has a programme dimension today. Showing the scoped variant for the
			// others would suggest they have one.
			if role == policy.RoleProgrammeLead {
				rows = append(rows, situation{
					label:  string(role),
					scoped: []string{"one"},
					actor:  leadOf(testdata.Vier, door.kind, programmeOne),
				})
				rows = append(rows, situation{
					label:  string(role),
					scoped: []string{"one", "two"},
					actor:  leadOf(testdata.Vier, door.kind, programmeOne, programmeTwo),
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

		b.WriteString(planningRow(
			s.label,
			door,
			scoped,
			answer(policy.MayPlanProgramme(s.actor, programmeOne)),
			answer(policy.MayPlanProgramme(s.actor, programmeTwo)),
			planningScopeLabel(policy.PlanningScope(s.actor)),
		))
	}

	return b.String()
}

func planningScopeLabel(s policy.ProgrammeScope) string {
	switch {
	case s.All:
		return "every programme"
	case len(s.IDs) == 0:
		return "none"
	case len(s.IDs) == 1:
		return "1 programme"
	default:
		return "2 programmes"
	}
}

const planningPreamble = `Planning — who may declare the demand of which study programme
==============================================================

Generated from internal/policy (TestPlanningMatrix). Do not edit by hand:

    go test ./internal/policy/ -update-golden

The rule
--------

Demand for a study programme may be declared by

  · the dean's office, for every programme — the import/export statistics are its job, and a
    programme created tomorrow is included, or
  · a programme lead, for the programmes they have been assigned — and for no others.

Nobody else, through either door. Planning is not confidential and not personnel data, so a
Personal Access Token reaches exactly as far as a browser session does: a colleague evaluating
their own programme's demand from a script is a use this API exists for.

The one that surprises people
-----------------------------

**A programme lead who has not been assigned a programme may plan nothing.** Not everything.

Elsewhere in this system an empty list means "unrestricted" — an empty token scope list, an
empty role selection — because both of those are mechanisms that can only ever *remove*, so
"nothing selected" has to mean "nothing removed". A programme assignment is not a narrowing of
the grant; it is what the grant is about. The role declares the demand of ONE programme, and
the role that means all of them is the dean's office.

Read the other way, the release that introduced programme assignments would have been the
moment every existing programme lead silently became faculty-wide.

What such a person is shown is not "you may not do this" — which would send them to ask for a
role they already hold — but "your programme leadership has not been assigned to a study
programme yet".

The columns
-----------

  Scoped to     Which programmes this person has been assigned.
  own / other   May they declare demand for programme one / programme two?
  Query scope   The same rule as a query restriction, and what the interface reads to decide
                which programmes to offer in a picker at all. "every programme" is not the
                list of programmes that exist today — a programme created afterwards is in it.

Notes
-----

  · ADMIN plans nothing, and that is the same decision the wish rule makes: running the system
    is a different job from planning with it. An administrator who genuinely has to plan is
    granted the role, visibly.
  · A subject group lead fills instances and does not declare them, so it plans nothing here.
  · Combinations of roles are not listed. Somebody holding several gets the union — checked
    over the complete cartesian product in TestPlanningGuardAndScopeAgree.
  · Only the programme lead has a scope dimension in this table. The subject group lead has
    one of its own — see assignment_matrix.golden — and the two are orthogonal: a subject
    group reaches across study programmes, and a study programme across subject groups.

`

// widths of its own, because the columns are not the wish matrix's columns.
var planningWidths = []int{20, 14, 12, 6, 7, 16}

func planningRow(cells ...string) string {
	var b strings.Builder
	for i, c := range cells {
		if i == len(cells)-1 {
			b.WriteString(c)
			break
		}
		b.WriteString(pad(c, planningWidths[i]))
	}
	b.WriteString("\n")
	return b.String()
}
