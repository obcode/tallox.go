package zpa_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/zpa"
)

// The fixtures here are invented, not recorded and scrubbed.
//
// Same rule as internal/testdata, and for a sharper reason: the real module objects carry the
// mail addresses of the colleagues responsible for them, and this repository is public.
// Scrubbing is a review step somebody skips exactly once.
//
// It costs nothing, because the client parses no shape — it reads an id and a label out of
// each object and keeps the rest opaque. The only thing a fixture has to be honest about is
// "an array of objects, each carrying an id", and for confidence against the real payloads
// there is TestTheRealFixturesParse below.

func TestEachKindIsFetchedAndItsIDRead(t *testing.T) {
	t.Parallel()

	cases := []struct {
		kind      domain.ZPAKind
		path      string
		wantCount int
		wantFirst int64
		wantLabel string
	}{
		{domain.ZPAKindSPO, "/rest/spo_info", 2, 801, "07-XX-2025"},
		{domain.ZPAKindBasket, "/rest/basket_info", 2, 701, "Pflichtmodule"},
		{domain.ZPAKindMSBA, "/rest/msba_info", 2, 301, "XX-B-0010"},
		// A module has no label: the module objects genuinely carry no name field — the name
		// exists only inside the nested object of an association row. Asserted rather than
		// worked around, so that the day it is fixed at the source, this test says so.
		{domain.ZPAKindModule, "/rest/module_info", 2, 501, ""},
	}

	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			t.Parallel()

			var gotAuth, gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
				serveFixture(t, w, filepath.Base(r.URL.Path)+".json")
			}))
			defer srv.Close()

			objects, err := newClient(t, srv.URL).Fetch(t.Context(), tc.kind)
			if err != nil {
				t.Fatalf("fetching %s: %v", tc.kind, err)
			}

			if gotPath != tc.path {
				t.Errorf("requested %q, want %q", gotPath, tc.path)
			}
			// Token, not Bearer. Django REST Framework, and the wrong scheme produces exactly
			// the same refusal as no scheme at all — which is why this is asserted rather than
			// assumed.
			if gotAuth != "Token example-token" {
				t.Errorf("sent authorization %q, want the Token scheme", gotAuth)
			}
			if len(objects) != tc.wantCount {
				t.Fatalf("got %d objects, want %d", len(objects), tc.wantCount)
			}
			if objects[0].ZpaID != tc.wantFirst {
				t.Errorf("first id is %d, want %d", objects[0].ZpaID, tc.wantFirst)
			}
			if objects[0].Label != tc.wantLabel {
				t.Errorf("first label is %q, want %q", objects[0].Label, tc.wantLabel)
			}
			// The payload is kept whole. This is what lets the change log say what changed in
			// fields Tallox has no opinion about.
			if !json.Valid(objects[0].Payload) {
				t.Errorf("payload is not valid json: %s", objects[0].Payload)
			}
		})
	}
}

// TestAnHTMLRefusalDoesNotReachTheErrorText.
//
// Outside the eduVPN the refusal is an Apache error page: 403, text/html, 319 bytes. An HTML
// document inside a log line is unreadable, and a proxy's error page can carry hostnames —
// so the message carries the shape of the answer and never the answer.
//
// The content type is kept, because it is most of the diagnosis: an HTML refusal means the
// request never reached the application and the network is wrong; a JSON one means it did and
// the credential is wrong.
func TestAnHTMLRefusalDoesNotReachTheErrorText(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=iso-8859-1")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<!DOCTYPE HTML><html><head><title>403 Forbidden</title></head>` +
			`<body><h1>Forbidden</h1><p>You don't have permission.</p></body></html>`))
	}))
	defer srv.Close()

	_, err := newClient(t, srv.URL).Fetch(t.Context(), domain.ZPAKindSPO)
	if !errors.Is(err, zpa.ErrNotAuthorised) {
		t.Fatalf("got %v, want ErrNotAuthorised", err)
	}
	if strings.ContainsAny(err.Error(), "<>") {
		t.Errorf("the error carries markup from the body: %v", err)
	}
	if !strings.Contains(err.Error(), "text/html") {
		t.Errorf("the error drops the content type, which is what distinguishes a wrong "+
			"network from a wrong credential: %v", err)
	}
}

// TestATwoHundredThatIsNotJSONIsRefused.
//
// The most important check in the package. A login interstitial or a proxy error page served
// with 200 would otherwise be stored as a payload — and against a content-hash cache that
// looks exactly like "everything in the catalogue changed last night".
func TestATwoHundredThatIsNotJSONIsRefused(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body>Bitte anmelden</body></html>"))
	}))
	defer srv.Close()

	_, err := newClient(t, srv.URL).Fetch(t.Context(), domain.ZPAKindSPO)
	if !errors.Is(err, zpa.ErrNotJSON) {
		t.Fatalf("got %v, want ErrNotJSON", err)
	}
}

// TestAnEmptyResultIsAFailure.
//
// The sync marks everything absent from a successful fetch as gone. Without this, one bad
// night retires the whole catalogue — and it would look like a deliberate change, complete
// with a change-log entry per module.
func TestAnEmptyResultIsAFailure(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}))
	defer srv.Close()

	_, err := newClient(t, srv.URL).Fetch(t.Context(), domain.ZPAKindSPO)
	if !errors.Is(err, zpa.ErrEmptyResult) {
		t.Fatalf("got %v, want ErrEmptyResult", err)
	}
}

// TestAnOutageIsRetriedAndARefusalIsNot.
//
// Two rules in one table, because they are the same decision seen from both sides: retry what
// might differ next time, never retry what will not. Three attempts against a wrong token
// would be twelve failed authentications per night against another institution's system.
func TestAnOutageIsRetriedAndARefusalIsNot(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		statuses     []int
		wantRequests int32
		wantErr      error
	}{
		{"recovers on the third try", []int{503, 503, 200}, 3, nil},
		{"gives up after three", []int{503, 503, 503}, 3, zpa.ErrUnavailable},
		{"a refused credential is asked once", []int{401}, 1, zpa.ErrNotAuthorised},
		{"a wrong path is asked once", []int{404}, 1, zpa.ErrUnknownEndpoint},
		{"an unexpected status is asked once", []int{418}, 1, zpa.ErrUnexpectedStatus},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := int(requests.Add(1)) - 1
				status := tc.statuses[min(n, len(tc.statuses)-1)]
				if status == http.StatusOK {
					serveFixture(t, w, filepath.Base(r.URL.Path)+".json")
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"detail":"nope"}`))
			}))
			defer srv.Close()

			// The injected sleep is what keeps this in microseconds. A retry test that really
			// waits five seconds is a test somebody eventually deletes.
			_, err := newClient(t, srv.URL).Fetch(t.Context(), domain.ZPAKindSPO)

			if tc.wantErr == nil && err != nil {
				t.Fatalf("want success, got %v", err)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("got %v, want %v", err, tc.wantErr)
			}
			if got := requests.Load(); got != tc.wantRequests {
				t.Errorf("made %d requests, want %d", got, tc.wantRequests)
			}
		})
	}
}

// TestAMissingIDNamesTheKeysItSawAndNoValues.
//
// This message is the instrument for the next time the interface changes shape — it already
// changed once. Key names are schema and safe to put in a mail to the maintainer; values are
// data from another institution's system.
func TestAMissingIDNamesTheKeysItSawAndNoValues(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"identifier": "801", "version": "2025", "secret": "s3cret"}]`))
	}))
	defer srv.Close()

	_, err := newClient(t, srv.URL).Fetch(t.Context(), domain.ZPAKindSPO)
	if !errors.Is(err, zpa.ErrNoObjectID) {
		t.Fatalf("got %v, want ErrNoObjectID", err)
	}
	for _, key := range []string{"identifier", "version", "secret"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("the error does not name the key %q it saw: %v", key, err)
		}
	}
	for _, value := range []string{"801", "2025", "s3cret"} {
		if strings.Contains(err.Error(), value) {
			t.Errorf("the error leaks the value %q — key names are schema, values are data: %v",
				value, err)
		}
	}
}

// TestTheTokenNeverAppearsInAnError. Every failure path, one assertion.
func TestTheTokenNeverAppearsInAnError(t *testing.T) {
	t.Parallel()

	for _, status := range []int{401, 403, 404, 418, 500} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
		}))

		_, err := newClient(t, srv.URL).Fetch(t.Context(), domain.ZPAKindSPO)
		if err == nil {
			t.Errorf("status %d was accepted", status)
		} else if strings.Contains(err.Error(), "example-token") {
			t.Errorf("status %d: the error carries the credential: %v", status, err)
		}
		srv.Close()
	}
}

// TestARequestIsCancellableFromOutside.
//
// http.Client.Timeout alone cannot be cancelled by the caller, so a shutdown or a disconnected
// client would leave the socket open until the backstop fires. The per-fetch context is what
// makes cancellation reach it.
func TestARequestIsCancellableFromOutside(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer func() { close(release); srv.Close() }()

	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	done := make(chan error, 1)
	go func() { _, err := newClient(t, srv.URL).Fetch(ctx, domain.ZPAKindSPO); done <- err }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a cancelled fetch succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelling the context did not reach the request")
	}
}

// TestTheRealFixturesParse runs the client against the recorded payloads in the private repo.
//
// The invented fixtures above prove the rules; these prove the assumption underneath them —
// that every endpoint really is an array of objects carrying `<kind>_id`. They cannot live
// here: they hold the mail addresses of the colleagues responsible for the modules.
//
// TALLOX_ZPA_FIXTURES_REQUIRED=1 turns the skip into a failure, the same construction
// TALLOX_TEST_DB_REQUIRED uses and for the same reason: a skipped check that renders as green
// quietly stops meaning anything. It is set where the fixtures exist and nowhere else.
func TestTheRealFixturesParse(t *testing.T) {
	t.Parallel()

	dir := os.Getenv("TALLOX_ZPA_FIXTURE_DIR")
	if dir == "" {
		if os.Getenv("TALLOX_ZPA_FIXTURES_REQUIRED") == "1" {
			t.Fatal("TALLOX_ZPA_FIXTURES_REQUIRED=1 but TALLOX_ZPA_FIXTURE_DIR is not set")
		}
		t.Skip("no recorded fixtures — set TALLOX_ZPA_FIXTURE_DIR to the private copies")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := os.ReadFile(filepath.Join(dir, filepath.Base(r.URL.Path)+".json"))
		if err != nil {
			http.Error(w, "no such fixture", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	client := newClient(t, srv.URL)
	for _, kind := range domain.AllZPAKinds() {
		objects, err := client.Fetch(t.Context(), kind)
		if err != nil {
			t.Errorf("%s: %v", kind, err)
			continue
		}
		seen := make(map[int64]bool, len(objects))
		for _, o := range objects {
			if o.ZpaID == 0 {
				t.Errorf("%s: an object came back with a zero id", kind)
			}
			if seen[o.ZpaID] {
				t.Errorf("%s: id %d appears twice — it is the cache's unique key", kind, o.ZpaID)
			}
			seen[o.ZpaID] = true
		}
		t.Logf("%s: %d objects", kind, len(objects))
	}
}

func newClient(t *testing.T, baseURL string) *zpa.Client {
	t.Helper()
	c, err := zpa.New(zpa.Config{BaseURL: baseURL, Token: "example-token"})
	if err != nil {
		t.Fatalf("building the client: %v", err)
	}
	zpa.SetSleepForTest(c, func(context.Context, time.Duration) error { return nil })
	return c
}

func serveFixture(t *testing.T, w http.ResponseWriter, name string) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		http.Error(w, "no such fixture", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}
