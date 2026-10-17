package policy

import (
	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/principal"
)

// ModuleSplitReason is what somebody who may not state a module's split is told.
const ModuleSplitReason = "Nur die Fachgruppenleitung dieses Moduls, die Studiengangsleitung " +
	"seines Heimatstudiengangs und das Dekanat können die SWS-Aufteilung festlegen."

// MayWriteModuleSplit is the rule for stating how a module's hours divide between lecture, lab
// and exercise.
//
// The lead of the module's home programme, the lead of the subject group it is filed under, and
// the dean's office. subjectGroupID is uuid.Nil for a module nobody has sorted yet, and then only
// the programme half can answer — the nil group is in nobody's reach.
//
// # Why the subject group lead is here
//
// Added 2026-10-17, on a request from the faculty: a subject group lead followed the "eintragen"
// link from the catalogue, entered a split, and was refused on saving. The split is not a
// planning decision about a programme — it says how a subject is taught, which the people who
// teach it know best, and which is the lead of its subject group's to arrange. It is also the
// assignment's union, MayWriteAssignment, for the same reason: the two axes reach the same
// module from different sides, and the faculty asked for both.
//
// What a race between the two decides is the last write, and that is acceptable here rather than
// handled: the split is one statement replaced as a whole, it is entered once per module, and
// the audit columns on module_component say who said it.
//
// No phase condition, like the rest of the catalogue: a module hangs off no semester.
func MayWriteModuleSplit(a principal.Actor, subjectGroupID, homeProgrammeID uuid.UUID) bool {
	return MayActInSubjectGroup(a, subjectGroupID) || MayPlanProgramme(a, homeProgrammeID)
}

// ModuleSplitRefusal picks the sentence for a refused split, out of the three that can be true.
//
// The two "scope missing" sentences come first for the reason AssignmentWriteRefusal gives:
// somebody who holds the role and has not been given a group or programme needs an
// administrator, and "you may not do this" would send them asking for a role they already hold.
// A person holding both leaderships unscoped is told about the subject group, the half this rule
// added.
func ModuleSplitRefusal(a principal.Actor) string {
	if HoldsSubjectGroupLeadWithoutScope(a) {
		return SubjectGroupScopeMissingReason
	}
	if HoldsProgrammeLeadWithoutScope(a) {
		return ProgrammeScopeMissingReason
	}
	return ModuleSplitReason
}
