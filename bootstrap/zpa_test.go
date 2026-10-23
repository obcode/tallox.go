package bootstrap_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
//
// With a gate, every fetch waits until the gate is closed: the run then stays RUNNING for as
// long as the test needs to look at it, which is the only way to observe a run in progress
// without a sleep.
type stubZPASource struct {
	objects []domain.ZPAObject
	gate    <-chan struct{}
}

func (s *stubZPASource) Fetch(ctx context.Context, _ domain.ZPAKind) ([]domain.ZPAObject, error) {
	if s.gate != nil {
		select {
		case <-s.gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return s.objects, nil
}

func oneRegulation() []domain.ZPAObject {
	return []domain.ZPAObject{
		{ZpaID: 801, Payload: json.RawMessage(`{"spo_id":"801"}`), Label: "07-XX-2025"},
	}
}

// importHandler builds the handler Serve would, with the import wired.
//
// configured=false is the state of every DevContainer and every CI run, and it has to behave:
// the reads answer, and starting a run refuses with a code rather than panicking.
func importHandler(t *testing.T, configured bool, people ...grants) http.Handler {
	t.Helper()

	var source domain.ZPASource
	if configured {
		source = &stubZPASource{objects: oneRegulation()}
	}
	h, _ := importStack(t, source, people...)
	return h
}

// importStack is importHandler with the source chosen by the caller, and the service handed
// back so a test can wait for the runs it started.
//
// A run started through the API outlives the request, so the cleanup waits for it: registered
// after storetest.New, it runs before the schema is dropped, and a run still writing never
// meets a schema that is gone.
func importStack(t *testing.T, source domain.ZPASource, people ...grants) (http.Handler, *domain.ZPASyncService) {
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

	directory := store.NewDirectory(s.Pool)
	imports := domain.NewZPASyncService(
		store.NewZPA(s.Pool), source, store.NewZPALock(s.Pool), store.NewCatalogue(s.Pool))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := imports.Wait(ctx); err != nil {
			t.Errorf("a sync started by this test did not finish: %v", err)
		}
	})

	return bootstrap.Handler(bootstrap.Options{
		Build:  buildinfo.Info{Version: "test"},
		Auth:   auth.Config{Mode: auth.ModeProxy, Users: directory, Tokens: directory},
		Import: imports,
	}), imports
}

const runsQuery = `{ zpaSyncRuns(limit: 5) { id status trigger } }`

// TestTheImportIsInteractiveOnly is the assertion the two doors are supposed to differ on, so
// it says so per door rather than quietly covering one.
//
// A token gets null rather than an error, because the field is nullable — a script that asks
// for it alongside something else still gets the rest of its answer. That is the whole reason
// the list is typed nullable.
func TestTheImportIsInteractiveOnly(t *testing.T) {
	// Not parallel: the sync lock is a PostgreSQL advisory lock and therefore database-wide,
	// while storetest isolates only a schema. Two of these at once would refuse each other
	// for a reason that has nothing to do with what either asserts.
	h := importHandler(t, true, grants{testdata.Sechs, []string{string(policy.RoleAdmin)}})

	// A real run to ask about, so that a null below means "not through this door" and cannot
	// be the ordinary "no such run".
	browser := graphqltest.New(h).AsUser(testdata.Sechs.Mail).On(graphqltest.Browser)
	var started struct {
		SyncZpaNow zpaRun `json:"syncZpaNow"`
	}
	browser.MustQuery(t, `mutation { syncZpaNow { id } }`, nil, &started)
	runID := followRun(t, browser, started.SyncZpaNow.ID).ID

	t.Run("browser", func(t *testing.T) {
		var out struct {
			ZpaSyncRuns []struct{ ID string } `json:"zpaSyncRuns"`
		}
		browser.MustQuery(t, runsQuery, nil, &out)
		if len(out.ZpaSyncRuns) != 1 {
			t.Errorf("the browser door sees %d runs, want 1", len(out.ZpaSyncRuns))
		}
	})

	t.Run("token", func(t *testing.T) {
		c := graphqltest.New(h).WithToken(testdata.Sechs.Token).On(graphqltest.Token)
		assertRefusal(t, c.Do(t, runsQuery, nil), "INTERACTIVE_ONLY")
		assertRefusal(t, c.Do(t, `mutation { syncZpaNow { id } }`, nil), "INTERACTIVE_ONLY")

		// Following a run is as interactive as starting one. The field is nullable, so a
		// token gets null rather than an error, for a run that exists.
		var out struct {
			ZpaSyncRun *zpaRun `json:"zpaSyncRun"`
		}
		c.MustQuery(t, runQuery, map[string]any{"id": runID}, &out)
		if out.ZpaSyncRun != nil {
			t.Errorf("a token read run %s: %+v", runID, *out.ZpaSyncRun)
		}
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

// zpaRun is what the import page reads while it follows a run.
type zpaRun struct {
	ID         string  `json:"id"`
	Status     string  `json:"status"`
	Trigger    string  `json:"trigger"`
	StartedBy  string  `json:"startedBy"`
	FinishedAt *string `json:"finishedAt"`
	Kinds      []struct {
		Kind   string `json:"kind"`
		Status string `json:"status"`
	} `json:"kinds"`
}

const runQuery = `query ($id: ID!) {
  zpaSyncRun(id: $id) { id status trigger startedBy finishedAt kinds { kind status } }
}`

// followRun reads the run the way the page does, until it has finished.
func followRun(t *testing.T, c *graphqltest.Client, id string) zpaRun {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for {
		var out struct {
			ZpaSyncRun zpaRun `json:"zpaSyncRun"`
		}
		c.MustQuery(t, runQuery, map[string]any{"id": id}, &out)
		if out.ZpaSyncRun.FinishedAt != nil {
			return out.ZpaSyncRun
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s is still %s after ten seconds", id, out.ZpaSyncRun.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestASecondSyncIsRefusedRatherThanRepeated.
//
// Two refusals, one for each reason. While a run is going, a second one is refused with
// ZPA_SYNC_RUNNING — the button should show the run in progress, not start another. After a
// successful one, it is refused for a while, because this is what keeps a button that can be
// held down from hammering another institution's system. Refusals rather than silent successes,
// because the caller asked for a new run and did not get one — answering "done" to that is how
// a button teaches people it does nothing.
func TestASecondSyncIsRefusedRatherThanRepeated(t *testing.T) {
	// Not parallel: the sync lock is a PostgreSQL advisory lock and therefore database-wide,
	// while storetest isolates only a schema. Two of these at once would refuse each other
	// for a reason that has nothing to do with what either asserts.
	gate := make(chan struct{})
	h, _ := importStack(t, &stubZPASource{objects: oneRegulation(), gate: gate},
		grants{testdata.Sechs, []string{string(policy.RoleAdmin)}})
	c := graphqltest.New(h).AsUser(testdata.Sechs.Mail).On(graphqltest.Browser)

	var first struct {
		SyncZpaNow zpaRun `json:"syncZpaNow"`
	}
	c.MustQuery(t, `mutation { syncZpaNow { id status trigger startedBy finishedAt } }`, nil, &first)
	run := first.SyncZpaNow

	// The answer comes while the run is still going: every fetch is waiting on the gate.
	if run.Status != string(domain.ZPASyncRunning) || run.FinishedAt != nil {
		t.Errorf("the mutation answered %s (finished %v), want a RUNNING run", run.Status, run.FinishedAt)
	}
	if run.Trigger != string(domain.ZPASyncTriggerManual) {
		t.Errorf("trigger is %q, want MANUAL", run.Trigger)
	}
	// Attributed to the person who signed in, which is the reason this field exists and the
	// reason the mutation is interactive-only.
	if run.StartedBy != testdata.Sechs.Name {
		t.Errorf("startedBy is %q, want %q", run.StartedBy, testdata.Sechs.Name)
	}

	assertRefusal(t, c.Do(t, `mutation { syncZpaNow { id } }`, nil), "ZPA_SYNC_RUNNING")

	close(gate)
	done := followRun(t, c, run.ID)
	if done.Status != string(domain.ZPASyncSucceeded) {
		t.Errorf("the run ended %s, want SUCCEEDED", done.Status)
	}
	if len(done.Kinds) != len(domain.AllZPAKinds()) {
		t.Errorf("the run reports %d endpoints, want %d", len(done.Kinds), len(domain.AllZPAKinds()))
	}

	assertRefusal(t, c.Do(t, `mutation { syncZpaNow { id } }`, nil), "ZPA_SYNCED_RECENTLY")
}

// TestARunOutlivesTheRequestThatStartedIt is the reason Start exists. The run used to live
// inside the request: a closed tab or a proxy giving up cancelled it halfway, and its row
// stayed RUNNING until the next restart.
func TestARunOutlivesTheRequestThatStartedIt(t *testing.T) {
	// Not parallel: the sync lock is a PostgreSQL advisory lock and therefore database-wide,
	// while storetest isolates only a schema. Two of these at once would refuse each other
	// for a reason that has nothing to do with what either asserts.
	gate := make(chan struct{})
	h, _ := importStack(t, &stubZPASource{objects: oneRegulation(), gate: gate},
		grants{testdata.Sechs, []string{string(policy.RoleAdmin)}})

	// By hand rather than through graphqltest, which has no way to hang up: this request's
	// context is cancelled the moment the answer is in, the way a closed connection does it.
	ctx, hangUp := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/query",
		strings.NewReader(`{"query":"mutation { syncZpaNow { id } }"}`)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Remote-User", testdata.Sechs.Mail)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	hangUp()

	var started struct {
		Data struct {
			SyncZpaNow struct{ ID string } `json:"syncZpaNow"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &started); err != nil || started.Data.SyncZpaNow.ID == "" {
		t.Fatalf("the run did not start: %s", rec.Body.String())
	}

	close(gate)
	c := graphqltest.New(h).AsUser(testdata.Sechs.Mail).On(graphqltest.Browser)
	if done := followRun(t, c, started.Data.SyncZpaNow.ID); done.Status != string(domain.ZPASyncSucceeded) {
		t.Errorf("a run whose request hung up ended %s, want SUCCEEDED", done.Status)
	}
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

// The development door hands out an identity nobody logged in as. That was harmless while
// nothing referenced the actor, and stopped being harmless the moment a write recorded who
// performed it: `created_by` is a foreign key to person, and an actor that exists in the request
// and not in the database fails it.
//
// The mode whose whole purpose is that the ordinary path works must not be the one where the
// ordinary path is refused.
func TestTheDevelopmentUserHasAPersonRow(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)

	id, err := store.EnsureDevelopmentUser(t.Context(), s.Pool,
		auth.DevUserID(""), auth.DevUserMail(""), auth.DevUserName)
	if err != nil {
		t.Fatalf("cannot give the development user a person row: %v", err)
	}
	if id != auth.DevUserID("") {
		t.Errorf("the row has id %s and the synthetic actor carries %s — a foreign key to it "+
			"would still be refused", id, auth.DevUserID(""))
	}

	// Twice is a no-op: this runs on every start.
	again, err := store.EnsureDevelopmentUser(t.Context(), s.Pool,
		auth.DevUserID(""), auth.DevUserMail(""), auth.DevUserName)
	if err != nil || again != id {
		t.Errorf("a second start produced %s / %v, want the same row", again, err)
	}

	// And it grants nothing. Which roles the development user has is a property of the mode, not
	// of a row somebody could edit into a different answer.
	var roles int
	if err := s.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM person_role WHERE person_id = $1`, id).Scan(&roles); err != nil {
		t.Fatalf("cannot count the roles: %v", err)
	}
	if roles != 0 {
		t.Errorf("the development user was granted %d role(s) in the database", roles)
	}
}
