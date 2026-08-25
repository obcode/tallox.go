package policy

import (
	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/principal"
)

// Who may fill the instances of which subject group.
//
// The counterpart of programme.go, and the second half of the sentence role.go has carried
// since the first migration:
//
//	"The grant is stored unscoped for now — which group it applies to becomes a question the
//	 moment subject groups exist as rows ... Anything that does depend on it must wait for the
//	 scoped form rather than approximate it with this one."
//
// Subject groups are rows now, so this is that scoped form, and the wish rule — the first rule
// that does depend on it — reads it rather than approximating.
//
// # An unscoped grant permits nothing
//
// The same reading as for study programmes, and worth repeating rather than cross-referencing,
// because it is the reading that is wrong everywhere else in this package. An empty token scope
// list and an empty role narrowing both mean "unrestricted", because both are mechanisms that
// can only ever remove. A subject group scope is not a narrowing of the grant; it is the
// grant's subject. SUBJECT_GROUP_LEAD fills the instances of ONE subject group, and the role
// that means all of them is DEANS_OFFICE.
//
// Here the widening direction would be worse than it was for programmes. Reading an unscoped
// lead as faculty-wide would make the deploy of migration 14 the moment every subject group
// lead silently gained faculty-wide access to other people's unpublished wishes — the exact
// thing the confidentiality rule exists to prevent, arriving as a side effect of a schema
// change nobody read as a permission change.
//
// # What membership is not
//
// person_subject_group says which subjects a colleague works in. It is not on this list and
// grants nothing here. The kickoff sentence "jeder in einer Fachgruppe müsste alles lesen
// können" is about planning data; unpublished wishes are read by the lead alone, because the
// first-come-first-served race the rule ends plays out inside a subject group and not across
// them.

// SubjectGroupScopeMissingReason is what a subject group lead with no subject group is told.
//
// Specific rather than generic, for the reason ProgrammeScopeMissingReason gives: somebody who
// reads "you may not do this" goes and asks for a role they already hold.
const SubjectGroupScopeMissingReason = "Ihre Fachgruppenleitung ist noch keiner Fachgruppe " +
	"zugeordnet. Bitte in der Verwaltung eintragen lassen."

// AssignmentReason is what everybody else is told.
const AssignmentReason = "Nur die Leitung dieser Fachgruppe und das Dekanat können die " +
	"Instanzen dieser Fachgruppe besetzen."

// SubjectGroupScope is the set of subject groups an actor may act in.
//
// The filter half of the pair this package keeps every rule in — the shape a WHERE clause is
// built from, so that the predicate runs in the database rather than over rows already read.
// Like ProgrammeScope it is also read directly by the interface, which needs "which subject
// groups am I responsible for" to render anything at all.
type SubjectGroupScope struct {
	// All is true for an actor whose reach is not enumerable — the dean's office.
	//
	// Deliberately not expressed as "every subject group id", which would look the same today
	// and would be a snapshot: a group created after the query was built would fall outside it.
	// That is not hypothetical here — the faculty expects to split groups in service.
	All bool
	// IDs are the subject groups an enumerable actor reaches. Empty with All false means none,
	// which is a real and common state: every lead is in it until somebody assigns them a group.
	IDs []uuid.UUID
}

// Allows reports whether this scope covers one subject group.
func (s SubjectGroupScope) Allows(subjectGroupID uuid.UUID) bool {
	return idScopeAllows(s.All, s.IDs, subjectGroupID)
}

// Empty reports whether this scope reaches nothing at all.
//
// The distinction the interface needs in order to say the useful sentence: an actor who holds
// SUBJECT_GROUP_LEAD and reaches nothing is waiting for an administrator, not being refused.
func (s SubjectGroupScope) Empty() bool { return idScopeEmpty(s.All, s.IDs) }

// AssignmentScope is what an actor may act in, by subject group.
//
// The dean's office across all of them; a subject group lead the groups it has been assigned;
// everybody else nothing. ADMIN is not on the list, the same decision the wish rule and the
// planning rule both make: running the system is a different job from planning with it.
//
// Membership is deliberately absent. A member of a subject group is a colleague who teaches its
// subjects, not somebody who fills its instances — and, once wishes exist, not somebody who
// reads other people's unpublished ones.
func AssignmentScope(a principal.Actor) SubjectGroupScope {
	roles := RolesOf(a)

	if roles.Has(RoleDeansOffice) {
		return SubjectGroupScope{All: true}
	}
	if !roles.Has(RoleSubjectGroupLead) {
		return SubjectGroupScope{}
	}

	return SubjectGroupScope{IDs: scopedIDs(a, RoleSubjectGroupLead, func(s principal.RoleScope) uuid.UUID {
		return s.SubjectGroupID
	})}
}

// MayActInSubjectGroup is the guard half: may this actor act in this subject group?
//
// The pair AssignmentScope/MayActInSubjectGroup is the same two-form arrangement as
// CanSeeWish/WishVisibility and PlanningScope/MayPlanProgramme, and for the same reason — one
// of them ends up in a WHERE clause and the other in a check on a row already in hand, and they
// have to agree. TestAssignmentGuardAndScopeAgree asserts it over the full cartesian product.
//
// Named for acting rather than for assigning, because the assignment phase does not exist yet
// and this already decides something real: which unpublished wishes a subject group lead reads.
func MayActInSubjectGroup(a principal.Actor, subjectGroupID uuid.UUID) bool {
	return AssignmentScope(a).Allows(subjectGroupID)
}

// HoldsSubjectGroupLeadWithoutScope distinguishes "not allowed" from "not set up yet".
//
// Only for choosing which sentence to show. It is not a permission and grants nothing — a
// caller that treated it as one would be reading an unscoped grant as a universal one, which is
// exactly the reading this file rejects.
func HoldsSubjectGroupLeadWithoutScope(a principal.Actor) bool {
	roles := RolesOf(a)
	if roles.Has(RoleDeansOffice) || !roles.Has(RoleSubjectGroupLead) {
		return false
	}
	return AssignmentScope(a).Empty()
}

// AssignmentRefusal is the sentence to show when acting in a subject group is refused.
func AssignmentRefusal(a principal.Actor) string {
	if HoldsSubjectGroupLeadWithoutScope(a) {
		return SubjectGroupScopeMissingReason
	}
	return AssignmentReason
}
