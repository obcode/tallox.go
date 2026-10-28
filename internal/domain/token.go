// Package domain holds the business logic: the rules that are neither storage nor transport.
//
// Resolvers translate between the wire and this package and do nothing else. That is what
// keeps a rule from existing twice — once for the browser and once for a CSV export — and it
// is why the token lifetime below is decided here rather than in a resolver argument default,
// where the next caller would have to know to repeat it.
package domain

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/auth"
	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
)

// Token lifetimes, in days.
//
// There is no "never expires". A token that outlives the reason it was created for is the one
// that is still in a script when its owner changes jobs — and the person best placed to
// notice that is nobody, unless the token expires and somebody has to think about renewing
// it.
//
// 90 days is short enough that a forgotten token disappears within a semester and long enough
// that renewing it is not a weekly chore. 365 is the ceiling for the case that genuinely
// wants a year — a cron job somebody maintains — and is deliberately not "as long as you
// like".
const (
	DefaultTokenDays = 90
	MaxTokenDays     = 365
	MinTokenDays     = 1
)

// MaxFullReachTokenDays is the ceiling for a token minted with full reach.
//
// Such a token reads what its owner reads in a browser — other people's unpublished wishes
// among it — and a credential like that should come up for renewal within the semester it was
// made for, not linger into the next one. Ninety days is also the default for every token, so
// somebody who changes nothing gets a valid full-reach token rather than a refusal. The database
// holds the same ceiling as a CHECK, with a day of slack for clock skew.
const MaxFullReachTokenDays = 90

// MaxDescriptionLength bounds what goes into the list. Long enough for a sentence, short
// enough that the list stays readable and that nobody stores a document in it.
const MaxDescriptionLength = 200

// The refusals this package produces. All of them are things a caller can act on.
var (
	// ErrNoDescription: an unnamed token cannot be told from the others when it is time to
	// revoke one, which is the moment it matters.
	ErrNoDescription = errors.New("a token needs a description")
	// ErrDescriptionTooLong is self-explanatory.
	ErrDescriptionTooLong = fmt.Errorf("a description may be at most %d characters", MaxDescriptionLength)
	// ErrLifetimeOutOfRange: fewer than a day, or more than the ceiling.
	ErrLifetimeOutOfRange = fmt.Errorf("a token lives between %d and %d days", MinTokenDays, MaxTokenDays)
	// ErrFullReachLifetime: a full-reach token asked for more than its ceiling.
	ErrFullReachLifetime = fmt.Errorf("a token with full reach lives at most %d days", MaxFullReachTokenDays)
	// ErrNoSuchToken covers both "no such token" and "not yours". One error on purpose: which
	// of the two it is is not information the caller is entitled to.
	ErrNoSuchToken = errors.New("no such token")
	// ErrNotAuthenticated: the caller has no identity to own a token.
	ErrNotAuthenticated = errors.New("not authenticated")
	// ErrInteractiveOnly: reached with a token, on a surface that is the browser's alone.
	//
	// The schema says the same with @interactiveOnly, and the generated code enforces that
	// before any resolver runs. Repeated here for the caller that is not a resolver: a CLI, an
	// export, a maintenance job that hands the service a token actor directly. The rule is the
	// service's, not the transport's — see people.go, which has always checked it itself.
	ErrInteractiveOnly = errors.New("only reachable in an interactive session")
	// ErrFullReachRequired: reached with a plain token, on a surface that needs the reach of a
	// browser session — a signed-in browser or a token minted with full reach. The service-side
	// twin of @fullReach, for the same reason ErrInteractiveOnly twins @interactiveOnly.
	ErrFullReachRequired = errors.New("needs a browser session or a token with full reach")
	// ErrScopeUnknown: a scope this build does not recognise. Refused rather than dropped —
	// silently discarding one would mint a token narrower than the caller asked for, and they
	// would find out from a refusal in a script weeks later.
	ErrScopeUnknown = errors.New("unknown scope")
	// ErrScopeRepeated: the same area:verb twice. Refused rather than deduplicated, because a
	// list with a repeat in it is a list somebody generated wrongly, and saying so is cheaper
	// than tidying it up.
	ErrScopeRepeated = errors.New("a scope may be listed only once")
)

// TokenRecord is a stored token as its owner may see it. No secret, no hash.
type TokenRecord struct {
	ID          string
	Description string
	Scopes      []string
	// FullReach: minted to reach what the owner reaches in a browser. Fixed at creation.
	FullReach  bool
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastUsedAt time.Time
	RevokedAt  time.Time
}

// CreatedToken is a new token: the record, plus the secret in the clear, once.
type CreatedToken struct {
	Record TokenRecord
	// Plaintext is `tallox_<id>_<secret>`. It exists here, is returned once, and is never
	// stored — the database holds a SHA-256 of the second half.
	Plaintext string
}

// TokenStore is the persistence this service needs, and nothing more.
type TokenStore interface {
	CreateToken(ctx context.Context, tokenID string, ownerID uuid.UUID, secretHash []byte,
		description string, scopes []string, fullReach bool, expiresAt time.Time) (TokenRecord, error)
	TokensOfOwner(ctx context.Context, ownerID uuid.UUID) ([]TokenRecord, error)
	// RevokeTokenOfOwner returns ErrNoSuchToken when the token does not exist *or* belongs to
	// somebody else. The store enforces ownership in the WHERE clause; this service does not
	// read first and decide afterwards.
	RevokeTokenOfOwner(ctx context.Context, tokenID string, ownerID uuid.UUID) (TokenRecord, error)
}

// TokenService is token management: create, list, revoke.
type TokenService struct {
	store TokenStore
	now   func() time.Time
}

// NewTokenService wires the service. now is injectable so that expiry has a test that does
// not sleep.
func NewTokenService(store TokenStore, now func() time.Time) *TokenService {
	if now == nil {
		now = time.Now
	}
	return &TokenService{store: store, now: now}
}

// Create mints a token for the actor and returns the plaintext exactly once.
//
// The actor is the owner — there is no "create a token for somebody else" argument, and that
// omission is the design. Handing a colleague a credential they did not generate makes the
// audit log a record of who *issued* rather than who *acted*, and it is the shape every
// "temporary" administrative shortcut takes.
//
// # Scopes
//
// An empty or nil list stores an empty list, which policy.ScopesAllow reads as unrestricted
// within the owner's roles. That is the pre-existing default and it stays the default: scopes
// only ever narrow, so "nothing selected" has to mean "nothing removed".
//
// They are not checked against what the caller may actually do, and that is not an oversight.
// A scope is a restriction the holder puts on their own credential; asking for TOKENS:WRITE
// while holding no ADMIN grant produces a token that can reach nothing there, which is exactly
// what it should produce. Validating it here would be a second permission model, and it would
// go wrong the day somebody is granted a role after minting the token.
//
// # Full reach
//
// fullReach mints a token that is judged like the owner's browser session (see
// policy.MayActWithFullReach): it reads the unpublished rows the owner's roles reach and may make
// the planning moves whose refusals answer questions about them. It is an explicit choice for
// the same reason scopes are fixed at creation — what the holder believes the token can do and
// what it can do must never be two different things — and it is capped at
// MaxFullReachTokenDays. It is not checked against the owner's roles either: a lecturer's
// full-reach token reaches a lecturer's rows, which is exactly right.
func (s *TokenService) Create(ctx context.Context, actor principal.Actor,
	description string, expiresInDays *int, scopes []policy.Scope, fullReach bool,
) (CreatedToken, error) {
	if !actor.Authenticated() {
		return CreatedToken{}, ErrNotAuthenticated
	}
	if !policy.MayReadInteractiveOnly(actor) {
		return CreatedToken{}, ErrInteractiveOnly
	}

	description = strings.TrimSpace(description)
	switch {
	case description == "":
		return CreatedToken{}, ErrNoDescription
	case len([]rune(description)) > MaxDescriptionLength:
		return CreatedToken{}, ErrDescriptionTooLong
	}

	days := DefaultTokenDays
	if expiresInDays != nil {
		days = *expiresInDays
	}
	if days < MinTokenDays || days > MaxTokenDays {
		// Refused rather than clamped. Somebody asking for 3650 days has a plan that this
		// system will not support, and silently giving them 365 means they find out a year
		// later, from a broken script, instead of now.
		return CreatedToken{}, ErrLifetimeOutOfRange
	}
	if fullReach && days > MaxFullReachTokenDays {
		// Refused rather than clamped, for the reason above.
		return CreatedToken{}, ErrFullReachLifetime
	}

	stored, err := storedScopes(scopes)
	if err != nil {
		return CreatedToken{}, err
	}

	minted, err := auth.Mint()
	if err != nil {
		return CreatedToken{}, fmt.Errorf("cannot generate a token: %w", err)
	}

	record, err := s.store.CreateToken(ctx, minted.ID, actor.ID, minted.SecretHash,
		description, stored, fullReach, s.now().AddDate(0, 0, days))
	if err != nil {
		return CreatedToken{}, fmt.Errorf("cannot store the token: %w", err)
	}

	return CreatedToken{Record: record, Plaintext: minted.Plaintext}, nil
}

// storedScopes turns the requested scopes into the form the column holds.
//
// Never nil: the store writes a text[] and the column is NOT NULL, and an empty slice and a nil
// one would otherwise reach it as different values for the same intent.
func storedScopes(scopes []policy.Scope) ([]string, error) {
	out := make([]string, 0, len(scopes))

	seen := make(map[policy.Scope]bool, len(scopes))
	for _, scope := range scopes {
		if !scope.Valid() {
			return nil, fmt.Errorf("%w: %s", ErrScopeUnknown, scope)
		}
		if seen[scope] {
			return nil, fmt.Errorf("%w: %s", ErrScopeRepeated, scope)
		}
		seen[scope] = true
		out = append(out, scope.String())
	}
	return out, nil
}

// List returns the caller's own tokens, newest first.
func (s *TokenService) List(ctx context.Context, actor principal.Actor) ([]TokenRecord, error) {
	if !actor.Authenticated() {
		return nil, ErrNotAuthenticated
	}
	if !policy.MayReadInteractiveOnly(actor) {
		return nil, ErrInteractiveOnly
	}
	return s.store.TokensOfOwner(ctx, actor.ID)
}

// Revoke withdraws one of the caller's own tokens.
//
// Immediate: the authenticator reads revoked_at on every request, so the token stops working
// on the next call rather than at the end of a cache interval. There is no un-revoke — a
// credential that can be reinstated is one that has to be treated as live after it was
// reported lost.
func (s *TokenService) Revoke(ctx context.Context, actor principal.Actor, tokenID string) (TokenRecord, error) {
	if !actor.Authenticated() {
		return TokenRecord{}, ErrNotAuthenticated
	}
	if !policy.MayReadInteractiveOnly(actor) {
		return TokenRecord{}, ErrInteractiveOnly
	}
	return s.store.RevokeTokenOfOwner(ctx, tokenID, actor.ID)
}
