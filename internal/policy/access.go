package policy

import "github.com/obcode/tallox.go/internal/principal"

// MayReadAccessLog reports whether an actor may read the access log.
//
// ADMIN and an interactive session — the same shape as MayAdministerPeople, and for the same
// reason: this is part of running the installation rather than part of planning with it.
//
// # Why this is not the union with DEANS_OFFICE
//
// MayReadZPAImport is that union, and the argument there was that the *need* to look arises
// inside planning: whether the catalogue updated is a planning question with an operational
// answer. Nothing of that transfers here. This log says when colleagues worked, from where, and
// which screens they opened; the dean's office has no planning question whose answer is in it,
// and a role that acquires this along the way is precisely the drift the access design works
// hardest to avoid. Narrowing is not breaking, widening under pressure is — so it starts narrow.
//
// # Why it is not a way around the confidentiality rule
//
// ADMIN is deliberately not on the exception list of the wish visibility rule. That decision
// only survives because of what the log does not contain: operation names and root field names,
// never arguments, variables or responses. "Prof. Eins called myWishes" is not a wish. The rule
// is enforced by the schema of the table rather than by this function, which is the right place
// for it — a filter here would be one somebody could forget on the next surface.
func MayReadAccessLog(a principal.Actor) bool {
	return MayReadInteractiveOnly(a) && RolesOf(a).Has(RoleAdmin)
}

// AccessLogReason is what a caller who failed that check is told. German, like every other
// string a person reads.
const AccessLogReason = "Nur die Administration sieht das Zugriffsprotokoll."
