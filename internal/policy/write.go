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
	// Open until the semester is finished. Every area, and that is the shape the faculty asked
	// for on 2026-08-28 rather than a table that lost its content.
	//
	// # What this table stopped being
	//
	// It used to be the answer to "may I write this now". It is not any more, and the reason is
	// that it was answering at the wrong grain: one value on the semester, for the whole faculty,
	// while the planning happens per study programme and per subject group and at different
	// speeds. What opens and closes is now decided by the people who carry that responsibility —
	// demand_completion announces, wish_window opens and shuts — and neither is a phase.
	//
	// # What it still is
	//
	// The one hard meaning the phase keeps: FINAL is finished. A semester that has been closed is
	// the record of what the faculty did, and a record that can still be edited is not one. That
	// is why every row below has the same shape, and why the table is worth keeping in spite of
	// it: the sentence "nothing may be written after the semester is closed" is one rule with one
	// place to read it, and the next decision about when something closes lands here rather than
	// in three conditions somebody has to find.
	//
	// The demand row changed with this: it used to stay open in FINAL, on the argument that a
	// late instance is a correction and a refused correction happens anyway. That argument is now
	// carried by the three phases before FINAL, all of which are open — and letting the demand
	// alone survive the close would make FINAL mean two things depending on which screen somebody
	// is looking at.
	WriteAreaDemand: {
		PhaseDemandPlanning: {RoleProgrammeLead, RoleDeansOffice},
		PhaseWishes:         {RoleProgrammeLead, RoleDeansOffice},
		PhaseAssignment:     {RoleProgrammeLead, RoleDeansOffice},
		PhaseFinal:          nil,
	},
	// LECTURER alone, and that is the whole list rather than an abbreviation of it: role.go says
	// LECTURER is the baseline everybody who appears in the planning holds, so a colleague who
	// also leads a programme registers interest as a lecturer like anybody else.
	//
	// Whether the door of a particular subject is open is not decided here — see wish_window and
	// domain.ErrWishWindowClosed. This row only says that a finished semester takes no more
	// entries: a wish registered afterwards would change the record of what the faculty
	// considered without changing anything about the teaching.
	WriteAreaWishes: {
		PhaseDemandPlanning: {RoleLecturer},
		PhaseWishes:         {RoleLecturer},
		PhaseAssignment:     {RoleLecturer},
		PhaseFinal:          nil,
	},
	// Open from the start, which reversed a decision taken one day earlier (2026-08-27) that shut
	// the two phases before ASSIGNMENT. The argument then was that filling an instance while the
	// wish phase runs is the first-come-first-served race the confidentiality rule exists to end.
	//
	// What the faculty answered is that the wish round is not a phase of the faculty at all: it
	// is the subject group's own, opened and closed by its lead, who is the same person who then
	// fills the instances. A tool that ordered those two for them would be ordering the work of
	// somebody who can see the whole of it — and the race it prevented is one that lead can now
	// simply not run, by shutting their window first.
	WriteAreaAssignment: {
		PhaseDemandPlanning: {RoleSubjectGroupLead, RoleProgrammeLead, RoleDeansOffice},
		PhaseWishes:         {RoleSubjectGroupLead, RoleProgrammeLead, RoleDeansOffice},
		PhaseAssignment:     {RoleSubjectGroupLead, RoleProgrammeLead, RoleDeansOffice},
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
// One sentence for all three areas since 2026-08-28, because there is now one closed cell per area
// and it is the same one: the semester is finished. It used to say "in dieser Phase", which was
// true of a table with several closed cells and would now be misleading — a reader would go
// looking for the phase in which it *is* allowed, and there is none after this one.
const PhaseClosedReason = "Dieses Semester ist abgeschlossen und wird nicht mehr geändert."

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

// AssignmentPhaseClosedReason is what somebody is told when the phase refuses an assignment.
//
// The same sentence PhaseClosedReason gives, and it is a named constant rather than a use of that
// one so that the assignment area keeps its own line to change. It said something else for a day:
// that filling is refused *before* its phase, which was the decision of 2026-08-27 and was
// reversed on 2026-08-28 — the wish round turned out to belong to the subject group rather than to
// the faculty, and a tool that ordered the two for a lead who can see both was ordering the wrong
// thing.
const AssignmentPhaseClosedReason = PhaseClosedReason

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
