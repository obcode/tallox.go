package policy

import (
	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/principal"
)

// Assignment is the minimum of an assignment that the visibility rule needs.
//
// The same three questions Wish asks, and the same reason for asking only these: it lets
// internal/store apply the rule to a row it has not fully loaded, and it keeps this package from
// growing a second copy of the domain model.
//
// Both responsibility ids are derived rather than stored on the assignment row — the programme
// through the course instance, the subject group through its module — for the reason the wish
// table gives: a copy would freeze responsibility at the moment somebody filled the part, so the
// lead of a subject group that has since been split would keep reading assignments for subjects
// that are no longer theirs.
type Assignment struct {
	// AssigneeID is the person who holds this part, or uuid.Nil when it is held by somebody with
	// no Tallox account.
	//
	// Nil is an ordinary state here, unlike Wish.OwnerID which is always somebody with a login:
	// a lecturer on contract is assignable and may never sign in. It fails closed — an assignment
	// with no person behind it is read through responsibility or after publication, and by nobody
	// as "their own".
	AssigneeID uuid.UUID
	// ProgrammeID is the study programme whose demand this instance is.
	//
	// The programme of the *instance*, never of the assignee. Somebody at home in one programme
	// who holds another programme's laboratory is visible to that programme's lead and not to
	// their own — what is being planned is the instance.
	ProgrammeID uuid.UUID
	// SubjectGroupID is the subject group of the module the instance offers, or uuid.Nil while
	// nobody has assigned one. Fails closed, exactly as it does for a wish.
	SubjectGroupID uuid.UUID
}

// AssignmentReadScope is how much of the assignment table a caller may read.
//
// The same four values WishScope has, and a type of its own rather than a reuse of it. The two
// rules are the same sentence today and are not the same rule: the wish window ends to stop a
// first-come-first-served race, the assignment window ends because a half-finished plan invites
// questions about decisions nobody has taken. They will be asked to differ, and a shared type is
// how one of them changes by accident.
type AssignmentReadScope string

const (
	// AssignmentReadScopeNone grants nothing at all. The anonymous caller.
	AssignmentReadScopeNone AssignmentReadScope = "none"
	// AssignmentReadScopeOwn grants only what the caller holds themselves. The ordinary case
	// during the confidentiality window — and the one that makes aggregates safe, because a COUNT
	// narrowed this way returns the caller's own number and not the faculty's.
	AssignmentReadScopeOwn AssignmentReadScope = "own"
	// AssignmentReadScopeOwnOrScoped grants what the caller holds plus everything they are
	// responsible for — the instances of a study programme they lead, and the modules of a subject
	// group they lead. The ordinary case for a planning role during the window.
	AssignmentReadScopeOwnOrScoped AssignmentReadScope = "own_or_scoped"
	// AssignmentReadScopeAll grants everything: no restriction.
	AssignmentReadScopeAll AssignmentReadScope = "all"
)

// AssignmentFilter is the visibility rule in the shape a query can apply.
//
// internal/store translates it the same way it translates a WishFilter:
//
//	AssignmentReadScopeNone         → WHERE false
//	AssignmentReadScopeOwn          → WHERE assignee_id = @assigneeID
//	AssignmentReadScopeOwnOrScoped  → WHERE assignee_id = @assigneeID
//	                                     OR programme_id = ANY(@programmeIDs)
//	                                     OR subject_group_id = ANY(@subjectGroupIDs)
//	AssignmentReadScopeAll          → no additional predicate
//
// And for the same reason it is a type rather than a bool: **the same filter goes on the COUNT.**
// "Zwei der drei Praktika sind schon vergeben" is the confidential fact with the names taken out,
// and an aggregate that skips the filter is the same failure as a list that skips it, only harder
// to notice.
type AssignmentFilter struct {
	// Scope is how much may be read.
	Scope AssignmentReadScope
	// AssigneeID is the person the scope is restricted to. Meaningful for the two scoped values —
	// what one holds oneself stays readable whatever else is.
	AssigneeID uuid.UUID
	// ProgrammeIDs are the study programmes this caller leads. Meaningful only for
	// AssignmentReadScopeOwnOrScoped, and empty is a real value: a lead with no programme reaches
	// none.
	ProgrammeIDs []uuid.UUID
	// SubjectGroupIDs are the subject groups this caller leads. Same reading as above.
	SubjectGroupIDs []uuid.UUID
}

// Matches reports whether a single assignment passes this filter.
//
// The counterpart of the WHERE clause, and the reason TestAssignmentGuardAndFilterAgree can
// compare the two forms of the rule at all.
func (f AssignmentFilter) Matches(a Assignment) bool {
	switch f.Scope {
	case AssignmentReadScopeAll:
		return true
	case AssignmentReadScopeOwn:
		return f.holds(a)
	case AssignmentReadScopeOwnOrScoped:
		// The same union the WHERE clause is. The nil guards inside idScopeAllows are what stop an
		// assignment whose module has no subject group from matching a caller who leads none.
		return f.holds(a) ||
			idScopeAllows(false, f.ProgrammeIDs, a.ProgrammeID) ||
			idScopeAllows(false, f.SubjectGroupIDs, a.SubjectGroupID)
	case AssignmentReadScopeNone:
		return false
	default:
		// An unknown scope is a programming error, and the safe reading of "I do not know how much
		// you may see" is "nothing".
		return false
	}
}

// holds is the half of the rule that is the same in both scoped cases.
//
// The nil guard carries more weight here than its counterpart on WishFilter: AssigneeID is
// legitimately Nil for everybody assigned without an account, so without it every such row would
// match every caller whose own id happened to be unset.
func (f AssignmentFilter) holds(a Assignment) bool {
	return f.AssigneeID != uuid.Nil && a.AssigneeID == f.AssigneeID
}

// CanSeeAssignment reports whether the actor may see this assignment. The guard form of the rule.
//
// Reads as the sentence it implements: visible iff one holds it oneself, or the assignments of the
// semester have been published, or the caller is one of the people the process requires to look
// early — and then only in an interactive session.
//
// The Kind condition is the same clause CanSeeWish carries and is there for the same reason: a
// Personal Access Token is long-lived, sits in a script, and decouples "who saw this" from any
// login event. What one holds oneself stays readable through both doors.
func CanSeeAssignment(actor principal.Actor, s SemesterState, a Assignment) bool {
	switch {
	case !actor.Authenticated():
		return false
	case actor.Owns(a.AssigneeID):
		return true
	case s.AssignmentsPublished():
		return true
	case !actor.Interactive():
		return false
	default:
		programmes, groups := UnpublishedAssignmentScope(actor)
		return programmes.Allows(a.ProgrammeID) || groups.Allows(a.SubjectGroupID)
	}
}

// AssignmentVisibility returns the same rule as a query filter. The filter form.
//
// Deliberately a second implementation rather than a call to CanSeeAssignment over every row: this
// one has to survive the trip into SQL. TestAssignmentGuardAndFilterAgree is the bridge between
// the two, over the complete cartesian product.
func AssignmentVisibility(actor principal.Actor, s SemesterState) AssignmentFilter {
	switch {
	case !actor.Authenticated():
		return AssignmentFilter{Scope: AssignmentReadScopeNone}
	case s.AssignmentsPublished():
		return AssignmentFilter{Scope: AssignmentReadScopeAll}
	case !actor.Interactive():
		return AssignmentFilter{Scope: AssignmentReadScopeOwn, AssigneeID: actor.ID}
	}

	programmes, groups := UnpublishedAssignmentScope(actor)

	// The dean's office reaches across both dimensions and is not enumerable — a programme or a
	// subject group created tomorrow is included — so it collapses to no restriction at all rather
	// than to a list that would be a snapshot.
	if programmes.All || groups.All {
		return AssignmentFilter{Scope: AssignmentReadScopeAll}
	}
	if programmes.Empty() && groups.Empty() {
		// Everybody else, and every lead nobody has given a subject to yet. Not "everything": an
		// unscoped grant is the grant's subject being unset, not a narrowing that was skipped.
		return AssignmentFilter{Scope: AssignmentReadScopeOwn, AssigneeID: actor.ID}
	}
	return AssignmentFilter{
		Scope:           AssignmentReadScopeOwnOrScoped,
		AssigneeID:      actor.ID,
		ProgrammeIDs:    programmes.IDs,
		SubjectGroupIDs: groups.IDs,
	}
}

// UnpublishedAssignmentScope is the exception list for assignments, in one place so that it can be
// read as a list of people rather than reconstructed from conditions.
//
// The same two orthogonal reaches UnpublishedWishScope returns, and — today — the same two values.
// A function of its own for the reason that one gives about PlanningScope and AssignmentScope: the
// two rules change for different reasons. Reading unpublished wishes is looking at what colleagues
// asked for; reading unpublished assignments is looking at what has been decided about them. The
// day the faculty wants one without the other, this is the one place that changes.
//
// ADMIN is deliberately absent, as everywhere else. Administering the system is a different job
// from planning with it.
func UnpublishedAssignmentScope(a principal.Actor) (ProgrammeScope, SubjectGroupScope) {
	return PlanningScope(a), AssignmentScope(a)
}
