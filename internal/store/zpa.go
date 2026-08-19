package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/obcode/tallox.go/internal/domain"
)

// ZPA is the persistence behind domain.ZPASyncService.
type ZPA struct {
	q *Queries
}

// NewZPA binds the cache queries to a pool or transaction.
func NewZPA(db DBTX) *ZPA { return &ZPA{q: New(db)} }

var _ domain.ZPAStore = (*ZPA)(nil)

func runFrom(row ZpaSyncRun) domain.ZPASyncRun {
	run := domain.ZPASyncRun{
		ID:          row.ID,
		Trigger:     domain.ZPASyncTrigger(row.Trigger),
		StartedAt:   row.StartedAt,
		Status:      domain.ZPASyncStatus(row.Status),
		Fetched:     int(row.Fetched),
		Appeared:    int(row.Appeared),
		Changed:     int(row.Changed),
		Disappeared: int(row.Disappeared),
	}
	if row.StartedBy.Valid {
		id := row.StartedBy.UUID
		run.StartedBy = &id
	}
	if row.FinishedAt.Valid {
		finished := row.FinishedAt.Time
		run.FinishedAt = &finished
	}
	if row.Error != nil {
		run.Error = *row.Error
	}
	return run
}

// StartRun writes the run before the first fetch, so a crash leaves evidence.
func (z *ZPA) StartRun(ctx context.Context, trigger domain.ZPASyncTrigger, startedBy *uuid.UUID) (domain.ZPASyncRun, error) {
	params := StartZPASyncRunParams{Trigger: string(trigger)}
	if startedBy != nil {
		params.StartedBy = uuid.NullUUID{UUID: *startedBy, Valid: true}
	}

	row, err := z.q.StartZPASyncRun(ctx, params)
	if err != nil {
		return domain.ZPASyncRun{}, fmt.Errorf("cannot start a sync run: %w", err)
	}
	return runFrom(row), nil
}

// FinishRun records the outcome and the counts.
func (z *ZPA) FinishRun(ctx context.Context, run domain.ZPASyncRun) (domain.ZPASyncRun, error) {
	params := FinishZPASyncRunParams{
		ID:          run.ID,
		Status:      string(run.Status),
		Fetched:     int32(run.Fetched),     //nolint:gosec // counts are bounded by the catalogue
		Appeared:    int32(run.Appeared),    //nolint:gosec // ditto
		Changed:     int32(run.Changed),     //nolint:gosec // ditto
		Disappeared: int32(run.Disappeared), //nolint:gosec // ditto
	}
	if run.Error != "" {
		message := run.Error
		params.Error = &message
	}

	row, err := z.q.FinishZPASyncRun(ctx, params)
	if err != nil {
		return domain.ZPASyncRun{}, fmt.Errorf("cannot finish the sync run: %w", err)
	}
	return runFrom(row), nil
}

// RecordRunKind stores what happened to one endpoint.
func (z *ZPA) RecordRunKind(ctx context.Context, runID uuid.UUID, kind domain.ZPASyncRunKind) error {
	params := RecordZPASyncRunKindParams{
		RunID:   runID,
		Kind:    string(kind.Kind),
		Status:  string(kind.Status),
		Fetched: int32(kind.Fetched), //nolint:gosec // bounded by the catalogue
	}
	if kind.Error != "" {
		message := kind.Error
		params.Error = &message
	}
	if err := z.q.RecordZPASyncRunKind(ctx, params); err != nil {
		return fmt.Errorf("cannot record the result for %s: %w", kind.Kind, err)
	}
	return nil
}

// StateByKind returns what is held, without the payloads.
func (z *ZPA) StateByKind(ctx context.Context, kind domain.ZPAKind) ([]domain.ZPAObjectState, error) {
	rows, err := z.q.ZPAObjectStateByKind(ctx, string(kind))
	if err != nil {
		return nil, fmt.Errorf("cannot read the cached state for %s: %w", kind, err)
	}

	states := make([]domain.ZPAObjectState, 0, len(rows))
	for _, row := range rows {
		state := domain.ZPAObjectState{ZpaID: row.ZpaID, IsGone: row.IsGone}
		if row.ContentHash != nil {
			state.ContentHash = *row.ContentHash
		}
		states = append(states, state)
	}
	return states, nil
}

// PayloadOf reads one stored payload back.
func (z *ZPA) PayloadOf(ctx context.Context, kind domain.ZPAKind, zpaID int64) (uuid.UUID, json.RawMessage, error) {
	row, err := z.q.ZPAObjectPayload(ctx, ZPAObjectPayloadParams{Kind: string(kind), ZpaID: zpaID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Not found is (zero, nil, nil), as everywhere else here. The caller has just read
			// a state row saying it exists, so this is a race with a concurrent retire rather
			// than an error worth failing a whole kind for.
			return uuid.Nil, nil, nil
		}
		return uuid.Nil, nil, fmt.Errorf("cannot read the cached payload for %s %d: %w", kind, zpaID, err)
	}
	return row.ID, row.Payload, nil
}

// Upsert writes one object whole and returns its row id.
func (z *ZPA) Upsert(ctx context.Context, kind domain.ZPAKind, object domain.ZPAObject) (uuid.UUID, error) {
	params := UpsertZPAObjectParams{
		Kind:    string(kind),
		ZpaID:   object.ZpaID,
		Payload: object.Payload,
	}
	if object.Label != "" {
		label := object.Label
		params.Label = &label
	}

	row, err := z.q.UpsertZPAObject(ctx, params)
	if err != nil {
		return uuid.Nil, fmt.Errorf("cannot store %s %d: %w", kind, object.ZpaID, err)
	}
	return row.ID, nil
}

// RetireMissing marks what a successful fetch stopped mentioning.
func (z *ZPA) RetireMissing(ctx context.Context, kind domain.ZPAKind, present []int64) ([]domain.RetiredZPAObject, error) {
	rows, err := z.q.RetireMissingZPAObjects(ctx, RetireMissingZPAObjectsParams{
		Kind:    string(kind),
		Present: present,
	})
	if err != nil {
		return nil, fmt.Errorf("cannot retire missing %s objects: %w", kind, err)
	}

	retired := make([]domain.RetiredZPAObject, 0, len(rows))
	for _, row := range rows {
		object := domain.RetiredZPAObject{ID: row.ID, ZpaID: row.ZpaID, Payload: row.Payload}
		if row.Label != nil {
			object.Label = *row.Label
		}
		retired = append(retired, object)
	}
	return retired, nil
}

// RecordChange appends one line to the report.
func (z *ZPA) RecordChange(ctx context.Context, change domain.RecordedZPAChange) error {
	params := RecordZPAChangeParams{
		RunID:         change.RunID,
		ObjectID:      change.ObjectID,
		Kind:          string(change.Kind),
		ZpaID:         change.ZpaID,
		Change:        string(change.Change),
		PayloadBefore: change.PayloadBefore,
		PayloadAfter:  change.PayloadAfter,
		ChangedKeys:   change.ChangedKeys,
	}
	if change.Label != "" {
		label := change.Label
		params.Label = &label
	}
	if params.ChangedKeys == nil {
		params.ChangedKeys = []string{}
	}
	if err := z.q.RecordZPAChange(ctx, params); err != nil {
		return fmt.Errorf("cannot record a change for %s %d: %w", change.Kind, change.ZpaID, err)
	}
	return nil
}

// Runs lists recent runs with their per-endpoint results.
func (z *ZPA) Runs(ctx context.Context, limit int) ([]domain.ZPASyncRun, error) {
	rows, err := z.q.ZPASyncRuns(ctx, int32(limit)) //nolint:gosec // the service bounds it at 100
	if err != nil {
		return nil, fmt.Errorf("cannot read the sync runs: %w", err)
	}

	runs := make([]domain.ZPASyncRun, 0, len(rows))
	for _, row := range rows {
		run := runFrom(row)
		if run.Kinds, err = z.kindsOf(ctx, run.ID); err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, nil
}

// RunByID returns one run, or (nil, nil).
func (z *ZPA) RunByID(ctx context.Context, id uuid.UUID) (*domain.ZPASyncRun, error) {
	row, err := z.q.ZPASyncRunByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil //nolint:nilnil // "not found" is (nil, nil) throughout this package
		}
		return nil, fmt.Errorf("cannot read the sync run: %w", err)
	}
	run := runFrom(row)
	if run.Kinds, err = z.kindsOf(ctx, run.ID); err != nil {
		return nil, err
	}
	return &run, nil
}

// LastSuccessfulRun is the freshness answer, or (nil, nil) when the import has never run.
func (z *ZPA) LastSuccessfulRun(ctx context.Context) (*domain.ZPASyncRun, error) {
	row, err := z.q.LastSuccessfulZPASyncRun(ctx)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil //nolint:nilnil // as above
		}
		return nil, fmt.Errorf("cannot read the last successful sync run: %w", err)
	}
	run := runFrom(row)
	if run.Kinds, err = z.kindsOf(ctx, run.ID); err != nil {
		return nil, err
	}
	return &run, nil
}

func (z *ZPA) kindsOf(ctx context.Context, runID uuid.UUID) ([]domain.ZPASyncRunKind, error) {
	rows, err := z.q.ZPASyncRunKinds(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("cannot read the per-endpoint results: %w", err)
	}

	kinds := make([]domain.ZPASyncRunKind, 0, len(rows))
	for _, row := range rows {
		kind := domain.ZPASyncRunKind{
			Kind:    domain.ZPAKind(row.Kind),
			Status:  domain.ZPASyncStatus(row.Status),
			Fetched: int(row.Fetched),
		}
		if row.Error != nil {
			kind.Error = *row.Error
		}
		kinds = append(kinds, kind)
	}
	return kinds, nil
}

// ChangesByRun lists what a run changed.
//
// Deliberately without the payloads. They are held for the durable answer six months later,
// not for a list, and shipping two JSON documents per row into an interface that renders a
// key list is a lot of bytes for nothing.
func (z *ZPA) ChangesByRun(ctx context.Context, runID uuid.UUID) ([]domain.ZPAChange, error) {
	rows, err := z.q.ZPAChangesByRun(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("cannot read the changes of a sync run: %w", err)
	}

	changes := make([]domain.ZPAChange, 0, len(rows))
	for _, row := range rows {
		change := domain.ZPAChange{
			ID:          row.ID,
			RunID:       row.RunID,
			ObjectID:    row.ObjectID,
			Kind:        domain.ZPAKind(row.Kind),
			ZpaID:       row.ZpaID,
			Change:      domain.ZPAChangeType(row.Change),
			ChangedKeys: row.ChangedKeys,
			DetectedAt:  row.DetectedAt,
		}
		if row.Label != nil {
			change.Label = *row.Label
		}
		changes = append(changes, change)
	}
	return changes, nil
}

// FailAbandonedRuns marks runs that outlived the process that started them.
//
// Called at startup beside the protected-admin reconciliation. Without it a crashed sync leaves
// a RUNNING row forever, and the interface shows a run in progress that nothing is progressing.
func (z *ZPA) FailAbandonedRuns(ctx context.Context, olderThan time.Duration) (int, error) {
	ids, err := z.q.FailAbandonedZPASyncRuns(ctx, time.Now().Add(-olderThan))
	if err != nil {
		return 0, fmt.Errorf("cannot fail the abandoned sync runs: %w", err)
	}
	return len(ids), nil
}
