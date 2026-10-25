package policy

import (
	"slices"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/principal"
)

// Assigned load: how many hours a week somebody already holds in a semester, in total and by the
// subject group of the modules they hold them in.
//
// # The one aggregate over confidential rows, and why it is allowed
//
// Before the assignments of a semester are published, a subject group lead reads the assignments
// of their own group's modules and nothing of anybody else's (UnpublishedAssignmentScope), and
// assignment.sql refuses any COUNT or SUM so that the rows a lead may not read do not reach them
// as a number instead. This rule is a deliberate exception to that, decided with the faculty on
// 2026-10-22: the lead of a group reads, for each member of it, the hours that member already
// holds in every subject group — "A: 4 · B: 8 · ohne Fachgruppe: 2".
//
// The reason is the question the lead has to answer while filling an instance: is this colleague
// already full, and if so, with whom do I have to talk? Somebody can be in several subject groups
// and heavily booked in another one, and without the figure the lead finds out after the fact. A
// total alone answers the first half; the breakdown by group answers the second, which is why the
// faculty chose it over the total.
//
// What it does not reveal: which modules, instances or parts, or anything about people who are not
// members of the lead's groups. It is the person-directed shape of DesiredHoursNoteVisibility, not
// the instance-directed one of AssignmentVisibility.
//
// Read by the person, through either door; in an interactive session also by the lead of any
// subject group the person is a member of, and by the dean's office; once the assignments are
// published, by everybody in a signed-in session. Through a token only one's own, in every state:
// the published assignments are public row by row, but a per-person total of the whole faculty in
// one script call is the bulk export of personnel data rule 2 keeps off the token door (decided
// 2026-10-25). Never by a programme lead on the strength of that role before publication: they
// reach instances, not people.

// AssignedLoad is the minimum of a person's load that the read rule needs.
type AssignedLoad struct {
	// OwnerID is whose load it is.
	OwnerID uuid.UUID
	// MemberOf are the subject groups the person is a member of, as of reading.
	MemberOf []uuid.UUID
}

// AssignedLoadFilter is the read rule in the shape a query can apply. The same scopes, with the
// same meaning, as DesiredHoursFilter: a filter over people, not over assignments.
type AssignedLoadFilter = DesiredHoursFilter

// CanSeeAssignedLoad reports whether the actor may see this person's load. The guard form.
func CanSeeAssignedLoad(a principal.Actor, s SemesterState, l AssignedLoad) bool {
	switch {
	case !a.Authenticated():
		return false
	case a.Owns(l.OwnerID):
		return true
	case PartnerOnly(a), !a.Interactive():
		return false
	case s.AssignmentsPublished():
		return true
	default:
		groups := AssignmentScope(a)
		return groups.All || slices.ContainsFunc(l.MemberOf, groups.Allows)
	}
}

// AssignedLoadVisibility returns the same rule as a query filter.
func AssignedLoadVisibility(a principal.Actor, s SemesterState) AssignedLoadFilter {
	switch {
	case !a.Authenticated():
		return AssignedLoadFilter{Scope: DesiredHoursScopeNone}
	case PartnerOnly(a), !a.Interactive():
		return AssignedLoadFilter{Scope: DesiredHoursScopeOwn, OwnerID: a.ID}
	case s.AssignmentsPublished():
		return AssignedLoadFilter{Scope: DesiredHoursScopeAll}
	default:
		// The note's rule, and on purpose the same function: "the person, the leads of their
		// groups, the dean's office, interactively" is one sentence, and two copies of it would
		// drift.
		return DesiredHoursNoteVisibility(a)
	}
}
