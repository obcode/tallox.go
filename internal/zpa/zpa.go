// Package zpa reads the module master data from the central examination office's REST
// interface.
//
// The first outbound HTTP connection in this system, and the design is shaped by that: it does
// as little as possible with what comes back. Each object is stored whole, and exactly one
// scalar is read out of it — the object's id — plus a best-effort label. Nothing else is
// parsed, typed or interpreted.
//
// That is not laziness, it is the property that makes the rest work. The interface's shape has
// already changed once; a client that mapped fields would need a migration every time it
// changes again, for a cache of a database this faculty neither owns nor influences. It also
// means the fixtures in this public repository can be invented rather than recorded, which
// matters because the real module objects carry the mail addresses of the colleagues
// responsible for them.
//
// Authentication is `Authorization: Token <value>`, not Bearer. The interface is Django REST
// Framework, which answers a missing credential with `WWW-Authenticate: Token`.
package zpa

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/obcode/tallox.go/internal/domain"
)

// The failure modes, as sentinels, so that the caller can decide without reading prose.
var (
	// ErrNotAuthorised is a 401 or 403. Never retried — a wrong token tried three times per
	// kind is twelve failed authentications against another institution's system, which is a
	// way to become a topic in somebody else's incident review.
	ErrNotAuthorised = errors.New("zpa refused the credential")
	// ErrUnknownEndpoint is a 404: the path moved, or the base address is wrong.
	ErrUnknownEndpoint = errors.New("zpa does not serve this endpoint")
	// ErrUnavailable is a 5xx or a transport failure. The only class that is retried.
	ErrUnavailable = errors.New("zpa is not answering")
	// ErrUnexpectedStatus is everything else, kept distinct so it cannot be mistaken for one of
	// the classes above and quietly retried or quietly not.
	ErrUnexpectedStatus = errors.New("zpa answered with an unexpected status")
	// ErrNotJSON is a 200 whose body is not JSON.
	//
	// The single most important check here. An SSO interstitial or a proxy error page served
	// with 200 would otherwise be hashed and stored as a payload, and against a content-hash
	// cache that looks exactly like "everything in the catalogue changed last night". Silent
	// corruption is the failure this integration can actually have.
	ErrNotJSON = errors.New("zpa answered with something that is not json")
	// ErrEmptyResult is a successful fetch that returned no objects.
	//
	// Treated as a failure because the sync marks anything absent from a successful fetch as
	// gone. One bad night would otherwise retire the entire catalogue.
	ErrEmptyResult = errors.New("zpa returned no objects")
	// ErrNoObjectID is a payload with no recognisable id field.
	ErrNoObjectID = errors.New("zpa object has no id field")
	// ErrTooLarge is a body over the cap. A refusal, never a truncation: half a JSON document
	// is not a smaller JSON document.
	ErrTooLarge = errors.New("zpa response is too large")
)

// endpoints maps the internal vocabulary to the paths. The only place in the program that
// knows what these are called on the wire.
var endpoints = map[domain.ZPAKind]string{
	domain.ZPAKindModule: "rest/module_info",
	domain.ZPAKindBasket: "rest/basket_info",
	domain.ZPAKindMSBA:   "rest/msba_info",
	domain.ZPAKindSPO:    "rest/spo_info",
}

// idFields are the candidate names of the id field, per kind, in the order they are tried.
//
// Today every endpoint answers with `<kind>_id` and the first candidate always hits. The list
// exists anyway, and so does the error below it, because the shape has changed once already:
// when it changes again the failure has to name what it found rather than silently store zeros.
var idFields = map[domain.ZPAKind][]string{
	domain.ZPAKindModule: {"module_id", "id", "pk"},
	domain.ZPAKindBasket: {"basket_id", "id", "pk"},
	domain.ZPAKindMSBA:   {"msba_id", "id", "pk"},
	domain.ZPAKindSPO:    {"spo_id", "id", "pk"},
}

// labelFields are the candidates for a human-readable name, per kind.
//
// Module has none, and that is not an oversight here: the module objects genuinely carry no
// name field — the module's name exists only inside the nested object of an association row.
// A module therefore gets an empty label until that is fixed at the source, and the interface
// falls back to showing the kind and the id.
var labelFields = map[domain.ZPAKind][]string{
	domain.ZPAKindModule: {},
	domain.ZPAKindBasket: {"basket", "name"},
	domain.ZPAKindMSBA:   {"module_code", "name"},
	domain.ZPAKindSPO:    {"primuss_id", "version", "name"},
}

const (
	// maxBody caps what will be read. The largest real response measured about 1.4 MB, so this
	// is roughly twenty times headroom — large enough that growth does not trip it, small
	// enough that a proxy streaming something unbounded does not become this process's memory
	// problem.
	maxBody = 32 << 20
	// perFetchTimeout bounds one endpoint. The slowest real fetch took about 8.5 seconds.
	perFetchTimeout = 45 * time.Second
	// attempts is the total number of tries for one retryable fetch, not the number of retries.
	attempts = 3
)

// Client fetches from one ZPA installation.
type Client struct {
	baseURL string
	token   string
	http    *http.Client

	// sleep is injectable so the retry test does not actually wait. Same shape as
	// auth.Config.Now, and for the same reason: a test that sleeps is a test somebody
	// eventually deletes.
	sleep func(context.Context, time.Duration) error
}

// Config is what the client needs. Both values are required; bootstrap has already refused to
// start if only one of them is set.
type Config struct {
	BaseURL string
	Token   string
}

// New builds a client.
func New(cfg Config) (*Client, error) {
	if cfg.BaseURL == "" || cfg.Token == "" {
		return nil, errors.New("zpa: both a base url and a token are required")
	}
	if _, err := url.Parse(cfg.BaseURL); err != nil {
		return nil, fmt.Errorf("zpa: base url: %w", err)
	}

	return &Client{
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		token:   cfg.Token,
		http: &http.Client{
			// Two layers, because they fail differently. ResponseHeaderTimeout catches a server
			// that accepts the connection and then says nothing, which is the hang that looks
			// like a working system. Timeout is the backstop that also covers reading the body.
			Transport: &http.Transport{
				ResponseHeaderTimeout: 15 * time.Second,
				IdleConnTimeout:       30 * time.Second,
			},
			Timeout: 60 * time.Second,
			// Redirects are not followed. A base address with a trailing slash produces a
			// doubled separator that many proxies answer with a redirect — to a URL that may
			// drop the Authorization header. Failing is the honest outcome; the configuration
			// trims the slash so this should never fire.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		sleep: sleepContext,
	}, nil
}

var _ domain.ZPASource = (*Client)(nil)

// Fetch returns every object of one kind.
func (c *Client) Fetch(ctx context.Context, kind domain.ZPAKind) ([]domain.ZPAObject, error) {
	path, known := endpoints[kind]
	if !known {
		return nil, fmt.Errorf("zpa: %w: %s", ErrUnknownEndpoint, kind)
	}

	body, err := c.getWithRetry(ctx, c.baseURL+"/"+path)
	if err != nil {
		return nil, fmt.Errorf("zpa: fetching %s: %w", kind, err)
	}

	objects, err := decode(kind, body)
	if err != nil {
		return nil, fmt.Errorf("zpa: reading %s: %w", kind, err)
	}
	if len(objects) == 0 {
		return nil, fmt.Errorf("zpa: reading %s: %w", kind, ErrEmptyResult)
	}
	return objects, nil
}

// getWithRetry runs one request, retrying only what is worth retrying.
func (c *Client) getWithRetry(ctx context.Context, target string) ([]byte, error) {
	var lastErr error
	for attempt := range attempts {
		if attempt > 0 {
			// 1s, 4s, with jitter. Bounded and short: this runs from a nightly job, and a
			// client that keeps trying for minutes turns one unavailable night into a long one.
			backoff := time.Duration(1<<(2*(attempt-1))) * time.Second
			jitter := time.Duration(rand.Int64N(int64(backoff / 4)))
			if err := c.sleep(ctx, backoff+jitter); err != nil {
				return nil, err
			}
		}

		body, err := c.get(ctx, target)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if !errors.Is(err, ErrUnavailable) {
			// Everything else is a decision that will not change by asking again: a refused
			// credential, a wrong path, a body that is not JSON.
			return nil, err
		}
	}
	return nil, lastErr
}

func (c *Client) get(ctx context.Context, target string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, perFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("cannot build the request: %w", err)
	}
	// Token, not Bearer. See the package doc.
	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		// A transport failure is not distinguishable from an outage from here, and both are
		// worth one more try. The error is wrapped rather than replaced so a context deadline
		// stays visible in the log.
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()

	contentType := resp.Header.Get("Content-Type")

	if resp.StatusCode != http.StatusOK {
		// Status first, body never. Outside the VPN this is an Apache error page in HTML, and
		// an HTML document inside a log line is unreadable — and a proxy's error page can carry
		// hostnames. Only the shape of the answer goes into the message.
		return nil, statusError(resp.StatusCode, contentType)
	}

	if !isJSON(contentType) {
		return nil, fmt.Errorf("%w: content-type %q", ErrNotJSON, contentType)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, fmt.Errorf("%w: cannot read the body: %w", ErrUnavailable, err)
	}
	if len(body) > maxBody {
		return nil, fmt.Errorf("%w: over %d bytes", ErrTooLarge, maxBody)
	}
	return body, nil
}

func statusError(status int, contentType string) error {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		// The two are one class on purpose, but the content type is worth carrying: an HTML
		// refusal means the request never reached the application (the network is wrong), a
		// JSON one means it did and the credential is wrong. That single distinction is most of
		// the diagnosis.
		return fmt.Errorf("%w: status %d, content-type %q", ErrNotAuthorised, status, contentType)
	case status == http.StatusNotFound:
		return fmt.Errorf("%w: status %d", ErrUnknownEndpoint, status)
	case status >= 500:
		return fmt.Errorf("%w: status %d", ErrUnavailable, status)
	default:
		return fmt.Errorf("%w: status %d", ErrUnexpectedStatus, status)
	}
}

func isJSON(contentType string) bool {
	mediaType, _, _ := strings.Cut(contentType, ";")
	return strings.EqualFold(strings.TrimSpace(mediaType), "application/json")
}

// decode turns a response body into objects, reading only the id and the label.
func decode(kind domain.ZPAKind, body []byte) ([]domain.ZPAObject, error) {
	var raw []json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		// The top level is an array in every endpoint today. If that ever becomes an envelope,
		// this is the message that says so — with what was found and not with a stack trace.
		return nil, fmt.Errorf("%w: expected an array at the top level: %w", ErrNotJSON, err)
	}

	objects := make([]domain.ZPAObject, 0, len(raw))
	for i, item := range raw {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(item, &fields); err != nil {
			return nil, fmt.Errorf("%w: element %d is not an object: %w", ErrNotJSON, i, err)
		}

		id, err := objectID(kind, fields)
		if err != nil {
			return nil, fmt.Errorf("element %d: %w", i, err)
		}

		objects = append(objects, domain.ZPAObject{
			ZpaID:   id,
			Payload: item,
			Label:   objectLabel(kind, fields),
		})
	}
	return objects, nil
}

func objectID(kind domain.ZPAKind, fields map[string]json.RawMessage) (int64, error) {
	for _, candidate := range idFields[kind] {
		raw, present := fields[candidate]
		if !present {
			continue
		}
		if id, ok := asInt64(raw); ok {
			return id, nil
		}
	}
	// Key names, never values. The names are schema and safe to log or paste into a mail to
	// the maintainer; the values are data from another institution's system. This message is
	// the instrument that answers the shape question on the first run after their next change.
	return 0, fmt.Errorf("%w (%s): saw %s", ErrNoObjectID, kind, strings.Join(sortedKeys(fields), ", "))
}

// asInt64 accepts a JSON number or a string holding one.
//
// Both, because the interface types nothing: ids, credit counts and even booleans arrive as
// strings, and `active` is the Python word "True". Refusing a numeric string here would mean
// refusing every object they publish today.
func asInt64(raw json.RawMessage) (int64, bool) {
	var asNumber int64
	if err := json.Unmarshal(raw, &asNumber); err == nil {
		return asNumber, true
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err != nil {
		return 0, false
	}
	n, err := strconv.ParseInt(strings.TrimSpace(asString), 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

func objectLabel(kind domain.ZPAKind, fields map[string]json.RawMessage) string {
	for _, candidate := range labelFields[kind] {
		raw, present := fields[candidate]
		if !present {
			continue
		}
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			continue
		}
		// "None" is a Python None that came through as text — three module rows carry it in
		// their home programme, and it is a null wearing the costume of a value.
		if s = strings.TrimSpace(s); s != "" && s != "None" {
			return s
		}
	}
	return ""
}

func sortedKeys(fields map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
