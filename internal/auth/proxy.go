package auth

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
)

// HeaderRemoteUser is the identity header the auth proxy sets.
//
// Caddy strips whatever the client sent before oauth2-proxy fills it in, on every branch —
// see deploy/. Reading it here is therefore trusting the reverse proxy, which is the same
// thing as trusting that this server is not reachable directly. That is a deployment
// property, not something this package can check.
const HeaderRemoteUser = "X-Remote-User"

// HeaderAssumeRoles is how a signed-in person asks to be judged by fewer roles than they
// hold — the "look at this as a lecturer" feature.
//
// Unlike HeaderRemoteUser this one is *not* set by the proxy and is not trusted. It does not
// have to be: policy.Narrow intersects the selection with the grants the person actually
// holds, so the worst a hand-written header can do is take privileges away from whoever sent
// it. That is the reason it may travel as an ordinary header at all, and the reason Caddy
// does not need a rule for it.
//
// Browser door only. A Personal Access Token already carries exactly its owner's roles, and
// narrowing what a script may do is what scopes are for — adding a second, header-shaped way
// to do it would mean two mechanisms answering one question.
const HeaderAssumeRoles = "X-Tallox-Assume-Roles"

// ProxyAuthenticator is the browser door: identity arrives as a header, the account and its
// roles come from the database.
//
// It deliberately does not read X-Remote-Displayname or any other header the proxy might
// offer. The name shown in the GUI is the one in the person row, so that renaming somebody is
// an edit in one place rather than a fact that depends on which identity provider answered.
type ProxyAuthenticator struct {
	users   UserLookup
	mode    Mode
	devUser string
}

// NewProxyAuthenticator builds the browser-door authenticator and, in dev mode, says so
// loudly.
func NewProxyAuthenticator(cfg Config) *ProxyAuthenticator {
	if cfg.Mode == ModeDev {
		// At Warn, every start, with the mail address in it. A server that quietly hands out
		// an administrator to whoever connects is a thing that has to be impossible to
		// overlook in a log — including in the log of a machine somebody meant to configure
		// as production.
		log.Warn().
			Str("devUser", devUserOr(cfg.DevUser)).
			Msg("auth.mode=dev: the browser door hands out a development user with every " +
				"role and no login. The token door stays real.")
	}
	return &ProxyAuthenticator{
		users:   cfg.Users,
		mode:    cfg.Mode,
		devUser: devUserOr(cfg.DevUser),
	}
}

// Door names the mount for log lines.
func (*ProxyAuthenticator) Door() string { return "browser" }

// Authenticate resolves the header into an actor.
func (a *ProxyAuthenticator) Authenticate(ctx context.Context, r *http.Request) (principal.Actor, error) {
	mail := strings.TrimSpace(r.Header.Get(HeaderRemoteUser))

	if mail == "" {
		if a.mode == ModeDev {
			return narrowIfRequested(a.developmentActor(), r), nil
		}
		// No header. In production that is the login page, the health check, or a request
		// that reached the server without passing the proxy — none of which is an error here:
		// the anonymous actor may read exactly the fields that are meant to answer without a
		// session.
		return principal.Anonymous, ErrNoCredential
	}

	// A header IS present, including in dev mode: sending X-Remote-User by hand is how a
	// developer looks at the application as a Dozent rather than as the development user, and
	// the answer has to come from the real lookup or it proves nothing.
	if a.users == nil {
		return principal.Anonymous, fmt.Errorf("%w: no user lookup configured", ErrUnavailable)
	}

	person, err := a.users.PersonByMail(ctx, mail)
	if err != nil {
		return principal.Anonymous, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	if person == nil {
		// Somebody the identity provider knows and this installation does not — a new
		// colleague before the import ran. A refusal that says so is worth more than a blank
		// page, because the fix is an import and not a login.
		return principal.Anonymous, fmt.Errorf("%w: %s", ErrUnknownUser, mail)
	}
	if !person.Active {
		return principal.Anonymous, fmt.Errorf("%w: %s", ErrInactiveUser, mail)
	}

	return narrowIfRequested(principal.Actor{
		ID:         person.ID,
		Mail:       person.Mail,
		Name:       person.Name,
		Roles:      person.Roles,
		RoleScopes: person.RoleScopes,
		Kind:       principal.KindInteractive,
	}, r), nil
}

// narrowIfRequested applies HeaderAssumeRoles, if it is there.
//
// Absent header and present-but-empty header are deliberately different. Absent means "judge
// me normally". Empty means "judge me as somebody with no grants at all", which is a view
// worth being able to look at — it is what a colleague sees on the day the import has created
// their person row and nobody has given them anything yet, and that page is one somebody
// should have seen before it happens.
//
// Everything the header can do is bounded by policy.Narrow, which intersects. There is
// therefore no validation to perform here and no refusal to write: a garbled value narrows to
// fewer roles, never to more.
func narrowIfRequested(a principal.Actor, r *http.Request) principal.Actor {
	values := r.Header.Values(HeaderAssumeRoles)
	if len(values) == 0 {
		return a
	}

	var selected []string
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			if part = strings.TrimSpace(part); part != "" {
				selected = append(selected, part)
			}
		}
	}

	return policy.Narrow(a, policy.ParseRoles(selected))
}

// DefaultDevUser is the mail address of the injected development user.
//
// Under example.org, which RFC 2606 reserves, for the same reason the test fixtures are: a
// value that escapes into a mail field must not be able to reach anybody.
const DefaultDevUser = "dev@example.org"

func devUserOr(mail string) string {
	if strings.TrimSpace(mail) == "" {
		return DefaultDevUser
	}
	return mail
}

// developmentActor is the synthetic user of auth.mode=dev.
//
// It holds every role, which is a trade with its eyes open. The alternative — ADMIN only —
// leaves a developer looking at a GUI that shows almost nothing, and the repair people reach
// for in that situation is to widen what ADMIN may see, which corrupts the rule the project
// rests on. Granting everything locally keeps that pressure off the policy.
//
// The cost is that role gating is invisible in dev mode. The escape hatch is the header: send
// X-Remote-User for a seeded person and the ordinary lookup applies, roles and all.
//
// Its id is derived from the mail address, exactly like a fixture persona, so that it is
// stable across restarts and — crucially — never uuid.Nil, the value principal.Actor.Owns
// refuses to match.
func (a *ProxyAuthenticator) developmentActor() principal.Actor {
	roles := make([]string, 0, len(policy.AllRoles()))
	for _, r := range policy.AllRoles() {
		roles = append(roles, string(r))
	}

	return principal.Actor{
		ID:    uuid.NewSHA1(uuid.NameSpaceURL, []byte("mailto:"+a.devUser)),
		Mail:  a.devUser,
		Name:  "Development User",
		Roles: roles,
		Kind:  principal.KindInteractive,
	}
}
