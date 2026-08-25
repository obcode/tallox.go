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
// A change here is meant to be deliberate: somebody closing the demand after the wish phase edits
// this test and the matrix together, and the pull request says so in two places.
func TestDemandIsOpenInEveryPhase(t *testing.T) {
	t.Parallel()

	lead := leadOf(testdata.Vier, principal.KindInteractive, programmeOne)

	for _, phase := range policy.AllPhases() {
		if !policy.MayWriteDemand(lead, programmeOne, phase) {
			t.Errorf("phase %s: the lead of programme one may not declare its demand — "+
				"a late instance is a correction, and a refused correction happens outside "+
				"the tool instead", phase)
		}
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
			phase:     policy.PhaseFinal,
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

	writers := policy.WritersIn(policy.WriteAreaDemand, policy.PhaseFinal)
	if len(writers) == 0 {
		t.Fatal("no writers in DEMAND/FINAL — the fixture this test needs is gone")
	}
	writers[0] = policy.RoleLecturer

	lecturer := testdata.Eins.Actor(principal.KindInteractive, string(policy.RoleLecturer))
	if policy.MayWriteInPhase(policy.WriteAreaDemand, policy.PhaseFinal, lecturer) {
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

const writePreamble = `Writing — when each part of the planning may be changed, and by whom
===================================================================

Generated from internal/policy (TestWriteMatrix). Do not edit by hand:

    go test ./internal/policy/ -update-golden

The rule
--------

The planning runs through four phases, and this table says what may be written in each. It is a
table rather than a condition somewhere in the code, for two reasons: it is an artefact the
faculty has to be able to read and agree with, and closing something later should be one row and
one reviewed diff.

Read it as "when". It does not say *which* study programme somebody reaches — that is the
planning matrix, and the two are intersected. Both have to say yes.

The one that surprises people
-----------------------------

**Almost nothing closes.** The demand may be changed in every phase, FINAL included; wishes may
be entered and changed in every phase except FINAL. Both are decisions and neither is an
oversight, and they rest on the same argument.

A course instance declared during the assignment is a correction: somebody falls ill, a cohort
turns out larger than the numbers said, a module was forgotten. So is somebody saying in March
that they would take the second laboratory group after all. Corrections happen, and a tool that
refuses one does not prevent it — it moves it into a spreadsheet or a mail passed around
outside, which is the thing this system replaces. Its own figures then become the wrong ones.

What protects a plan already being worked on is therefore not a closed phase. It is the refusal
to withdraw an instance that something already hangs off, which is enforced on the row itself and
says nothing about what that something is — and, for the assignment, the assignment itself, which
is a decision somebody takes rather than a consequence of what is on the wish list.

The one cell that does close is wishes in FINAL. A finished semester is the record of what the
faculty did, and a wish registered afterwards would change that record without changing anything
about the teaching.

Notes
-----

  · The phase is stored on the semester and advanced by an audited mutation, never derived from
    the calendar.
  · A phase this binary does not recognise permits nothing. The plausible guess would be
    DEMAND_PLANNING — the most permissive phase there is — so guessing would turn a database
    this binary cannot read into one it writes to.
  · ADMIN writes nothing here, the same decision the other two tables make: running the system
    is a different job from planning with it.
  · Doors are not a dimension of *this* rule — but they are a dimension of the demand, and the
    two are easy to confuse. Every mutation that writes the demand is @interactiveOnly, so a
    Personal Access Token cannot perform one at all. That is not about the demand being
    confidential (it is not): a withdrawal refused with INSTANCE_IN_USE is an answer about who
    wants an instance, and through a token the wish rule reaches only your own. Registering your
    own wish stays open through both doors, because that is your own data.

`

const writeIntersection = `How this combines with the planning matrix
-----------------------------------------

    may write = (this table says yes for the phase) AND (the planning matrix says yes for the
                study programme)

Neither half is redundant. A programme lead in an open phase may still only write for their own
programmes; the dean's office reaches every programme but is still bound by the phase.

The refusal names which half said no, because the repair differs: an unassigned programme lead
needs an administrator, the lead of another programme needs the right programme, and a closed
phase needs the phase moved.
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
