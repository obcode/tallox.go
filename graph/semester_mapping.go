package graph

import (
	"errors"

	"github.com/obcode/tallox.go/graph/model"
	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/policy"
)

// This file holds what the semester resolvers translate with. Separate from
// semester.resolvers.go because gqlgen rewrites that file from the schema on every generate
// and moves anything it did not put there to the end of it, under a warning banner — a helper
// left in there survives exactly until the next `go generate`.

// semesterModel reshapes a domain semester for the wire.
//
// reachablePhases is computed here rather than stored, from the same policy function the
// mutation checks against. That is the point of exposing it at all: an interface that renders
// its buttons from this list cannot offer a step the rule will refuse, and it does not need a
// copy of the adjacency rule in TypeScript to do it.
// The id never goes out: a semester is addressed by its code, which is the name it has in the
// faculty and the one that survives being written into a filename or a colleague's script. The
// uuid stays where it earns its keep, as the key the later tables point at.
func semesterModel(s domain.Semester) *model.Semester {
	out := &model.Semester{
		Code:               s.Code,
		Phase:              s.Phase,
		IsPlanningSemester: s.IsPlanning,
		ReachablePhases:    s.Phase.Neighbours(),
	}
	// The zero time is "nothing has been decided about this semester yet", which on the wire is
	// null. It is the ordinary state of most of the list.
	if !s.UpdatedAt.IsZero() {
		decided := s.UpdatedAt
		out.DecidedAt = &decided
	}
	// The zero time is "not published", which on the wire is null. Any other rendering would
	// make the confidentiality window look closed at the beginning of 1 CE.
	if !s.WishesPublishedAt.IsZero() {
		published := s.WishesPublishedAt
		out.WishesPublishedAt = &published
	}
	if !s.AssignmentsPublishedAt.IsZero() {
		published := s.AssignmentsPublishedAt
		out.AssignmentsPublishedAt = &published
	}
	if out.ReachablePhases == nil {
		// A phase the policy does not know has no neighbours, and the schema types this list
		// as non-null. An empty list is the honest answer — nothing can be reached from a
		// phase this build cannot place — where a nil would be a marshalling error.
		out.ReachablePhases = []policy.Phase{}
	}
	return out
}

// semesterUserFacing decides what a caller is told about a failed semester operation.
//
// Same contract as userFacing for tokens: the code is what a client branches on, the German
// sentence is the part that gets reworded. The reasons come from internal/policy where the
// refusal is a rule rather than a validation, so that the sentence a person reads and the rule
// that produced it cannot drift apart.
func semesterUserFacing(err error) error {
	switch {
	case errors.Is(err, domain.ErrForbidden):
		return refusal("FORBIDDEN", policy.SemesterAdminReason)
	case semesterRefusal(err) != nil:
		return semesterRefusal(err)
	case errors.Is(err, domain.ErrPhaseNotAdjacent):
		return refusal("PHASE_NOT_ADJACENT",
			"Ein Semester lässt sich nur schrittweise umschalten — immer nur eine Phase "+
				"vor oder zurück.")
	case errors.Is(err, domain.ErrPhaseMovedOn):
		return refusal("PHASE_MOVED_ON",
			"Das Semester wurde inzwischen von jemand anderem umgeschaltet. "+
				"Bitte die Seite neu laden.")
	case errors.Is(err, domain.ErrPhaseUnknown):
		return refusal("PHASE_UNKNOWN", "Diese Phase kennt der Server nicht.")
	default:
		// Everything else — a failing query, a driver error — gets a generic sentence. Those
		// messages carry table names and constraint names, and the habit of not forwarding
		// them is what the wish workflow will depend on.
		return refusal("INTERNAL", "Die Aktion konnte nicht ausgeführt werden.")
	}
}

// semesterRefusal maps the two refusals about a semester *code* that every area can meet, or
// returns nil when err is neither.
//
// One place, because three areas reach it — the semester workflow, the demand and the wishes,
// since every one of them names a semester — and two of the three were passing
// `domain.Err….Error()` straight through. Those strings are English: this repository writes
// everything in English except what a person reads, so an internal error text reaching a screen
// is not a wording problem but a category error. It showed up as "this semester is too far away
// to plan" on a German page.
//
// Sharing it also settles the other half: one meaning per code, whichever field produced it,
// which is what lets the interface branch on the code at all.
func semesterRefusal(err error) error {
	switch {
	case errors.Is(err, domain.ErrSemesterCodeInvalid):
		return refusal("SEMESTER_CODE_INVALID",
			"Ein Semesterkürzel besteht aus vier Ziffern, einem Bindestrich und SS oder WS, "+
				"zum Beispiel 2026-WS. Die Jahreszahl ist die des Semesterbeginns.")
	case errors.Is(err, domain.ErrSemesterOutOfRange):
		// Not "does not exist" — it does, and saying otherwise would be untrue. What it is is
		// out of reach: there is no way to undo a decision about a semester, so one recorded
		// for a mistyped year would stay in the faculty's planning for good.
		return refusal("SEMESTER_OUT_OF_RANGE",
			"Dieses Semester liegt mehr als zehn Jahre von heute entfernt — so weit "+
				"voraus oder zurück lässt sich hier nicht planen.")
	}
	return nil
}
