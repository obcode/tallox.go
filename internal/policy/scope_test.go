package policy_test

import (
	"testing"

	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
)

func TestParseScope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		in    string
		want  policy.Scope
		valid bool
	}{
		{
			name:  "the stored form",
			in:    "PROFILE:READ",
			want:  policy.Scope{Area: policy.ScopeAreaProfile, Verb: policy.ScopeVerbRead},
			valid: true,
		},
		{
			name:  "lower case, because a colleague typing one by hand will not shout",
			in:    "tokens:write",
			want:  policy.Scope{Area: policy.ScopeAreaTokens, Verb: policy.ScopeVerbWrite},
			valid: true,
		},
		{
			name:  "surrounding whitespace, because a YAML list or a copy-paste carries it",
			in:    "  ADMIN:READ  ",
			want:  policy.Scope{Area: policy.ScopeAreaAdmin, Verb: policy.ScopeVerbRead},
			valid: true,
		},
		{name: "no separator", in: "PROFILE"},
		{name: "empty", in: ""},
		// WISHES was the stand-in for an unknown area here until the wish table arrived and it
		// became a real one — which is the whole point of areas arriving with the fields that need
		// them. Replaced rather than deleted: an area this build does not know still has to parse
		// as nothing, and that is the case an evaluation script written against a newer server
		// meets.
		{name: "an area this build does not know", in: "ASSIGNMENTS:READ"},
		{name: "a verb this build does not know", in: "PROFILE:DELETE"},
		{name: "an empty half", in: "PROFILE:"},
		{name: "the separator alone", in: ":"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := policy.ParseScope(tt.in)
			if ok != tt.valid {
				t.Fatalf("ParseScope(%q) parsed = %v, want %v", tt.in, ok, tt.valid)
			}
			if got != tt.want {
				t.Errorf("ParseScope(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// TestParseScopeRoundTrips is the pairing that keeps the stored form and the rendered form
// from drifting. They meet in the database, where one build writes and another reads.
func TestParseScopeRoundTrips(t *testing.T) {
	t.Parallel()

	for _, area := range policy.AllScopeAreas() {
		for _, verb := range policy.AllScopeVerbs() {
			scope := policy.Scope{Area: area, Verb: verb}

			parsed, ok := policy.ParseScope(scope.String())
			if !ok {
				t.Errorf("%s renders to %q, which does not parse back", scope, scope.String())
				continue
			}
			if parsed != scope {
				t.Errorf("%s round-tripped to %v", scope, parsed)
			}
		}
	}
}

func TestScopeGrants(t *testing.T) {
	t.Parallel()

	var (
		profileRead  = policy.Scope{Area: policy.ScopeAreaProfile, Verb: policy.ScopeVerbRead}
		profileWrite = policy.Scope{Area: policy.ScopeAreaProfile, Verb: policy.ScopeVerbWrite}
		tokensRead   = policy.Scope{Area: policy.ScopeAreaTokens, Verb: policy.ScopeVerbRead}
		tokensWrite  = policy.Scope{Area: policy.ScopeAreaTokens, Verb: policy.ScopeVerbWrite}
	)

	tests := []struct {
		name  string
		held  policy.Scope
		want  policy.Scope
		grant bool
	}{
		{name: "the same scope", held: profileRead, want: profileRead, grant: true},
		{name: "write implies read in its own area", held: profileWrite, want: profileRead, grant: true},
		{name: "read does not imply write", held: profileRead, want: profileWrite},
		{name: "write does not cross areas", held: profileWrite, want: tokensWrite},
		{name: "read does not cross areas", held: profileRead, want: tokensRead},
		{
			name: "and the implication does not cross them either",
			held: profileWrite,
			want: tokensRead,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.held.Grants(tt.want); got != tt.grant {
				t.Errorf("%s.Grants(%s) = %v, want %v", tt.held, tt.want, got, tt.grant)
			}
		})
	}
}

// TestNoScopeGrantsEverything guards the decision that there is no super-scope.
//
// It would be an easy thing to add and a hard thing to take back: the moment one exists, every
// dialog offers it, every script asks for it, and the area list becomes decoration. Written as
// an exhaustive sweep so that a new area cannot quietly become one.
func TestNoScopeGrantsEverything(t *testing.T) {
	t.Parallel()

	for _, heldArea := range policy.AllScopeAreas() {
		for _, heldVerb := range policy.AllScopeVerbs() {
			held := policy.Scope{Area: heldArea, Verb: heldVerb}

			for _, wantArea := range policy.AllScopeAreas() {
				if wantArea == heldArea {
					continue
				}
				want := policy.Scope{Area: wantArea, Verb: policy.ScopeVerbRead}
				if held.Grants(want) {
					t.Errorf("%s grants %s — no scope may reach outside its own area", held, want)
				}
			}
		}
	}
}

func TestScopesAllow(t *testing.T) {
	t.Parallel()

	want := policy.Scope{Area: policy.ScopeAreaProfile, Verb: policy.ScopeVerbRead}

	tests := []struct {
		name  string
		actor principal.Actor
		allow bool
	}{
		{
			name:  "an interactive session has no scopes to narrow it",
			actor: principal.Actor{Kind: principal.KindInteractive},
			allow: true,
		},
		{
			name: "and holding an unrelated scope list does not change that",
			actor: principal.Actor{
				Kind:   principal.KindInteractive,
				Scopes: []string{"ADMIN:READ"},
			},
			allow: true,
		},
		{
			name:  "an anonymous caller is not narrowed either — the role decides",
			actor: principal.Actor{Kind: principal.KindNone},
			allow: true,
		},
		{
			name:  "a token with no scopes is not narrowed",
			actor: principal.Actor{Kind: principal.KindToken},
			allow: true,
		},
		{
			name:  "an empty non-nil list is the same thing",
			actor: principal.Actor{Kind: principal.KindToken, Scopes: []string{}},
			allow: true,
		},
		{
			name:  "a token holding exactly the scope",
			actor: principal.Actor{Kind: principal.KindToken, Scopes: []string{"PROFILE:READ"}},
			allow: true,
		},
		{
			name:  "a token holding the write scope of the same area",
			actor: principal.Actor{Kind: principal.KindToken, Scopes: []string{"PROFILE:WRITE"}},
			allow: true,
		},
		{
			name:  "a token holding only another area",
			actor: principal.Actor{Kind: principal.KindToken, Scopes: []string{"ADMIN:WRITE"}},
		},
		{
			name: "one of several matches",
			actor: principal.Actor{
				Kind:   principal.KindToken,
				Scopes: []string{"ADMIN:WRITE", "PROFILE:READ"},
			},
			allow: true,
		},
		{
			name: "a scope this build cannot parse is ignored, not fatal",
			actor: principal.Actor{
				Kind:   principal.KindToken,
				Scopes: []string{"WISHES:READ", "PROFILE:READ"},
			},
			allow: true,
		},
		{
			name:  "a list of nothing but unparseable scopes narrows to nothing",
			actor: principal.Actor{Kind: principal.KindToken, Scopes: []string{"WISHES:READ"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := policy.ScopesAllow(tt.actor, want); got != tt.allow {
				t.Errorf("ScopesAllow(%v, %s) = %v, want %v", tt.actor.Scopes, want, got, tt.allow)
			}
		})
	}
}

// TestAddingAScopeNeverRemovesOne is the counterpart of TestNarrowCanOnlyEverRemove.
//
// Both express the same property of this system's two narrowing mechanisms — that they are
// monotone, so a longer list cannot be a smaller permission. It is what lets a dialog add a
// checkbox without anybody reasoning about interference between the boxes.
//
// The empty list is excluded because it is the unrestricted default rather than a point on
// this scale; that transition is pinned by TestScopesAllow instead.
func TestAddingAScopeNeverRemovesOne(t *testing.T) {
	t.Parallel()

	var all []policy.Scope
	for _, area := range policy.AllScopeAreas() {
		for _, verb := range policy.AllScopeVerbs() {
			all = append(all, policy.Scope{Area: area, Verb: verb})
		}
	}

	for _, base := range all {
		for _, added := range all {
			for _, want := range all {
				before := policy.ScopesAllow(principal.Actor{
					Kind:   principal.KindToken,
					Scopes: []string{base.String()},
				}, want)

				after := policy.ScopesAllow(principal.Actor{
					Kind:   principal.KindToken,
					Scopes: []string{base.String(), added.String()},
				}, want)

				if before && !after {
					t.Errorf("holding [%s] allows %s, holding [%s %s] does not",
						base, want, base, added)
				}
			}
		}
	}
}

// TestScopeFallbackIsTheMostPrivilegedCombination pins the direction of the default.
//
// Not a tautology: it fails if somebody "fixes" the fallback to something reachable, which is
// the tempting change to make while debugging a field that refuses to answer.
func TestScopeFallbackIsTheMostPrivilegedCombination(t *testing.T) {
	t.Parallel()

	if !policy.ScopeFallback.Valid() {
		t.Fatalf("the fallback %v is not a valid scope", policy.ScopeFallback)
	}
	if policy.ScopeFallback.Verb != policy.ScopeVerbWrite {
		t.Errorf("the fallback is %s — a field with no annotation must require the strongest "+
			"verb, not a readable one", policy.ScopeFallback)
	}

	// Every other area must fail to grant it, which is what "most privileged" means here.
	for _, area := range policy.AllScopeAreas() {
		if area == policy.ScopeFallback.Area {
			continue
		}
		for _, verb := range policy.AllScopeVerbs() {
			held := policy.Scope{Area: area, Verb: verb}
			if held.Grants(policy.ScopeFallback) {
				t.Errorf("%s grants the fallback %s — then forgetting an annotation is not "+
					"fail-closed for a token holding %s", held, policy.ScopeFallback, held)
			}
		}
	}
}

// TestPublicCannotBeNarrowedAway pins the exception, and the reason it is not a convenience.
//
// What sits behind PUBLIC answers without any credential at all. A token scoped away from it
// would therefore reach *less* than an anonymous caller — and the one field there is exists to
// answer when everything else is refused, so that its owner can tell a broken credential from
// a broken route. Losing that is the one thing a narrowing must not do.
//
// Found by writing TestAMintedTokenIsNarrowedAtTheDoor: a freshly minted PLANNING-only token
// could not ask the server for its own version.
func TestPublicCannotBeNarrowedAway(t *testing.T) {
	t.Parallel()

	public := policy.Scope{Area: policy.ScopeAreaPublic, Verb: policy.ScopeVerbRead}

	for _, held := range []([]string){
		nil,
		{},
		{"PLANNING:READ"},
		{"ADMIN:WRITE"},
		{"WISHES:READ"},
		{"PROFILE:READ", "PLANNING:WRITE"},
	} {
		actor := principal.Actor{Kind: principal.KindToken, Scopes: held}
		if !policy.ScopesAllow(actor, public) {
			t.Errorf("a token holding %v cannot read PUBLIC — it would see less than an "+
				"anonymous caller", held)
		}
	}
}

// TestOnlyPublicIsExempt keeps that exception from spreading.
//
// Every other area has to be refusable, or a scope list would stop being a narrowing. Written
// as a sweep so that a new area is covered the day it is added.
func TestOnlyPublicIsExempt(t *testing.T) {
	t.Parallel()

	// A token that holds exactly one scope, in an area of its own.
	actor := principal.Actor{Kind: principal.KindToken, Scopes: []string{"PLANNING:READ"}}

	for _, area := range policy.AllScopeAreas() {
		want := policy.Scope{Area: area, Verb: policy.ScopeVerbRead}
		allowed := policy.ScopesAllow(actor, want)

		switch area {
		case policy.ScopeAreaPublic, policy.ScopeAreaPlanning:
			if !allowed {
				t.Errorf("%s should be reachable", want)
			}
		default:
			if allowed {
				t.Errorf("%s is reachable for a token that does not hold it", want)
			}
		}
	}
}
