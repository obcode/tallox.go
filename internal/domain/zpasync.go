package domain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// The refusals, as sentinels with a code the interface branches on.
var (
	// ErrZPANotConfigured is the answer when tallox.yaml carries no zpa block. A state, not a
	// fault: every DevContainer and every CI run is in it.
	ErrZPANotConfigured = errors.New("der ZPA-Import ist nicht konfiguriert")
	// ErrZPASyncRunning is returned with the run that is already in progress, so the caller can
	// show it rather than being told to try again.
	ErrZPASyncRunning = errors.New("ein Abgleich läuft bereits")
	// ErrZPASyncedRecently refuses a manual sync that would repeat one from minutes ago.
	//
	// A refusal rather than a silent success: the caller asked for a new run and did not get
	// one, and answering "done" to that is how a button teaches people that it does nothing.
	ErrZPASyncedRecently = errors.New("der letzte Abgleich ist noch keine zehn Minuten her")
)

// MinimumZPASyncInterval is how long a manual sync waits after a successful one.
//
// Here and not in tallox.yaml. A configuration key that duplicates a constant is a second
// truth, and this number is a judgement about being a good neighbour to another institution's
// system rather than something that differs between environments. Ten minutes is a guess made
// deliberately loose; the runs are recorded, so it can be revisited against evidence.
const MinimumZPASyncInterval = 10 * time.Minute

// ZPASyncTrigger says what started a run.
type ZPASyncTrigger string

const (
	// ZPASyncTriggerSchedule is the nightly job on the host.
	ZPASyncTriggerSchedule ZPASyncTrigger = "SCHEDULE"
	// ZPASyncTriggerManual is somebody pressing the button.
	ZPASyncTriggerManual ZPASyncTrigger = "MANUAL"
)

// ZPASyncStatus is how a run ended.
type ZPASyncStatus string

const (
	// ZPASyncRunning is the state a run is written in, before its first fetch.
	ZPASyncRunning ZPASyncStatus = "RUNNING"
	// ZPASyncSucceeded means every kind was fetched and applied.
	ZPASyncSucceeded ZPASyncStatus = "SUCCEEDED"
	// ZPASyncPartial means some kinds were applied and others failed. Deliberately not the same
	// as failure: three of four endpoints is real progress, and the kinds that did arrive are
	// correctly up to date.
	ZPASyncPartial ZPASyncStatus = "PARTIAL"
	// ZPASyncFailed means nothing was applied.
	ZPASyncFailed ZPASyncStatus = "FAILED"
)

// ZPAChangeType is what happened to one object in one run.
type ZPAChangeType string

const (
	// ZPAChangeAppeared is an object seen for the first time.
	ZPAChangeAppeared ZPAChangeType = "APPEARED"
	// ZPAChangeChanged is a different payload for an object already held.
	ZPAChangeChanged ZPAChangeType = "CHANGED"
	// ZPAChangeDisappeared is an object a successful fetch no longer mentions.
	ZPAChangeDisappeared ZPAChangeType = "DISAPPEARED"
	// ZPAChangeReappeared is one that had disappeared and is back.
	ZPAChangeReappeared ZPAChangeType = "REAPPEARED"
)

// ZPASyncRun is one run of the import.
type ZPASyncRun struct {
	ID          uuid.UUID
	Trigger     ZPASyncTrigger
	StartedBy   *uuid.UUID
	StartedAt   time.Time
	FinishedAt  *time.Time
	Status      ZPASyncStatus
	Fetched     int
	Appeared    int
	Changed     int
	Disappeared int
	Error       string
	Kinds       []ZPASyncRunKind
}

// ZPASyncRunKind is what happened to one endpoint within a run.
type ZPASyncRunKind struct {
	Kind    ZPAKind
	Status  ZPASyncStatus
	Fetched int
	Error   string
}

// ZPAChange is one line of the report.
type ZPAChange struct {
	ID          uuid.UUID
	RunID       uuid.UUID
	ObjectID    uuid.UUID
	Kind        ZPAKind
	ZpaID       int64
	Label       string
	Change      ZPAChangeType
	ChangedKeys []string
	DetectedAt  time.Time
}

// ZPAObjectState is what the cache holds for one object, without its payload.
//
// Enough to decide the diff. Fetching the payloads too would move the whole cache through the
// wire on every run to discover that nothing changed, which is the ordinary outcome.
type ZPAObjectState struct {
	ZpaID       int64
	ContentHash string
	IsGone      bool
}

// ZPAStore is the persistence the sync needs.
type ZPAStore interface {
	StartRun(ctx context.Context, trigger ZPASyncTrigger, startedBy *uuid.UUID) (ZPASyncRun, error)
	FinishRun(ctx context.Context, run ZPASyncRun) (ZPASyncRun, error)
	RecordRunKind(ctx context.Context, runID uuid.UUID, kind ZPASyncRunKind) error

	StateByKind(ctx context.Context, kind ZPAKind) ([]ZPAObjectState, error)
	PayloadOf(ctx context.Context, kind ZPAKind, zpaID int64) (uuid.UUID, json.RawMessage, error)
	Upsert(ctx context.Context, kind ZPAKind, object ZPAObject) (uuid.UUID, error)
	RetireMissing(ctx context.Context, kind ZPAKind, present []int64) ([]RetiredZPAObject, error)

	RecordChange(ctx context.Context, change RecordedZPAChange) error

	Runs(ctx context.Context, limit int) ([]ZPASyncRun, error)
	RunByID(ctx context.Context, id uuid.UUID) (*ZPASyncRun, error)
	LastSuccessfulRun(ctx context.Context) (*ZPASyncRun, error)
	ChangesByRun(ctx context.Context, runID uuid.UUID) ([]ZPAChange, error)
}

// RetiredZPAObject is an object a successful fetch stopped mentioning.
type RetiredZPAObject struct {
	ID      uuid.UUID
	ZpaID   int64
	Label   string
	Payload json.RawMessage
}

// RecordedZPAChange is one row of the change log, as the sync writes it.
type RecordedZPAChange struct {
	RunID         uuid.UUID
	ObjectID      uuid.UUID
	Kind          ZPAKind
	ZpaID         int64
	Label         string
	Change        ZPAChangeType
	PayloadBefore json.RawMessage
	PayloadAfter  json.RawMessage
	ChangedKeys   []string
}

// ZPASyncService runs the import.
//
// It is deliberately unaware of what started it. The nightly job on the host and the button in
// the interface call the same method with a different trigger, so there is no path through
// which one of them can behave differently from the other — which is the same argument the two
// authentication doors make about sharing one handler.
type ZPASyncService struct {
	store  ZPAStore
	source ZPASource
	now    func() time.Time
}

// NewZPASyncService wires the service. A nil source is the unconfigured case: every read still
// works, and starting a run refuses with ErrZPANotConfigured.
func NewZPASyncService(store ZPAStore, source ZPASource) *ZPASyncService {
	return &ZPASyncService{store: store, source: source, now: time.Now}
}

// Configured reports whether a sync can run at all.
func (s *ZPASyncService) Configured() bool { return s.source != nil }

// Sync fetches every kind and applies what it finds.
//
// The run record is written first and finished last, so a process that dies in between leaves
// evidence rather than silence.
//
// Each kind is independent: one that fails does not stop the others and does not retire
// anything of its own. That is why the result can be PARTIAL, and why a night on which the
// largest endpoint times out still leaves the three small ones correctly up to date instead of
// rolling everything back to yesterday.
func (s *ZPASyncService) Sync(ctx context.Context, trigger ZPASyncTrigger, startedBy *uuid.UUID) (ZPASyncRun, error) {
	if !s.Configured() {
		return ZPASyncRun{}, ErrZPANotConfigured
	}

	run, err := s.store.StartRun(ctx, trigger, startedBy)
	if err != nil {
		return ZPASyncRun{}, fmt.Errorf("cannot start a sync run: %w", err)
	}

	var succeeded, failed int
	for _, kind := range AllZPAKinds() {
		result := s.syncKind(ctx, run.ID, kind)
		run.Fetched += result.Fetched
		run.Appeared += result.appeared
		run.Changed += result.changed
		run.Disappeared += result.disappeared
		run.Kinds = append(run.Kinds, result.ZPASyncRunKind)

		if result.Status == ZPASyncSucceeded {
			succeeded++
		} else {
			failed++
			// The first failure is the one worth showing: the later ones are often the same
			// outage seen again, and a message concatenating four of them is read by nobody.
			if run.Error == "" {
				run.Error = fmt.Sprintf("%s: %s", kind, result.Error)
			}
		}

		if err := s.store.RecordRunKind(ctx, run.ID, result.ZPASyncRunKind); err != nil {
			return run, fmt.Errorf("cannot record the result for %s: %w", kind, err)
		}
	}

	switch {
	case failed == 0:
		run.Status = ZPASyncSucceeded
	case succeeded == 0:
		run.Status = ZPASyncFailed
	default:
		run.Status = ZPASyncPartial
	}

	finished, err := s.store.FinishRun(ctx, run)
	if err != nil {
		return run, fmt.Errorf("cannot finish the sync run: %w", err)
	}
	finished.Kinds = run.Kinds
	return finished, nil
}

type kindResult struct {
	ZPASyncRunKind
	appeared    int
	changed     int
	disappeared int
}

func (s *ZPASyncService) syncKind(ctx context.Context, runID uuid.UUID, kind ZPAKind) kindResult {
	result := kindResult{ZPASyncRunKind: ZPASyncRunKind{Kind: kind, Status: ZPASyncSucceeded}}

	// The counts are kept, not zeroed. A fetch that fails has none anyway, but a store error
	// can arrive after a hundred objects are already written — and a report claiming nothing
	// was fetched, while the change log shows a hundred entries, is worse than a partial
	// number. It is also the one case where the retirement below is correctly skipped: a kind
	// only ever retires what a *complete* successful pass did not mention.
	fail := func(err error) kindResult {
		result.Status = ZPASyncFailed
		result.Error = err.Error()
		return result
	}

	objects, err := s.source.Fetch(ctx, kind)
	if err != nil {
		return fail(err)
	}
	result.Fetched = len(objects)

	before, err := s.store.StateByKind(ctx, kind)
	if err != nil {
		return fail(err)
	}
	known := make(map[int64]ZPAObjectState, len(before))
	for _, state := range before {
		known[state.ZpaID] = state
	}

	present := make([]int64, 0, len(objects))
	for _, object := range objects {
		present = append(present, object.ZpaID)

		previous, seenBefore := known[object.ZpaID]

		// The payload is read back only for an object that might have changed. On an ordinary
		// night nothing has, and this is what keeps the run from moving the whole cache twice.
		var payloadBefore json.RawMessage
		if seenBefore {
			_, payloadBefore, err = s.store.PayloadOf(ctx, kind, object.ZpaID)
			if err != nil {
				return fail(err)
			}
		}

		objectID, err := s.store.Upsert(ctx, kind, object)
		if err != nil {
			return fail(err)
		}

		change, interesting := classify(seenBefore, previous, payloadBefore, object)
		if !interesting {
			continue
		}

		if err := s.store.RecordChange(ctx, RecordedZPAChange{
			RunID:         runID,
			ObjectID:      objectID,
			Kind:          kind,
			ZpaID:         object.ZpaID,
			Label:         object.Label,
			Change:        change,
			PayloadBefore: payloadBefore,
			PayloadAfter:  object.Payload,
			ChangedKeys:   changedKeysFor(change, payloadBefore, object.Payload),
		}); err != nil {
			return fail(err)
		}

		switch change {
		case ZPAChangeAppeared, ZPAChangeReappeared:
			result.appeared++
		case ZPAChangeChanged:
			result.changed++
		case ZPAChangeDisappeared:
		}
	}

	// Only reached when the fetch above succeeded and returned something — the client refuses
	// an empty result precisely so that one bad night cannot retire a whole catalogue.
	retired, err := s.store.RetireMissing(ctx, kind, present)
	if err != nil {
		return fail(err)
	}
	for _, object := range retired {
		if err := s.store.RecordChange(ctx, RecordedZPAChange{
			RunID:         runID,
			ObjectID:      object.ID,
			Kind:          kind,
			ZpaID:         object.ZpaID,
			Label:         object.Label,
			Change:        ZPAChangeDisappeared,
			PayloadBefore: object.Payload,
			ChangedKeys:   []string{},
		}); err != nil {
			return fail(err)
		}
		result.disappeared++
	}

	return result
}

// classify decides what kind of change an object represents, if any.
func classify(seenBefore bool, previous ZPAObjectState, payloadBefore json.RawMessage, object ZPAObject) (ZPAChangeType, bool) {
	switch {
	case !seenBefore:
		return ZPAChangeAppeared, true
	case previous.IsGone:
		// Back after an absence. Reported as its own kind rather than as a change, because
		// "this module is in the catalogue again" and "this module's credits moved" are
		// different pieces of news.
		return ZPAChangeReappeared, true
	case !sameJSON(payloadBefore, object.Payload):
		return ZPAChangeChanged, true
	default:
		return "", false
	}
}

func changedKeysFor(change ZPAChangeType, before, after json.RawMessage) []string {
	if change != ZPAChangeChanged {
		// Naming every key of a new object is noise, not information.
		return []string{}
	}
	return ChangedKeys(before, after)
}

// sameJSON compares two payloads by value rather than by bytes.
//
// The stored side has been through jsonb and the fetched side has not, so their key order and
// whitespace differ for reasons that are not changes. Comparing bytes would report the entire
// catalogue as changed on the first run after any serialiser change on either side.
func sameJSON(a, b json.RawMessage) bool {
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		return false
	}
	ac, err := json.Marshal(av)
	if err != nil {
		return false
	}
	bc, err := json.Marshal(bv)
	if err != nil {
		return false
	}
	return string(ac) == string(bc)
}

// Runs lists the recent runs, newest first.
func (s *ZPASyncService) Runs(ctx context.Context, limit int) ([]ZPASyncRun, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	return s.store.Runs(ctx, limit)
}

// RunByID returns one run with its per-endpoint results, or (nil, nil).
func (s *ZPASyncService) RunByID(ctx context.Context, id uuid.UUID) (*ZPASyncRun, error) {
	return s.store.RunByID(ctx, id)
}

// LastSuccessfulRun is the number the interface shows largest and the deploy check asserts is
// recent. The failure this import will actually have is not a wrong diff — it is a job that
// quietly stopped weeks ago.
func (s *ZPASyncService) LastSuccessfulRun(ctx context.Context) (*ZPASyncRun, error) {
	return s.store.LastSuccessfulRun(ctx)
}

// Changes lists what a run changed.
func (s *ZPASyncService) Changes(ctx context.Context, runID uuid.UUID) ([]ZPAChange, error) {
	return s.store.ChangesByRun(ctx, runID)
}

// MayStartManualSync reports whether a manual run is allowed yet.
//
// Separate from Sync so the interface can grey out the button for the same reason the server
// would refuse it, without the two holding different opinions about how long the interval is.
func (s *ZPASyncService) MayStartManualSync(ctx context.Context) error {
	if !s.Configured() {
		return ErrZPANotConfigured
	}
	last, err := s.store.LastSuccessfulRun(ctx)
	if err != nil {
		return err
	}
	if last == nil || last.FinishedAt == nil {
		return nil
	}
	if s.now().Sub(*last.FinishedAt) < MinimumZPASyncInterval {
		return ErrZPASyncedRecently
	}
	return nil
}
