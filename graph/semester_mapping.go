package graph

import (
	"errors"

	"github.com/google/uuid"

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
func semesterModel(s domain.Semester) *model.Semester {
	out := &model.Semester{
		ID:              s.ID.String(),
		Code:            s.Code,
		Phase:           s.Phase,
		ReachablePhases: s.Phase.Neighbours(),
		CreatedAt:       s.CreatedAt,
		UpdatedAt:       s.UpdatedAt,
	}
	// The zero time is "not published", which on the wire is null. Any other rendering would
	// make the confidentiality window look closed at the beginning of 1 CE.
	if !s.WishesPublishedAt.IsZero() {
		published := s.WishesPublishedAt
		out.WishesPublishedAt = &published
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
	case errors.Is(err, domain.ErrSemesterCodeInvalid):
		return refusal("SEMESTER_CODE_INVALID",
			"Ein Semesterkürzel besteht aus vier Ziffern, einem Bindestrich und SS oder WS, "+
				"zum Beispiel 2026-WS. Die Jahreszahl ist die des Semesterbeginns.")
	case errors.Is(err, domain.ErrSemesterExists):
		return refusal("SEMESTER_EXISTS", "Dieses Semester gibt es bereits.")
	case errors.Is(err, domain.ErrNoSuchSemester):
		return refusal("SEMESTER_NOT_FOUND", "Dieses Semester existiert nicht.")
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

// semesterID parses an id from the wire.
//
// A malformed uuid is reported as "no such semester" and not as a parse error. The two are the
// same fact from the caller's side — the id they hold does not name a semester — and one
// answer means the field cannot be used to tell a well-formed unknown id from a malformed one.
func semesterID(raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, domain.ErrNoSuchSemester
	}
	return id, nil
}
