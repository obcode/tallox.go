package principal

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// Where a request came from, carried through the context beside the actor.
//
// Here rather than on Actor, because it is a fact about the request and not about who is
// making it: an anonymous caller and a refused sign-in both have one, and neither has an
// actor worth speaking of. And here rather than in internal/auth, because two packages that
// may not import each other need to read it — the authentication middleware, which records
// refusals, and the GraphQL layer, which records everything else.
//
// No rule reads it. It exists for the access log, where it answers the one question a stolen
// credential can be recognised by.

type sourceKey struct{}

// WithSource returns a copy of ctx carrying the address the request came from.
func WithSource(ctx context.Context, addr netip.Addr) context.Context {
	return context.WithValue(ctx, sourceKey{}, addr)
}

// SourceFrom returns the address in the context, or the zero Addr when there is none.
func SourceFrom(ctx context.Context) netip.Addr {
	addr, _ := ctx.Value(sourceKey{}).(netip.Addr)
	return addr
}

// SourceOf reads the client address off a request.
//
// # The one-hop assumption
//
// In production there is exactly one proxy in front of this server: Caddy proxies /query and
// /api/graphql straight to the container. (forward_auth is a subrequest and does not add a
// hop.) Caddy appends the peer it saw to X-Forwarded-For, so the LAST entry of that header is
// the actual client and everything before it is whatever the client chose to send.
//
// Hence: last entry, not first. The first entry is the one an ordinary "trust the header"
// reading would take, and it is the one a caller can write themselves — which in an audit log
// means an attacker choosing what it says about them.
//
// If a second proxy is ever put in front, this reads that proxy instead of the client, and the
// place to fix it is here together with deploy/Caddyfile. That is a wrong-but-consistent answer
// rather than a forgeable one, which is the right way round for this column.
//
// Without the header — every local run, every test — RemoteAddr is used, which is then the real
// peer anyway.
func SourceOf(r *http.Request) netip.Addr {
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		parts := strings.Split(forwarded, ",")
		last := strings.TrimSpace(parts[len(parts)-1])
		if addr, err := netip.ParseAddr(last); err == nil {
			return addr
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// Not host:port. httptest and a unix socket both produce this; there is nothing to
		// record and nothing to report.
		host = r.RemoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	return addr
}
