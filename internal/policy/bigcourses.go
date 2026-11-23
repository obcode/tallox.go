package policy

import (
	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/principal"
)

// Big courses: the faculty council's rule that whoever teaches at least 20 hours a week averaged
// over a study year holds at least two big compulsory or basket courses in it, and at least one
// from 10 hours. The overview shows it per professor: the hours of the winter and the summer, the
// target, the big courses counted, and whether the target is met.
//
// # Who reads it
//
// Those who plan against the rule, while they plan: the dean's office, and the leads — of a
// subject group or of a programme — with full reach, every professor. Decided with the faculty on
// 2026-11-23, and deliberately wider than the assigned load (assignedload.go), which a programme
// lead does not read before publication: the rule is a duty of the whole faculty rather than of one
// subject group, and the programme leads were asked to check it during the planning. They see
// hours, a count and the courses counted, never wishes, and nothing of the deputat.
//
// A lead whose grant names no subject group and no programme is not set up yet and reads what
// everybody reads, the same reading AssignmentScope and PlanningScope give an empty scope.
//
// Everybody else reads their own row, through either door. A plain token reads only its owner's
// row whatever roles stand behind it: the whole faculty's per-person figures in one script call is
// the bulk export rule 2 keeps off the plain token door. An onlooker (partner, observer) reads
// other people's statements nowhere, and does not here.

// BigCourseScope is how much of the overview a caller may read.
type BigCourseScope string

const (
	// BigCourseScopeNone grants nothing. The anonymous caller.
	BigCourseScopeNone BigCourseScope = "none"
	// BigCourseScopeOwn grants the caller's own row.
	BigCourseScopeOwn BigCourseScope = "own"
	// BigCourseScopeAll grants every professor's row.
	BigCourseScopeAll BigCourseScope = "all"
)

// BigCourseFilter is the read rule in the shape a query can apply.
type BigCourseFilter struct {
	Scope   BigCourseScope
	OwnerID uuid.UUID
}

// Matches reports whether the row of this person (uuid.Nil for a professor without an account)
// passes the filter. The counterpart of the WHERE clause; TestBigCourseGuardAndFilterAgree holds
// the two together.
func (f BigCourseFilter) Matches(personID uuid.UUID) bool {
	switch f.Scope {
	case BigCourseScopeAll:
		return true
	case BigCourseScopeOwn:
		return f.OwnerID != uuid.Nil && personID == f.OwnerID
	default:
		return false
	}
}

// ReadsEveryBigCourseRow reports whether the actor reads every professor's row.
func ReadsEveryBigCourseRow(a principal.Actor) bool {
	if !a.Authenticated() || Onlooker(a) || !a.ActsWithFullReach() {
		return false
	}
	return !AssignmentScope(a).Empty() || !PlanningScope(a).Empty()
}

// CanSeeBigCourseRow reports whether the actor may see the row of this person. The guard form.
func CanSeeBigCourseRow(a principal.Actor, personID uuid.UUID) bool {
	switch {
	case !a.Authenticated():
		return false
	case a.Owns(personID):
		return true
	default:
		return ReadsEveryBigCourseRow(a)
	}
}

// BigCourseVisibility returns the same rule as a query filter.
func BigCourseVisibility(a principal.Actor) BigCourseFilter {
	switch {
	case !a.Authenticated():
		return BigCourseFilter{Scope: BigCourseScopeNone}
	case ReadsEveryBigCourseRow(a):
		return BigCourseFilter{Scope: BigCourseScopeAll}
	default:
		return BigCourseFilter{Scope: BigCourseScopeOwn, OwnerID: a.ID}
	}
}
