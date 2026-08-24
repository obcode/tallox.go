// Package auth turns a request into a principal.Actor. Two doors, one middleware.
//
//	/query, /download/*   X-Remote-User, set by the auth proxy in front of this server
//	/api/graphql          Authorization: Bearer tallox_…, verified here
//
// Authentication is all this package does. It decides *who* is calling and never *what they
// may do* — that lives in internal/policy, so that the answer cannot depend on which door the
// request came through. The one thing the doors leave behind is principal.Kind, which the few
// rules that genuinely differ (a long-lived token is not an audited browser session) can ask
// about explicitly.
//
// # Why the browser door does not authenticate
//
// In production Caddy runs forward_auth against oauth2-proxy, which does the OIDC dance with
// sso.hm.edu, and only then passes the request on with an X-Remote-User header it sets
// authoritatively — stripping whatever the client sent. Trusting that header is therefore
// trusting the reverse proxy, which is the same thing as trusting that the server is not
// reachable directly. That is a deployment property, and it is asserted in deploy/ rather
// than here.
//
// The token door is different: nothing in front of it verifies anything, so this package does.
//
// # Errors are answers, not surprises
//
// A request with no credential is not an error. It produces the anonymous actor and carries
// on, because buildInfo has to answer before a session exists — the GUI footer renders on the
// login page, and a smoke test that fails on authorization cannot tell a broken proxy from a
// broken server.
//
// A request with a credential that does not work is a 401 and stops there. Silently degrading
// it to anonymous would turn "your token expired" into "this field is empty", which is the
// kind of failure people debug for an afternoon.
//
// A lookup that fails because the database is unreachable is a 503, never a 401. Telling a
// script its token is invalid because Postgres is restarting is how a colleague ends up
// rotating a token that was never broken.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/obcode/tallox.go/internal/principal"
)

// Mode is how this server authenticates. Deliberately not a boolean pair: "dev" and
// "off-token" are different questions, and a bool for each would allow the two combinations
// nobody has thought about.
type Mode string

const (
	// ModeDev injects a development user on the browser door — but leaves the token door
	// real. That asymmetry is the whole point: the production credential path is exercised
	// every day by whoever is writing against it, instead of being discovered in October.
	ModeDev Mode = "dev"
	// ModeProxy is production: identity comes from the header the auth proxy sets.
	ModeProxy Mode = "proxy"
	// ModeOffToken serves the browser door only and does not mount /api/graphql at all.
	//
	// The emergency stop. If a token ever leaks and revoking it individually is not fast
	// enough, this switches off the entire machine-facing surface with a restart and no
	// redeploy — and because the mount is absent rather than guarded, there is no code path
	// left that could be wrong about it.
	ModeOffToken Mode = "off-token"
)

// ParseMode validates a configured mode.
func ParseMode(s string) (Mode, error) {
	switch Mode(s) {
	case ModeDev, ModeProxy, ModeOffToken:
		return Mode(s), nil
	default:
		return "", fmt.Errorf("auth mode %q is not one of %s, %s, %s",
			s, ModeDev, ModeProxy, ModeOffToken)
	}
}

// TokenDoorEnabled reports whether /api/graphql should be mounted.
func (m Mode) TokenDoorEnabled() bool { return m != ModeOffToken }

// Person is what a lookup returns about a human being: enough to build an actor, and nothing
// else. No teaching load, no notes — those are personnel data and they have no business travelling
// through the authentication path.
type Person struct {
	ID     uuid.UUID
	Mail   string
	Name   string
	Active bool
	Roles  []string
	// RoleScopes narrow individual grants to individual things — which study programme a
	// programme lead leads. Carried here because authentication is the one place that reads a
	// person's grants, and a second lookup later would be a second place to forget.
	RoleScopes []principal.RoleScope
}

// Token is a Personal Access Token as stored, with its owner.
//
// Expired and revoked tokens are returned rather than filtered away, so that this package can
// say *why* a token stopped working. The caller owns the token; telling them "expired" costs
// nothing and saves an afternoon.
type Token struct {
	ID         string
	SecretHash []byte
	Scopes     []string
	ExpiresAt  time.Time
	// RevokedAt is the zero time for a token that has not been revoked.
	RevokedAt time.Time
	Owner     Person
}

// UserLookup resolves the identity the auth proxy asserts.
//
// A hand-written interface with one method rather than a mock library: the seam is this
// narrow because everything else about a person belongs to layers that authentication has no
// reason to reach. "Not found" is (nil, nil) — an error means the lookup itself failed, which
// is a 503 and not a 401.
type UserLookup interface {
	PersonByMail(ctx context.Context, mail string) (*Person, error)
}

// TokenLookup resolves a Personal Access Token by its public id, and records use.
type TokenLookup interface {
	TokenByID(ctx context.Context, tokenID string) (*Token, error)
	// MarkTokenUsed updates last_used_at, coarsely. Called off the request path — see
	// TokenAuthenticator.
	MarkTokenUsed(ctx context.Context, tokenID string) error
}

// Config is everything the authenticators need.
type Config struct {
	// Mode is dev, proxy or off-token.
	Mode Mode
	// Users resolves proxy identities. Required unless Mode is dev and no header ever arrives.
	Users UserLookup
	// Tokens resolves Personal Access Tokens. Required whenever the token door is mounted.
	Tokens TokenLookup
	// DevUser is the mail address the injected development user carries. Only read in dev
	// mode.
	DevUser string
	// Now is the clock, injectable so that expiry has a test that does not sleep.
	Now func() time.Time
}

func (c Config) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Authenticator turns a request into an actor. One implementation per door.
type Authenticator interface {
	// Authenticate returns the actor, or an error explaining why this request has no valid
	// identity. ErrNoCredential means "nobody presented anything", which is not a failure.
	Authenticate(ctx context.Context, r *http.Request) (principal.Actor, error)
	// Door names the mount, for log lines.
	Door() string
	// Kind is which door this is, in the vocabulary the rules and the access log use.
	//
	// Beside Door() rather than derived from it: one is a word for a human reading a log line
	// and the other is a value other code branches on, and deriving the second from the first
	// would make renaming the log line a behaviour change.
	Kind() principal.Kind
	// Asserted is what the request claimed to be, without any lookup — the address on the
	// browser door, the token's public half on the token door.
	//
	// Only the refusal path uses it, and it exists because that path has to record WHO was
	// turned away. Reading it off the request rather than out of the error is the point: the
	// address is in the error's message today, and an audit trail that works by parsing a
	// sentence stops working the first time somebody rewords it.
	Asserted(r *http.Request) Asserted
}

// The reasons authentication can decline. Each maps to a status and a message in Middleware.
var (
	// ErrNoCredential: the request carries no identity at all. Handled as anonymous.
	ErrNoCredential = errors.New("no credential presented")
	// ErrUnknownUser: the proxy asserted somebody this installation has never heard of.
	ErrUnknownUser = errors.New("no account for this identity")
	// ErrInactiveUser: the account exists but has been deactivated. Deactivating a person is
	// how a leaver loses everything at once, tokens included, so this is checked on both
	// doors.
	ErrInactiveUser = errors.New("account is deactivated")
	// ErrMalformedToken: the Authorization header is not a Tallox token.
	ErrMalformedToken = errors.New("token is malformed")
	// ErrInvalidToken: no such token, or the secret does not match. Deliberately one error
	// for both — the difference is exactly what an attacker would like to learn.
	ErrInvalidToken = errors.New("token is invalid")
	// ErrExpiredToken: the token was valid and has run out.
	ErrExpiredToken = errors.New("token has expired")
	// ErrRevokedToken: the token was withdrawn.
	ErrRevokedToken = errors.New("token has been revoked")
	// ErrUnavailable: the lookup itself failed. Never a 401.
	ErrUnavailable = errors.New("cannot verify credentials right now")
)

// Middleware authenticates a request and puts the actor in its context.
//
// One middleware for both doors, parameterised by the authenticator, so the handling of a
// refusal — status, body shape, what gets logged — cannot drift between them. That drift is
// the realistic failure here: somebody improves the message on the path they are working on.
func Middleware(a Authenticator, recorder AccessRecorder) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Before authentication, and on every branch: the GraphQL layer records the
			// address for operations that succeed, this one records it for sign-ins that do
			// not, and neither should have to know how the other gets it.
			ctx := principal.WithSource(r.Context(), principal.SourceOf(r))
			r = r.WithContext(ctx)

			actor, err := a.Authenticate(ctx, r)

			switch {
			case errors.Is(err, ErrNoCredential):
				// Anonymous, explicitly. Putting the actor in the context even when it is
				// nobody means a handler can tell "the middleware ran and nobody is logged
				// in" from "this route has no authentication", which is a question that
				// otherwise gets answered by guessing.
				//
				// Not recorded as a refusal: nothing was refused. The request carries on and
				// the GraphQL layer records whatever it goes on to ask for.
				next.ServeHTTP(w, r.WithContext(
					principal.NewContext(ctx, principal.Anonymous)))

			case err != nil:
				status, message := refusal(err)
				log.WithLevel(severity(err)).
					Err(err).
					Str("door", a.Door()).
					Int("status", status).
					Msg("authentication declined")
				recordRefusal(ctx, recorder, a, r, err)
				writeGraphQLError(w, status, message)

			default:
				next.ServeHTTP(w, r.WithContext(principal.NewContext(ctx, actor)))
			}
		})
	}
}

// severity is how loudly a refusal is logged.
//
// The distinction that matters is who caused it and who can act on it.
//
// A failing lookup is an Error: nobody's credential is broken, the database is, and that is
// an operational event somebody has to see. It was the worst of the three to have at Debug —
// an outage that leaves no trace in the log at all.
//
// A refusal that names a real account or a real token — unknown identity, deactivated person,
// expired or revoked token — is Info. Those are the lines somebody greps for when a colleague
// says "it stopped working", and there is one per human event rather than one per request.
// Production runs at Info, so this is the level at which a wave of 401s is visible at all.
//
// A malformed or invalid token stays at Debug. Not because it is uninteresting, but because
// its volume is chosen by whoever is sending it: anybody who can reach the token door can
// produce these at any rate they like, and a log that can be flooded from outside is a log
// that gets ignored — or fills a disk. The owner of a real token that stopped working lands
// in one of the Info cases above.
func severity(err error) zerolog.Level {
	switch {
	case errors.Is(err, ErrUnavailable):
		return zerolog.ErrorLevel
	case errors.Is(err, ErrUnknownUser),
		errors.Is(err, ErrInactiveUser),
		errors.Is(err, ErrExpiredToken),
		errors.Is(err, ErrRevokedToken):
		return zerolog.InfoLevel
	default:
		return zerolog.DebugLevel
	}
}

// refusal maps a reason to a status and a message the caller reads.
//
// The messages are German: they are read by colleagues, in the GUI and in the JSON their own
// scripts print. They are also deliberately specific — "abgelaufen" rather than "ungültig" —
// wherever the caller already knows the secret, and deliberately vague where they do not: an
// unknown token id and a wrong secret produce the same sentence.
func refusal(err error) (int, string) {
	switch {
	case errors.Is(err, ErrUnavailable):
		// Not a 401. A script told "your token is invalid" while the database restarts is a
		// colleague rotating a credential that was never broken.
		return http.StatusServiceUnavailable,
			"Anmeldung derzeit nicht möglich. Bitte später erneut versuchen."
	case errors.Is(err, ErrUnknownUser):
		return http.StatusUnauthorized,
			"Für diese Kennung gibt es in Tallox kein Konto."
	case errors.Is(err, ErrInactiveUser):
		return http.StatusUnauthorized,
			"Dieses Konto ist deaktiviert."
	case errors.Is(err, ErrExpiredToken):
		return http.StatusUnauthorized,
			"Das Token ist abgelaufen. Bitte ein neues anlegen."
	case errors.Is(err, ErrRevokedToken):
		return http.StatusUnauthorized,
			"Das Token wurde widerrufen."
	case errors.Is(err, ErrMalformedToken):
		return http.StatusUnauthorized,
			"Das Token hat nicht das erwartete Format (tallox_…)."
	default:
		return http.StatusUnauthorized, "Token ungültig."
	}
}

// recordRefusal writes one refused sign-in to the access log.
//
// Best effort, like the operation entries the GraphQL layer writes: a request that was already
// being refused must not turn into a different failure because the log could not be written,
// and an installation whose database is down refuses everybody *and* cannot record it. Logged
// at Error so it reaches the reporter rather than being silence.
//
// Deliberately after the log line and before the response: the caller learns nothing about
// whether they were recorded, and nothing about how long the recording took.
func recordRefusal(ctx context.Context, recorder AccessRecorder, a Authenticator, r *http.Request, cause error) {
	if recorder == nil {
		return
	}

	asserted := a.Asserted(r)
	err := recorder.RecordRefusal(ctx, Refusal{
		Mail:    asserted.Mail,
		TokenID: asserted.TokenID,
		Door:    a.Kind(),
		Code:    RefusalCode(cause),
		Source:  principal.SourceFrom(ctx),
	})
	if err != nil {
		log.Error().Err(err).Str("door", a.Door()).Msg("cannot record the refused sign-in")
	}
}

// writeGraphQLError answers in the shape a GraphQL client expects.
//
// Both doors serve GraphQL, so a refusal that arrived as plain text or HTML would surface in
// the GUI as "unexpected token < in JSON" and in a script as a parse error — neither of which
// says "log in again". The status code carries the same information for anything that reads
// it, and the extensions code is what a client switches on.
func writeGraphQLError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	// Tell a browser that a challenge exists without naming a scheme it should perform: the
	// interactive login is the proxy's business, not this server's.
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", `Bearer realm="tallox"`)
	}
	w.WriteHeader(status)

	body := map[string]any{
		"data": nil,
		"errors": []map[string]any{{
			"message":    message,
			"extensions": map[string]any{"code": "UNAUTHENTICATED"},
		}},
	}
	if err := json.NewEncoder(w).Encode(body); err != nil {
		log.Error().Err(err).Msg("cannot write authentication error")
	}
}
