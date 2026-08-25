package policy

import (
	"slices"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/principal"
)

// When something may be written, as a table rather than as a cascade of ifs.
//
// The rules in this package have answered "who" since the first migration. This file answers
// "when": the planning process is a sequence of phases, and what a role may change is not the
// same in all of them. CLAUDE.md has carried the shape of it as a draft — "matrix Area × Phase →
// RoleSet, as data, not as an if-cascade" — since before there was anything to write.
//
// # Why data
//
// Because this table is an artefact people outside the code have to read and agree with. It is
// rendered to testdata/write_matrix.golden and is a slide at the faculty retreat, exactly like
// the wish matrix and the planning matrix. An if-cascade renders to nothing, and a rule that
// cannot be shown to the faculty is a rule the faculty has not agreed to.
//
// It also makes the shape of a change small. Closing the demand after the wish phase is a
// changed row and a golden diff somebody reads — not a condition somebody has to find first.
//
// # Why the demand is open in every phase today
//
// Decided with the faculty, and it is a decision rather than an omission. A course instance
// declared in the middle of the assignment is a correction, and corrections happen: a colleague
// falls ill, a cohort turns out larger than the numbers said, a module was forgotten. The tool's
// competitor is a spreadsheet passed around by mail, and the way to lose to it is to refuse a
// correction that the process itself permits — the correction then happens anyway, outside the
// system, and the system's numbers become the wrong ones.
//
// What protects the plan is not a closed phase but the refusal to delete something that is
// already in use, which is enforced where it can be enforced honestly: in the database, on the
// row.
//
// So the table below has one area and four open cells, and it is still worth existing. It is the
// place the next decision lands in, and the reason the phase is already read on every write path
// — the expensive half of a rule like this is not the condition, it is threading the phase
// through every caller afterwards.

// WriteArea is a part of the planning process that opens and closes as the phases advance.
type WriteArea string

const (
	// WriteAreaDemand is declaring which course instances a study programme needs: creating,
	// changing and withdrawing instances and their parts.
	WriteAreaDemand WriteArea = "DEMAND"

	// WriteAreaWishes is registering interest in an instance part: creating, changing and
	// withdrawing one's own wishes.
	WriteAreaWishes WriteArea = "WISHES"
)

// AllWriteAreas returns every area, in the order of the planning process.
//
// New areas arrive with the tables that need them — WISHES with the wish rows, ASSIGNMENT with
// the assignments — rather than being declared in advance, for the same reason ScopeArea gives:
// an area with nothing behind it is a promise that somebody can read and nobody keeps.
func AllWriteAreas() []WriteArea {
	return []WriteArea{WriteAreaDemand, WriteAreaWishes}
}

// Valid reports whether a is an area this package knows.
func (a WriteArea) Valid() bool {
	return slices.Contains(AllWriteAreas(), a)
}

// writeMatrix is the table: which roles may write in which area, in which phase.
//
// Absent means nobody, and that direction is deliberate — the same fail-closed default the scope
// directive takes. It is not a licence to leave a cell out, though: TestWriteMatrixDecidesEveryCell
// requires an entry for every area and every phase, so a phase added to the process cannot
// quietly close an area that nobody meant to close.
//
// The roles here are about *when*; which study programme somebody reaches is a separate question
// answered by PlanningScope, and the two are intersected by MayWriteDemand below. Keeping them
// apart is what lets this table be read as a sentence about the process rather than about grants.
var writeMatrix = map[WriteArea]map[Phase][]Role{
	WriteAreaDemand: {
		PhaseDemandPlanning: {RoleProgrammeLead, RoleDeansOffice},
		PhaseWishes:         {RoleProgrammeLead, RoleDeansOffice},
		PhaseAssignment:     {RoleProgrammeLead, RoleDeansOffice},
		PhaseFinal:          {RoleProgrammeLead, RoleDeansOffice},
	},
	// The first row with a closed cell, which is what makes PhaseClosedReason reachable at last.
	//
	// LECTURER alone, and that is the whole list rather than an abbreviation of it: role.go says
	// LECTURER is the baseline everybody who appears in the planning holds, so a colleague who
	// also leads a programme registers interest as a lecturer like anybody else.
	//
	// Open in the wish phase and closed everywhere else, which is a decision about the process and
	// not a mechanism. Before it, the demand is not settled and there is nothing stable to want;
	// after it, the assignment is working from a list that would move underneath it. If the
	// faculty wants it otherwise it is one changed row and a golden diff somebody reads — which is
	// exactly what this table is for.
	WriteAreaWishes: {
		PhaseDemandPlanning: nil,
		PhaseWishes:         {RoleLecturer},
		PhaseAssignment:     nil,
		PhaseFinal:          nil,
	},
}

// WritersIn returns the roles that may write in one cell of the table.
//
// The renderer of the golden file reads this, and so does anything that wants to explain a
// refusal. A copy, so that a caller cannot edit the matrix by holding one of its slices.
func WritersIn(area WriteArea, phase Phase) []Role {
	return slices.Clone(writeMatrix[area][phase])
}

// Decided reports whether the table has an entry for this cell at all.
//
// The distinction WritersIn cannot make: a cell that says "nobody" and a cell nobody filled in
// both come back as an empty list, and they mean opposite things. Absent means no — that is the
// fail-closed default a lookup should have — but absent must not be *allowed*, or adding a phase
// to the process would close every area in it on a deploy without anybody choosing that.
//
// TestWriteMatrixDecidesEveryCell is the only caller, and that is the point: the distinction
// exists to be asserted, not to be branched on.
func Decided(area WriteArea, phase Phase) bool {
	phases, ok := writeMatrix[area]
	if !ok {
		return false
	}
	_, ok = phases[phase]
	return ok
}

// MayWriteInPhase is the phase half of a write rule: does this actor hold a role that writes
// here, in this phase?
//
// Not a permission on its own. Every area has a subject — a study programme, later a subject
// group — and the rule that decides the subject lives next to that subject. What this answers is
// the question the phase alone can answer.
//
// An unknown phase permits nothing, and that is the one refusal this table produces today. It
// means the semester row says something this binary cannot act on, which is exactly the situation
// in which guessing is worst: the plausible guess is DEMAND_PLANNING, the most permissive phase
// there is.
func MayWriteInPhase(area WriteArea, phase Phase, a principal.Actor) bool {
	roles := RolesOf(a)
	for _, r := range writeMatrix[area][phase] {
		if roles.Has(r) {
			return true
		}
	}
	return false
}

// MayWriteDemand is the whole rule for declaring demand: the right role, in a phase that is open,
// for a study programme this actor actually leads.
//
// The intersection is the point. Neither half is sufficient and neither half is redundant:
// PlanningScope says which programmes, the matrix says when, and the two are maintained
// separately because they change for different reasons.
func MayWriteDemand(a principal.Actor, programmeID uuid.UUID, phase Phase) bool {
	return MayWriteInPhase(WriteAreaDemand, phase, a) && MayPlanProgramme(a, programmeID)
}

// PhaseClosedReason is what somebody is told when the phase is what refuses them.
//
// Written before it was reachable, because the day the table gets its first closed cell is the day
// this sentence is needed — and a refusal invented in a hurry is how a German sentence ends up
// saying "0 rows". That day is here: the wish row has three closed cells.
const PhaseClosedReason = "In dieser Phase kann der Bedarf nicht mehr geändert werden."

// DemandRefusal picks the sentence for a refused write, out of the three that can be true.
//
// Three, because the repair differs each time: an unassigned programme lead needs an
// administrator, a lead of another programme needs to be in the right one, and a closed phase
// needs the phase moved. A single generic refusal sends all three to ask the wrong person.
func DemandRefusal(a principal.Actor, programmeID uuid.UUID, phase Phase) string {
	if !MayPlanProgramme(a, programmeID) {
		return PlanningRefusal(a)
	}
	return PhaseClosedReason
}
