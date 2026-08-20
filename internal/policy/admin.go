package policy

import "github.com/obcode/tallox.go/internal/principal"

// MayAdministerPeople reports whether an actor may see and change who has access to this
// installation, and with which roles.
//
// Two conditions, and the second one is the interesting one. ADMIN is necessary and it is not
// sufficient: user administration is interactive-only, so a Personal Access Token never
// administers anybody even when its owner holds ADMIN. The reason is the one behind every
// other @interactiveOnly field — a long-lived token in a script decouples "who did this" from
// a login event, and the operation it would decouple here is granting somebody a role.
//
// It is spelled out as a rule rather than left to the directive alone. The directive protects
// the GraphQL fields; this function is what a future CSV export of the user list, or a
// maintenance command, has to ask. A rule that only exists as an annotation on a schema field
// is a rule that the next surface does not have.
func MayAdministerPeople(a principal.Actor) bool {
	return MayReadInteractiveOnly(a) && RolesOf(a).Has(RoleAdmin)
}

// AdminReason is what a caller who failed that check is told. German, like every other string
// a person reads.
const AdminReason = "Nur die Administration darf Personen und Rollen verwalten."

// Narrow returns the actor as they would be if they held only the selected roles.
//
// This is the "temporarily be somebody else" feature, and the shape of it is the whole
// security argument:
//
//	effective = held ∩ selected
//
// An intersection can only ever remove. There is no selection, no header and no cookie that
// makes this function return a role the person does not already hold — so nothing downstream
// has to trust where the selection came from, and a hand-written X-Tallox-Assume-Roles sent
// straight at /query by somebody curious gains them exactly nothing.
//
// That property is why this is safe and why the obvious alternative is not. "Let an
// administrator preview any role" would hand ADMIN the ability to read unpublished wishes as
// DEANS_OFFICE — quietly, and in a system whose one politically load-bearing rule is that
// wishes are confidential. The list of people who can see them has to stay short and
// justifiable to the colleagues it protects, and "the administrator, temporarily, whenever
// they like" is neither.
//
// The consequence for the person using it: to preview what a lecturer sees, hold LECTURER.
// That is not a workaround, it is the mechanism working — the preview shows a real subset of
// a real set of grants, so what it shows is true.
//
// NarrowedFrom carries the grants the person actually holds, so that the interface can say so
// and the audit log can record it. Roles is the effective set, which means every existing rule
// keeps reading Roles and is narrowed automatically. A version that left Roles alone and
// asked rules to consult a second field would be narrowed only in the rules somebody
// remembered to change.
func Narrow(a principal.Actor, selected []Role) principal.Actor {
	held := RolesOf(a)

	effective := make([]string, 0, len(selected))
	for _, r := range AllRoles() {
		if !held.Has(r) {
			continue
		}
		for _, want := range selected {
			if want == r {
				effective = append(effective, string(r))
				break
			}
		}
	}

	// NarrowedFrom is set even when the selection happens to cover everything the person
	// holds. "I asked to be narrowed and nothing changed" is a state the interface should
	// show honestly rather than hide, and the alternative — comparing the two sets and
	// silently dropping the marker — makes the banner flicker on and off depending on which
	// roles somebody was granted this morning.
	narrowedFrom := make([]string, 0, len(a.Roles))
	narrowedFrom = append(narrowedFrom, a.Roles...)

	a.Roles = effective
	a.NarrowedFrom = narrowedFrom
	a.RoleScopes = scopesOfRoles(a.RoleScopes, effective)
	return a
}

// scopesOfRoles drops the scopes of roles that are not in the effective set.
//
// Without this, narrowing away PROGRAMME_LEAD would leave the programmes it was scoped to on
// the actor, and the next rule to ask "which programmes may this actor plan" would answer with
// them — a grant surviving the removal of the grant it belongs to. It is the same failure the
// composite foreign key on person_programme_scope prevents in the database, one layer up, and
// it is worth closing in both places: the database one covers a revocation, this one covers a
// request.
//
// The direction matters. Like Narrow itself this can only ever remove, which is what keeps the
// whole mechanism safe to drive from an unverified header.
func scopesOfRoles(scopes []principal.RoleScope, effective []string) []principal.RoleScope {
	if len(scopes) == 0 {
		return scopes
	}

	keep := make(map[string]bool, len(effective))
	for _, role := range effective {
		keep[role] = true
	}

	out := make([]principal.RoleScope, 0, len(scopes))
	for _, scope := range scopes {
		if keep[scope.Role] {
			out = append(out, scope)
		}
	}
	return out
}

// ParseRoles turns a selection as it arrived over the wire into roles, dropping anything this
// package does not know.
//
// Unknown strings are dropped rather than rejected, for the same reason RolesOf drops unknown
// grants: a role this package cannot interpret grants nothing, so silently not selecting it
// is the fail-closed direction. The caller cannot use the difference to learn anything either
// — the intersection in Narrow would have removed it regardless.
func ParseRoles(raw []string) []Role {
	out := make([]Role, 0, len(raw))
	for _, s := range raw {
		if r, ok := ParseRole(s); ok {
			out = append(out, r)
		}
	}
	return out
}
