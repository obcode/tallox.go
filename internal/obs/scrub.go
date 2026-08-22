// Package obs is the observability layer of tallox.go: it reports errors to a
// Sentry-compatible backend (GlitchTip) and owns the scrubber that decides what
// may leave this host.
//
// The scrubber is the security-relevant part. Read this file before adding
// anything that sends.
//
// It is the counterpart of plexams.go's obs package and deliberately keeps its
// shape, so that a fix found in one repository reads the same in the other. The
// three places it differs are marked; each is a difference in what tallox holds,
// not a difference of opinion.
package obs

import (
	"net/url"
	"regexp"
	"strings"

	sentry "github.com/getsentry/sentry-go"
)

// zerologLogger is the value sentryzerolog stamps on every event it builds. It
// is how the scrubber tells a log line apart from an event captured elsewhere:
// only the former needs the caller fingerprint, because only the former has a
// useless stack trace.
const zerologLogger = "zerolog"

// SkipField set on a zerolog line drops that line from the error report while
// leaving it in the local log:
//
//	log.Error().Err(err).Bool(obs.SkipField, true).Msg("...")
//
// Reach for sentry.ignoreerrors in the configuration first — this one needs a
// deploy. It exists for the single call site that is known noise.
const SkipField = "sentry_skip"

// allowedTags is a POSITIVE list, and it is the whole point of this file.
//
// sentryzerolog turns EVERY unknown zerolog field into a Sentry tag, unfiltered.
// A deny list would have to be kept in step with every log line anyone ever
// writes; this direction is safe by itself — a field nobody thought about is
// dropped, not sent.
//
// tallox holds the teaching load and the wishes of named colleagues. Today not
// one Error-level line carries a person (checked 2026-08-22: all ten sites log
// Err(err) and a message, nothing else), so this list starts from what an error
// could usefully say rather than from what already exists.
//
// Deliberately NOT here: email, name, person, personID, user, lecturer, token,
// tokenID — and `wish`, because a wish IS the sensitive object in this
// application, not a harmless identifier. Add a key only after checking that it
// cannot carry a colleague's identity or their stated preference.
var allowedTags = map[string]bool{
	"caller":    true, // the fingerprint key, and the join key back to the logs
	"semester":  true,
	"module":    true,
	"programme": true,
	"phase":     true,
	"role":      true, // the ROLE, never who holds it
	"kind":      true,
	"source":    true,
	"operation": true, // GraphQL operation NAME, never its arguments
	"field":     true,
	"addr":      true,
	"host":      true,
	"status":    true,
	"applied":   true,
	"runs":      true,
}

// allowedHeaders are the request headers worth keeping. Same direction as the
// tags, and for a sharper reason: X-Remote-User carries the logged-in person's
// mail address, and Cookie carries their session.
var allowedHeaders = map[string]bool{
	"User-Agent":   true,
	"Content-Type": true,
	"Accept":       true,
}

// allowedContexts are the SDK's own runtime contexts. Nothing in tallox sets a
// context, so anything else appearing here is unaccounted for and goes.
var allowedContexts = map[string]bool{
	"device":  true,
	"os":      true,
	"runtime": true,
	"trace":   true,
}

// reEmail matches the one personal identifier that turns up in free text — a
// message, an error string, a URL — where no allow list can help.
//
// DIFFERENCE FROM plexams: no Matrikelnummer pattern. plexams redacts runs of
// 7 to 10 digits because it handles student registrations; tallox has no
// Matrikelnummern at all, and the same rule here would only eat epoch
// milliseconds and durations out of otherwise readable messages.
//
// Names cannot be matched by a pattern. That is why they are handled the other
// way round, by allowedTags, and why this is the second line of defence rather
// than the first.
var reEmail = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}`)

func redact(s string) string {
	if s == "" {
		return s
	}
	return reEmail.ReplaceAllString(s, "[email]")
}

// scrubber is the BeforeSend hook. Its zero value is the safe configuration, so
// a test can use scrubber{} and get production behaviour.
type scrubber struct{}

// scrub is what every event passes through on its way out. It is fail-closed
// throughout: it copies the few things that are allowed into fresh containers
// rather than deleting the things it knows are bad, so a field, header or
// context nobody anticipated is dropped by default.
func (s scrubber) scrub(event *sentry.Event, _ *sentry.EventHint) *sentry.Event {
	if event == nil {
		return nil
	}

	// The emergency exit, honoured before anything else.
	if _, skip := event.Tags[SkipField]; skip {
		return nil
	}

	// Group log lines by their call site. sentryzerolog builds the stack trace
	// INSIDE its Write method, so the top frames are identical zerolog internals
	// for every log site and the default grouping would fold unrelated failures
	// into one issue. The caller field points at the failing line instead.
	//
	// Events captured elsewhere keep the default grouping — their stacks are real.
	if caller := event.Tags["caller"]; caller != "" && event.Logger == zerologLogger {
		event.Fingerprint = []string{caller}
	}

	event.Tags = allowMap(event.Tags, allowedTags)
	event.Message = redact(event.Message)
	event.Transaction = redact(event.Transaction)

	for i := range event.Exception {
		event.Exception[i].Type = redact(event.Exception[i].Type)
		event.Exception[i].Value = redact(event.Exception[i].Value)
	}

	for _, b := range event.Breadcrumbs {
		if b == nil {
			continue
		}
		b.Message = redact(b.Message)
		b.Data = allowData(b.Data)
	}

	// Rebuild rather than prune: query string, POST body, cookies and the CGI
	// environment all go, and the headers survive only by name.
	if r := event.Request; r != nil {
		event.Request = &sentry.Request{
			Method:  r.Method,
			URL:     redactURL(r.URL),
			Headers: allowMap(r.Headers, allowedHeaders),
		}
	}

	event.Contexts = allowContexts(event.Contexts)

	// No user is ever attached. DIFFERENCE FROM plexams: that one keys a stable
	// pseudonym with its existing secrets.key, which buys "3 people affected"
	// without sending an address. tallox has no such key, and inventing a
	// hashing scheme to carry identity out of this host is not a thing to do in
	// passing. Until there is a key and a decision, nobody is named.
	event.User = sentry.User{}

	return event
}

func allowMap(in map[string]string, allowed map[string]bool) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		if allowed[k] {
			out[k] = redact(v)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func allowData(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		if !allowedTags[k] {
			continue
		}
		if s, ok := v.(string); ok {
			out[k] = redact(s)
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func allowContexts(in map[string]sentry.Context) map[string]sentry.Context {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]sentry.Context, len(in))
	for k, v := range in {
		if allowedContexts[k] {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// redactURL keeps scheme, host and path and throws the query away wholesale —
// a filter parameter is exactly where a name would show up.
func redactURL(raw string) string {
	if raw == "" {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		// Unparsable: fall back to cutting at the first '?' and redacting the
		// rest, rather than passing it through.
		if i := strings.IndexByte(raw, '?'); i >= 0 {
			return redact(raw[:i])
		}
		return redact(raw)
	}
	u.RawQuery = ""
	u.Fragment = ""
	u.User = nil
	return redact(u.String())
}

// allowedHeaderTerms is allowedHeaders as the SDK wants it, kept derived so the
// two cannot drift apart.
func allowedHeaderTerms() []string {
	terms := make([]string, 0, len(allowedHeaders))
	for h := range allowedHeaders {
		terms = append(terms, h)
	}
	return terms
}
