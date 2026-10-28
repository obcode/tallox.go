package bootstrap_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/obcode/tallox.go/bootstrap"
	"github.com/obcode/tallox.go/internal/graphqltest"
)

// unsized hides the reader's type from httptest.NewRequest, which otherwise sets
// Content-Length for the usual buffer types: the request arrives as a chunked body would.
type unsized struct{ io.Reader }

// TestAnOversizedBodyIsRefusedBeforeItIsRead: the bound sits outside the authenticator, so
// the refusal costs no database round trip, and it answers the same on both doors.
func TestAnOversizedBodyIsRefusedBeforeItIsRead(t *testing.T) {
	t.Parallel()

	h := bootstrap.Handler(bootstrap.Options{MaxRequestBytes: 512})
	padding := strings.Repeat(" ", 2048)
	big := `{"query":"{ buildInfo { version } }` + padding + `"}`

	for _, door := range []string{"/query", "/api/graphql"} {
		t.Run(door, func(t *testing.T) {
			// Declared up front: refused outright.
			req := httptest.NewRequest(http.MethodPost, door, strings.NewReader(big))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusRequestEntityTooLarge {
				t.Errorf("a %d-byte body with Content-Length answered %d, want 413", len(big), rec.Code)
			}

			// Chunked: read up to one byte past the bound and refused the same way.
			req = httptest.NewRequest(http.MethodPost, door, unsized{strings.NewReader(big)})
			req.Header.Set("Content-Type", "application/json")
			rec = httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusRequestEntityTooLarge {
				t.Errorf("a chunked %d-byte body answered %d, want 413: %s", len(big), rec.Code, rec.Body)
			}
		})
	}

	// And a request of the ordinary size still works, anonymously, on the browser door.
	var got struct {
		BuildInfo struct{ Version string }
	}
	graphqltest.New(h).Anonymous().MustQuery(t, `{ buildInfo { version } }`, nil, &got)
}

// TestAnOperationOverTheComplexityBudgetIsRefusedWithACode: the budget is on the shared
// handler, and the refusal names itself so that a colleague's evaluation that outgrew it can
// be read as that rather than as an outage.
func TestAnOperationOverTheComplexityBudgetIsRefusedWithACode(t *testing.T) {
	t.Parallel()

	// buildInfo { version commit builtAt } is four fields: the root and three leaves.
	const query = `{ buildInfo { version commit builtAt } }`

	tight := graphqltest.New(bootstrap.Handler(bootstrap.Options{MaxComplexity: 3})).Anonymous()
	resp := tight.Do(t, query, nil)
	if !resp.Failed() {
		t.Fatalf("four fields passed a budget of three:\n%s", resp.Body)
	}
	if code, _ := resp.Errors[0].Extensions["code"].(string); code != "COMPLEXITY_LIMIT_EXCEEDED" {
		t.Errorf("over budget answered %q, want COMPLEXITY_LIMIT_EXCEEDED", code)
	}

	// Aliases count each: the same leaf asked for twice is two fields, which is the shape of
	// the request the budget exists for.
	resp = tight.Do(t, `{ a: buildInfo { version } b: buildInfo { version } }`, nil)
	if !resp.Failed() {
		t.Errorf("two aliased roots passed a budget of three:\n%s", resp.Body)
	}

	roomy := graphqltest.New(bootstrap.Handler(bootstrap.Options{MaxComplexity: 4})).Anonymous()
	var got struct {
		BuildInfo struct{ Version string }
	}
	roomy.MustQuery(t, query, nil, &got)
}
