// Package principal carries the authenticated caller — the Actor — through the request
// context.
//
// It is the narrow waist between the two doors and every rule in the system:
//
//	internal/auth   produces an Actor (from X-Remote-User, or from a Personal Access Token)
//	internal/policy consumes an Actor (and nothing else) to decide what may be seen or done
//
// That is what makes "two doors, one authorization model" more than a sentence in a README.
// Once a request has an Actor, nothing downstream can tell which door it came through unless
// it asks Kind explicitly, so a rule cannot accidentally apply to only one of them.
//
// # Dependencies
//
// stdlib and github.com/google/uuid. Nothing else, on purpose. In the sibling project the
// equivalent package imports the generated GraphQL models, which drags the transport layer
// into the domain and makes it impossible to test a rule without constructing a GraphQL
// request. Here, a policy test constructs an Actor as a struct literal.
//
// # Why roles are opaque strings
//
// An Actor carries the role grants exactly as they are stored, and does not interpret them.
// The meaning of LECTURER or DEANS_OFFICE lives in internal/policy, which is where the rules
// that use them live. Two consequences, both deliberate:
//
//   - The dependency runs policy → principal and never back. If Role were defined here,
//     principal would either import policy (a cycle) or own a copy of the rules' vocabulary
//     that can drift from them.
//   - There is nowhere to put an Actor.IsAdmin() helper. That method is how authorization
//     leaks out of the policy package one convenience at a time, and its absence is the
//     point: to know what an Actor may do, you have to ask internal/policy.
//
// A role string the policy does not know grants nothing — see policy.RolesOf.
package principal

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// Kind is how the caller authenticated. It is the third factor of the invariant in CLAUDE.md:
//
//	effective permission = (what the Role allows) ∩ (what the Scopes grant) ∩ (what the Kind allows)
//
// Most rules never look at it. The few that do are the ones where a long-lived token in a
// script is a different risk from an audited interactive session — personnel data, other
// people's unpublished wishes, token management itself.
type Kind string

const (
	// KindNone is an unauthenticated caller. The zero value, so an Actor that was never
	// filled in has no privileges rather than the wrong ones.
	KindNone Kind = ""
	// KindInteractive is a person in a browser, behind the auth proxy.
	KindInteractive Kind = "interactive"
	// KindToken is a Personal Access Token: a script, a notebook, a cron job.
	KindToken Kind = "token"
)

// RoleScope narrows one grant to one thing.
//
// Uninterpreted here, exactly like the role strings it accompanies: this package carries who
// the caller is and what was granted to them, and internal/policy decides what a role name and
// a programme id mean together. A field named after the rule would put the rule here.
//
// # Why two named fields rather than one Target
//
// Exactly one of them is set, and the role string already says which — so a single
// Target uuid.UUID would carry the same information in half the space. It is two fields
// because the failure it has to survive is a row whose role string is wrong. With named
// fields such a row is visibly malformed: a subject group id sitting in ProgrammeID is a
// scope that matches no programme. With one field it would be indistinguishable from a
// well-formed scope of the other kind, and the rule reading it would grant something nobody
// granted. policy.TestTheTwoScopesDoNotReadEachOther asserts that a crossed row grants
// nothing, and store.TestRoleScopesNameExactlyOneThing asserts that the queries never build
// one.
type RoleScope struct {
	// Role is the grant this narrows, as stored.
	Role string
	// ProgrammeID is the study programme the grant applies to. uuid.Nil when this scope names
	// something else.
	ProgrammeID uuid.UUID
	// SubjectGroupID is the subject group the grant applies to. uuid.Nil when this scope names
	// something else.
	SubjectGroupID uuid.UUID
}

// NamesNothing reports whether this scope points at no thing at all.
//
// A scope like that grants nothing — it is the shape a malformed row takes — and every rule
// that reads RoleScopes drops it rather than treating the zero uuid as a wildcard. The check
// lives here and not in the rules because it is a statement about the struct, not about a
// permission.
func (s RoleScope) NamesNothing() bool {
	return s.ProgrammeID == uuid.Nil && s.SubjectGroupID == uuid.Nil
}

// Actor is the authenticated caller of the current request.
//
// The zero value is the anonymous caller, which is what makes the context helpers below safe:
// a handler that runs without the auth middleware sees somebody with no identity and no
// roles, not somebody with unchecked ones.
//
// Treat the slices as read-only. An Actor is copied by value all over the request path, and
// the copies share their backing arrays.
type Actor struct {
	// ID is the person row this actor belongs to. uuid.Nil for an unauthenticated caller.
	ID uuid.UUID
	// Mail is the identity the auth proxy asserts (X-Remote-User), and the natural key of a
	// person. Lower-cased by the store's citext column, not here.
	Mail string
	// Name is for rendering and for log lines.
	Name string
	// Roles are the grants this request is judged by, uninterpreted. See the package doc.
	//
	// Ordinarily these are the grants as stored. When the caller asked to be narrowed — the
	// "look at this as a lecturer" feature — they are a subset of them, and NarrowedFrom
	// holds what was stored. Rules read this field and nothing else, so narrowing applies to
	// every rule automatically rather than to the ones somebody remembered.
	Roles []string
	// NarrowedFrom holds the grants as stored, when and only when this actor was narrowed.
	// nil otherwise.
	//
	// It exists for two consumers and no rule: the interface, which has to say that what you
	// are looking at is not your own view, and the audit log, which has to record that an
	// action was taken while narrowed. A rule that consulted it would be undoing the
	// narrowing it is supposed to respect.
	NarrowedFrom []string
	// RoleScopes narrow individual grants to individual things, uninterpreted like Roles.
	//
	// A role in Roles says what somebody may do; an entry here says what they may do it to.
	// Only some roles have a scope dimension — leading a study programme is leading *one* —
	// and a role with no entry here has none rather than all of them. Which way round that is
	// read is internal/policy's business, not this package's.
	//
	// Deliberately not folded into Roles as "PROGRAMME_LEAD:<uuid>" strings. Every rule that
	// asks "does this actor hold PROGRAMME_LEAD" would then have to know about the encoding,
	// and the first one to forget would be reading a scoped grant as no grant at all.
	//
	// Narrowed with the roles: an actor that kept the scopes of a role it no longer holds is
	// a bug in the direction that leaks.
	RoleScopes []RoleScope
	// Scopes are the area:verb grants of the Personal Access Token used, empty for an
	// interactive session (where the role alone bounds what is allowed).
	//
	// Enforced per operation against the @scope annotation on the root fields — see
	// policy.ScopesAllow and graph.EnforceScopes. Strings rather than a typed slice for the
	// same reason Roles are: this package sits below the rules and does not interpret them,
	// so a scope a newer server wrote and this one cannot parse arrives here intact and is
	// discarded by the rule rather than by the transport.
	//
	// An empty list is *not* the empty permission. It means unrestricted within the owner's
	// roles, exactly as an unnarrowed role set does — scopes can only ever remove.
	Scopes []string
	// Kind is how this actor authenticated.
	Kind Kind
	// TokenID is the public part of the Personal Access Token used, empty for an interactive
	// session. It is what lets the audit log say which token did something after that token
	// has been revoked — which is why revocation sets a timestamp instead of deleting a row.
	//
	// The secret is never here. It is compared once, in internal/auth, and then forgotten.
	TokenID string
}

// Anonymous is the caller with no identity: no proxy header, no token.
//
// Named rather than written as Actor{} at call sites, because "no principal in the context"
// and "the anonymous principal" have to be the same thing for the fail-closed default below
// to hold, and a name is how that stays visible.
var Anonymous = Actor{}

// Authenticated reports whether this actor is anybody at all.
//
// Identity is the ID, not the mail address: a mail address can be filled in from a header
// before anything has looked it up, and "the proxy sent a name we do not know" must not
// count as authenticated.
func (a Actor) Authenticated() bool { return a.ID != uuid.Nil }

// Interactive reports whether the caller is a person in a browser rather than a token.
//
// A plain accessor, not a rule: what interactivity *permits* is a question for
// internal/policy.
func (a Actor) Interactive() bool { return a.Kind == KindInteractive }

// Narrowed reports whether this actor asked to be judged by fewer roles than they hold.
//
// A plain accessor like Interactive, and for the same reason: what narrowing *means* is a
// question for internal/policy, which is also the only package that produces the state. This
// exists so the interface can show a banner and the log can carry a field.
func (a Actor) Narrowed() bool { return a.NarrowedFrom != nil }

// Owns reports whether the given owner reference is this actor.
//
// The guard that matters is the nil case, and it is not defensive padding. Ownership is what
// lets somebody see their own unpublished wish, and both an unauthenticated actor and an
// unset owner column are the nil UUID — so a naive a.ID == ownerID would answer "yes, that is
// yours" to an anonymous caller looking at an orphaned row. That is the confidentiality rule
// failing silently, in the direction that leaks.
func (a Actor) Owns(ownerID uuid.UUID) bool {
	return a.ID != uuid.Nil && ownerID != uuid.Nil && a.ID == ownerID
}

// String renders the actor for a log line: who, and through which door.
//
// Deliberately includes the token ID — the public half — because "which token did this" is
// the first question when a script misbehaves, and deliberately cannot include the secret,
// which this type never holds.
func (a Actor) String() string {
	if !a.Authenticated() {
		return "anonymous"
	}
	if a.Kind == KindToken {
		return fmt.Sprintf("%s (token %s)", a.Mail, a.TokenID)
	}
	return fmt.Sprintf("%s (%s)", a.Mail, a.Kind)
}

// contextKey is unexported and of a struct type of its own, so no other package can construct
// the key and overwrite the principal of a request in flight.
type contextKey struct{}

// NewContext returns a copy of ctx carrying the actor.
func NewContext(ctx context.Context, a Actor) context.Context {
	return context.WithValue(ctx, contextKey{}, a)
}

// From returns the actor in the context, or Anonymous when there is none.
//
// The absent case is a legitimate one — /healthz, the playground, any handler mounted outside
// the auth middleware — and it resolves towards no privileges. A version that panicked or
// returned an error would push every caller into writing a fallback, and the tempting
// fallback is the wrong one.
func From(ctx context.Context) Actor {
	a, _ := Lookup(ctx)
	return a
}

// Lookup returns the actor and whether the middleware ran at all.
//
// For the rare caller that has to tell "anonymous" from "unauthenticated code path" — a
// diagnostic, a test that the middleware is mounted where it should be. Rules use From.
func Lookup(ctx context.Context) (Actor, bool) {
	a, ok := ctx.Value(contextKey{}).(Actor)
	if !ok {
		return Anonymous, false
	}
	return a, true
}
