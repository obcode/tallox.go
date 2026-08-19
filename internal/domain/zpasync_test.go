package domain_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/domain"
)

// The store is faked here and nowhere near the real rules.
//
// That is not in tension with "do not mock the database": the rule exists because the wish
// filter is a WHERE clause and "no aggregates before publication" is what COUNT does with it,
// so a fake passes while the shipped query leaks. Nothing of that shape is being tested here.
// What the SQL does — the upsert, the retirement, the timestamps — is tested against a real
// PostgreSQL in internal/store. What is tested here is the decision the service makes about
// each object, which is Go and has no SQL in it at all.

type fakeZPASource struct {
	objects map[domain.ZPAKind][]domain.ZPAObject
	fail    map[domain.ZPAKind]error
	calls   []domain.ZPAKind
}

func (f *fakeZPASource) Fetch(_ context.Context, kind domain.ZPAKind) ([]domain.ZPAObject, error) {
	f.calls = append(f.calls, kind)
	if err, failing := f.fail[kind]; failing {
		return nil, err
	}
	return f.objects[kind], nil
}

type fakeZPAStore struct {
	run      domain.ZPASyncRun
	kinds    []domain.ZPASyncRunKind
	held     map[domain.ZPAKind]map[int64]heldObject
	changes  []domain.RecordedZPAChange
	retired  map[domain.ZPAKind][]int64
	lastRun  *domain.ZPASyncRun
	upserted []domain.ZPAObject
}

type heldObject struct {
	id      uuid.UUID
	payload json.RawMessage
	gone    bool
}

func newFakeZPAStore() *fakeZPAStore {
	return &fakeZPAStore{
		held:    map[domain.ZPAKind]map[int64]heldObject{},
		retired: map[domain.ZPAKind][]int64{},
	}
}

func (f *fakeZPAStore) StartRun(_ context.Context, trigger domain.ZPASyncTrigger, by *uuid.UUID) (domain.ZPASyncRun, error) {
	f.run = domain.ZPASyncRun{
		ID: uuid.New(), Trigger: trigger, StartedBy: by,
		StartedAt: time.Now(), Status: domain.ZPASyncRunning,
	}
	return f.run, nil
}

func (f *fakeZPAStore) FinishRun(_ context.Context, run domain.ZPASyncRun) (domain.ZPASyncRun, error) {
	finished := time.Now()
	run.FinishedAt = &finished
	f.run = run
	return run, nil
}

func (f *fakeZPAStore) RecordRunKind(_ context.Context, _ uuid.UUID, kind domain.ZPASyncRunKind) error {
	f.kinds = append(f.kinds, kind)
	return nil
}

func (f *fakeZPAStore) StateByKind(_ context.Context, kind domain.ZPAKind) ([]domain.ZPAObjectState, error) {
	states := make([]domain.ZPAObjectState, 0, len(f.held[kind]))
	for zpaID, held := range f.held[kind] {
		states = append(states, domain.ZPAObjectState{ZpaID: zpaID, IsGone: held.gone})
	}
	return states, nil
}

func (f *fakeZPAStore) PayloadOf(_ context.Context, kind domain.ZPAKind, zpaID int64) (uuid.UUID, json.RawMessage, error) {
	held, present := f.held[kind][zpaID]
	if !present {
		return uuid.Nil, nil, nil
	}
	return held.id, held.payload, nil
}

func (f *fakeZPAStore) Upsert(_ context.Context, kind domain.ZPAKind, object domain.ZPAObject) (uuid.UUID, error) {
	f.upserted = append(f.upserted, object)
	if f.held[kind] == nil {
		f.held[kind] = map[int64]heldObject{}
	}
	held, present := f.held[kind][object.ZpaID]
	if !present {
		held.id = uuid.New()
	}
	held.payload, held.gone = object.Payload, false
	f.held[kind][object.ZpaID] = held
	return held.id, nil
}

func (f *fakeZPAStore) RetireMissing(_ context.Context, kind domain.ZPAKind, present []int64) ([]domain.RetiredZPAObject, error) {
	var retired []domain.RetiredZPAObject
	for zpaID, held := range f.held[kind] {
		if held.gone || slices.Contains(present, zpaID) {
			continue
		}
		held.gone = true
		f.held[kind][zpaID] = held
		f.retired[kind] = append(f.retired[kind], zpaID)
		retired = append(retired, domain.RetiredZPAObject{ID: held.id, ZpaID: zpaID, Payload: held.payload})
	}
	return retired, nil
}

func (f *fakeZPAStore) RecordChange(_ context.Context, change domain.RecordedZPAChange) error {
	f.changes = append(f.changes, change)
	return nil
}

func (f *fakeZPAStore) Runs(context.Context, int) ([]domain.ZPASyncRun, error) { return nil, nil }

func (f *fakeZPAStore) RunByID(context.Context, uuid.UUID) (*domain.ZPASyncRun, error) {
	return nil, nil //nolint:nilnil // matches the store contract
}

func (f *fakeZPAStore) LastSuccessfulRun(context.Context) (*domain.ZPASyncRun, error) {
	return f.lastRun, nil
}

func (f *fakeZPAStore) ChangesByRun(context.Context, uuid.UUID) ([]domain.ZPAChange, error) {
	return nil, nil
}

func object(id int64, payload string) domain.ZPAObject {
	return domain.ZPAObject{ZpaID: id, Payload: json.RawMessage(payload)}
}

// TestASyncRecordsOnlyWhatActuallyMoved.
//
// The ordinary night is one on which nothing changed, and it has to produce an empty report.
// A sync that logged every object it saw would bury the four that matter under 3861 that did
// not — and the whole purpose of the change log is to be readable at breakfast.
func TestASyncRecordsOnlyWhatActuallyMoved(t *testing.T) {
	t.Parallel()

	store := newFakeZPAStore()
	source := &fakeZPASource{objects: map[domain.ZPAKind][]domain.ZPAObject{
		domain.ZPAKindSPO: {object(801, `{"spo_id":"801","version":"2025"}`)},
	}}
	service := domain.NewZPASyncService(store, source, nil)

	if _, err := service.Sync(t.Context(), domain.ZPASyncTriggerSchedule, nil); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if len(store.changes) != 1 || store.changes[0].Change != domain.ZPAChangeAppeared {
		t.Fatalf("first sync recorded %+v, want one APPEARED", store.changes)
	}

	store.changes = nil
	if _, err := service.Sync(t.Context(), domain.ZPASyncTriggerSchedule, nil); err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if len(store.changes) != 0 {
		t.Errorf("an unchanged sync recorded %+v, want nothing", store.changes)
	}
}

// TestReorderedKeysAreNotAChange.
//
// The stored side has been through jsonb and the fetched side has not, so their key order
// differs for reasons that are not changes. Comparing bytes would report the entire catalogue
// as changed on the first run after any serialiser change on either side — 3861 change-log
// entries and a mail saying everything moved.
func TestReorderedKeysAreNotAChange(t *testing.T) {
	t.Parallel()

	store := newFakeZPAStore()
	source := &fakeZPASource{objects: map[domain.ZPAKind][]domain.ZPAObject{
		domain.ZPAKindSPO: {object(801, `{"spo_id":"801","version":"2025"}`)},
	}}
	service := domain.NewZPASyncService(store, source, nil)

	if _, err := service.Sync(t.Context(), domain.ZPASyncTriggerSchedule, nil); err != nil {
		t.Fatalf("first sync: %v", err)
	}

	store.changes = nil
	source.objects[domain.ZPAKindSPO] = []domain.ZPAObject{
		object(801, `{"version":"2025",  "spo_id":"801"}`),
	}
	if _, err := service.Sync(t.Context(), domain.ZPASyncTriggerSchedule, nil); err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if len(store.changes) != 0 {
		t.Errorf("reordering the keys was recorded as a change: %+v", store.changes)
	}
}

// TestAFailedKindRetiresNothing is the rule that keeps one bad night from emptying the
// catalogue.
//
// Retirement means "a successful fetch did not mention this". If a failed fetch could retire,
// an outage would mark everything gone — with a change-log entry per module, which is exactly
// what a deliberate deletion looks like.
func TestAFailedKindRetiresNothing(t *testing.T) {
	t.Parallel()

	store := newFakeZPAStore()
	source := &fakeZPASource{objects: map[domain.ZPAKind][]domain.ZPAObject{
		domain.ZPAKindSPO:    {object(801, `{"spo_id":"801"}`)},
		domain.ZPAKindModule: {object(501, `{"module_id":"501"}`)},
	}}
	service := domain.NewZPASyncService(store, source, nil)

	if _, err := service.Sync(t.Context(), domain.ZPASyncTriggerSchedule, nil); err != nil {
		t.Fatalf("seeding sync: %v", err)
	}

	// Now the modules endpoint is down and the SPOs legitimately lost one.
	source.fail = map[domain.ZPAKind]error{domain.ZPAKindModule: errors.New("service unavailable")}
	source.objects[domain.ZPAKindSPO] = nil
	source.objects[domain.ZPAKindSPO] = []domain.ZPAObject{object(802, `{"spo_id":"802"}`)}

	run, err := service.Sync(t.Context(), domain.ZPASyncTriggerSchedule, nil)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}

	if len(store.retired[domain.ZPAKindModule]) != 0 {
		t.Errorf("a failed endpoint retired %v — an outage would empty the catalogue",
			store.retired[domain.ZPAKindModule])
	}
	if !slices.Contains(store.retired[domain.ZPAKindSPO], int64(801)) {
		t.Error("the endpoint that succeeded did not retire what it stopped mentioning")
	}
	if run.Status != domain.ZPASyncPartial {
		t.Errorf("run status is %s, want PARTIAL — three of four endpoints is real progress "+
			"and must not read as total failure", run.Status)
	}
	if run.Error == "" {
		t.Error("a partial run carries no error text, so nothing says which endpoint failed")
	}
}

// TestEveryEndpointIsTriedEvenAfterOneFails. One outage must not hide the state of the others.
func TestEveryEndpointIsTriedEvenAfterOneFails(t *testing.T) {
	t.Parallel()

	source := &fakeZPASource{
		objects: map[domain.ZPAKind][]domain.ZPAObject{},
		fail:    map[domain.ZPAKind]error{domain.ZPAKindSPO: errors.New("down")},
	}
	for _, kind := range domain.AllZPAKinds() {
		source.objects[kind] = []domain.ZPAObject{object(1, `{"id":"1"}`)}
	}

	service := domain.NewZPASyncService(newFakeZPAStore(), source, nil)
	run, err := service.Sync(t.Context(), domain.ZPASyncTriggerSchedule, nil)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}

	if !slices.Equal(source.calls, domain.AllZPAKinds()) {
		t.Errorf("fetched %v, want every kind in order", source.calls)
	}
	if len(run.Kinds) != len(domain.AllZPAKinds()) {
		t.Errorf("the run reports %d endpoints, want %d", len(run.Kinds), len(domain.AllZPAKinds()))
	}
}

// TestAllEndpointsFailingIsAFailureNotAPartial. PARTIAL means some progress; nothing applied
// is a failure, and the two are read very differently on the page.
func TestAllEndpointsFailingIsAFailureNotAPartial(t *testing.T) {
	t.Parallel()

	source := &fakeZPASource{fail: map[domain.ZPAKind]error{}}
	for _, kind := range domain.AllZPAKinds() {
		source.fail[kind] = errors.New("down")
	}

	run, err := domain.NewZPASyncService(newFakeZPAStore(), source, nil).
		Sync(t.Context(), domain.ZPASyncTriggerSchedule, nil)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if run.Status != domain.ZPASyncFailed {
		t.Errorf("run status is %s, want FAILED", run.Status)
	}
}

// TestAReturningObjectIsReportedAsSuchAndNotAsNew.
//
// "This module is back in the catalogue" and "this module is new" are different pieces of
// news, and only one of them should make a study-programme lead look twice.
func TestAReturningObjectIsReportedAsSuchAndNotAsNew(t *testing.T) {
	t.Parallel()

	store := newFakeZPAStore()
	source := &fakeZPASource{objects: map[domain.ZPAKind][]domain.ZPAObject{
		domain.ZPAKindSPO: {object(801, `{"spo_id":"801"}`)},
	}}
	service := domain.NewZPASyncService(store, source, nil)

	if _, err := service.Sync(t.Context(), domain.ZPASyncTriggerSchedule, nil); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	source.objects[domain.ZPAKindSPO] = []domain.ZPAObject{object(802, `{"spo_id":"802"}`)}
	if _, err := service.Sync(t.Context(), domain.ZPASyncTriggerSchedule, nil); err != nil {
		t.Fatalf("second sync: %v", err)
	}

	store.changes = nil
	source.objects[domain.ZPAKindSPO] = []domain.ZPAObject{
		object(801, `{"spo_id":"801"}`), object(802, `{"spo_id":"802"}`),
	}
	if _, err := service.Sync(t.Context(), domain.ZPASyncTriggerSchedule, nil); err != nil {
		t.Fatalf("third sync: %v", err)
	}

	if len(store.changes) != 1 {
		t.Fatalf("recorded %+v, want one change", store.changes)
	}
	if store.changes[0].Change != domain.ZPAChangeReappeared {
		t.Errorf("a returning object was recorded as %s, want REAPPEARED", store.changes[0].Change)
	}
}

// TestAChangeNamesTheKeysThatMoved. Without this the report says "something changed", which is
// the amount of information a hash already carried.
func TestAChangeNamesTheKeysThatMoved(t *testing.T) {
	t.Parallel()

	store := newFakeZPAStore()
	source := &fakeZPASource{objects: map[domain.ZPAKind][]domain.ZPAObject{
		domain.ZPAKindModule: {object(501, `{"module_id":"501","sws":"4","credits":"5"}`)},
	}}
	service := domain.NewZPASyncService(store, source, nil)

	if _, err := service.Sync(t.Context(), domain.ZPASyncTriggerSchedule, nil); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	// An appearance names no keys: listing every field of a new object is noise.
	if got := store.changes[0].ChangedKeys; len(got) != 0 {
		t.Errorf("an appearance named the keys %q", got)
	}

	store.changes = nil
	source.objects[domain.ZPAKindModule] = []domain.ZPAObject{
		object(501, `{"module_id":"501","sws":"2","credits":"5"}`),
	}
	if _, err := service.Sync(t.Context(), domain.ZPASyncTriggerSchedule, nil); err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if len(store.changes) != 1 {
		t.Fatalf("recorded %+v, want one change", store.changes)
	}
	if !slices.Equal(store.changes[0].ChangedKeys, []string{"sws"}) {
		t.Errorf("changed keys are %q, want [sws]", store.changes[0].ChangedKeys)
	}
}

// TestAnUnconfiguredImportRefusesToRunAndStillReads.
//
// Every DevContainer and every CI run is in this state. Reading has to keep working, because
// the page that says "not configured" is itself a read.
func TestAnUnconfiguredImportRefusesToRunAndStillReads(t *testing.T) {
	t.Parallel()

	service := domain.NewZPASyncService(newFakeZPAStore(), nil, nil)

	if service.Configured() {
		t.Error("a service with no source reports itself configured")
	}
	if _, err := service.Sync(t.Context(), domain.ZPASyncTriggerManual, nil); !errors.Is(err, domain.ErrZPANotConfigured) {
		t.Errorf("got %v, want ErrZPANotConfigured", err)
	}
	if _, err := service.Runs(t.Context(), 10); err != nil {
		t.Errorf("reading the runs of an unconfigured import failed: %v", err)
	}
}

// TestAManualSyncIsRefusedRightAfterASuccessfulOne.
//
// A refusal, not a silent success: the caller asked for a new run and did not get one, and
// answering "done" is how a button teaches people that it does nothing. The interval is a
// judgement about being a good neighbour to another institution's system, so it lives beside
// its reasoning in Go and not in a configuration key that would duplicate it.
func TestAManualSyncIsRefusedRightAfterASuccessfulOne(t *testing.T) {
	t.Parallel()

	store := newFakeZPAStore()
	service := domain.NewZPASyncService(store, &fakeZPASource{}, nil)

	if err := service.MayStartManualSync(t.Context()); err != nil {
		t.Errorf("the first ever sync was refused: %v", err)
	}

	justNow := time.Now()
	store.lastRun = &domain.ZPASyncRun{Status: domain.ZPASyncSucceeded, FinishedAt: &justNow}
	if err := service.MayStartManualSync(t.Context()); !errors.Is(err, domain.ErrZPASyncedRecently) {
		t.Errorf("got %v, want ErrZPASyncedRecently", err)
	}

	older := time.Now().Add(-domain.MinimumZPASyncInterval - time.Minute)
	store.lastRun = &domain.ZPASyncRun{Status: domain.ZPASyncSucceeded, FinishedAt: &older}
	if err := service.MayStartManualSync(t.Context()); err != nil {
		t.Errorf("a sync after the interval was refused: %v", err)
	}
}
