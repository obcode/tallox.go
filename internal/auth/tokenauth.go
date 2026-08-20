package auth

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/obcode/tallox.go/internal/principal"
)

// bearerPrefix is the only scheme accepted on the token door. Case-insensitively, because
// RFC 7235 says the scheme is case-insensitive and a client library that sends "bearer" is
// not wrong.
const bearerPrefix = "bearer "

// dummyHash is compared against when no token row was found.
//
// Without it, an unknown token id returns before hashing and comparing while a known one does
// both, and the difference is measurable from outside. That would let somebody enumerate
// valid token ids — not catastrophic on its own, but it is the sort of thing that combines
// with something else later, and closing it costs one comparison against a fixed array.
var dummyHash = HashSecret("this secret exists only to keep the miss path the same length")

// TokenAuthenticator is the door for Personal Access Tokens: Bearer only, no fallback.
//
// No fallback is a deliberate omission. It would be easy to also accept X-Remote-User here,
// and it would mean that a request reaching this mount with a forged header authenticates —
// on the one route that, by design, has no proxy in front of it stripping headers. The two
// doors are separate routes in Caddy for the same reason.
type TokenAuthenticator struct {
	tokens TokenLookup
	now    func() time.Time

	// inflight tracks the fire-and-forget last-used writes, so that a test and a graceful
	// shutdown can both wait for them instead of racing a closing pool.
	inflight sync.WaitGroup
}

// NewTokenAuthenticator builds the token-door authenticator.
func NewTokenAuthenticator(cfg Config) *TokenAuthenticator {
	return &TokenAuthenticator{
		tokens: cfg.Tokens,
		now:    cfg.now,
	}
}

// Door names the mount for log lines.
func (*TokenAuthenticator) Door() string { return "token" }

// Wait blocks until the pending last-used writes have finished.
//
// For tests. Without it, a goroutine writing to a pool that t.Cleanup is about to close
// produces an error log — and worse, a data race the -race detector reports — from a test
// that has already passed.
//
// Shutdown deliberately does not call it: last_used_at is bookkeeping, and delaying a
// restart to persist "this token was used a moment ago" would trade something that matters
// for something that does not.
func (a *TokenAuthenticator) Wait() { a.inflight.Wait() }

// Authenticate verifies a bearer token and returns its owner as an actor.
func (a *TokenAuthenticator) Authenticate(ctx context.Context, r *http.Request) (principal.Actor, error) {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if header == "" {
		// Nothing presented. Anonymous, so that buildInfo answers here too — a smoke test
		// against the machine door has to be able to reach *something* without a credential,
		// or it cannot distinguish a broken server from a wrong token.
		return principal.Anonymous, ErrNoCredential
	}
	if len(header) < len(bearerPrefix) || !strings.EqualFold(header[:len(bearerPrefix)], bearerPrefix) {
		return principal.Anonymous, fmt.Errorf("%w: not a Bearer credential", ErrMalformedToken)
	}

	parsed, err := ParseToken(strings.TrimSpace(header[len(bearerPrefix):]))
	if err != nil {
		return principal.Anonymous, err
	}

	if a.tokens == nil {
		return principal.Anonymous, fmt.Errorf("%w: no token lookup configured", ErrUnavailable)
	}

	stored, err := a.tokens.TokenByID(ctx, parsed.ID)
	if err != nil {
		return principal.Anonymous, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}

	presented := HashSecret(parsed.Secret)
	if stored == nil {
		// Compare anyway, against a constant, and then refuse. Same work, same shape, same
		// error as a wrong secret: "no such token" and "wrong secret" must be one answer.
		subtle.ConstantTimeCompare(dummyHash, presented)
		return principal.Anonymous, fmt.Errorf("%w: no token with id %s", ErrInvalidToken, parsed.ID)
	}
	if subtle.ConstantTimeCompare(stored.SecretHash, presented) != 1 {
		return principal.Anonymous, fmt.Errorf("%w: secret does not match token %s",
			ErrInvalidToken, parsed.ID)
	}

	// Everything below here is only reachable by somebody who holds the secret, which is why
	// these refusals may be specific: they tell the owner of a token why it stopped working.
	if !stored.RevokedAt.IsZero() {
		return principal.Anonymous, fmt.Errorf("%w: %s", ErrRevokedToken, parsed.ID)
	}
	if !stored.ExpiresAt.After(a.now()) {
		return principal.Anonymous, fmt.Errorf("%w: %s expired at %s",
			ErrExpiredToken, parsed.ID, stored.ExpiresAt.Format(time.RFC3339))
	}
	if !stored.Owner.Active {
		// Deactivating a person is how a leaver loses access to everything at once. Checking
		// it here is what makes that include their tokens, without anybody having to remember
		// to revoke them one by one.
		return principal.Anonymous, fmt.Errorf("%w: owner of %s", ErrInactiveUser, parsed.ID)
	}

	a.markUsed(parsed.ID)

	return principal.Actor{
		ID:   stored.Owner.ID,
		Mail: stored.Owner.Mail,
		Name: stored.Owner.Name,
		// Roles come from the owner on every request rather than from the token row. That is
		// what makes "a token can never exceed its owner" true by construction: revoking a
		// role demotes every token that person holds, immediately, without touching one.
		Roles:      stored.Owner.Roles,
		RoleScopes: stored.Owner.RoleScopes,
		Scopes:     stored.Scopes,
		Kind:       principal.KindToken,
		TokenID:    stored.ID,
	}, nil
}

// markUsed records the use without making the caller wait for it.
//
// Off the request path on purpose: last_used_at is a row lock plus a WAL write, and doing it
// synchronously serialises the requests of a single script against its own bookkeeping — the
// busiest token would be the slowest one. The write itself is coarse (the query only touches
// rows untouched for five minutes), so the goroutines are rare in practice.
//
// context.WithoutCancel is not available here on purpose either: the request context is about
// to be cancelled when the response is written, and inheriting it would cancel most of these
// writes. A fresh context with a short timeout is what makes the write independent of the
// request that triggered it.
func (a *TokenAuthenticator) markUsed(tokenID string) {
	a.inflight.Add(1)
	go func() {
		defer a.inflight.Done()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := a.tokens.MarkTokenUsed(ctx, tokenID); err != nil {
			// Not the caller's problem: the request has been authenticated and answered.
			log.Debug().Err(err).Str("tokenID", tokenID).Msg("cannot record token use")
		}
	}()
}
