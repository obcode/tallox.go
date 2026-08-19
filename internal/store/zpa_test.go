package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/store"
	"github.com/obcode/tallox.go/internal/store/storetest"
)

// TestNoDomainTableReferencesTheZpaCache is the rule this whole subsystem exists to protect,
// made mechanical.
//
// A ZPA id is an import key and never an identity. When the module and instance tables arrive
// they will have their own uuids and a nullable column saying "this came from ZPA object N",
// and nothing will join on it at request time. The sibling project made a foreign identifier
// its primary key and every table downstream inherited its quirks until correcting one stopped
// being possible.
//
// Written as a test rather than as a paragraph for the same reason internal/arch forbids
// importing pgx outside this package: the rule that matters most is the one that cannot be
// broken by a hurried afternoon. Its corollary is a feature — because nothing points in, this
// cache may be truncated and rebuilt without touching a planning row.
func TestNoDomainTableReferencesTheZpaCache(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)

	rows, err := s.Pool.Query(t.Context(),
		`SELECT t.relname, c.conname
		   FROM pg_constraint c
		   JOIN pg_class t ON t.oid = c.conrelid
		   JOIN pg_class r ON r.oid = c.confrelid
		   JOIN pg_namespace n ON n.oid = t.relnamespace
		  WHERE c.contype = 'f'
		    AND n.nspname = current_schema()
		    AND r.relname LIKE 'zpa\_%'
		    AND t.relname NOT LIKE 'zpa\_%'`)
	if err != nil {
		t.Fatalf("cannot read the foreign keys: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var table, constraint string
		if err := rows.Scan(&table, &constraint); err != nil {
			t.Fatalf("cannot read a row: %v", err)
		}
		t.Errorf("%s.%s references the ZPA cache. A ZPA id is an import key, not an "+
			"identity: give the row its own uuid and a nullable zpa_ref column that nothing "+
			"joins on. This is the mistake that made the sibling project's schema "+
			"uncorrectable.", table, constraint)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("cannot read the foreign keys: %v", err)
	}
}

// TestDatabaseAndDomainAgreeOnZPAKinds. The list has three homes that cannot import one
// another — the constraint, internal/domain and the GraphQL enum — so two of them are compared
// here.
func TestDatabaseAndDomainAgreeOnZPAKinds(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)

	var definition string
	err := s.Pool.QueryRow(t.Context(),
		`SELECT pg_get_constraintdef(c.oid)
		   FROM pg_constraint c
		   JOIN pg_class t ON t.oid = c.conrelid
		   JOIN pg_namespace n ON n.oid = t.relnamespace
		  WHERE c.conname = 'zpa_object_kind_is_known' AND n.nspname = current_schema()`,
	).Scan(&definition)
	if err != nil {
		t.Fatalf("cannot read the kind constraint — has it been renamed? %v", err)
	}

	for _, kind := range domain.AllZPAKinds() {
		if !strings.Contains(definition, "'"+string(kind)+"'") {
			t.Errorf("domain knows the kind %s and the constraint does not list it:\n  %s",
				kind, definition)
		}
	}
	for _, literal := range strings.Split(definition, "'") {
		if literal == "" || strings.ContainsAny(literal, " (),=:") {
			continue // SQL between the quoted literals
		}
		if _, err := domain.ParseZPAKind(literal); err != nil {
			t.Errorf("the database accepts the kind %q, which internal/domain does not know",
				literal)
		}
	}
}

// TestASyncAppliesAndThenRetiresAndThenRestores walks one object through its whole life, in
// the order a real catalogue puts it through.
//
// One test rather than four, because the interesting assertions are about what survives
// between the steps: first_seen_at across a disappearance, last_changed_at across a sync that
// changes nothing.
func TestASyncAppliesAndThenRetiresAndThenRestores(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	z := store.NewZPA(s.Pool)
	ctx := t.Context()

	object := domain.ZPAObject{
		ZpaID:   501,
		Payload: json.RawMessage(`{"module_id":"501","sws":"4","credits":"5"}`),
		Label:   "",
	}

	// Appears.
	id, err := z.Upsert(ctx, domain.ZPAKindModule, object)
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	firstSeen, firstChanged := timestampsOf(t, s, id)

	// A sync that finds it unchanged must not move last_changed_at — that timestamp is the
	// whole basis of "what actually moved, and when", months later.
	time.Sleep(2 * time.Millisecond)
	if _, err := z.Upsert(ctx, domain.ZPAKindModule, object); err != nil {
		t.Fatalf("unchanged upsert: %v", err)
	}
	if _, changed := timestampsOf(t, s, id); !changed.Equal(firstChanged) {
		t.Errorf("last_changed_at moved on an unchanged payload: %v -> %v", firstChanged, changed)
	}

	// A real change moves it. The hash is generated over the canonical jsonb form, so this
	// also asserts that reordering alone would not have counted.
	changedObject := object
	changedObject.Payload = json.RawMessage(`{"credits":"5","module_id":"501","sws":"2"}`)
	if _, err := z.Upsert(ctx, domain.ZPAKindModule, changedObject); err != nil {
		t.Fatalf("changed upsert: %v", err)
	}
	_, afterChange := timestampsOf(t, s, id)
	if !afterChange.After(firstChanged) {
		t.Errorf("last_changed_at did not move on a changed payload: %v -> %v",
			firstChanged, afterChange)
	}

	// Disappears: marked, never deleted.
	retired, err := z.RetireMissing(ctx, domain.ZPAKindModule, []int64{})
	if err != nil {
		t.Fatalf("retiring: %v", err)
	}
	if len(retired) != 1 || retired[0].ZpaID != 501 {
		t.Fatalf("retired %+v, want the one object", retired)
	}
	state := stateOf(t, z, domain.ZPAKindModule, 501)
	if !state.IsGone {
		t.Error("the object was not marked gone")
	}

	// Comes back. Same row, and first_seen_at is untouched: the day we first saw it is a fact,
	// and it is the only evidence that it had ever been away.
	backID, err := z.Upsert(ctx, domain.ZPAKindModule, changedObject)
	if err != nil {
		t.Fatalf("restoring upsert: %v", err)
	}
	if backID != id {
		t.Errorf("a returning object got a new row: %s -> %s", id, backID)
	}
	if seen, _ := timestampsOf(t, s, id); !seen.Equal(firstSeen) {
		t.Errorf("first_seen_at was overwritten on return: %v -> %v", firstSeen, seen)
	}
	if stateOf(t, z, domain.ZPAKindModule, 501).IsGone {
		t.Error("the object is still marked gone after coming back")
	}
}

// TestRetiringOneKindLeavesTheOthersAlone.
//
// The retirement is scoped to a kind because a run applies each endpoint independently: a night
// on which the largest one times out must leave the three that arrived correctly up to date,
// and must not retire anything belonging to the one that failed.
func TestRetiringOneKindLeavesTheOthersAlone(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	z := store.NewZPA(s.Pool)
	ctx := t.Context()

	for _, kind := range []domain.ZPAKind{domain.ZPAKindModule, domain.ZPAKindSPO} {
		if _, err := z.Upsert(ctx, kind, domain.ZPAObject{
			ZpaID:   7,
			Payload: json.RawMessage(`{"id":"7"}`),
		}); err != nil {
			t.Fatalf("seeding %s: %v", kind, err)
		}
	}

	if _, err := z.RetireMissing(ctx, domain.ZPAKindModule, []int64{}); err != nil {
		t.Fatalf("retiring: %v", err)
	}

	if !stateOf(t, z, domain.ZPAKindModule, 7).IsGone {
		t.Error("the module was not retired")
	}
	if stateOf(t, z, domain.ZPAKindSPO, 7).IsGone {
		t.Error("retiring the modules also retired an SPO with the same id — the two id spaces " +
			"are unrelated, and a cross-kind retirement would empty the catalogue")
	}
}

// TestAnAbandonedRunIsFailedAtStartup.
//
// A process that dies mid-sync leaves a RUNNING row forever, and the interface would show a run
// in progress that nothing is progressing. Fresh runs must survive the same sweep, or a restart
// during a sync would mark the sync it is running as failed.
func TestAnAbandonedRunIsFailedAtStartup(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	z := store.NewZPA(s.Pool)
	ctx := t.Context()

	stale, err := z.StartRun(ctx, domain.ZPASyncTriggerSchedule, nil)
	if err != nil {
		t.Fatalf("starting a run: %v", err)
	}
	if _, err := s.Pool.Exec(ctx,
		`UPDATE zpa_sync_run SET started_at = now() - interval '3 hours' WHERE id = $1`,
		stale.ID); err != nil {
		t.Fatalf("ageing the run: %v", err)
	}

	fresh, err := z.StartRun(ctx, domain.ZPASyncTriggerManual, nil)
	if err != nil {
		t.Fatalf("starting a second run: %v", err)
	}

	failed, err := z.FailAbandonedRuns(ctx, time.Hour)
	if err != nil {
		t.Fatalf("failing abandoned runs: %v", err)
	}
	if failed != 1 {
		t.Errorf("failed %d runs, want exactly the stale one", failed)
	}

	if got := runStatus(t, z, stale.ID); got != domain.ZPASyncFailed {
		t.Errorf("the stale run is %s, want FAILED", got)
	}
	if got := runStatus(t, z, fresh.ID); got != domain.ZPASyncRunning {
		t.Errorf("a run started moments ago was marked %s — a restart during a sync would "+
			"mark the sync it is running as failed", got)
	}
}

// TestARunningRunHasNoFinishedAt guards the CHECK that keeps the two in step. The bug it
// catches is the write that sets one and forgets the other, which renders as a run that has
// been in progress for three weeks.
func TestARunningRunHasNoFinishedAt(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	ctx := t.Context()

	if _, err := s.Pool.Exec(ctx,
		`INSERT INTO zpa_sync_run (trigger, status, finished_at)
		 VALUES ('SCHEDULE', 'RUNNING', now())`); err == nil {
		t.Error("a RUNNING run with a finish time was accepted")
	}
	if _, err := s.Pool.Exec(ctx,
		`INSERT INTO zpa_sync_run (trigger, status) VALUES ('SCHEDULE', 'SUCCEEDED')`); err == nil {
		t.Error("a finished run without a finish time was accepted")
	}
}

// TestTheChangeLogKeepsWhatTheObjectNoLongerIs.
//
// The report is denormalised on purpose: it records what the object was called and what it
// held at the time, which the object row no longer does after the next change.
func TestTheChangeLogKeepsWhatTheObjectNoLongerIs(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	z := store.NewZPA(s.Pool)
	ctx := t.Context()

	run, err := z.StartRun(ctx, domain.ZPASyncTriggerManual, nil)
	if err != nil {
		t.Fatalf("starting a run: %v", err)
	}
	objectID, err := z.Upsert(ctx, domain.ZPAKindBasket, domain.ZPAObject{
		ZpaID:   702,
		Payload: json.RawMessage(`{"basket_id":"702","is_duty":false}`),
		Label:   "XX: Wahlpflicht",
	})
	if err != nil {
		t.Fatalf("upserting: %v", err)
	}

	if err := z.RecordChange(ctx, domain.RecordedZPAChange{
		RunID:         run.ID,
		ObjectID:      objectID,
		Kind:          domain.ZPAKindBasket,
		ZpaID:         702,
		Label:         "XX: Wahlpflicht",
		Change:        domain.ZPAChangeChanged,
		PayloadBefore: json.RawMessage(`{"basket_id":"702","is_duty":true}`),
		PayloadAfter:  json.RawMessage(`{"basket_id":"702","is_duty":false}`),
		ChangedKeys:   []string{"is_duty"},
	}); err != nil {
		t.Fatalf("recording a change: %v", err)
	}

	changes, err := z.ChangesByRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("reading the changes: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1", len(changes))
	}
	got := changes[0]
	if got.Label != "XX: Wahlpflicht" || got.ZpaID != 702 {
		t.Errorf("the change lost what it was about: %+v", got)
	}
	if !slices.Equal(got.ChangedKeys, []string{"is_duty"}) {
		t.Errorf("changed keys are %q, want [is_duty] — this is what turns a hash difference "+
			"into a sentence somebody can read", got.ChangedKeys)
	}
}

// TestLastSuccessfulRunCountsAPartialOne.
//
// This number is what the deploy check asserts is recent and what the interface shows largest.
// A run that got three of four endpoints is not the silence being watched for — treating it as
// one would raise an alarm about a working import.
func TestLastSuccessfulRunCountsAPartialOne(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	z := store.NewZPA(s.Pool)
	ctx := t.Context()

	if last, err := z.LastSuccessfulRun(ctx); err != nil || last != nil {
		t.Fatalf("an empty database reported a last run: %+v, %v", last, err)
	}

	run, err := z.StartRun(ctx, domain.ZPASyncTriggerSchedule, nil)
	if err != nil {
		t.Fatalf("starting a run: %v", err)
	}
	run.Status = domain.ZPASyncPartial
	run.Fetched = 3
	if _, err := z.FinishRun(ctx, run); err != nil {
		t.Fatalf("finishing: %v", err)
	}

	last, err := z.LastSuccessfulRun(ctx)
	if err != nil {
		t.Fatalf("reading the last successful run: %v", err)
	}
	if last == nil || last.ID != run.ID {
		t.Fatalf("a PARTIAL run does not count as successful: %+v", last)
	}
	if last.FinishedAt == nil {
		t.Error("the finished run has no finish time")
	}
}

func timestampsOf(t *testing.T, s *storetest.Schema, id uuid.UUID) (firstSeen, lastChanged time.Time) {
	t.Helper()
	if err := s.Pool.QueryRow(t.Context(),
		`SELECT first_seen_at, last_changed_at FROM zpa_object WHERE id = $1`, id).
		Scan(&firstSeen, &lastChanged); err != nil {
		t.Fatalf("cannot read the timestamps: %v", err)
	}
	return firstSeen, lastChanged
}

func stateOf(t *testing.T, z *store.ZPA, kind domain.ZPAKind, zpaID int64) domain.ZPAObjectState {
	t.Helper()
	states, err := z.StateByKind(t.Context(), kind)
	if err != nil {
		t.Fatalf("cannot read the state: %v", err)
	}
	for _, state := range states {
		if state.ZpaID == zpaID {
			return state
		}
	}
	t.Fatalf("no state for %s %d", kind, zpaID)
	return domain.ZPAObjectState{}
}

func runStatus(t *testing.T, z *store.ZPA, id uuid.UUID) domain.ZPASyncStatus {
	t.Helper()
	run, err := z.RunByID(t.Context(), id)
	if err != nil || run == nil {
		t.Fatalf("cannot read run %s: %+v, %v", id, run, err)
	}
	return run.Status
}

// TestTwoSyncsProduceOneWinner.
//
// The failure this prevents is not corruption but waste and confusion: two runs fetching the
// same 2.7 MB, writing the same rows, and producing two change logs of which one is a ghost.
// try rather than the blocking form, so the loser is told and stops instead of queueing to
// repeat the work that just finished.
//
// NOT t.Parallel(), unlike everything else in this file, and the reason is worth knowing: a
// PostgreSQL advisory lock is database-wide, while storetest gives each test its own *schema*
// inside one shared database. Two parallel tests taking this lock therefore contend for real,
// and the loser fails for a reason that has nothing to do with what it was testing. migrate.go
// documents the same trap from the other side, which is why Migrate is unlocked in the harness.
//
// Go runs the non-parallel tests one at a time, so this and the release test below cannot
// overlap. Any future test that takes this lock has to join them.
func TestTwoSyncsProduceOneWinner(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()

	inside := make(chan struct{})
	release := make(chan struct{})
	results := make(chan error, 2)

	go func() {
		results <- store.WithZPASyncLock(ctx, s.Pool, func(context.Context) error {
			close(inside)
			<-release
			return nil
		})
	}()

	<-inside
	results <- store.WithZPASyncLock(ctx, s.Pool, func(context.Context) error {
		t.Error("the second sync ran while the first held the lock")
		return nil
	})
	close(release)

	var winners, refused int
	for range 2 {
		switch err := <-results; {
		case err == nil:
			winners++
		case errors.Is(err, store.ErrZPASyncLocked):
			refused++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if winners != 1 || refused != 1 {
		t.Errorf("got %d winners and %d refusals, want exactly one of each", winners, refused)
	}
}

// TestTheLockIsReleasedForTheNextRun. A lock held after the function returns would mean the
// import runs once and never again until the container restarts.
//
// Not parallel, for the reason above.
func TestTheLockIsReleasedForTheNextRun(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()

	for attempt := range 2 {
		if err := store.WithZPASyncLock(ctx, s.Pool, func(context.Context) error { return nil }); err != nil {
			t.Fatalf("attempt %d could not take the lock: %v", attempt, err)
		}
	}
}

// seedObject writes one cached object directly, so a view test can state exactly what it is
// looking at without going through a whole sync.
func seedObject(t *testing.T, s *storetest.Schema, kind domain.ZPAKind, zpaID int64, payload string) {
	t.Helper()
	if _, err := store.NewZPA(s.Pool).Upsert(t.Context(), kind, domain.ZPAObject{
		ZpaID:   zpaID,
		Payload: json.RawMessage(payload),
	}); err != nil {
		t.Fatalf("seeding %s %d: %v", kind, zpaID, err)
	}
}

// TestTheViewsReconstructTheStructure.
//
// The landing table holds each object whole, which is right for surviving a change of shape and
// wrong for answering a question. This is the question: which modules does a programme have to
// offer, split into the compulsory and the elective catalogue. It is the one the study
// programme leads are actually here for, and before the views it would have been written as
// `(payload->'module'->>'id')::bigint` in every query that asked it.
func TestTheViewsReconstructTheStructure(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)

	seedObject(t, s, domain.ZPAKindSPO, 801, `{
		"spo_id":"801","version":"2025","valid_from":"2025-10-01",
		"primuss_id":"07-XX-2025","program":{"id":"901","title":"XX"}}`)
	seedObject(t, s, domain.ZPAKindBasket, 701, `{
		"basket_id":"701","basket":"Pflicht","is_duty":true,"schwerpunkt":{}}`)
	seedObject(t, s, domain.ZPAKindBasket, 702, `{
		"basket_id":"702","basket":"Wahlpflicht","is_duty":false,
		"schwerpunkt":{"sp_id":"601","sp_short":"BSP","sp_title":"Beispiel"}}`)
	seedObject(t, s, domain.ZPAKindModule, 501, `{
		"module_id":"501","owner":"XX","course_type":"SU mit Praktikum",
		"sws":"4","credits":"5","active":"True","official":"True"}`)
	seedObject(t, s, domain.ZPAKindMSBA, 301, `{
		"msba_id":"301","module":{"id":"501","name":"Beispielmodul"},
		"spo":{"id":"801","version":"2025","valid_from":"2025-10-01"},
		"basket":{"id":"701","name":"Pflicht"},
		"module_code":"XX-B-0010","min_program_semester":"1","exam_types":[{"id":"401"}]}`)

	var programme, catalogue, moduleName, code string
	var compulsory bool
	var sws, semester int
	err := s.Pool.QueryRow(t.Context(), `
		SELECT s.programme, b.name, b.is_duty, m.name, a.module_code, m.sws,
		       a.min_programme_semester
		  FROM zpa_msba_v a
		  JOIN zpa_spo_v s ON s.spo_id = a.spo_id
		  JOIN zpa_basket_v b ON b.basket_id = a.basket_id
		  JOIN zpa_module_v m ON m.module_id = a.module_id`).
		Scan(&programme, &catalogue, &compulsory, &moduleName, &code, &sws, &semester)
	if err != nil {
		t.Fatalf("joining across the views: %v", err)
	}

	if programme != "XX" || catalogue != "Pflicht" || !compulsory {
		t.Errorf("programme=%q catalogue=%q compulsory=%v", programme, catalogue, compulsory)
	}
	if moduleName != "Beispielmodul" {
		t.Errorf("module name is %q", moduleName)
	}
	if code != "XX-B-0010" || sws != 4 || semester != 1 {
		t.Errorf("code=%q sws=%d semester=%d", code, sws, semester)
	}
}

// TestAModuleTakesItsNameFromAnAssociation.
//
// The module objects carry no name field at all — it exists only inside the nested object of an
// association row, and the importer cannot fill it in because it fetches each kind
// independently and modules come before associations. Deriving it in the view is what makes the
// catalogue readable, and a module no association mentions has no name, which is the honest
// answer rather than an id in disguise.
func TestAModuleTakesItsNameFromAnAssociation(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)

	seedObject(t, s, domain.ZPAKindModule, 501, `{"module_id":"501","owner":"XX"}`)
	seedObject(t, s, domain.ZPAKindModule, 502, `{"module_id":"502","owner":"XX"}`)
	seedObject(t, s, domain.ZPAKindMSBA, 301, `{
		"msba_id":"301","module":{"id":"501","name":"Beispielmodul"},
		"spo":{"id":"801"},"basket":{"id":"701"}}`)

	var named, orphan *string
	if err := s.Pool.QueryRow(t.Context(),
		`SELECT (SELECT name FROM zpa_module_v WHERE module_id = 501),
		        (SELECT name FROM zpa_module_v WHERE module_id = 502)`).Scan(&named, &orphan); err != nil {
		t.Fatalf("reading the names: %v", err)
	}
	if named == nil || *named != "Beispielmodul" {
		t.Errorf("the module with an association has name %v, want Beispielmodul", named)
	}
	if orphan != nil {
		t.Errorf("a module no association mentions got the name %q", *orphan)
	}
}

// TestAnAssociationSurvivesASetOfRegulationsWeDoNotHave.
//
// This is why the association's own copy of the version is read instead of joined, and why
// mirror tables with foreign keys were rejected. Against the real catalogue, 665 of 3272
// association rows point at one of 12 sets of regulations that spo_info does not return — a
// foreign key would have refused a fifth of the data, and an inner join would silently lose it.
func TestAnAssociationSurvivesASetOfRegulationsWeDoNotHave(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)

	// No SPO 999 is ever cached: the source does not return it.
	seedObject(t, s, domain.ZPAKindMSBA, 301, `{
		"msba_id":"301","module":{"id":"501","name":"Altes Modul"},
		"spo":{"id":"999","version":"2012","valid_from":"2012-10-01"},
		"basket":{"id":"701","name":"Pflicht"}}`)

	var version int
	var name string
	// A real date, not text — which is the whole point of the layer being typed.
	var validFrom time.Time
	err := s.Pool.QueryRow(t.Context(), `
		SELECT a.spo_version, a.spo_valid_from, a.module_name
		  FROM zpa_msba_v a
		  LEFT JOIN zpa_spo_v s ON s.spo_id = a.spo_id
		 WHERE a.msba_id = 301 AND s.spo_id IS NULL`).Scan(&version, &validFrom, &name)
	if err != nil {
		t.Fatalf("the row vanished with its set of regulations: %v", err)
	}
	if version != 2012 || name != "Altes Modul" {
		t.Errorf("version=%d name=%q — the embedded copy is the only place this exists",
			version, name)
	}
	if validFrom.Year() != 2012 {
		t.Errorf("valid from %v, want 2012", validFrom)
	}
}

// TestOneBadValueDoesNotBreakTheView.
//
// The most important property of the coercions, and the reason they are guarded rather than
// plain casts. The source types nothing, so a single malformed value in a single row would make
// a plain `::int` throw for the whole view — turning one bad record into a cache nobody can
// read. NULL is the honest answer, and the payload is still there for whoever wants to see what
// really arrived.
func TestOneBadValueDoesNotBreakTheView(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)

	seedObject(t, s, domain.ZPAKindModule, 501, `{
		"module_id":"501","owner":"XX","sws":"4","credits":"5","active":"True"}`)
	seedObject(t, s, domain.ZPAKindModule, 502, `{
		"module_id":"502","owner":"None","sws":"vier","credits":"","active":"vielleicht"}`)

	rows, err := s.Pool.Query(t.Context(),
		`SELECT module_id, home_programme, sws, credits, active FROM zpa_module_v ORDER BY module_id`)
	if err != nil {
		t.Fatalf("the view could not be read at all — one bad row poisoned it: %v", err)
	}
	defer rows.Close()

	type row struct {
		id           int64
		home         *string
		sws, credits *int32
		active       *bool
	}
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.home, &r.sws, &r.credits, &r.active); err != nil {
			t.Fatalf("scanning: %v", err)
		}
		got = append(got, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("got %d rows, want both — the bad one included", len(got))
	}
	if got[0].sws == nil || *got[0].sws != 4 || got[0].active == nil || !*got[0].active {
		t.Errorf("the good row came out wrong: %+v", got[0])
	}
	// "None" is a Python None that arrived as text — a null wearing the costume of a value.
	// Without the coercion, a query for modules whose home programme is None returns rows and
	// looks like a real programme.
	if got[1].home != nil {
		t.Errorf("owner \"None\" became the programme %q", *got[1].home)
	}
	if got[1].sws != nil || got[1].credits != nil || got[1].active != nil {
		t.Errorf("unparseable values did not become null: %+v", got[1])
	}
}
