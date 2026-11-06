package policy

import "github.com/obcode/tallox.go/internal/principal"

// MayEnterTeacher reports whether an actor may enter somebody who teaches and whom the ZPA does
// not list yet, or correct such a row.
//
// The subject group leads — of any group, since the person is not in a group yet — and the dean's
// office: the people who assign, and the ones who would otherwise have nobody to pick. A lead
// nobody has assigned a group to yet reaches no group and so may not either; that is the same
// waiting state every other act of a lead has.
//
// With full reach, like the assignment the row exists for. The refusal of a taken address answers
// whether somebody already has an account, which a plain token is not told.
func MayEnterTeacher(a principal.Actor) bool {
	return a.Authenticated() && a.ActsWithFullReach() && !AssignmentScope(a).Empty()
}

// EnterTeacherReason is the refusal when somebody may not enter a teacher by hand.
const EnterTeacherReason = "Lehrpersonen, die noch nicht im ZPA stehen, legen die " +
	"Fachgruppenleitungen und das Dekanat an."
