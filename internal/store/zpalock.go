package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrZPASyncLocked means another sync holds the lock.
var ErrZPASyncLocked = errors.New("another zpa sync is running")

// zpaSyncLockKey is an arbitrary constant, chosen once and never changed.
//
// PostgreSQL advisory locks share one namespace per database, so the only requirement is that
// nothing else in this system picks the same number. Nothing else uses advisory locks at all
// except goose, which computes its own from its table name — so a hand-picked constant with a
// comment is clearer than a hash of a string somebody may later reword.
const zpaSyncLockKey int64 = 0x7A50_0001 // "ZPA" 0001

// WithZPASyncLock runs fn while holding the sync lock, or refuses.
//
// pg_try_advisory_lock rather than the blocking form, and that is the decision in this file. A
// second sync should be told "one is already running" and stop — not queue behind the first and
// then immediately refetch everything it just fetched. The manual button turns that refusal
// into something useful: it shows the run that is already in progress, which is the honest
// answer to "sync now" while a sync is happening.
//
// Session-level and taken on a connection acquired for the duration, so a process killed
// mid-sync releases it when its session ends rather than leaving the next one waiting for a
// lock nobody holds. Same reasoning as the migration lock.
func WithZPASyncLock(ctx context.Context, pool *pgxpool.Pool, fn func(context.Context) error) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("cannot acquire a connection for the sync lock: %w", err)
	}
	defer conn.Release()

	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, zpaSyncLockKey).Scan(&acquired); err != nil {
		return fmt.Errorf("cannot take the sync lock: %w", err)
	}
	if !acquired {
		return ErrZPASyncLocked
	}

	defer func() {
		// Released on the same connection it was taken on, which is what makes it a lock and
		// not a leak. A failure here is worth a wrapped error only if fn succeeded; the session
		// ending would release it anyway.
		_, _ = conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, zpaSyncLockKey)
	}()

	return fn(ctx)
}
