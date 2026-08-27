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

// The table has to decide every cell, and a missing one must be a test failure rather than a
// silent no.
//
// Fail-closed is the right default for a lookup — but as a *default* it would mean that adding a
// phase to the process closes every area in it, on a deploy, without anybody choosing that. The
// two properties are not in tension: absent means no, and absent is not allowed.
func TestWriteMatrixDecidesEveryCell(t *testing.T) {
	t.Parallel()

	for _, area := range policy.AllWriteAreas() {
		for _, phase := range policy.AllPhases() {
			if !policy.Decided(area, phase) {
				t.Errorf("%s in %s: no entry — every cell has to be decided, "+
					"including the ones that decide nobody may write", area, phase)
			}
		}
	}
}

// An unknown phase permits nothing, in every area, for every role.
//
// The realistic way to get one is a semester row written by a newer binary, or a typo repaired by
// hand at midnight. The plausible guess would be DEMAND_PLANNING — which is the most permissive
// phase there is, so guessing turns a database this binary cannot read into a database it happily
// writes to.
func TestUnknownPhaseWritesNothing(t *testing.T) {
	t.Parallel()

	unknown := []policy.Phase{"", "DEMAND", "demand_planning", "KLAUSURTAGUNG"}

	for _, area := range policy.AllWriteAreas() {
		for _, phase := range unknown {
			for _, actor := range []principal.Actor{
				testdata.Vier.Actor(principal.KindInteractive, string(policy.RoleDeansOffice)),
				testdata.Vier.Actor(principal.KindInteractive, string(policy.RoleAdmin)),
			} {
				if policy.MayWriteInPhase(area, phase, actor) {
					t.Errorf("%s in unknown phase %q: %v may write", area, phase, actor.Roles)
				}
			}
		}
	}
}

// The decision, asserted as itself rather than left to be inferred from the golden file.
//
// Demand may be added at any point up to the close, including while the assignment runs: a late
// instance is a correction, and a refused correction happens outside the tool instead. What it
// may not survive is the close, because a finished semester is a record — see
// TestNothingIsWrittenAfterTheSemesterIsFinished, which asserts the other half.
func TestDemandIsOpenUntilTheSemesterIsFinished(t *testing.T) {
	t.Parallel()

	lead := leadOf(testdata.Vier, principal.KindInteractive, programmeOne)

	for _, phase := range policy.AllPhases() {
		want := phase != policy.PhaseFinal
		if got := policy.MayWriteDemand(lead, programmeOne, phase); got != want {
			t.Errorf("phase %s: the lead of programme one may write the demand = %v, want %v",
				phase, got, want)
		}
	}
}

// The one hard meaning the phase keeps, across every area at once.
//
// Since 2026-08-28 this is the whole of what the write matrix decides, so it is worth one test
// that says it in those words: after the close, nobody writes anything anywhere. Everything else
// about when the planning is open moved to demand_completion and wish_window, which are not
// phases and are not here.
func TestNothingIsWrittenAfterTheSemesterIsFinished(t *testing.T) {
	t.Parallel()

	checked := 0
	for _, area := range policy.AllWriteAreas() {
		for _, actor := range everyActor() {
			checked++
			if policy.MayWriteInPhase(area, policy.PhaseFinal, actor) {
				t.Errorf("%s: %v may still write into a finished semester", area, actor.Roles)
			}
		}
	}
	if checked == 0 {
		t.Fatal("the cartesian product was empty — this test checked nothing")
	}
}

// Neither half of the intersection is redundant, and the table says which half refused.
func TestMayWriteDemandIsTheIntersection(t *testing.T) {
	t.Parallel()

	unscopedLead := testdata.Vier.Actor(principal.KindInteractive, string(policy.RoleProgrammeLead))

	cases := []struct {
		name      string
		actor     principal.Actor
		programme uuid.UUID
		phase     policy.Phase
		want      bool
	}{
		{
			name:      "the lead of this programme, in an open phase",
			actor:     leadOf(testdata.Vier, principal.KindInteractive, programmeOne),
			programme: programmeOne,
			phase:     policy.PhaseAssignment,
			want:      true,
		},
		{
			name:      "the lead of another programme",
			actor:     leadOf(testdata.Vier, principal.KindInteractive, programmeTwo),
			programme: programmeOne,
			phase:     policy.PhaseDemandPlanning,
			want:      false,
		},
		{
			name:      "a lead nobody has assigned a programme to",
			actor:     unscopedLead,
			programme: programmeOne,
			phase:     policy.PhaseDemandPlanning,
			want:      false,
		},
		{
			name:      "the dean's office, for a programme it leads none of",
			actor:     testdata.Vier.Actor(principal.KindInteractive, string(policy.RoleDeansOffice)),
			programme: programmeOne,
			phase:     policy.PhaseAssignment,
			want:      true,
		},
		{
			name:      "through a token, which reaches exactly as far",
			actor:     leadOf(testdata.Vier, principal.KindToken, programmeOne),
			programme: programmeOne,
			phase:     policy.PhaseWishes,
			want:      true,
		},
		{
			name:      "an administrator, who runs the system rather than plans with it",
			actor:     testdata.Vier.Actor(principal.KindInteractive, string(policy.RoleAdmin)),
			programme: programmeOne,
			phase:     policy.PhaseDemandPlanning,
			want:      false,
		},
		{
			name:      "a lecturer",
			actor:     testdata.Eins.Actor(principal.KindInteractive, string(policy.RoleLecturer)),
			programme: programmeOne,
			phase:     policy.PhaseDemandPlanning,
			want:      false,
		},
		{
			name:      "nobody at all",
			actor:     principal.Anonymous,
			programme: programmeOne,
			phase:     policy.PhaseDemandPlanning,
			want:      false,
		},
		{
			name:      "the right lead, in a phase this binary does not know",
			actor:     leadOf(testdata.Vier, principal.KindInteractive, programmeOne),
			programme: programmeOne,
			phase:     policy.Phase("KLAUSURTAGUNG"),
			want:      false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := policy.MayWriteDemand(tc.actor, tc.programme, tc.phase); got != tc.want {
				t.Errorf("MayWriteDemand = %v, want %v", got, tc.want)
			}
		})
	}
}

// Each refusal names the repair, because the three repairs are three different people.
func TestDemandRefusalNamesItsRepair(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		actor principal.Actor
		phase policy.Phase
		want  string
	}{
		{
			name:  "no programme assigned — an administrator has to act",
			actor: testdata.Vier.Actor(principal.KindInteractive, string(policy.RoleProgrammeLead)),
			phase: policy.PhaseDemandPlanning,
			want:  policy.ProgrammeScopeMissingReason,
		},
		{
			name:  "somebody else's programme",
			actor: leadOf(testdata.Vier, principal.KindInteractive, programmeTwo),
			phase: policy.PhaseDemandPlanning,
			want:  policy.PlanningReason,
		},
		{
			name:  "the right person, in a phase that is not open",
			actor: leadOf(testdata.Vier, principal.KindInteractive, programmeOne),
			phase: policy.Phase("KLAUSURTAGUNG"),
			want:  policy.PhaseClosedReason,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := policy.DemandRefusal(tc.actor, programmeOne, tc.phase); got != tc.want {
				t.Errorf("DemandRefusal = %q, want %q", got, tc.want)
			}
		})
	}
}

// A caller holding one of the matrix's slices must not be able to edit the matrix through it.
func TestWritersInHandsOutACopy(t *testing.T) {
	t.Parallel()

	writers := policy.WritersIn(policy.WriteAreaDemand, policy.PhaseAssignment)
	if len(writers) == 0 {
		t.Fatal("no writers in DEMAND/ASSIGNMENT — the fixture this test needs is gone")
	}
	writers[0] = policy.RoleLecturer

	lecturer := testdata.Eins.Actor(principal.KindInteractive, string(policy.RoleLecturer))
	if policy.MayWriteInPhase(policy.WriteAreaDemand, policy.PhaseAssignment, lecturer) {
		t.Error("editing the returned slice edited the matrix")
	}
}

// TestWriteMatrix renders when each area of the planning may be written, and by whom.
//
// The third of the three tables — after the wish matrix ("who sees what") and the planning matrix
// ("who plans which programme") — and the one that answers "may I still change this now".
//
// Re-record with: go test ./internal/policy/ -update-golden — and then read the diff.
func TestWriteMatrix(t *testing.T) {
	t.Parallel()

	golden.Assert(t, "write_matrix", renderWriteMatrix())
}

func renderWriteMatrix() string {
	var b strings.Builder

	b.WriteString(writePreamble)

	for _, area := range policy.AllWriteAreas() {
		b.WriteString(string(area) + "\n")
		b.WriteString(strings.Repeat("-", len(string(area))) + "\n\n")

		cells := []string{"Role"}
		for _, phase := range policy.AllPhases() {
			cells = append(cells, string(phase))
		}
		header := writeRow(cells...)
		b.WriteString(header)
		b.WriteString(rule(header))

		for _, role := range policy.AllRoles() {
			// Scoped, because an unscoped lead answers "no" everywhere and that is the *other*
			// table's rule. This one is about the phase, so it shows the phase's answer.
			actor := testdata.Vier.Actor(principal.KindInteractive, string(role))
			row := []string{string(role)}
			for _, phase := range policy.AllPhases() {
				row = append(row, answer(policy.MayWriteInPhase(area, phase, actor)))
			}
			b.WriteString(writeRow(row...))
		}

		b.WriteString("\n")
		unknown := []string{"(unknown phase)"}
		for range policy.AllPhases() {
			unknown = append(unknown, answer(
				policy.MayWriteInPhase(area, policy.Phase("KLAUSURTAGUNG"),
					testdata.Vier.Actor(principal.KindInteractive, string(policy.RoleDeansOffice)))))
		}
		b.WriteString(writeRow(unknown...))
		b.WriteString("\n")
	}

	b.WriteString(writeIntersection)

	return b.String()
}

const writePreamble = `Writing — what a finished semester still allows, and what decides the rest
==========================================================================

Generated from internal/policy (TestWriteMatrix). Do not edit by hand:

    go test ./internal/policy/ -update-golden

The rule
--------

The planning runs through four phases, and this table used to say what may be written in each. It
says almost nothing now, and that is the result of a decision rather than of neglect.

Read it as: **everything is open until the semester is finished.** Demand may be declared, wishes
registered and instances filled at any point up to the close, by the roles named below, and after
the close by nobody.

What decides the rest
---------------------

The planning does not open and close for the whole faculty at once. It was modelled that way and
the faculty corrected it on 2026-08-28: the study programmes settle their demand at different
times, and each subject group runs its own wish round. So two things that are **not phases** carry
what this table used to:

  demand_completion   One study programme announcing that its demand for a semester is settled,
                      as far as it knows today. An ANNOUNCEMENT: it blocks nothing at all. Demand
                      may still be added, and adding some makes the announcement out of date
                      rather than false — which is what withdrawing it is for.
  wish_window         One subject group's wish round, opened and shut by its lead, at any moment
                      and in either direction. A DOOR. An absent row means open.

Neither is here, because neither is a stage the process passes through. They are facts somebody
states about their own work.

The one that surprises people
-----------------------------

**This table is deliberately almost empty, and it is worth keeping anyway.**

Three reasons. The sentence it does state — nothing is written after the semester is closed — is
one rule, and one place to read it beats three conditions somebody has to find. The phase is still
what a colleague sees to know where a semester roughly stands, and a table generated from the code
is how that stays honest. And it is where the next decision about closing something lands.

A phase this binary does not recognise permits nothing. The plausible guess would be
DEMAND_PLANNING — the most permissive phase there is — so guessing would turn a database this
binary cannot read into one it writes to.

What changed, and what it cost
------------------------------

Two rows moved on 2026-08-28, in opposite directions.

**The demand closes in FINAL, where it used to stay open.** The argument for keeping it open was
that a late instance is a correction and a refused correction happens anyway, in a spreadsheet
outside the tool. That argument still holds — and it is now carried by the three phases before
FINAL, all of which are open. Letting the demand alone survive the close would make FINAL mean two
different things depending on which screen somebody is looking at.

**The assignment opens from the start, where it was shut before its own phase.** That cell was
closed for one day, on the argument that filling an instance while the wish round runs is the
first-come-first-served race the confidentiality rule exists to end. What the faculty answered is
that the wish round belongs to the subject group, not to the faculty — its lead opens and shuts it
and is the same person who then fills the instances. A tool that ordered those two would be
ordering the work of somebody who can see the whole of it, and the race is one that lead does not
have to run.

Notes
-----

  · The phase is stored on the semester and advanced by an audited mutation, never derived from
    the calendar.
  · ADMIN writes nothing here, the same decision the other tables make: running the system is a
    different job from planning with it.
  · Doors are not a dimension of *this* rule, but they are of two areas and the three are easy to
    confuse. Every mutation that writes the demand or an assignment is @interactiveOnly, so a
    Personal Access Token cannot perform one at all. That is not about either being confidential:
    a refusal that says an instance is wanted, or a part taken, is an answer about wishes and
    assignments, and through a token both rules reach only your own. Registering your own wish
    stays open through both doors, because that is your own data.

`

const writeIntersection = `How this combines with everything else
-------------------------------------

    demand      = (this table) AND (the planning matrix, for the study programme)
    wishes      = (this table) AND (the subject group's wish window is open)
    assignment  = (this table) AND ((the subject group matrix, for the module's subject group)
                                    OR (the planning matrix, for the instance's study programme))

Three shapes, and each half of each line refuses something the others do not.

The union in the third line is the decision of 2026-08-27. What settled it is the module that
belongs to no subject group: with the subject groups alone, filling its instances would be the
dean's office or nobody, and the catalogue holds plenty of those while it is being sorted. It has
a consequence this table cannot express, so it is written here: **two roles may write one row.**
What decides a race is therefore not permission but the write itself — an assignment is replaced
only when the caller names the one they are replacing, so a write that names nothing can only ever
fill a part that is free.

The wish window in the second line is the one that is data rather than a rule, and the one that is
open unless somebody said otherwise. A module in no subject group has no window and stays open.

The refusal names which half said no, because the repair differs every time: an unassigned lead
needs an administrator, the lead of another programme or subject needs the right one, a closed
wish window needs its own subject group lead, and a finished semester needs nobody — it is
finished.
`

// Its own widths again: these columns are the phases, and the phases are long words.
var writeWidths = []int{20, 17, 9, 12, 7}

func writeRow(cells ...string) string {
	var b strings.Builder
	for i, c := range cells {
		if i == len(cells)-1 {
			b.WriteString(c)
			break
		}
		b.WriteString(pad(c, writeWidths[i]))
	}
	b.WriteString("\n")
	return b.String()
}
