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
//
// Names all three ways to be responsible, and it grew the middle one on 2026-08-27 when the
// faculty decided that a study programme lead fills instances too. A refusal that listed only the
// subject group would send somebody who leads the programme to ask for a role they hold.
const AssignmentReason = "Diese Instanz kann nur die Leitung ihrer Fachgruppe, die Leitung " +
	"ihres Studiengangs oder das Dekanat besetzen."

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
// Named for acting rather than for assigning, and the name has outlived its first reason. It was
// written before the assignment existed, when what it already decided was which unpublished wishes
// a subject group lead reads. It stays because it is still the wider statement: this is one of the
// two axes MayWriteAssignment takes the union of, and it answers the wish rule as well.
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

// ModuleFilingReason is what somebody who may not file this module is told.
//
// Names the two things that can be wrong, because they have different repairs: the target group
// is not one you lead, or the module is currently in somebody else's. A refusal that said only
// "not allowed" would send a lead who is looking at her own group to ask for a role she holds.
const ModuleFilingReason = "Module lassen sich nur in eine Fachgruppe einsortieren, die Sie " +
	"leiten — und nur, solange sie in keiner anderen Fachgruppe stehen."

// MayFileModule reports whether a may move one module from the subject group it is in today
// into the one it should be in.
//
// uuid.Nil on either side means "no subject group": as `from` it is a module nobody has sorted
// yet, which is the ordinary state until the faculty has worked through its catalogue, and as
// `to` it is taking a module out of every group.
//
// # Both sides, not just the target
//
// Filing is the one act in this package that touches two subject groups at once, and checking
// only the target would make "move this module into mine" a unilateral act against the group it
// is taken from. So both ends have to be in reach: a lead may pull in a module that is
// unsorted, and may let go of one that is hers, and may do nothing at all to one that is filed
// under a colleague's group. Re-cutting the catalogue across two groups stays what it was — an
// administrator's job, done once, visibly.
//
// # Why the leads at all
//
// The faculty asked for it, and the reason is that the assignment already works this way: the
// lead of a group is the person who fills its instances and who reads the unpublished wishes on
// them (AssignmentScope, above). Being unable to say which modules those *are* made her
// responsible for a set somebody else had to maintain for her. The scope she already holds is
// exactly the right size for the question.
//
// ADMIN keeps the unrestricted form: sorting 506 modules in October is administration, and it
// crosses every group by construction. It is the one place in this file where ADMIN appears at
// all — AssignmentScope leaves it out on purpose, because running the system is a different job
// from planning with it, and filing the catalogue is the administrative half of this act rather
// than the planning half.
//
// DEANS_OFFICE reaches every group through AssignmentScope, and that is deliberate rather than
// incidental — it is the role that means "all subject groups", and a special case saying
// otherwise here would be a rule that holds nowhere else.
func MayFileModule(a principal.Actor, from, to uuid.UUID) bool {
	// An actor who reaches nothing is refused before the two sides are looked at. Without this,
	// moving a module from no group to no group — a row in a batch that changes nothing —
	// would come back true for everybody, and a rule that says "anybody may do nothing" is a
	// rule somebody will later read as "anybody may do this".
	scope := FilingScope(a)
	if scope.Empty() {
		return false
	}

	return (from == uuid.Nil || scope.Allows(from)) &&
		(to == uuid.Nil || scope.Allows(to))
}

// FilingScope is the filter half: which subject groups an actor may file modules across.
//
// The pair FilingScope/MayFileModule is the same two-form arrangement as
// WishVisibility/CanSeeWish, and it exists here for the same reason. The target of a move is a
// single value the service has in its hand, but "which group is this module in today" is a
// column — one per module in a batch of five hundred. Asking Go would mean reading the rows
// first and deciding afterwards, with the decision resting on a state from before the write.
// As a scope it becomes a WHERE clause in the same statement.
//
// TestFilingGuardAndScopeAgree asserts the two forms give the same answer over the full
// cartesian product, which is the realistic way this arrangement breaks: somebody changes one
// of them.
//
// Through a token it reaches nothing — not even for an administrator. The mutation carries
// @interactiveOnly and the rule repeats it rather than leaving it to the directive alone:
// re-filing a module moves who may read the unpublished wishes on its instances, and a
// long-lived token in a script could do that quietly and in bulk.
func FilingScope(a principal.Actor) SubjectGroupScope {
	if !MayReadInteractiveOnly(a) {
		return SubjectGroupScope{}
	}
	if RolesOf(a).Has(RoleAdmin) {
		return SubjectGroupScope{All: true}
	}
	return AssignmentScope(a)
}

// ModuleFilingRefusal is the sentence to show when filing a module is refused.
func ModuleFilingRefusal(a principal.Actor) string {
	if HoldsSubjectGroupLeadWithoutScope(a) {
		return SubjectGroupScopeMissingReason
	}
	return ModuleFilingReason
}
