package policy

import (
	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/principal"
)

// Who may declare the demand of which study programme.
//
// This is the first rule in the system whose answer depends on *which* thing is being acted on
// rather than only on who is acting. Wish confidentiality is faculty-wide by construction; a
// programme lead leads one programme, and role.go has said so since the first migration while
// deliberately not being able to enforce it:
//
//	"The grant is stored unscoped for now — which group it applies to becomes a question the
//	 moment subject groups exist as rows ... Anything that does depend on it must wait for the
//	 scoped form rather than approximate it with this one."
//
// This is that scoped form for study programmes. Subject groups get theirs when they become
// rows; the constant below and person_programme_scope's CHECK are both written so that widening
// is one line.
//
// # An unscoped grant permits nothing
//
// This runs against the precedent set twice elsewhere in this package — an empty token scope
// list and an empty role narrowing both mean "unrestricted" — and the precedent does not
// transfer. Those two are mechanisms that can only ever remove, so "nothing selected" has to
// mean "nothing removed" or the empty set becomes the most restrictive value of a column whose
// default is empty. A programme scope is not a narrowing of the grant; it is the grant's
// subject. PROGRAMME_LEAD declares the demand of one programme, and a role that means all of
// them already exists and is called DEANS_OFFICE.
//
// Read the other way, the migration that introduced the table would have been the deploy on
// which every existing programme lead silently became faculty-wide — the widening direction,
// which nothing in this system takes. What a lead without a scope meets instead is a refusal
// that names its own repair.

// ProgrammeScopeMissingReason is what a programme lead with no programme is told.
//
// Specific rather than generic, and that is the point of it existing separately from the
// ordinary refusal: somebody who reads "you may not do this" goes and asks for a role they
// already hold. Somebody who reads this asks for the thing that is actually missing.
const ProgrammeScopeMissingReason = "Ihre Studiengangsleitung ist noch keinem Studiengang " +
	"zugeordnet. Bitte in der Verwaltung eintragen lassen."

// PlanningReason is what everybody else is told.
const PlanningReason = "Nur die Studiengangsleitung dieses Studiengangs und das Dekanat " +
	"können den Bedarf festlegen."

// ProgrammeScope is the set of study programmes an actor may plan for.
//
// The filter half of the pair this package keeps every rule in — the shape a WHERE clause is
// built from, so that the predicate runs in the database rather than over rows already read.
// Unlike the wish filter it is also read directly by the interface, which needs "which
// programmes may I plan" to render a picker at all.
type ProgrammeScope struct {
	// All is true for an actor whose reach is not enumerable — the dean's office, which reads
	// and plans across programmes because the import/export statistics are its job.
	//
	// Deliberately not expressed as "every programme id", which would look the same today and
	// would be a snapshot: a programme created after the query was built would silently fall
	// outside it.
	All bool
	// IDs are the programmes an enumerable actor may plan for. Empty with All false means the
	// actor may plan for none, which is a real and common state — every programme lead is in
	// it until somebody assigns them a programme.
	IDs []uuid.UUID
}

// Allows reports whether this scope covers one programme.
//
// The nil programme is not a programme — see idScopeAllows, which is where that guard and the
// rest of the mechanics live, shared with the subject group scope.
func (s ProgrammeScope) Allows(programmeID uuid.UUID) bool {
	return idScopeAllows(s.All, s.IDs, programmeID)
}

// Empty reports whether this scope reaches nothing at all.
//
// The distinction the interface needs in order to say the useful sentence: an actor who holds
// PROGRAMME_LEAD and reaches nothing is waiting for an administrator, not being refused.
func (s ProgrammeScope) Empty() bool { return idScopeEmpty(s.All, s.IDs) }

// PlanningScope is what an actor may plan for.
//
// The dean's office plans across programmes; a programme lead plans the programmes it has been
// scoped to; everybody else plans nothing. ADMIN is not on the list, and that is the same
// decision the wish rule makes: running the system is a different job from planning with it,
// and an administrator who genuinely has to plan is granted the role visibly.
func PlanningScope(a principal.Actor) ProgrammeScope {
	roles := RolesOf(a)

	if roles.Has(RoleDeansOffice) {
		return ProgrammeScope{All: true}
	}
	if !roles.Has(RoleProgrammeLead) {
		return ProgrammeScope{}
	}

	return ProgrammeScope{IDs: scopedIDs(a, RoleProgrammeLead, func(s principal.RoleScope) uuid.UUID {
		return s.ProgrammeID
	})}
}

// MayPlanProgramme is the guard half: may this actor declare demand for this programme?
//
// The pair PlanningScope/MayPlanProgramme is the same two-form arrangement as
// CanSeeWish/WishVisibility, and for the same reason — one of them ends up in a WHERE clause
// and the other in a check on a row already in hand, and they have to agree. A property test
// over the full cartesian product asserts that they do; drift between them is the realistic way
// this design fails.
func MayPlanProgramme(a principal.Actor, programmeID uuid.UUID) bool {
	return PlanningScope(a).Allows(programmeID)
}

// HoldsProgrammeLeadWithoutScope distinguishes "not allowed" from "not set up yet".
//
// Only for choosing which sentence to show. It is not a permission and grants nothing — a
// caller that treated it as one would be reading an unscoped grant as a universal one, which is
// exactly the reading this file rejects.
func HoldsProgrammeLeadWithoutScope(a principal.Actor) bool {
	roles := RolesOf(a)
	if roles.Has(RoleDeansOffice) || !roles.Has(RoleProgrammeLead) {
		return false
	}
	return PlanningScope(a).Empty()
}

// PlanningRefusal is the sentence to show when planning is refused, chosen between the two.
func PlanningRefusal(a principal.Actor) string {
	if HoldsProgrammeLeadWithoutScope(a) {
		return ProgrammeScopeMissingReason
	}
	return PlanningReason
}
