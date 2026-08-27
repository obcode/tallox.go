package policy

import (
	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/principal"
)

// Who may open and close the two things that are not phases.
//
// Decided 2026-08-28, and the decision was about grain rather than about permission. The planning
// used to open and close for the whole faculty at once, on semester.phase. It does not work that
// way: the study programmes settle their demand at different times, and each subject group runs
// its own wish round. So both are now switched where the responsibility already lies, by people
// who hold no new role to do it.
//
// Neither switch is in the write matrix, and that is deliberate. The matrix answers "when in the
// process", which is now one question with one answer — until the semester is finished. These two
// answer "has this programme said it is done" and "is this subject taking entries", which are
// facts somebody states rather than stages a process passes through.

// DemandAnnouncementReason is what somebody is told who may not announce this programme's demand.
const DemandAnnouncementReason = "Nur die Leitung dieses Studiengangs und das Dekanat können " +
	"den Bedarf als fertig melden."

// WishWindowSwitchReason is what somebody is told who may not open or close this subject's wishes.
const WishWindowSwitchReason = "Nur die Leitung dieser Fachgruppe und das Dekanat können die " +
	"Wunschphase dieser Fachgruppe öffnen und schließen."

// WishWindowClosedReason is what a lecturer is told whose subject is not taking entries.
//
// It names who can change it, because that is the repair — and it says "derzeit", because this
// door opens again. A sentence that read like the end of the process would send somebody to the
// dean's office about a switch their own subject group lead holds.
const WishWindowClosedReason = "Die Wunschphase dieser Fachgruppe ist derzeit geschlossen. " +
	"Die Fachgruppenleitung kann sie wieder öffnen."

// MayAnnounceDemandComplete reports whether this actor may say that a programme's demand is
// settled for a semester.
//
// The same reach as writing the demand, and a function of its own so that the two can part
// company: announcing is a statement about work somebody did, and the day the faculty wants a
// programme lead who may declare instances but not speak for the programme, this is the line that
// changes.
//
// Not bound by the phase. Announcing is not a write to the plan — and a finished semester whose
// demand was never announced is a fact somebody may still want to record.
func MayAnnounceDemandComplete(a principal.Actor, programmeID uuid.UUID) bool {
	return MayPlanProgramme(a, programmeID)
}

// DemandCompletionRefusal picks the sentence for a refused announcement.
func DemandCompletionRefusal(a principal.Actor, programmeID uuid.UUID) string {
	if HoldsProgrammeLeadWithoutScope(a) {
		return ProgrammeScopeMissingReason
	}
	_ = programmeID
	return DemandAnnouncementReason
}

// MaySetWishWindow reports whether this actor may open or close a subject group's wish round.
//
// The same reach as filling that subject's instances, which is the point: the lead who closes the
// round is the one who then works from what it collected. Nobody needs a new role for it, and
// nobody outside the subject can shut it.
//
// The dean's office reaches every group, including one created tomorrow — the same enumerable /
// not-enumerable distinction AssignmentScope makes.
func MaySetWishWindow(a principal.Actor, subjectGroupID uuid.UUID) bool {
	return MayActInSubjectGroup(a, subjectGroupID)
}

// WishWindowRefusal picks the sentence for a refused switch.
func WishWindowRefusal(a principal.Actor) string {
	if HoldsSubjectGroupLeadWithoutScope(a) {
		return SubjectGroupScopeMissingReason
	}
	return WishWindowSwitchReason
}
