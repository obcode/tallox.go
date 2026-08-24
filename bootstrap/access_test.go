package bootstrap_test

import (
	"net/http"
	"testing"

	"github.com/obcode/tallox.go/bootstrap"
	"github.com/obcode/tallox.go/internal/auth"
	"github.com/obcode/tallox.go/internal/buildinfo"
	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/graphqltest"
	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/store"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

const accessLogQuery = `
query AccessLog {
  accessLog(limit: 50) {
    id
    mail
    door
    fields
    operation
    outcome
    errorCode
    roles
  }
}`

// accessHandler seeds the given personas with their fixture tokens and returns the handler
// Serve would build, with the access log wired exactly as production wires it.
func accessHandler(t *testing.T, roles map[testdata.Persona][]string) (http.Handler, *store.Access) {
	t.Helper()

	s := storetest.New(t)
	for persona, held := range roles {
		storetest.SeedPerson(t, s, persona, held...)

		parsed, err := auth.ParseToken(persona.Token)
		if err != nil {
			t.Fatalf("fixture token of %s does not parse: %v", persona.Name, err)
		}
		storetest.SeedToken(t, s, persona, auth.HashSecret(parsed.Secret),
			storetest.TokenOptions{Description: "access log test"})
	}

	directory := store.NewDirectory(s.Pool)
	access := store.NewAccess(s.Pool)

	handler := bootstrap.Handler(bootstrap.Options{
		Build:  buildinfo.Info{Version: "test"},
		Auth:   auth.Config{Mode: auth.ModeProxy, Users: directory, Tokens: directory},
		Access: domain.NewAccessService(access),
	})
	return handler, access
}

// TestTheAccessLogIsAdminAndInteractiveOnly runs the same read through both doors.
//
// The doors are *supposed* to differ here, so this asserts per door rather than asserting one
// thing twice: the browser gives an administrator the rows, and the token gives them null with
// INTERACTIVE_ONLY however good their roles are. A long-lived token in a script must not be
// able to enumerate when colleagues worked.
func TestTheAccessLogIsAdminAndInteractiveOnly(t *testing.T) {
	t.Parallel()

	handler, _ := accessHandler(t, map[testdata.Persona][]string{
		testdata.Sechs: {string(policy.RoleAdmin)},
	})

	c := graphqltest.New(handler)

	t.Run("browser", func(t *testing.T) {
		resp := c.AsUser(testdata.Sechs.Mail).On(graphqltest.Browser).
			Do(t, accessLogQuery, nil)
		if resp.Failed() {
			t.Fatalf("an administrator in a browser was refused:\n%s", resp.Body)
		}
	})

	// Null rather than an error, which is what @interactiveOnly does on a nullable field: a
	// script asking for several things gets the ones it may have. What matters here is that
	// the rows are not among them.
	t.Run("token: the query answers null", func(t *testing.T) {
		var got struct {
			AccessLog *[]struct {
				ID string `json:"id"`
			} `json:"accessLog"`
		}
		c.WithToken(testdata.Sechs.Token).On(graphqltest.Token).
			MustQuery(t, accessLogQuery, nil, &got)
		if got.AccessLog != nil {
			t.Errorf("a token read the access log: %v", *got.AccessLog)
		}
	})
}

// TestOnlyAnAdministratorReadsTheAccessLogThroughTheApi is the role half, through both doors.
//
// The dean's office is the case worth having: it reads everything the planning process
// produces, and MayReadZPAImport is deliberately the union of it and ADMIN. This rule is
// deliberately not, and a test is what keeps the two from being tidied into agreement.
func TestOnlyAnAdministratorReadsTheAccessLogThroughTheApi(t *testing.T) {
	t.Parallel()

	handler, _ := accessHandler(t, map[testdata.Persona][]string{
		testdata.Fuenf: {string(policy.RoleDeansOffice)},
		testdata.Vier:  {string(policy.RoleProgrammeLead)},
	})

	c := graphqltest.New(handler)

	for _, persona := range []testdata.Persona{testdata.Fuenf, testdata.Vier} {
		t.Run(persona.Name, func(t *testing.T) {
			resp := c.AsUser(persona.Mail).On(graphqltest.Browser).Do(t, accessLogQuery, nil)
			if !resp.Failed() {
				t.Fatalf("%s (%s) read the access log:\n%s", persona.Name, persona.Part, resp.Body)
			}
			if code, _ := resp.Errors[0].Extensions["code"].(string); code != "FORBIDDEN" {
				t.Errorf("refusal code is %q, want FORBIDDEN\n%s", code, resp.Body)
			}
		})
	}
}

// TestTheLogRecordsWhatWasAskedForAndNoArguments is the rule this whole subsystem exists to
// protect, driven through the real handler.
//
// A lecturer asks for a person by id — an argument that is a uuid, on a field whose result is
// somebody else's data. The entry must name the field and must not contain the argument, the
// variables or anything out of the response. ADMIN is not on the exception list of the wish
// visibility rule, and a log carrying arguments would hand that exception back through the
// side door.
func TestTheLogRecordsWhatWasAskedForAndNoArguments(t *testing.T) {
	t.Parallel()

	handler, access := accessHandler(t, map[testdata.Persona][]string{
		testdata.Eins:  {string(policy.RoleLecturer)},
		testdata.Sechs: {string(policy.RoleAdmin)},
	})

	c := graphqltest.New(handler)

	secret := "a-secret-argument-nobody-should-find-in-a-log"
	c.AsUser(testdata.Eins.Mail).On(graphqltest.Browser).Do(t, `
		query LooksAtSomebody($needle: String!) {
		  people(search: $needle) { id }
		}`, map[string]any{"needle": secret})

	entries, err := access.Entries(t.Context(), domain.AccessFilter{})
	if err != nil {
		t.Fatalf("cannot read the log: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("nothing was recorded — the middleware is not wired")
	}

	var found bool
	for _, entry := range entries {
		for _, field := range entry.Fields {
			if field == "people" {
				found = true
			}
		}
		graphqltest.AssertNoLeak(t, entry.Operation, secret)
		for _, field := range entry.Fields {
			graphqltest.AssertNoLeak(t, field, secret)
		}
		graphqltest.AssertNoLeak(t, entry.ErrorCode, secret)
	}
	if !found {
		t.Errorf("no entry names the root field that was asked for: %+v", entries)
	}
}

// TestARefusedSignInReachesTheLog: somebody the identity provider knows and this installation
// does not. There is no person row, so the entry has none — and it is the entry the nightly
// report is mostly about.
func TestARefusedSignInReachesTheLog(t *testing.T) {
	t.Parallel()

	handler, access := accessHandler(t, map[testdata.Persona][]string{
		testdata.Sechs: {string(policy.RoleAdmin)},
	})

	graphqltest.New(handler).AsUser("niemand@example.org").On(graphqltest.Browser).
		Do(t, buildInfoQuery, nil)

	entries, err := access.Entries(t.Context(), domain.AccessFilter{OnlyRefused: true})
	if err != nil {
		t.Fatalf("cannot read the log: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("recorded %d refusals, want 1: %+v", len(entries), entries)
	}
	if entries[0].Outcome != domain.AccessRefusedAuth {
		t.Errorf("outcome = %q, want REFUSED_AUTH", entries[0].Outcome)
	}
	if entries[0].ActorMail != "niemand@example.org" {
		t.Errorf("mail = %q, want niemand@example.org", entries[0].ActorMail)
	}
	if entries[0].ActorID != nil {
		t.Errorf("actor id = %v, want nil", entries[0].ActorID)
	}
}

// TestARefusedOperationIsLoggedNotSwallowed pins the middleware ORDER.
//
// EnforceScopes answers with graphql.OneShot instead of calling through, so only a middleware
// registered before it — and therefore wrapped around it — sees that response. Register them
// the other way round and the log holds every operation that was allowed and no record of the
// ones that were not, which is exactly backwards for an audit trail. Nothing else would fail.
func TestARefusedOperationIsLoggedNotSwallowed(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	storetest.SeedPerson(t, s, testdata.Sechs, string(policy.RoleAdmin))

	parsed, err := auth.ParseToken(testdata.Sechs.Token)
	if err != nil {
		t.Fatalf("fixture token does not parse: %v", err)
	}
	// A token narrowed to PUBLIC:READ, asking for something under PLANNING.
	storetest.SeedToken(t, s, testdata.Sechs, auth.HashSecret(parsed.Secret),
		storetest.TokenOptions{Description: "narrow", Scopes: []string{"PUBLIC:READ"}})

	directory := store.NewDirectory(s.Pool)
	access := store.NewAccess(s.Pool)
	handler := bootstrap.Handler(bootstrap.Options{
		Build:  buildinfo.Info{Version: "test"},
		Auth:   auth.Config{Mode: auth.ModeProxy, Users: directory, Tokens: directory},
		Access: domain.NewAccessService(access),
	})

	resp := graphqltest.New(handler).WithToken(testdata.Sechs.Token).On(graphqltest.Token).
		Do(t, `{ semesters { code } }`, nil)
	if !resp.Failed() {
		t.Fatalf("the narrow token was not refused:\n%s", resp.Body)
	}

	entries, err := access.Entries(t.Context(), domain.AccessFilter{})
	if err != nil {
		t.Fatalf("cannot read the log: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("recorded %d entries, want 1 — a refused operation still happened: %+v",
			len(entries), entries)
	}
	if entries[0].Outcome != domain.AccessRefusedScope {
		t.Errorf("outcome = %q, want REFUSED_SCOPE — is RecordAccess still registered "+
			"before EnforceScopes?", entries[0].Outcome)
	}
	if entries[0].TokenID != testdata.Sechs.TokenID {
		t.Errorf("tokenId = %q, want %q", entries[0].TokenID, testdata.Sechs.TokenID)
	}
}

// TestWhatIsNotWorthARow.
//
// Introspection and buildInfo. Both are polled in a loop by something that is not a person —
// an editor while somebody types, the monitoring every few seconds — and neither says anything
// about anybody. The cost of logging them is not disk but attention: thousands of rows that
// say nothing bury the handful that say something.
func TestWhatIsNotWorthARow(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		query string
	}{
		{"introspection", `{ __schema { queryType { name } } }`},
		{"the monitoring asking for the version", buildInfoQuery},
		{"the version with a name on it, as the GUI sends it", `query Version { buildInfo { version } }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			handler, access := accessHandler(t, map[testdata.Persona][]string{
				testdata.Eins: {string(policy.RoleLecturer)},
			})

			graphqltest.New(handler).AsUser(testdata.Eins.Mail).On(graphqltest.Browser).
				Do(t, tc.query, nil)

			entries, err := access.Entries(t.Context(), domain.AccessFilter{})
			if err != nil {
				t.Fatalf("cannot read the log: %v", err)
			}
			if len(entries) != 0 {
				t.Errorf("this was logged: %+v", entries)
			}
		})
	}
}

// TestAQuietFieldBesideALoudOneIsStillARow.
//
// The exemption decides WHETHER to write an entry, never what it says. An operation that asks
// for the version and for something real is a real access, and its entry describes the whole
// operation — an entry that quietly dropped half of what was asked would be worse than no
// exemption at all.
func TestAQuietFieldBesideALoudOneIsStillARow(t *testing.T) {
	t.Parallel()

	handler, access := accessHandler(t, map[testdata.Persona][]string{
		testdata.Eins: {string(policy.RoleLecturer)},
	})

	graphqltest.New(handler).AsUser(testdata.Eins.Mail).On(graphqltest.Browser).
		Do(t, `query Kopfzeile { buildInfo { version } me { mail } }`, nil)

	entries, err := access.Entries(t.Context(), domain.AccessFilter{})
	if err != nil {
		t.Fatalf("cannot read the log: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("recorded %d entries, want 1: %+v", len(entries), entries)
	}

	fields := entries[0].Fields
	if len(fields) != 2 || fields[0] != "buildInfo" || fields[1] != "me" {
		t.Errorf("fields = %v, want [buildInfo me] — the entry describes the whole operation",
			fields)
	}
}

// TestARefusedSignInIsRecordedWhateverItAskedFor.
//
// The buildInfo exemption is on the operation path. A sign-in refused at the door never gets
// that far — and somebody unknown knocking is worth a row even when all they wanted was the
// version number. Written as its own test because the two paths look alike from the outside
// and an exemption added to the wrong one would silence exactly the entries this log is for.
func TestARefusedSignInIsRecordedWhateverItAskedFor(t *testing.T) {
	t.Parallel()

	handler, access := accessHandler(t, map[testdata.Persona][]string{
		testdata.Eins: {string(policy.RoleLecturer)},
	})

	graphqltest.New(handler).AsUser("niemand@example.org").On(graphqltest.Browser).
		Do(t, buildInfoQuery, nil)

	entries, err := access.Entries(t.Context(), domain.AccessFilter{})
	if err != nil {
		t.Fatalf("cannot read the log: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("recorded %d entries, want 1: %+v", len(entries), entries)
	}
	if entries[0].Outcome != domain.AccessRefusedAuth {
		t.Errorf("outcome = %q, want REFUSED_AUTH", entries[0].Outcome)
	}
}
