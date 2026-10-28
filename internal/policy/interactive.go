package policy

import "github.com/obcode/tallox.go/internal/principal"

// MayReadInteractiveOnly reports whether an actor may read a field marked @interactiveOnly.
//
// The third factor of the invariant, on its own:
//
//	effective permission = (what the Role allows) ∩ (what the Scopes grant) ∩ (what the Kind allows)
//
// It is deliberately not "is this a token" spelled inline at the directive. The question is a
// rule, rules live here, and putting it here means the answer is the same for the GraphQL
// directive, for a future CSV export and for anything else that grows a reason to ask.
//
// Anonymous callers fail it too, and that is the correct reading rather than an accident of
// the implementation: an unauthenticated request has no audited session behind it either, so
// none of the arguments for the interactive path apply to it.
//
// What sits behind it, since 2026-10-28, is deliberately little: token management itself — a
// leaked token that can mint its successors outlives its own expiry date — who has an account
// and which roles it holds, and the access log. Everything else the browser reaches, a token
// minted with full reach reaches too; that is MayActWithFullReach below.
func MayReadInteractiveOnly(a principal.Actor) bool {
	return a.Authenticated() && a.Interactive()
}

// InteractiveOnlyReason is what a caller who failed that check is told.
//
// German, like every other string a person reads, and specific enough to act on: the useful
// next step is a browser, not a retry. It deliberately does not name the field — the schema
// already documents which fields are interactive-only, and a message that enumerates them
// would be a list to keep in sync.
const InteractiveOnlyReason = "Dieses Feld ist über ein Personal Access Token nicht verfügbar. " +
	"Bitte im Browser aufrufen."

// MayActWithFullReach reports whether an actor reaches what a signed-in browser session reaches:
// a person in a browser, or a Personal Access Token its owner minted with full reach.
//
// This is the Kind term for everything except the handful of surfaces that stay the browser's
// alone (MayReadInteractiveOnly: token management, person and role administration, the access
// log). It is what the reach rules ask — whose unpublished wishes, assignments and competences
// the caller sees — and what the @fullReach directive asks of the planning mutations.
//
// The two belong together, and that is the argument for the whole design. A planning mutation's
// refusal (INSTANCE_IN_USE, PART_ALREADY_ASSIGNED, PART_ASSIGNED) answers a question about rows,
// and it is harmless exactly when the caller may already read those rows. A plain token reads
// only its owner's own, so it gets neither the rows nor the mutations. A full-reach token reads
// what its owner reads in a browser, so it gets both — and nothing a browser session would not.
//
// The roles still decide everything else: a full-reach token of a lecturer reaches a lecturer's
// rows, and one of a programme lead reaches that programme. It widens the Kind term, never the
// Role term or the Scope term.
func MayActWithFullReach(a principal.Actor) bool {
	return a.Authenticated() && a.ActsWithFullReach()
}

// FullReachReason is what a caller who failed MayActWithFullReach is told. German, like every
// string a person reads, and it names the remedy: a browser, or a token minted for it.
const FullReachReason = "Dafür braucht es den Browser oder ein Planungs-Token mit voller Reichweite. " +
	"Ein Planungs-Token legst Du unter „API-Tokens“ an."
