package policy

import (
	"slices"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/principal"
)

// Desired teaching hours: how many hours a week somebody wants to teach in one semester, as a
// range from a minimum to a maximum.
//
// From the kickoff, under the teaching load: the figure somebody states, and a reason to freeze it
// at some point — "sonst wird erst wenig angegeben und später nachgetragen, um Spezial-LVs zu
// bekommen". The dean answered how on 2026-10-22, in place of the traffic light: everybody states
// their range, the dean's office freezes the semester's figures at a deadline, the faculty
// leadership reads them and settles anything implausible with the person, and then the figures are
// published to everybody, "damit alle sehen, ob sie wem was wegnehmen, der es noch bräuchte".
//
// So there are three states, and each rule below is one of them:
//
//   - Open: the person states and changes their own (MayStateDesiredHours). Read by the person, by
//     the lead of any of their subject groups and by the dean's office; through a token only
//     one's own. The competence rule turned towards the person: there is no module and no
//     instance, so responsibility runs through the subject groups the person is a member of.
//   - Frozen: only the dean's office changes a figure, anybody's (MayCorrectDesiredHours). The
//     correction is agreed with the person and recorded as the dean's office's.
//   - Published: everybody reads the range in a signed-in session. Still frozen — the figures
//     colleagues plan against do not move under them. Through a token it stays one's own: the
//     whole faculty's teaching plans in one script call is the silent bulk export rule 2 exists
//     against, and nothing in the process needs it (decided 2026-10-25).
//
// The note is not published with the range. It is the person's own words and can be personal
// ("Forschungssemester im Herbst"), and nothing in the process needs a colleague to read it: it
// keeps the read rule of the open state in every state (DesiredHoursNoteVisibility).

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

// CanSeeDesiredHours reports whether the actor may see this statement's range. The guard form.
func CanSeeDesiredHours(a principal.Actor, s SemesterState, d DesiredHours) bool {
	switch {
	case !a.Authenticated():
		return false
	case a.Owns(d.OwnerID):
		return true
	case PartnerOnly(a), !a.Interactive():
		return false
	case s.DesiredHoursPublished():
		return true
	default:
		return CanReadDesiredHoursNote(a, d)
	}
}

// CanReadDesiredHoursNote reports whether the actor may read the note on this statement, and who
// corrected it. The rule from before publication, in every state.
func CanReadDesiredHoursNote(a principal.Actor, d DesiredHours) bool {
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

// DesiredHoursVisibility returns the read rule for the range as a query filter.
//
// Published, it is everybody's in a signed-in session, except a partner faculty's, which reads its
// own and nothing else — the line WishVisibility draws, for the reason it gives. A token reads its
// own in every state. Before publication it is the note's rule.
func DesiredHoursVisibility(a principal.Actor, s SemesterState) DesiredHoursFilter {
	switch {
	case !a.Authenticated():
		return DesiredHoursFilter{Scope: DesiredHoursScopeNone}
	case PartnerOnly(a), !a.Interactive():
		return DesiredHoursFilter{Scope: DesiredHoursScopeOwn, OwnerID: a.ID}
	case s.DesiredHoursPublished():
		return DesiredHoursFilter{Scope: DesiredHoursScopeAll}
	default:
		return DesiredHoursNoteVisibility(a)
	}
}

// DesiredHoursNoteVisibility returns the note's read rule as a query filter.
//
// The reach is AssignmentScope's: the people who fill a subject group's instances are the ones
// who need to know what its members said about their teaching. A programme lead is not on it —
// they reach instances, not people — which is a decision the retreat may revisit, and this is
// where it would change.
func DesiredHoursNoteVisibility(a principal.Actor) DesiredHoursFilter {
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

// MayStateDesiredHours reports whether the actor may state, change or take back their own figure.
//
// The matrix row says when (open until FINAL, in both directions, like the wishes), and the freeze
// closes it earlier for everybody. A partner faculty states nothing. Through either door: it is
// one's own statement, like a wish.
func MayStateDesiredHours(a principal.Actor, s SemesterState) bool {
	return a.Authenticated() && !PartnerOnly(a) && !s.DesiredHoursFrozen() &&
		MayWriteInPhase(WriteAreaDesiredHours, s.Phase, a)
}

// MayCorrectDesiredHours reports whether the actor may change somebody's figure after the freeze.
//
// The dean's office, in a signed-in session, in a frozen semester that is not finished. Only after
// the freeze: before it the figure is the person's to change, and a correction then would be
// overwritten by the next thing they type. Interactive only, because it is a change to somebody
// else's statement and the person will ask who made it — that should be a sign-in, not a script.
func MayCorrectDesiredHours(a principal.Actor, s SemesterState) bool {
	return MayReadInteractiveOnly(a) && RolesOf(a).Has(RoleDeansOffice) &&
		s.DesiredHoursFrozen() && s.Phase != PhaseFinal
}

// DesiredHoursFrozenReason is what somebody changing their own figure after the freeze is told.
const DesiredHoursFrozenReason = "Die Wunsch-SWS dieses Semesters sind eingefroren. " +
	"Änderungen bitte über das Dekanat."

// CorrectDesiredHoursReason is what a caller who failed MayCorrectDesiredHours is told.
const CorrectDesiredHoursReason = "Nur das Dekanat kann eingefrorene Wunsch-SWS ändern, und " +
	"nur im Browser, nicht über ein Personal Access Token."
