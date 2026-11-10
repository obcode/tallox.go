package policy

import "github.com/obcode/tallox.go/internal/principal"

// MayEnterTeacher reports whether an actor may enter somebody who teaches and whom the ZPA does
// not list yet, or correct such a row.
//
// The subject group leads — of any group, since the person is not in a group yet — the programme
// leads, and the dean's office: the people who assign, and the ones who would otherwise have
// nobody to pick. A programme lead reviews the assignment of its programmes and may change it
// until the semester ends, so it meets the same missing name. A lead nobody has scoped yet
// reaches no group or programme and so may not either; that is the same waiting state every
// other act of a lead has.
//
// With full reach, like the assignment the row exists for. The refusal of a taken address answers
// whether somebody already has an account, which a plain token is not told.
func MayEnterTeacher(a principal.Actor) bool {
	return a.Authenticated() && a.ActsWithFullReach() &&
		(!AssignmentScope(a).Empty() || !PlanningScope(a).Empty())
}

// EnterTeacherReason is the refusal when somebody may not enter a teacher by hand.
const EnterTeacherReason = "Lehrpersonen, die noch nicht im ZPA stehen, legen die " +
	"Fachgruppenleitungen, die Studiengangsleitungen und das Dekanat an."
