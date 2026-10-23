package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/store"
)

// TestAProjectionCutOffMidwayIsRecordedAsFailed.
//
// The request that asked for a projection can go away while it runs. The transaction then
// rolls back, which is correct; what must not happen is the row staying RUNNING because the
// write that finishes it used the same dead context.
//
// Deterministic rather than timed: another transaction holds the first table the projection
// writes, so the projection is provably in the middle of its work when the context is cancelled.
func TestAProjectionCutOffMidwayIsRecordedAsFailed(t *testing.T) {
	t.Parallel()

	s := seededSchema(t)
	blocker, err := s.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(context.WithoutCancel(t.Context())) }()
	if _, err := blocker.Exec(t.Context(), `LOCK TABLE programme IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatalf("locking programme: %v", err)
	}

	ctx, hangUp := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() {
		_, err := store.NewCatalogue(s.Pool).Project(ctx, nil)
		result <- err
	}()

	// Wait until the projection has written its RUNNING row and is waiting on the lock.
	var id string
	deadline := time.Now().Add(10 * time.Second)
	for {
		err := s.Pool.QueryRow(t.Context(),
			`SELECT id::text FROM zpa_catalogue_projection WHERE status = 'RUNNING'`).Scan(&id)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the projection never started")
		}
		time.Sleep(10 * time.Millisecond)
	}

	hangUp()
	if err := <-result; err == nil {
		t.Fatal("a projection whose context was cancelled reported success")
	}
	_ = blocker.Rollback(t.Context())

	var status string
	if err := s.Pool.QueryRow(t.Context(),
		`SELECT status FROM zpa_catalogue_projection WHERE id = $1`, id).Scan(&status); err != nil {
		t.Fatalf("reading the projection: %v", err)
	}
	if status != string(domain.ProjectionFailed) {
		t.Errorf("the cut-off projection is %s, want FAILED", status)
	}
}

// TestAnAbandonedProjectionIsFailedAtStartup mirrors the same test for sync runs: only the
// stale row is touched, never one a live process may be working on.
func TestAnAbandonedProjectionIsFailedAtStartup(t *testing.T) {
	t.Parallel()

	s := seededSchema(t)
	ctx := t.Context()
	var stale, fresh string
	for _, row := range []struct {
		id  *string
		age string
	}{{&stale, "3 hours"}, {&fresh, "0 seconds"}} {
		if err := s.Pool.QueryRow(ctx, `INSERT INTO zpa_catalogue_projection (started_at)
			VALUES (now() - $1::interval) RETURNING id::text`, row.age).Scan(row.id); err != nil {
			t.Fatalf("seeding a projection: %v", err)
		}
	}

	failed, err := store.NewCatalogue(s.Pool).FailAbandonedProjections(ctx, time.Hour)
	if err != nil {
		t.Fatalf("failing abandoned projections: %v", err)
	}
	if failed != 1 {
		t.Errorf("failed %d projections, want exactly the stale one", failed)
	}
	for id, want := range map[string]string{stale: "FAILED", fresh: "RUNNING"} {
		var status string
		if err := s.Pool.QueryRow(ctx, `SELECT status FROM zpa_catalogue_projection WHERE id = $1`, id).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status != want {
			t.Errorf("projection %s is %s, want %s", id, status, want)
		}
	}
}
