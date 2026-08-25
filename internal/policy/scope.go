package policy

import (
	"strings"

	"github.com/obcode/tallox.go/internal/principal"
)

// ScopeArea is the part of the API a scope refers to.
//
// The areas are coarse on purpose. A scope list is something a colleague ticks in a dialog
// while their mind is on the evaluation script they are about to write, not on this system's
// authorization model — and a list of thirty checkboxes gets answered by ticking all of them,
// which produces a token that is nominally scoped and actually unrestricted. Four areas that
// map to recognisable parts of the tool ("my profile", "my tokens") is a list somebody reads.
//
// They also are not a substitute for roles. The area says which part of the API a token may
// reach; whether the owner may see anything there at all is still decided by their grants. A
// LECTURER with an ADMIN-scoped token administers nothing.
//
// New areas arrive with the fields that need them — PLANNING with the demand, WISHES with the
// wish table — rather than being declared in advance. An area with no field behind it is a
// promise in an enum that colleagues can read via introspection.
type ScopeArea string

const (
	// ScopeAreaPublic is what answers without an identity: the build info. Its purpose is to
	// stay reachable when everything else is refused, so that "my token is broken" and "the
	// route is broken" are distinguishable from the client side.
	//
	// It is the one area a scope list cannot narrow away — see ScopesAllow. Listing it is
	// therefore never necessary and never wrong; the enum value exists so that the fields
	// behind it are annotated and documented like every other root field.
	ScopeAreaPublic ScopeArea = "PUBLIC"

	// ScopeAreaProfile is the caller's own identity and session: who am I, which roles do I
	// hold, which of them am I being judged by.
	ScopeAreaProfile ScopeArea = "PROFILE"

	// ScopeAreaPlanning is the planning process itself: which semesters exist, where each one
	// stands, and — as those tables arrive — the demand, the assignments and the statistics.
	//
	// The first area a token can actually be narrowed *to* something useful with. PUBLIC and
	// PROFILE are the caller describing themselves; TOKENS and ADMIN are unreachable through a
	// token at all. This is the one where "this token may read the planning and nothing else"
	// becomes a sentence somebody would want to say.
	ScopeAreaPlanning ScopeArea = "PLANNING"

	// ScopeAreaWishes is registering interest in instance parts, and reading what may be read of
	// other people's.
	//
	// Its own area rather than part of PLANNING, because it is the one part of the planning a
	// colleague might sensibly want a token narrowed *to*: "this script keeps my wishes in step
	// with my calendar" is a sentence somebody would say, and it should not carry the demand of
	// the whole faculty with it. The reverse matters more — an evaluation script scoped to
	// PLANNING does not thereby reach anybody's wishes.
	//
	// What it does not do is decide what is visible. Through a token the wish rule collapses to
	// the caller's own entries whatever the scope says; the area bounds the surface, the policy
	// bounds the rows.
	ScopeAreaWishes ScopeArea = "WISHES"

	// ScopeAreaTokens is Personal Access Token management.
	//
	// A token can never actually reach it — those fields are @interactiveOnly, precisely so
	// that a leaked token cannot mint its successors. The area exists anyway, because the
	// alternative is a field with no scope, and a field with no scope is the case the
	// fail-closed default and its CI test exist to prevent. Two independent reasons to refuse
	// is the correct number here.
	ScopeAreaTokens ScopeArea = "TOKENS"

	// ScopeAreaAdmin is user and role administration. Also @interactiveOnly, for the same
	// reason and with the same consequence as TOKENS.
	ScopeAreaAdmin ScopeArea = "ADMIN"
)

// AllScopeAreas returns every area this package knows, in a stable order.
//
// Ordered by how much they expose, which is the order a person ticking checkboxes should read
// them in. Like AllRoles, the order carries no authority: scopes are a set.
func AllScopeAreas() []ScopeArea {
	return []ScopeArea{
		ScopeAreaPublic,
		ScopeAreaProfile,
		ScopeAreaPlanning,
		ScopeAreaWishes,
		ScopeAreaTokens,
		ScopeAreaAdmin,
	}
}

// Valid reports whether a is one of the known areas.
func (a ScopeArea) Valid() bool {
	for _, known := range AllScopeAreas() {
		if a == known {
			return true
		}
	}
	return false
}

// ScopeVerb is whether a scope permits reading or changing.
type ScopeVerb string

const (
	// ScopeVerbRead permits queries.
	ScopeVerbRead ScopeVerb = "READ"
	// ScopeVerbWrite permits mutations, and reading in the same area — see Scope.Grants.
	ScopeVerbWrite ScopeVerb = "WRITE"
)

// AllScopeVerbs returns both verbs, weakest first.
func AllScopeVerbs() []ScopeVerb {
	return []ScopeVerb{ScopeVerbRead, ScopeVerbWrite}
}

// Valid reports whether v is one of the known verbs.
func (v ScopeVerb) Valid() bool {
	return v == ScopeVerbRead || v == ScopeVerbWrite
}

// Scope is one area:verb pair — both what a schema field requires and what a token holds.
//
// Deliberately one type for both sides. The alternative, a RequiredScope and a GrantedScope,
// reads as more careful and buys nothing: the comparison between them is the only operation
// either side has, and having it as a method on a single type is what keeps the rule in one
// place instead of in the caller.
type Scope struct {
	Area ScopeArea
	Verb ScopeVerb
}

// ScopeFallback is what a root field with no @scope directive is taken to require.
//
// The most privileged combination there is, so that the failure mode of forgetting the
// annotation is a field nothing can reach rather than a field everything can. The sibling
// project defaults the other way, and the consequence is that its newest endpoints are its
// least protected ones — exactly backwards, because a new endpoint is the one nobody has
// reviewed yet.
//
// This is the second line of defence and not the first. TestEveryRootFieldDeclaresAScope
// fails the build over a missing annotation, so in a healthy repository this value is never
// reached. It exists for the state between somebody adding a field and CI telling them.
var ScopeFallback = Scope{Area: ScopeAreaAdmin, Verb: ScopeVerbWrite}

// String renders the scope in the form tokens store and colleagues type: AREA:VERB.
func (s Scope) String() string { return string(s.Area) + ":" + string(s.Verb) }

// Valid reports whether both halves are known values.
func (s Scope) Valid() bool { return s.Area.Valid() && s.Verb.Valid() }

// ParseScope reads the stored form. The bool is false for anything unrecognised.
//
// Unknown input grants nothing rather than erroring, which matters because the input is a
// text[] column: a scope string this build does not know is what a downgrade looks like from
// inside the older binary. Ignoring it means the token loses that one capability; failing the
// request would take out the token's other, perfectly valid scopes as well.
//
// Case-insensitive on the way in, because the stored form is uppercase and a colleague
// writing a scope by hand will not be.
func ParseScope(s string) (Scope, bool) {
	area, verb, found := strings.Cut(strings.ToUpper(strings.TrimSpace(s)), ":")
	if !found {
		return Scope{}, false
	}

	parsed := Scope{Area: ScopeArea(area), Verb: ScopeVerb(verb)}
	if !parsed.Valid() {
		return Scope{}, false
	}
	return parsed, true
}

// Grants reports whether holding s permits what want requires.
//
// # WRITE implies READ
//
// Within one area, and only within it. A token that may change something but not look at it
// is not a capability anybody wants: every realistic script reads before it writes, so the
// implication would end up being made by every dialog ticking both boxes anyway. Making it
// explicit here means it is documented, tested and the same everywhere, rather than emergent
// from how the checkboxes happened to be wired.
//
// It does not cross areas. ADMIN:WRITE says nothing about PROFILE, and there is no scope that
// implies all the others — a token holding "everything" should have to say so by listing it.
func (s Scope) Grants(want Scope) bool {
	if s.Area != want.Area {
		return false
	}
	return s.Verb == want.Verb || s.Verb == ScopeVerbWrite
}

// ScopesAllow reports whether the actor's credential permits an operation requiring want.
//
// This is the middle factor of the invariant, on its own:
//
//	effective permission = (what the Role allows) ∩ (what the Scopes grant) ∩ (what the Kind allows)
//
// It answers only that middle term. A true here means the credential does not stand in the
// way; whether the person may see the data is the role's question, asked separately and
// always. Nothing in this function can widen anything.
//
// # Only tokens carry scopes
//
// An interactive session has no scope list, because there is nothing to narrow: the person is
// present, the session is audited, and their roles already bound what they can reach. Scopes
// exist for the credential that is *not* the person — a string in a script that outlives the
// afternoon it was written on.
//
// # An empty list does not narrow
//
// A token with no scopes may do everything its owner's roles allow. This mirrors Narrow, and
// the argument is the same one: the mechanism can only ever remove, so "nothing selected"
// has to mean "nothing removed". Reading it as "nothing permitted" would make the empty set
// the most restrictive value of a field whose default is empty, which is how every existing
// token would have stopped working on the deploy that shipped this file.
//
// The direction to be careful about is therefore the minting path, not this one: whatever
// creates a token decides whether the caller gets a narrowed list or the unrestricted default,
// and today it always passes an empty list. Until a scope can be chosen at creation, this term
// only ever bites on tokens seeded by tests.
func ScopesAllow(a principal.Actor, want Scope) bool {
	if a.Kind != principal.KindToken {
		return true
	}
	if len(a.Scopes) == 0 {
		return true
	}

	// PUBLIC cannot be narrowed away, and the reason is not convenience. What is behind it
	// answers without any credential at all, so refusing it to a scoped token would hand its
	// holder *less* than an anonymous caller gets — and the one field there exists precisely to
	// answer when everything else is refused. A token whose diagnosis field is scoped off is a
	// token whose owner cannot tell a broken credential from a broken route.
	//
	// Nothing is given away by this: it is the same build stamp the unauthenticated caller
	// reads. Found by TestAMintedTokenIsNarrowedAtTheDoor, which minted a PLANNING-only token
	// and could not ask the server for its version.
	if want.Area == ScopeAreaPublic {
		return true
	}

	for _, held := range a.Scopes {
		if parsed, ok := ParseScope(held); ok && parsed.Grants(want) {
			return true
		}
	}
	return false
}

// InsufficientScopeReason is what a caller who failed that check is told.
//
// German, like every string a person reads, and it names the scope that was missing — unlike
// InteractiveOnlyReason, which deliberately does not name the field. The difference is that
// this one is actionable: the reader can mint a token with that scope, so telling them which
// one saves a round through the documentation. It is not a disclosure, because the schema
// carries the same annotation and introspection is on.
func InsufficientScopeReason(want Scope) string {
	return "Das verwendete Token hat nicht den nötigen Scope " + want.String() + "."
}
