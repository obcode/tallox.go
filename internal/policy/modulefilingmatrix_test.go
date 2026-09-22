package policy_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/golden"
	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
	"github.com/obcode/tallox.go/internal/testdata"
)

// TestModuleFilingMatrix renders who may file which module into which subject group.
//
// Its own table rather than a column on the assignment matrix, because the question has two
// subject groups in it and that one has one. Filing is the only act in this package that moves
// something *between* groups, so the interesting cases are the pairs — pulling in an unsorted
// module, letting go of one's own, and the two that must stay refused: taking one out of a
// colleague's group, and pushing one into it.
//
// Re-record with: go test ./internal/policy/ -update-golden — and then read the diff.
func TestModuleFilingMatrix(t *testing.T) {
	t.Parallel()

	golden.Assert(t, "module_filing_matrix", renderModuleFilingMatrix())
}

// The five moves the table asks about. `none` is uuid.Nil on that side.
var moduleFilingMoves = []struct {
	label    string
	from, to uuid.UUID
}{
	{"none→none", uuid.Nil, uuid.Nil},
	{"none→1", uuid.Nil, groupOne},
	{"1→none", groupOne, uuid.Nil},
	{"1→1", groupOne, groupOne},
	{"2→1", groupTwo, groupOne},
	{"1→2", groupOne, groupTwo},
}

func renderModuleFilingMatrix() string {
	var b strings.Builder

	b.WriteString(moduleFilingPreamble)

	cells := []string{"Role", "Door", "Leads"}
	for _, m := range moduleFilingMoves {
		cells = append(cells, m.label)
	}
	header := moduleFilingRow(cells...)
	b.WriteString(header)
	b.WriteString(rule(header))

	type situation struct {
		label string
		leads string
		actor principal.Actor
	}

	rows := []situation{{label: "(not signed in)", actor: principal.Anonymous}}

	for _, role := range policy.AllRoles() {
		for _, door := range []struct {
			label string
			kind  principal.Kind
		}{
			{"interactive", principal.KindInteractive},
			{"token", principal.KindToken},
		} {
			rows = append(rows, situation{
				label: string(role),
				leads: "—",
				actor: testdata.Drei.Actor(door.kind, string(role)),
			})

			// Only the lead has a subject group dimension; showing a scoped variant of the
			// others would suggest they have one.
			if role == policy.RoleSubjectGroupLead {
				rows = append(rows, situation{
					label: string(role),
					leads: "one",
					actor: headOf(testdata.Drei, door.kind, groupOne),
				})
			}
		}
	}

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
		leads := s.leads
		if leads == "" {
			leads = "—"
		}

		cells := []string{s.label, door, leads}
		for _, m := range moduleFilingMoves {
			cells = append(cells, answer(policy.MayFileModule(s.actor, m.from, m.to)))
		}
		b.WriteString(moduleFilingRow(cells...))
	}

	return b.String()
}

// widths of its own: three descriptive columns and then five narrow answers.
var moduleFilingWidths = []int{20, 14, 8, 12, 9, 9, 7, 7, 7}

func moduleFilingRow(cells ...string) string {
	var b strings.Builder
	for i, c := range cells {
		if i == len(cells)-1 {
			b.WriteString(c)
			break
		}
		b.WriteString(pad(c, moduleFilingWidths[i]))
	}
	b.WriteString("\n")
	return b.String()
}

const moduleFilingPreamble = `Filing modules — who may put which module into which subject group
==================================================================

Generated from internal/policy (TestModuleFilingMatrix). Do not edit by hand:

    go test ./internal/policy/ -update-golden

The rule
--------

Somebody may move a module from the subject group it is in today into the one it should be in if

  · they are an administrator — any module, any group, and
  · in a signed-in browser session either way: through a Personal Access Token, nobody, or
  · they lead subject groups, and **both** ends of the move are groups they lead.

"No group" counts as an end that is always in reach: a module nobody has sorted yet may be
pulled in by whoever wants it, and a module may always be let go of by whoever holds it.

Both ends, not just the target
------------------------------

This is the only act in this package that touches two subject groups at once. Checking only
where a module is going would make "move this into mine" a unilateral act against the group it
came from — which is the one thing a lead must not be able to do to a colleague. So a lead may

  · pull in an unsorted module                     (none→1)
  · let go of one of her own                       (1→none)
  · re-file one of her own into her own            (1→1, the no-op a batch produces)

and may not

  · take one out of a colleague's group            (2→1)
  · push one into a colleague's group              (1→2)

Re-cutting the catalogue across two groups stays what it was: an administrator's job, done once
and visibly.

Why the leads at all
--------------------

The lead of a group is already the person who fills its instances and who reads the unpublished
wishes on them — that is the assignment matrix. Being unable to say which modules those *are*
made her responsible for a set somebody else had to maintain on her behalf, and the faculty
asked for it back. The scope she already holds is exactly the right size for the question.

The one that surprises people, again
------------------------------------

**A lead who has not been assigned a subject group may do nothing here either.** Not everything.
The same reading as everywhere else in this file, and she is shown
SubjectGroupScopeMissingReason rather than a refusal, because the repair is an administrator's
and not hers.

Note the row it produces: even the move that changes nothing — none→none, which a batch of
"take these out of every group" produces for a module that was already out — is refused for
somebody who leads nothing. A rule that answered "yes, you may do nothing" is one somebody
later reads as "yes, you may do this".

The token door
--------------

Empty, on every row. Re-filing a module moves who may read the unpublished wishes on its
instances, so it is exactly the kind of act that belongs to a session somebody signed into and
not to a token in a script. The mutation carries @interactiveOnly as well; this table is the
second half of that sentence, so that the rule does not live only in a directive.

The columns
-----------

  Leads     Which subject groups this person has been assigned. Group one below.
  none→none Filing a module that is in no group into no group: the row a batch of "take these
            out of every group" produces for one that was already out. It changes nothing, and
            it is still answered by the rule rather than waved through.
  none→1    Filing an unsorted module into group one.
  1→none    Taking a module out of group one, into no group at all.
  1→1       Filing a module into the group it is already in — what a batch does to the rows
            that were already right.
  2→1       Taking a module out of group two and into group one.
  1→2       The other direction: out of group one and into group two.

`
