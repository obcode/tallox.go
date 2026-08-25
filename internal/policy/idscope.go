package policy

import (
	"slices"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/principal"
)

// The mechanics shared by every "which things may this actor act on" rule.
//
// There are two such rules today — study programmes for the demand, subject groups for the
// assignment — and they are the same three lines of logic with two different subjects. The
// logic lives here once; the types and their doc comments stay separate, because what has to
// be read and agreed with is the *argument* for each rule, and those arguments differ. A
// single exported Scope type would save nothing and would make "which grant is this about"
// a question about a field rather than about a name.

// idScopeAllows reports whether an enumerable-or-total scope covers one id.
//
// The nil guard is the load-bearing half. Without it an actor with an empty scope and a caller
// passing a zero uuid would meet in the middle — the shape of mistake person_id_not_nil exists
// to prevent one table over — and the result would be a permission granted by two absences.
func idScopeAllows(all bool, ids []uuid.UUID, id uuid.UUID) bool {
	if all {
		return true
	}
	if id == uuid.Nil {
		return false
	}
	return slices.Contains(ids, id)
}

// idScopeEmpty reports whether a scope reaches nothing at all.
func idScopeEmpty(all bool, ids []uuid.UUID) bool { return !all && len(ids) == 0 }

// scopedIDs reads the things one role has been scoped to off an actor.
//
// pick says which of RoleScope's named targets this role uses. A scope naming nothing is
// dropped rather than read as a wildcard: the zero uuid is what a malformed row looks like,
// and the safe reading of "this grant names no subject" is that it grants no subject.
// Duplicates are dropped so that a caller can count what came back.
func scopedIDs(a principal.Actor, role Role, pick func(principal.RoleScope) uuid.UUID) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(a.RoleScopes))
	for _, scope := range a.RoleScopes {
		if scope.Role != string(role) {
			continue
		}
		id := pick(scope)
		if id == uuid.Nil {
			continue
		}
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return ids
}
