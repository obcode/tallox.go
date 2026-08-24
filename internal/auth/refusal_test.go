package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/obcode/tallox.go/internal/auth"
	"github.com/obcode/tallox.go/internal/principal"
)

// recordingRefusals collects what the middleware records, in order.
type recordingRefusals struct {
	got []auth.Refusal
}

func (r *recordingRefusals) RecordRefusal(_ context.Context, refusal auth.Refusal) error {
	r.got = append(r.got, refusal)
	return nil
}

func refused(t *testing.T, a auth.Authenticator, r *http.Request) []auth.Refusal {
	t.Helper()

	recorder := &recordingRefusals{}
	handler := auth.Middleware(a, recorder)(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	handler.ServeHTTP(httptest.NewRecorder(), r)
	return recorder.got
}

// TestRefusedSignInsAreRecorded is the entry an administrator most wants and the one a design
// keyed on the person row cannot hold: somebody the identity provider knows and this
// installation does not.
//
// It also pins that the identity comes off the REQUEST rather than out of the error message.
// The address is in that message today, and an audit trail that worked by parsing a sentence
// would go blank the first time somebody reworded it.
func TestRefusedSignInsAreRecorded(t *testing.T) {
	t.Parallel()

	browser := auth.NewProxyAuthenticator(auth.Config{Mode: auth.ModeProxy, Users: fakeUsers{}})

	request := httptest.NewRequest(http.MethodPost, "/query", nil)
	request.Header.Set(auth.HeaderRemoteUser, "niemand@example.org")
	request.Header.Set("X-Forwarded-For", "10.1.2.3")

	got := refused(t, browser, request)
	if len(got) != 1 {
		t.Fatalf("recorded %d refusals, want 1", len(got))
	}
	if got[0].Mail != "niemand@example.org" {
		t.Errorf("mail = %q, want niemand@example.org", got[0].Mail)
	}
	if got[0].Code != auth.CodeUnknownUser {
		t.Errorf("code = %q, want %q", got[0].Code, auth.CodeUnknownUser)
	}
	if got[0].Door != principal.KindInteractive {
		t.Errorf("door = %q, want interactive", got[0].Door)
	}
	if got[0].Source.String() != "10.1.2.3" {
		t.Errorf("source = %v, want 10.1.2.3", got[0].Source)
	}
	if got[0].TokenID != "" {
		t.Errorf("tokenId = %q, want empty on the browser door", got[0].TokenID)
	}
}

// TestARefusedTokenIsRecordedByItsPublicHalf.
//
// The token id is recorded even though no such token exists — that is the interesting refusal,
// and it is why the log's token column is not a foreign key. The secret half must not appear.
func TestARefusedTokenIsRecordedByItsPublicHalf(t *testing.T) {
	t.Parallel()

	door := auth.NewTokenAuthenticator(auth.Config{Mode: auth.ModeProxy, Tokens: fakeTokens{}})

	// 43 characters, the format's fixed length for the secret half.
	const secret = "0000000000000000000000000000000000000000000"
	request := httptest.NewRequest(http.MethodPost, "/api/graphql", nil)
	request.Header.Set("Authorization", "Bearer tallox_ZZZZZZZZZZZZZZZZ_"+secret)

	got := refused(t, door, request)
	if len(got) != 1 {
		t.Fatalf("recorded %d refusals, want 1", len(got))
	}
	if got[0].TokenID != "ZZZZZZZZZZZZZZZZ" {
		t.Errorf("tokenId = %q, want ZZZZZZZZZZZZZZZZ", got[0].TokenID)
	}
	if got[0].Mail != "" {
		t.Errorf("mail = %q, want empty on the token door", got[0].Mail)
	}
	if got[0].Code != auth.CodeInvalidToken {
		t.Errorf("code = %q, want %q", got[0].Code, auth.CodeInvalidToken)
	}
}

// TestAnonymousRequestsAreNotRefusals. Nothing was presented, so nothing was refused: the
// request carries on as the anonymous actor and the GraphQL layer records what it asks for.
// Recording it here would fill the log with one entry per health check and per login page.
func TestAnonymousRequestsAreNotRefusals(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		auth auth.Authenticator
		path string
	}{
		{"browser", auth.NewProxyAuthenticator(auth.Config{Mode: auth.ModeProxy, Users: fakeUsers{}}), "/query"},
		{"token", auth.NewTokenAuthenticator(auth.Config{Mode: auth.ModeProxy, Tokens: fakeTokens{}}), "/api/graphql"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := refused(t, tc.auth, httptest.NewRequest(http.MethodPost, tc.path, nil))
			if len(got) != 0 {
				t.Errorf("recorded %d refusals for a request with no credential, want 0", len(got))
			}
		})
	}
}

// TestARecorderIsOptional. Nil means "do not record", and the middleware must not need one to
// work: every unit test in this package runs that way, and so does any entry point that
// authenticates without a database behind it.
func TestARecorderIsOptional(t *testing.T) {
	t.Parallel()

	browser := auth.NewProxyAuthenticator(auth.Config{Mode: auth.ModeProxy, Users: fakeUsers{}})
	handler := auth.Middleware(browser, nil)(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))

	request := httptest.NewRequest(http.MethodPost, "/query", nil)
	request.Header.Set(auth.HeaderRemoteUser, "niemand@example.org")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}
