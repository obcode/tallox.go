package policy

import (
	"time"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/principal"
)

// SemesterState is the part of a semester the read rules depend on.
//
// A struct rather than two arguments, because it is about to grow (milestones, the point from
// which the requested SWS stop being editable) and because "which of these two timestamps did
// I pass first" is a bug that compiles.
type SemesterState struct {
	// Phase is where the semester stands. Deliberately unused by wish visibility — see
	// TestVisibilityDoesNotDependOnThePhase — and carried here anyway, because every rule
	// that follows this one is a write rule and every write rule needs it.
	Phase Phase
	// WishesPublishedAt is the moment the wishes of this semester became public, or the zero
	// time if that has not happened. Mirrors semester.wishes_published_at, where the rule is
	// IS NOT NULL.
	//
	// A zero time.Time rather than a *time.Time: the nil pointer would be one more thing every
	// call site could dereference by accident, and there is no meaningful difference here
	// between "never published" and "no value".
	WishesPublishedAt time.Time
}

// WishesPublished reports whether the confidentiality window has closed.
func (s SemesterState) WishesPublished() bool { return !s.WishesPublishedAt.IsZero() }

// Wish is the minimum of a wish that the visibility rule needs.
//
// Three fields, and they are exactly the three the rule asks about: who entered it, and the two
// things a planning role can be responsible for. Not the instance part, not the priority, not the
// hours. Keeping it minimal is what lets internal/store apply the rule to a row it has not fully
// loaded, and what keeps this package from acquiring a second copy of the domain model.
//
// Both ids are derived rather than stored on the wish row — the programme through the course
// instance, the subject group through its module — and that is deliberate: a copy would freeze
// responsibility at the moment somebody registered interest, so the lead of a subject group that
// has since been split would keep reading wishes for subjects that are no longer theirs.
type Wish struct {
	// OwnerID is the person who registered the interest.
	OwnerID uuid.UUID
	// ProgrammeID is the study programme whose demand this instance is.
	//
	// The programme of the *instance*, never of the person. Somebody at home in one programme who
	// registers interest in another programme's instance is visible to that programme's lead and
	// not to their own — which is the right way round: what is being planned is the instance.
	ProgrammeID uuid.UUID
	// SubjectGroupID is the subject group of the module the instance offers, or uuid.Nil while
	// nobody has assigned one.
	//
	// Nil is an ordinary state until the faculty has worked through its catalogue, and it fails
	// closed: a wish on a module in no subject group is read by its programme's lead and by the
	// dean's office, and by no subject group lead at all.
	SubjectGroupID uuid.UUID
}

// WishScope is how much of the wish table a caller may read.
type WishScope string

const (
	// WishScopeNone grants nothing at all. The anonymous caller.
	WishScopeNone WishScope = "none"
	// WishScopeOwn grants only the caller's own entries. The ordinary case during the
	// confidentiality window — and the one that makes aggregates safe, because a COUNT
	// narrowed this way returns the caller's own number and not the faculty's.
	WishScopeOwn WishScope = "own"
	// WishScopeOwnOrScoped grants the caller's own entries plus everything they are responsible
	// for — the instances of a study programme they lead, and the modules of a subject group they
	// lead. The ordinary case for a planning role during the confidentiality window.
	//
	// A single scope value rather than two, because the two reaches are a union and a query
	// applies them in one WHERE clause. Splitting them would make "which of my two roles let me
	// see this row" a question every caller has to answer, and the answer is never used.
	WishScopeOwnOrScoped WishScope = "own_or_scoped"
	// WishScopeAll grants everything: no restriction.
	WishScopeAll WishScope = "all"
)

// WishFilter is the visibility rule in the shape a query can apply.
//
// internal/store translates it, and the translation is the whole point of the type:
//
//	WishScopeNone         → WHERE false
//	WishScopeOwn          → WHERE owner_id = @ownerID
//	WishScopeOwnOrScoped  → WHERE owner_id = @ownerID
//	                           OR programme_id = ANY(@programmeIDs)
//	                           OR subject_group_id = ANY(@subjectGroupIDs)
//	WishScopeAll          → no additional predicate
//
// The rule that makes this worth a type rather than a bool: **the same filter goes on the
// COUNT**. "3 Kolleg:innen haben bereits Interesse" leaks the confidential information
// completely without naming anybody, so an aggregate that skips the filter is the same
// failure as a list that skips it, only harder to notice. A filter that has to be passed
// explicitly is one that shows up as missing in review.
type WishFilter struct {
	// Scope is how much may be read.
	Scope WishScope
	// OwnerID is the person the scope is restricted to. Meaningful for WishScopeOwn and
	// WishScopeOwnOrScoped — one's own entries stay readable whatever else is.
	OwnerID uuid.UUID
	// ProgrammeIDs are the study programmes this caller leads. Meaningful only for
	// WishScopeOwnOrScoped, and empty is a real value: a lead with no programme reaches none.
	ProgrammeIDs []uuid.UUID
	// SubjectGroupIDs are the subject groups this caller leads. Same reading as above.
	SubjectGroupIDs []uuid.UUID
}

// Matches reports whether a single wish passes this filter.
//
// The counterpart of the WHERE clause, and the reason the property test can compare the two
// forms of the rule at all. The nil check on WishScopeOwn is the same guard as
// principal.Actor.Owns: an unset owner column must not match an unset filter owner.
func (f WishFilter) Matches(w Wish) bool {
	switch f.Scope {
	case WishScopeAll:
		return true
	case WishScopeOwn:
		return f.owns(w)
	case WishScopeOwnOrScoped:
		// The same union the WHERE clause is. The nil guards inside idScopeAllows are what stop a
		// wish whose module has no subject group from matching a caller who leads none.
		return f.owns(w) ||
			idScopeAllows(false, f.ProgrammeIDs, w.ProgrammeID) ||
			idScopeAllows(false, f.SubjectGroupIDs, w.SubjectGroupID)
	case WishScopeNone:
		return false
	default:
		// An unknown scope is a programming error, and the safe reading of "I do not know how
		// much you may see" is "nothing".
		return false
	}
}

// owns is the half of the rule that is the same in both scoped cases.
func (f WishFilter) owns(w Wish) bool {
	return f.OwnerID != uuid.Nil && w.OwnerID == f.OwnerID
}

// CanSeeWish reports whether the actor may see this wish. The guard form of the rule.
//
// Reads as the sentence it implements: visible iff owner, or the wishes of the semester have
// been published, or the caller is one of the people the process requires to look early — and
// then only in an interactive session.
//
// The Kind condition is the least obvious clause and the one most likely to be dropped by
// somebody tidying up. It is there because a Personal Access Token is a different risk class
// from a browser session: it is long-lived, it sits in a script, and it makes silent bulk
// export possible while decoupling "who saw this" from any login event. A planner reading
// unpublished wishes in the GUI leaves a session behind; the same person's token in a cron
// job does not. Their own wishes stay readable through both doors — that is their data.
func CanSeeWish(a principal.Actor, s SemesterState, w Wish) bool {
	switch {
	case !a.Authenticated():
		return false
	case a.Owns(w.OwnerID):
		return true
	case s.WishesPublished():
		return true
	case !a.Interactive():
		return false
	default:
		programmes, groups := UnpublishedWishScope(a)
		return programmes.Allows(w.ProgrammeID) || groups.Allows(w.SubjectGroupID)
	}
}

// WishVisibility returns the same rule as a query filter. The filter form.
//
// Deliberately a second implementation rather than a call to CanSeeWish over every row: this
// one has to survive the trip into SQL, where "call the guard for each candidate" is not
// available and would be a table scan if it were. TestGuardAndFilterAgree is the bridge
// between the two, over the complete cartesian product.
func WishVisibility(a principal.Actor, s SemesterState) WishFilter {
	switch {
	case !a.Authenticated():
		return WishFilter{Scope: WishScopeNone}
	case s.WishesPublished():
		return WishFilter{Scope: WishScopeAll}
	case !a.Interactive():
		return WishFilter{Scope: WishScopeOwn, OwnerID: a.ID}
	}

	programmes, groups := UnpublishedWishScope(a)

	// The dean's office reaches across both dimensions and is not enumerable — a programme or a
	// subject group created tomorrow is included — so it collapses to no restriction at all
	// rather than to a list that would be a snapshot.
	if programmes.All || groups.All {
		return WishFilter{Scope: WishScopeAll}
	}
	if programmes.Empty() && groups.Empty() {
		// Everybody else, and every lead nobody has given a subject to yet. Not "everything":
		// an unscoped grant is the grant's subject being unset, not a narrowing that was skipped.
		return WishFilter{Scope: WishScopeOwn, OwnerID: a.ID}
	}
	return WishFilter{
		Scope:           WishScopeOwnOrScoped,
		OwnerID:         a.ID,
		ProgrammeIDs:    programmes.IDs,
		SubjectGroupIDs: groups.IDs,
	}
}

// UnpublishedWishScope is the exception list, in one place so that it can be read as a list of
// people rather than reconstructed from conditions.
//
// Two reaches, because there are two ways to be responsible for a wish and they are orthogonal:
//
//   - the study programme whose demand the instance is — a subject group reaches across
//     programmes, so this is not implied by the other, and
//   - the subject group of the module the instance offers — a programme reaches across subject
//     groups, so neither is implied by the first.
//
// Somebody sees a row through one of the two or not at all. The dean's office reaches across
// both, because evaluating across programmes is its job.
//
// This used to be a boolean over the role set, which was faculty-wide by construction and was
// correct only while there was nothing for a role to be scoped to. It is not a boolean now: an
// IG lead has no business in IF wishes, and a mathematics lead none in the software subjects.
//
// ADMIN is deliberately absent. Administering the system is a different job from planning with
// it, and an exception list that a colleague can read and accept is worth more than one that
// quietly includes whoever holds the keys. An admin who genuinely needs to look can be granted
// DEANS_OFFICE — visibly, as an audited role grant. If the faculty decides otherwise at the
// retreat, this function and a few lines of the golden matrix are the whole change.
//
// # Why not PlanningScope and AssignmentScope directly
//
// Today it returns exactly those two, character for character. It is a function of its own for
// the reason write.go keeps its matrix and its scopes apart: the rules change for different
// reasons. The day a subject group lead may read wishes without being allowed to declare demand —
// or the reverse — this is the one place that has to change, instead of a call site somebody has
// to find first.
func UnpublishedWishScope(a principal.Actor) (ProgrammeScope, SubjectGroupScope) {
	return PlanningScope(a), AssignmentScope(a)
}
