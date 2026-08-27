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

	// WriteAreaWishes is registering interest in a course instance: creating, changing and
	// withdrawing one's own wishes.
	WriteAreaWishes WriteArea = "WISHES"

	// WriteAreaAssignment is filling the parts of a course instance: who holds the lecture, who
	// holds each laboratory group.
	WriteAreaAssignment WriteArea = "ASSIGNMENT"
)

// AllWriteAreas returns every area, in the order of the planning process.
//
// New areas arrive with the tables that need them — WISHES with the wish rows, ASSIGNMENT with
// the assignments — rather than being declared in advance, for the same reason ScopeArea gives:
// an area with nothing behind it is a promise that somebody can read and nobody keeps.
func AllWriteAreas() []WriteArea {
	return []WriteArea{WriteAreaDemand, WriteAreaWishes, WriteAreaAssignment}
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
	// # Open until the semester is finished
	//
	// Decided with the faculty on 2026-08-25, and it replaced a narrower reading — open in the
	// wish phase alone — that this table had carried for a day. The rule they asked for is
	// "solange das Semester noch nicht abgeschlossen ist, also solange die Zuteilung nicht
	// erfolgt ist", which is every phase up to and including ASSIGNMENT.
	//
	// It is the same argument the demand row makes one line up, and it is worth reading twice
	// because both times it beats the tidier rule. A colleague falls ill, a cohort turns out
	// larger than the numbers said, somebody is asked in the corridor whether they would take the
	// second cohort after all. Refusing to record that does not stop it happening — it moves it
	// into a mail to the subject group lead, and then the tool's list is the wrong one. What
	// protects the assignment is not a closed phase but the assignment itself, which is a
	// decision somebody takes and not a consequence of what is on the wish list.
	//
	// DEMAND_PLANNING is open for the less obvious half of the same reason: the demand of the
	// *next* semester is often visible long before the wish phase opens, and somebody who knows
	// now which subject they want should be able to say so now.
	//
	// FINAL is closed, and it is the only closed cell in this table. A finished semester is a
	// record of what the faculty did; a wish registered afterwards would change that record
	// without changing anything about the teaching.
	WriteAreaWishes: {
		PhaseDemandPlanning: {RoleLecturer},
		PhaseWishes:         {RoleLecturer},
		PhaseAssignment:     {RoleLecturer},
		PhaseFinal:          nil,
	},
	// The row that closes early rather than late, and it is the only one in this table that does.
	//
	// # Why the two early cells are shut
	//
	// Decided 2026-08-27. Every other closed cell in this table is closed because the record is
	// finished; these two are closed because of what filling a part early would do to the step
	// before it. A colleague deciding whether to register interest, who finds the instance already
	// filled, is in exactly the first-come-first-served race the confidentiality rule exists to
	// end — and this time the tool would have staged it. Publishing the wishes and closing the
	// wish phase are separate acts precisely so that the assignment can start from a complete
	// picture; starting it earlier makes the wish phase decorative.
	//
	// It is worth being explicit that this is not an argument about who is trusted. A subject
	// group lead who already knows in June who will hold the lecture can say so in June — in a
	// mail, in a corridor, in the subject group's own minutes. What the closed cell refuses is
	// making that provisional decision look, to the person reading the wish screen, like the
	// finished one.
	//
	// # Why FINAL is open
	//
	// The same argument the demand row makes, and it beats the tidier rule here too. Somebody
	// falls ill in November, a lecturer on contract cancels, a laboratory group is handed over.
	// Refusing to record that does not prevent it — it moves it into a mail, and then the tool's
	// list is the wrong one, which is the failure mode this system exists to remove. What protects
	// a finished plan is not a closed phase but the fact that changing it is a decision somebody
	// takes and signs their name to: assigned_by is on every row.
	//
	// Note the asymmetry with the wish row directly above, which *is* shut in FINAL. A wish
	// registered after the semester is settled would change the record of what the faculty
	// considered without changing anything about the teaching. A reassignment changes the
	// teaching, which is why it is the one that stays open.
	WriteAreaAssignment: {
		PhaseDemandPlanning: nil,
		PhaseWishes:         nil,
		PhaseAssignment:     {RoleSubjectGroupLead, RoleProgrammeLead, RoleDeansOffice},
		PhaseFinal:          {RoleSubjectGroupLead, RoleProgrammeLead, RoleDeansOffice},
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

// AssignmentPhaseClosedReason is what somebody is told when the phase is what refuses the write.
//
// Its own sentence rather than PhaseClosedReason, which says "der Bedarf" in plain words. It also
// has to say something that one does not: the assignment is refused *before* its phase and not
// after it, so "die Phase ist vorbei" would be the wrong half of the truth in the case that
// actually occurs. Somebody meeting this refusal is early, and the repair is to advance the phase.
const AssignmentPhaseClosedReason = "Zugeteilt wird ab der Zuteilungsphase. Solange die " +
	"Wunschphase läuft, sollen die Instanzen offen bleiben."

// MayWriteAssignment is the whole rule for filling a part: the right role, in a phase that is
// open, for an instance this actor is responsible for.
//
// Responsibility is a union of the two orthogonal reaches, not an intersection, and that is the
// one thing about this rule that has to be read carefully. A subject group lead reaches the
// modules of their subject across every study programme; a study programme lead reaches the
// instances of their programme across every subject. Either is enough.
//
// # Why the programme lead is here
//
// Decided 2026-08-27, and it replaces the reading the subject group matrix carried before: "a
// programme lead declares instances and does not fill them". The faculty asked for both, and the
// argument that settled it is the module that belongs to no subject group — with subject groups
// alone, filling it would be the dean's office or nobody, and the catalogue has plenty of those
// while it is being sorted.
//
// The consequence, which is real and is handled rather than avoided: two roles may now write the
// same row. What decides a race is therefore not this function but the write itself —
// internal/store replaces an assignment only when the caller names the one they are replacing, so
// an unconditional write can only ever fill a part that is free. Compare AdvanceSemesterPhase,
// which took the same shape for the same reason.
func MayWriteAssignment(a principal.Actor, subjectGroupID, programmeID uuid.UUID, phase Phase) bool {
	if !MayWriteInPhase(WriteAreaAssignment, phase, a) {
		return false
	}
	return MayActInSubjectGroup(a, subjectGroupID) || MayPlanProgramme(a, programmeID)
}

// AssignmentWriteRefusal picks the sentence for a refused assignment, out of the four that can be
// true.
//
// Four, because the repair differs every time, and the ordering matters: the phase is checked
// last, so that somebody who is not responsible for this instance is not told to go and ask for
// the phase to be advanced.
//
// The two "scope missing" sentences are the reason this is not one generic refusal. A subject
// group lead nobody has given a subject to, and a programme lead nobody has given a programme to,
// both need an administrator — and both would otherwise read "you may not do this" and go asking
// for a role they already hold.
func AssignmentWriteRefusal(a principal.Actor, subjectGroupID, programmeID uuid.UUID, phase Phase) string {
	if MayActInSubjectGroup(a, subjectGroupID) || MayPlanProgramme(a, programmeID) {
		return AssignmentPhaseClosedReason
	}
	if HoldsSubjectGroupLeadWithoutScope(a) {
		return SubjectGroupScopeMissingReason
	}
	if HoldsProgrammeLeadWithoutScope(a) {
		return ProgrammeScopeMissingReason
	}
	return AssignmentReason
}
