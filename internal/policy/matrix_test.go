package policy_test

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/obcode/tallox.go/internal/golden"
	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
	"github.com/obcode/tallox.go/internal/testdata"
)

// TestVisibilityMatrix renders the whole wish-visibility rule as a table and compares it
// against a committed file.
//
// Two jobs, and the second one is why this exists in week one, before the wish workflow does:
//
//  1. Every change to who sees what becomes a reviewable diff instead of a sentence in a
//     commit message. A rule that is only readable by executing it in your head is a rule that
//     gets widened by accident.
//  2. The file is the slide for the faculty retreat. "Wer sieht was, wann" is the question
//     this project will be asked in front of a room, and the answer should be a printed table
//     generated from the code that enforces it — not a drawing that agrees with it today.
//
// Re-record with: go test ./internal/policy/ -update-golden — and then read the diff.
func TestVisibilityMatrix(t *testing.T) {
	t.Parallel()

	golden.Assert(t, "visibility_matrix", renderMatrix())
}

// caller is one row group of the matrix: who is asking, through which door, responsible for what.
type caller struct {
	label  string
	door   string
	scoped string
	actor  principal.Actor
}

func callers() []caller {
	out := []caller{{
		label: "(not signed in)",
		door:  "—",
		actor: principal.Anonymous,
	}}

	for _, role := range policy.AllRoles() {
		for _, door := range []struct {
			label string
			kind  principal.Kind
		}{
			{"interactive", principal.KindInteractive},
			{"token", principal.KindToken},
		} {
			out = append(out, caller{
				label: string(role),
				door:  door.label,
				actor: testdata.Eins.Actor(door.kind, string(role)),
			})

			// The scoped variant, for the two roles that have a subject. Showing one for the
			// others would suggest they have one; leaving it off these two would show the rule
			// only in the state nobody is meant to stay in.
			switch role {
			case policy.RoleProgrammeLead:
				scoped := testdata.Eins.Actor(door.kind, string(role))
				scoped.RoleScopes = []principal.RoleScope{
					{Role: string(role), ProgrammeID: programmeOne},
				}
				out = append(out, caller{
					label: string(role), door: door.label, scoped: "programme 1", actor: scoped,
				})
			case policy.RoleSubjectGroupLead:
				scoped := testdata.Eins.Actor(door.kind, string(role))
				scoped.RoleScopes = []principal.RoleScope{
					{Role: string(role), SubjectGroupID: groupOne},
				}
				out = append(out, caller{
					label: string(role), door: door.label, scoped: "group 1", actor: scoped,
				})
			}
		}
	}
	return out
}

func renderMatrix() string {
	var b strings.Builder

	b.WriteString(matrixPreamble)

	header := row("Role", "Door", "Responsible for", "Phase", "Wishes",
		"own", "prog. 1", "group 1", "neither", "Filter")
	b.WriteString(header)
	b.WriteString(rule(header))

	previous := ""
	for _, c := range callers() {
		if previous != "" && c.label != previous {
			b.WriteString("\n")
		}
		previous = c.label

		for _, state := range []struct {
			label string
			state policy.SemesterState
		}{
			{"unpublished", unpublished},
			{"published", published},
		} {
			for _, phase := range policy.AllPhases() {
				s := state.state
				s.Phase = phase

				// Four wishes, one per column. The owner is the caller only in the first; the
				// other three belong to a colleague and differ in what they hang off, which is
				// what makes the two reaches readable as separate columns.
				own := policy.Wish{
					OwnerID: c.actor.ID, ProgrammeID: programmeTwo, SubjectGroupID: groupTwo,
				}
				inProgramme := policy.Wish{
					OwnerID: testdata.Zwei.ID(), ProgrammeID: programmeOne, SubjectGroupID: groupTwo,
				}
				inGroup := policy.Wish{
					OwnerID: testdata.Zwei.ID(), ProgrammeID: programmeTwo, SubjectGroupID: groupOne,
				}
				neither := policy.Wish{
					OwnerID: testdata.Zwei.ID(), ProgrammeID: programmeTwo, SubjectGroupID: groupTwo,
				}

				scoped := c.scoped
				if scoped == "" {
					scoped = "—"
				}

				b.WriteString(row(
					c.label,
					c.door,
					scoped,
					string(phase),
					state.label,
					answer(policy.CanSeeWish(c.actor, s, own)),
					answer(policy.CanSeeWish(c.actor, s, inProgramme)),
					answer(policy.CanSeeWish(c.actor, s, inGroup)),
					answer(policy.CanSeeWish(c.actor, s, neither)),
					filterLabel(policy.WishVisibility(c.actor, s)),
				))
			}
		}
		// One blank line per row group: the table is read by eye, on a slide, by somebody looking
		// for their own row.
		b.WriteString("\n")
	}

	return b.String()
}

const matrixPreamble = `Wish visibility — who sees which wish, and when
===============================================

Generated from internal/policy (TestVisibilityMatrix). Do not edit by hand:

    go test ./internal/policy/ -update-golden

The rule
--------

A wish is visible if and only if

  · it belongs to the person asking, or
  · the wishes of that semester have been published
    (semester.wishes_published_at IS NOT NULL), or
  · the person is responsible for it — they lead the study programme whose demand the instance
    is, or the subject group the instance's module belongs to, or they are the dean's office
    — and then only in an interactive session, never through a Personal Access Token.

The purpose is to end the first-come-first-served race: a new colleague should be able to
register interest without it looking like an attack on somebody who has taught the subject
for years.

Responsible for what, exactly
-----------------------------

Two reaches, and they are orthogonal — neither implies the other:

  Study programme   The programme whose demand the instance is. **The programme of the
                    instance, never of the person.** Somebody at home in IF who registers
                    interest in an IG instance is visible to the IG lead and not to the IF one:
                    what is being planned is the instance.
  Subject group     The subject group of the module the instance offers. Derived through the
                    module, so it holds across semesters — and a module nobody has sorted into
                    a group yet reaches no subject group lead at all, which is the ordinary
                    state until the faculty has worked through its catalogue.

A subject group reaches across study programmes and a study programme across subject groups,
so somebody sees a row through one axis or through neither.

The one that surprises people
-----------------------------

**A lead who has not been assigned a subject reads only their own entries.** Not everything.

The same reading the planning and assignment matrices take, and the one that is wrong
everywhere else in this system: an empty token scope list and an empty role selection both mean
"unrestricted", because both are mechanisms that can only ever remove. A programme or a subject
group is not a narrowing of the grant; it is what the grant is about. The role that means all of
them is the dean's office.

The columns
-----------

  Responsible for   What this person has been assigned. Programme 1 and group 1 below.
  own               Their own entry — always visible, through either door.
  prog. 1           A colleague's entry on an instance of programme 1, whose module is in
                    group 2.
  group 1           A colleague's entry on an instance of programme 2, whose module is in
                    group 1. The column that shows the two reaches are separate.
  neither           A colleague's entry on programme 2, in group 2.
  Filter            The same rule as a query restriction. **Counts run through exactly this
                    filter** — otherwise "three colleagues have already registered interest"
                    gives the confidential answer away in full, without naming anybody.

Notes
-----

  · The phase appears in every row and never changes the answer. Publication is a timestamp
    of its own, not a consequence of the phase: the wish phase can end without publishing,
    and publication can happen while the assignment is already running. What the phase *does*
    decide is whether a wish may be written — see write_matrix.golden.
  · Combinations of roles are not listed. Somebody holding several sees the union — checked
    over the complete cartesian product in TestGuardAndFilterAgree.
  · ADMIN is deliberately not a wish reader. Running the system is a different job from
    planning with it; an administrator who genuinely needs to look is granted DEANS_OFFICE,
    visibly.
  · Through a Personal Access Token, even a lead sees only their own wishes until publication.
    A long-lived token in a script makes silent bulk export possible and decouples "who saw
    this" from any login event.
  · **Being in a subject group is not leading it.** Membership decides what the wish screen
    offers first and grants nothing here. The kickoff sentence "jeder in einer Fachgruppe
    müsste alles lesen können" is about planning data: if it covered wishes, the rule would
    switch itself off precisely inside the subject group, which is where the
    first-come-first-served race actually happens.

`

// widths are the column widths, in runes. Fixed rather than computed, so that adding a role
// with a long name produces a visible one-line change here instead of reflowing the entire
// file and drowning the actual diff.
var widths = []int{20, 13, 17, 17, 13, 6, 9, 9, 9, 16}

func row(cells ...string) string {
	var b strings.Builder
	for i, c := range cells {
		if i == len(cells)-1 {
			b.WriteString(c)
			break
		}
		b.WriteString(pad(c, widths[i]))
	}
	b.WriteString("\n")
	return b.String()
}

func rule(header string) string {
	return strings.Repeat("-", utf8.RuneCountInString(strings.TrimRight(header, "\n"))) + "\n"
}

// pad left-aligns to n columns, counting runes.
//
// Not %-20s: that pads to a byte width, and half the words in this table (Tür, Wünsche,
// unveröffentlicht) contain multi-byte runes. The result would be a table that looks aligned
// to fmt and ragged to a reader — on the one artefact whose whole purpose is to be read.
func pad(s string, n int) string {
	if missing := n - utf8.RuneCountInString(s); missing > 0 {
		return s + strings.Repeat(" ", missing)
	}
	return s + " "
}

func answer(ok bool) string {
	if ok {
		return "yes"
	}
	return "no"
}

func filterLabel(f policy.WishFilter) string {
	switch f.Scope {
	case policy.WishScopeAll:
		return "all"
	case policy.WishScopeOwnOrScoped:
		return "own + scoped"
	case policy.WishScopeOwn:
		return "own only"
	case policy.WishScopeNone:
		return "no access"
	default:
		return fmt.Sprintf("?? %s", f.Scope)
	}
}
