package policy

import (
	"slices"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/principal"
)

// Desired teaching hours: how many hours a week somebody wants to teach in one semester.
//
// From the kickoff, under the teaching load: the figure somebody states, and a reason to freeze it
// at some point — "sonst wird erst wenig angegeben und später nachgetragen, um Spezial-LVs zu
// bekommen". When it freezes is an open question for the retreat on 2026-10-27; until it is
// answered the write row in the matrix has the wishes' shape, and the answer lands there.
//
// The read rule is the competence rule turned towards the person: there is no module and no
// instance, so responsibility runs through the subject groups the person is a member of. Read by
// the person, by the lead of any of their subject groups, and by the dean's office; through a
// token only one's own. Never published — it is a statement about one's own workload, closer to
// the personnel data than to the wishes, and nothing in the process needs a colleague to read it.

// DesiredHours is the minimum of a statement the read rule needs.
type DesiredHours struct {
	// OwnerID is whose statement it is.
	OwnerID uuid.UUID
	// MemberOf are the subject groups the owner is a member of, as of reading. Derived, like the
	// subject group of a wish: moving somebody to another group changes who reads their figure.
	MemberOf []uuid.UUID
}

// DesiredHoursScope is how much of the table a caller may read. The same four values, with the
// same meaning, as WishScope.
type DesiredHoursScope string

const (
	// DesiredHoursScopeNone grants nothing. The anonymous caller.
	DesiredHoursScopeNone DesiredHoursScope = "none"
	// DesiredHoursScopeOwn grants the caller's own statements.
	DesiredHoursScopeOwn DesiredHoursScope = "own"
	// DesiredHoursScopeOwnOrScoped adds the members of the subject groups the caller leads.
	DesiredHoursScopeOwnOrScoped DesiredHoursScope = "own_or_scoped"
	// DesiredHoursScopeAll grants everything.
	DesiredHoursScopeAll DesiredHoursScope = "all"
)

// DesiredHoursFilter is the read rule in the shape a query can apply.
type DesiredHoursFilter struct {
	Scope           DesiredHoursScope
	OwnerID         uuid.UUID
	SubjectGroupIDs []uuid.UUID
}

// Matches reports whether a single statement passes this filter. The counterpart of the WHERE
// clause; TestDesiredHoursGuardAndFilterAgree holds the two together.
func (f DesiredHoursFilter) Matches(d DesiredHours) bool {
	owns := f.OwnerID != uuid.Nil && d.OwnerID == f.OwnerID
	switch f.Scope {
	case DesiredHoursScopeAll:
		return true
	case DesiredHoursScopeOwn:
		return owns
	case DesiredHoursScopeOwnOrScoped:
		return owns || slices.ContainsFunc(d.MemberOf, func(g uuid.UUID) bool {
			return idScopeAllows(false, f.SubjectGroupIDs, g)
		})
	default:
		return false
	}
}

// CanSeeDesiredHours reports whether the actor may see this statement. The guard form.
func CanSeeDesiredHours(a principal.Actor, d DesiredHours) bool {
	switch {
	case !a.Authenticated():
		return false
	case a.Owns(d.OwnerID):
		return true
	case !a.Interactive():
		return false
	default:
		groups := AssignmentScope(a)
		return groups.All || slices.ContainsFunc(d.MemberOf, groups.Allows)
	}
}

// DesiredHoursVisibility returns the same rule as a query filter.
//
// The reach is AssignmentScope's: the people who fill a subject group's instances are the ones
// who need to know how much its members want to teach. A programme lead is not on it — they reach
// instances, not people — which is a decision the retreat may revisit, and this is where it would
// change.
func DesiredHoursVisibility(a principal.Actor) DesiredHoursFilter {
	switch {
	case !a.Authenticated():
		return DesiredHoursFilter{Scope: DesiredHoursScopeNone}
	case !a.Interactive():
		return DesiredHoursFilter{Scope: DesiredHoursScopeOwn, OwnerID: a.ID}
	}

	groups := AssignmentScope(a)
	switch {
	case groups.All:
		return DesiredHoursFilter{Scope: DesiredHoursScopeAll}
	case groups.Empty():
		return DesiredHoursFilter{Scope: DesiredHoursScopeOwn, OwnerID: a.ID}
	default:
		return DesiredHoursFilter{
			Scope:           DesiredHoursScopeOwnOrScoped,
			OwnerID:         a.ID,
			SubjectGroupIDs: groups.IDs,
		}
	}
}
