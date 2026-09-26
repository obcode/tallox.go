package policy

import (
	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/principal"
)

// Competences: who can teach which module, and who would like to.
//
// The read rule is the wish rule without a publication date. WOULD_LIKE is a wish without a
// semester — "Kollege X will auch Verteilte Systeme" is exactly the first-come signal the wish
// rule exists to suppress — and because there is no semester, nothing ever lifts it. Decided
// 2026-09-26: read by the holder, by whoever is responsible for the module, and by the dean's
// office; through a token only one's own.
//
// Responsible means the same two axes as for a wish, with one substitution: a competence has no
// instance, so the programme is the module's home programme rather than the demanding one.

// MinCompulsoryCompetences is how many compulsory modules of a subject group somebody in it
// should be able to teach. The kickoff said "mindestens 3 oder 4 Fächer aus dem Pflichtkatalog";
// three is the lower end, and it is a warning and a work list rather than a validation (decided
// 2026-09-26), so the number can move without anybody's profile becoming invalid.
const MinCompulsoryCompetences = 3

// Competence is the minimum of a competence the read rule needs.
type Competence struct {
	// OwnerID is the account the statement is about, or uuid.Nil for a teacher without one. Such a
	// row is nobody's own and is read only through responsibility.
	OwnerID uuid.UUID
	// ProgrammeID is the home programme of the module.
	ProgrammeID uuid.UUID
	// SubjectGroupID is the module's subject group, or uuid.Nil while it has none. Fails closed:
	// no subject group lead reaches it.
	SubjectGroupID uuid.UUID
}

// CompetenceScope is how much of the competence table a caller may read. Same four values, and
// the same meaning, as WishScope — the query translates them identically.
type CompetenceScope string

const (
	// CompetenceScopeNone grants nothing. The anonymous caller.
	CompetenceScopeNone CompetenceScope = "none"
	// CompetenceScopeOwn grants the caller's own statements.
	CompetenceScopeOwn CompetenceScope = "own"
	// CompetenceScopeOwnOrScoped adds the modules of the programmes and subject groups the caller
	// leads.
	CompetenceScopeOwnOrScoped CompetenceScope = "own_or_scoped"
	// CompetenceScopeAll grants everything.
	CompetenceScopeAll CompetenceScope = "all"
)

// CompetenceFilter is the read rule in the shape a query can apply. See WishFilter, whose
// translation into SQL this one shares line for line.
type CompetenceFilter struct {
	Scope           CompetenceScope
	OwnerID         uuid.UUID
	ProgrammeIDs    []uuid.UUID
	SubjectGroupIDs []uuid.UUID
}

// Matches reports whether a single competence passes this filter. The counterpart of the WHERE
// clause; TestCompetenceGuardAndFilterAgree holds the two together.
func (f CompetenceFilter) Matches(c Competence) bool {
	owns := f.OwnerID != uuid.Nil && c.OwnerID == f.OwnerID
	switch f.Scope {
	case CompetenceScopeAll:
		return true
	case CompetenceScopeOwn:
		return owns
	case CompetenceScopeOwnOrScoped:
		return owns ||
			idScopeAllows(false, f.ProgrammeIDs, c.ProgrammeID) ||
			idScopeAllows(false, f.SubjectGroupIDs, c.SubjectGroupID)
	default:
		return false
	}
}

// CanSeeCompetence reports whether the actor may see this competence. The guard form.
//
// Visible iff one's own, or — in an interactive session — the actor is responsible for the
// module. There is no publication clause, which is the whole difference from CanSeeWish.
func CanSeeCompetence(a principal.Actor, c Competence) bool {
	switch {
	case !a.Authenticated():
		return false
	case a.Owns(c.OwnerID):
		return true
	case !a.Interactive():
		return false
	default:
		programmes, groups := CompetenceReadScope(a)
		return programmes.Allows(c.ProgrammeID) || groups.Allows(c.SubjectGroupID)
	}
}

// CompetenceVisibility returns the same rule as a query filter.
func CompetenceVisibility(a principal.Actor) CompetenceFilter {
	switch {
	case !a.Authenticated():
		return CompetenceFilter{Scope: CompetenceScopeNone}
	case !a.Interactive():
		return CompetenceFilter{Scope: CompetenceScopeOwn, OwnerID: a.ID}
	}

	programmes, groups := CompetenceReadScope(a)
	if programmes.All || groups.All {
		return CompetenceFilter{Scope: CompetenceScopeAll}
	}
	if programmes.Empty() && groups.Empty() {
		return CompetenceFilter{Scope: CompetenceScopeOwn, OwnerID: a.ID}
	}
	return CompetenceFilter{
		Scope:           CompetenceScopeOwnOrScoped,
		OwnerID:         a.ID,
		ProgrammeIDs:    programmes.IDs,
		SubjectGroupIDs: groups.IDs,
	}
}

// CompetenceReadScope is who reads other people's competences, in one place.
//
// Today the same two reaches as the unpublished wishes — a function of its own for the reason
// UnpublishedWishScope gives: the day the two rules part, this is the one line that changes.
// ADMIN is absent for the same reason as there.
func CompetenceReadScope(a principal.Actor) (ProgrammeScope, SubjectGroupScope) {
	return PlanningScope(a), AssignmentScope(a)
}

// MayReadSubjectGroupCompetences reports whether the actor may read every competence of one
// subject group — the precondition for the two aggregates over a group, which count rows the
// caller could otherwise not see one by one.
//
// Only through the subject group axis: a programme lead reaches the modules of their programme,
// which cut across groups, so they may read some of a group's rows but not count all of them.
// Interactive only, like every read of other people's statements.
func MayReadSubjectGroupCompetences(a principal.Actor, subjectGroupID uuid.UUID) bool {
	if !a.Authenticated() || !a.Interactive() {
		return false
	}
	_, groups := CompetenceReadScope(a)
	return groups.Allows(subjectGroupID)
}

// MayStateOwnCompetence is the write rule for one's own statement: only the subjects of a group
// one is in. "Wenn man sich eine Fachgruppe erschließen will, sollte man auch die grundlegenden
// Fächer halten können, nicht nur Rosinen" — joining the group is the explicit act, and it puts
// the group's compulsory modules in front of whoever joins.
//
// Membership is self-service (setMySubjectGroups), so this is not a barrier. It is what makes the
// minimum readable at all: a statement about a module outside one's groups would count towards
// nothing.
func MayStateOwnCompetence(a principal.Actor, moduleInAGroupOfMine bool) bool {
	return a.Authenticated() && moduleInAGroupOfMine
}

// MayStateCompetenceForTeacher is the write rule for a teacher without an account: the lead of the
// module's subject group, or the dean's office. Interactive only — a token reads only its owner's
// statements, and a write path it could reach would be one more place for the two to part.
func MayStateCompetenceForTeacher(a principal.Actor, subjectGroupID uuid.UUID) bool {
	return a.Authenticated() && a.Interactive() && AssignmentScope(a).Allows(subjectGroupID)
}

// CompetenceForTeacherReason is the refusal when somebody may not write for a teacher.
const CompetenceForTeacherReason = "Kompetenzen von Lehrenden ohne Konto trägt die Leitung der " +
	"Fachgruppe des Moduls ein."

// CompetenceOutsideGroupsReason is the refusal when somebody states a competence for a module
// outside their subject groups.
const CompetenceOutsideGroupsReason = "Kompetenzen lassen sich nur für Module der eigenen " +
	"Fachgruppen angeben. Treten Sie zuerst der Fachgruppe bei."
