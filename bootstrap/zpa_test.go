package bootstrap_test

import (
	"context"
	"encoding/json"
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

// A source that answers from memory, so these tests never touch the network.
//
// The real client's behaviour against a real HTTP server is internal/zpa's business; what is
// under test here is who is allowed to reach these fields through which door.
type stubZPASource struct {
	objects []domain.ZPAObject
}

func (s *stubZPASource) Fetch(context.Context, domain.ZPAKind) ([]domain.ZPAObject, error) {
	return s.objects, nil
}

// importHandler builds the handler Serve would, with the import wired.
//
// configured=false is the state of every DevContainer and every CI run, and it has to behave:
// the reads answer, and starting a run refuses with a code rather than panicking.
func importHandler(t *testing.T, configured bool, people ...grants) http.Handler {
	t.Helper()

	s := storetest.New(t)
	for _, p := range people {
		storetest.SeedPerson(t, s, p.who, p.roles...)

		parsed, err := auth.ParseToken(p.who.Token)
		if err != nil {
			t.Fatalf("fixture token of %s does not parse: %v", p.who.Name, err)
		}
		storetest.SeedToken(t, s, p.who, auth.HashSecret(parsed.Secret), storetest.TokenOptions{
			Description: "zpa test",
		})
	}

	var source domain.ZPASource
	if configured {
		source = &stubZPASource{objects: []domain.ZPAObject{
			{ZpaID: 801, Payload: json.RawMessage(`{"spo_id":"801"}`), Label: "07-XX-2025"},
		}}
	}

	directory := store.NewDirectory(s.Pool)

	return bootstrap.Handler(bootstrap.Options{
		Build: buildinfo.Info{Version: "test"},
		Auth:  auth.Config{Mode: auth.ModeProxy, Users: directory, Tokens: directory},
		Import: domain.NewZPASyncService(
			store.NewZPA(s.Pool), source, store.NewZPALock(s.Pool), store.NewCatalogue(s.Pool)),
	})
}

const runsQuery = `{ zpaSyncRuns(limit: 5) { id status trigger } }`

// TestTheImportIsInteractiveOnly is the assertion the two doors are supposed to differ on, so
// it says so per door rather than quietly covering one.
//
// A token gets null rather than an error, because the field is nullable — a script that asks
// for it alongside something else still gets the rest of its answer. That is the whole reason
// the list is typed nullable.
func TestTheImportIsInteractiveOnly(t *testing.T) {
	t.Parallel()

	h := importHandler(t, true, grants{testdata.Sechs, []string{string(policy.RoleAdmin)}})

	t.Run("browser", func(t *testing.T) {
		var out struct {
			ZpaSyncRuns []struct{ ID string } `json:"zpaSyncRuns"`
		}
		graphqltest.New(h).AsUser(testdata.Sechs.Mail).On(graphqltest.Browser).
			MustQuery(t, runsQuery, nil, &out)
	})

	t.Run("token", func(t *testing.T) {
		resp := graphqltest.New(h).WithToken(testdata.Sechs.Token).On(graphqltest.Token).
			Do(t, runsQuery, nil)
		assertRefusal(t, resp, "INTERACTIVE_ONLY")
	})
}

// TestWhoSeesTheImport walks the cast across the rule.
//
// The union of ADMIN and DEANS_OFFICE is the first rule in this codebase that joins those two,
// so it is worth a table rather than a sentence: the act is operational, the need for it arises
// in planning, and forcing the dean's office to acquire ADMIN for a refresh button would push
// exactly the behaviour the access design works hardest to avoid.
func TestWhoSeesTheImport(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		who     testdata.Persona
		roles   []string
		allowed bool
	}{
		{"an administrator", testdata.Sechs, []string{string(policy.RoleAdmin)}, true},
		{"the dean's office", testdata.Fuenf, []string{string(policy.RoleDeansOffice)}, true},
		{"a lecturer", testdata.Eins, []string{string(policy.RoleLecturer)}, false},
		{"a programme lead", testdata.Vier, []string{string(policy.RoleProgrammeLead)}, false},
		{"a subject group lead", testdata.Drei, []string{string(policy.RoleSubjectGroupLead)}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := importHandler(t, true, grants{tc.who, tc.roles})
			resp := graphqltest.New(h).AsUser(tc.who.Mail).On(graphqltest.Browser).
				Do(t, runsQuery, nil)

			if tc.allowed {
				if resp.Failed() {
					t.Fatalf("%s was refused: %s", tc.name, resp.Body)
				}
				return
			}
			assertRefusal(t, resp, "FORBIDDEN")
		})
	}
}

// TestAnUnconfiguredImportStillAnswersItsReads.
//
// Every DevContainer and every CI run is in this state, and the page that says "not configured"
// is itself a read. Starting a run refuses with a code the interface can branch on.
func TestAnUnconfiguredImportStillAnswersItsReads(t *testing.T) {
	t.Parallel()

	h := importHandler(t, false, grants{testdata.Sechs, []string{string(policy.RoleAdmin)}})
	c := graphqltest.New(h).AsUser(testdata.Sechs.Mail).On(graphqltest.Browser)

	var out struct {
		ZpaSyncRuns []struct{ ID string } `json:"zpaSyncRuns"`
	}
	c.MustQuery(t, runsQuery, nil, &out)
	if len(out.ZpaSyncRuns) != 0 {
		t.Errorf("an unconfigured import reported %d runs", len(out.ZpaSyncRuns))
	}

	assertRefusal(t, c.Do(t, `mutation { syncZpaNow { id } }`, nil), "ZPA_NOT_CONFIGURED")
}

// TestASecondSyncIsRefusedRatherThanRepeated.
//
// The limit is what keeps a button that can be held down from hammering another institution's
// system. A refusal rather than a silent success, because the caller asked for a new run and
// did not get one — answering "done" to that is how a button teaches people it does nothing.
func TestASecondSyncIsRefusedRatherThanRepeated(t *testing.T) {
	t.Parallel()

	h := importHandler(t, true, grants{testdata.Sechs, []string{string(policy.RoleAdmin)}})
	c := graphqltest.New(h).AsUser(testdata.Sechs.Mail).On(graphqltest.Browser)

	var first struct {
		SyncZpaNow struct {
			Status    string `json:"status"`
			Trigger   string `json:"trigger"`
			StartedBy string `json:"startedBy"`
		} `json:"syncZpaNow"`
	}
	c.MustQuery(t, `mutation { syncZpaNow { status trigger startedBy } }`, nil, &first)

	if first.SyncZpaNow.Trigger != string(domain.ZPASyncTriggerManual) {
		t.Errorf("trigger is %q, want MANUAL", first.SyncZpaNow.Trigger)
	}
	// Attributed to the person who signed in, which is the reason this field exists and the
	// reason the mutation is interactive-only.
	if first.SyncZpaNow.StartedBy != testdata.Sechs.Name {
		t.Errorf("startedBy is %q, want %q", first.SyncZpaNow.StartedBy, testdata.Sechs.Name)
	}

	assertRefusal(t, c.Do(t, `mutation { syncZpaNow { id } }`, nil), "ZPA_SYNCED_RECENTLY")
}

// TestARefusalNamesNoInternals. The generic path must not carry a table name, a constraint or
// a host into an answer.
func TestARefusalNamesNoInternals(t *testing.T) {
	t.Parallel()

	h := importHandler(t, true, grants{testdata.Eins, []string{string(policy.RoleLecturer)}})
	resp := graphqltest.New(h).AsUser(testdata.Eins.Mail).On(graphqltest.Browser).
		Do(t, runsQuery, nil)

	for _, message := range resp.Messages() {
		graphqltest.AssertNoLeak(t, message,
			append(graphqltest.DatabaseNoise(), "zpa_object", "zpa_sync_run", "zpa.cs.hm.edu")...)
	}
}
