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
// It is the first half of a two-part key; the second half is the current schema (see
// lockZPASync). PostgreSQL advisory locks share one namespace per database, so the only
// requirement is that nothing else in this system picks the same pair. Nothing else uses
// advisory locks at all except goose, which takes a single bigint key — and the two-int form
// lives in a separate key space from that, so the two cannot collide even by accident. A
// hand-picked constant with a comment is clearer than a hash of a string somebody may later
// reword.
const zpaSyncLockKey int32 = 0x7A50_0001 // "ZPA" 0001

// The two statements share their key expression, so that lock and unlock cannot drift apart.
//
// Why the schema is part of the key: the lock guards the ZPA cache, and that cache lives in a
// schema. In production there is exactly one, so this is the same lock it always was. The
// integration harness, though, gives every test a schema of its own inside one shared database,
// and with a database-wide key two tests in different packages — bootstrap's sync tests and the
// store's lock tests — took the same lock at the same time. The loser was refused for a reason
// that had nothing to do with its assertion, and TestTwoSyncsProduceOneWinner waited forever
// for a holder that never got in. On a 32-core machine `go test ./...` hit it on most runs.
//
// coalesce, because hashtext(NULL) is NULL and pg_try_advisory_lock(int, NULL) answers NULL
// rather than an error: with an empty search_path the scan would fail with a confusing message
// instead of the lock simply working on a fixed key.
const (
	lockZPASync   = `SELECT pg_try_advisory_lock($1, hashtext(coalesce(current_schema(), '')))`
	unlockZPASync = `SELECT pg_advisory_unlock($1, hashtext(coalesce(current_schema(), '')))`
)

// AcquireZPASyncLock takes the sync lock and returns what releases it, or refuses.
//
// pg_try_advisory_lock rather than the blocking form, and that is the decision in this file. A
// second sync should be told "one is already running" and stop — not queue behind the first and
// then immediately refetch everything it just fetched. The manual button turns that refusal
// into something useful: it shows the run that is already in progress, which is the honest
// answer to "sync now" while a sync is happening.
//
// Session-level and taken on a connection held until release, so a process killed mid-sync
// releases it when its session ends rather than leaving the next one waiting for a lock nobody
// holds. Same reasoning as the migration lock.
//
// The release does not take a context. It runs when the run is over, often after the request
// that started it has gone, and unlocking must not depend on a cancellation that belongs to
// somebody else.
func AcquireZPASyncLock(ctx context.Context, pool *pgxpool.Pool) (func(), error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot acquire a connection for the sync lock: %w", err)
	}

	var acquired bool
	if err := conn.QueryRow(ctx, lockZPASync, zpaSyncLockKey).Scan(&acquired); err != nil {
		conn.Release()
		return nil, fmt.Errorf("cannot take the sync lock: %w", err)
	}
	if !acquired {
		conn.Release()
		return nil, ErrZPASyncLocked
	}

	return func() {
		// Released on the same connection it was taken on, which is what makes it a lock and
		// not a leak. A failure is not reported: the session ending releases it anyway.
		_, _ = conn.Exec(context.WithoutCancel(ctx), unlockZPASync, zpaSyncLockKey)
		conn.Release()
	}, nil
}

// WithZPASyncLock runs fn while holding the sync lock, or returns ErrZPASyncLocked.
func WithZPASyncLock(ctx context.Context, pool *pgxpool.Pool, fn func(context.Context) error) error {
	release, err := AcquireZPASyncLock(ctx, pool)
	if err != nil {
		return err
	}
	defer release()
	return fn(ctx)
}

// ZPALock is the domain's locker, bound to a pool.
//
// A type of its own rather than a method on ZPA, because the lock needs the pool and the cache
// queries do not — ZPA is deliberately constructible from a transaction as well.
type ZPALock struct {
	pool *pgxpool.Pool
}

// NewZPALock binds the sync lock to a pool.
func NewZPALock(pool *pgxpool.Pool) *ZPALock { return &ZPALock{pool: pool} }

// AcquireSyncLock takes the lock, or returns ErrZPASyncLocked.
func (l *ZPALock) AcquireSyncLock(ctx context.Context) (func(), error) {
	return AcquireZPASyncLock(ctx, l.pool)
}
