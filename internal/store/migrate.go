package store

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"strings"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	// Registers the "pgx" database/sql driver, which is how goose reaches the same database
	// the pool is bound to.
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/obcode/tallox.go/db"
)

// migrationsFS is db/migrations, re-rooted so goose sees the .sql files at the top level.
func migrationsFS() (fs.FS, error) {
	sub, err := fs.Sub(db.Migrations, "migrations")
	if err != nil {
		return nil, fmt.Errorf("cannot open embedded migrations: %w", err)
	}
	return sub, nil
}

// newProvider builds the goose provider over the embedded migrations.
//
// Uses goose's provider API rather than the package-level goose.Up: the package-level
// functions keep the dialect and the base filesystem in globals, which is fine for a CLI and
// a race for parallel integration tests, each of which migrates its own throwaway schema.
func newProvider(sqlDB *sql.DB, opts ...goose.ProviderOption) (*goose.Provider, error) {
	fsys, err := migrationsFS()
	if err != nil {
		return nil, err
	}

	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, fsys, opts...)
	if err != nil {
		return nil, fmt.Errorf("cannot set up migrations: %w", err)
	}
	return provider, nil
}

// exclusively makes a migration run wait for any other process migrating the same database.
//
// The case it exists for is a deploy that runs more than one API container. Migrations are
// applied at startup, so two starting replicas migrate the same database at the same moment,
// and goose's version table does not make that safe by itself: both read "nothing applied",
// both begin the same migration, and what they collide on depends on which statement gets
// there first. The symptom is a container that dies on a schema error during a deploy, which
// is the worst moment to be reading a stack trace.
//
// Today there is exactly one API container, so this never engages. That is the reason to add
// it now rather than later: the day somebody scales the service for an unrelated reason,
// nothing about this has to be remembered.
//
// A PostgreSQL session-level advisory lock — held on the connection, released when the session
// ends, so a container killed mid-migration does not leave the next one waiting for a lock
// nobody holds. Deliberately *not* the row locks used for the last-administrator guard: this
// has to cover a migration that creates the very tables such a lock would name.
//
// Polled every second rather than goose's default five: a replica that has to wait should
// start a second late, not five. The ceiling stays five minutes, which is far longer than any
// migration this schema will plausibly grow and still short enough to fail a deploy rather
// than hang it.
func exclusively() (goose.ProviderOption, error) {
	locker, err := lock.NewPostgresSessionLocker(lock.WithLockTimeout(1, 300))
	if err != nil {
		return nil, fmt.Errorf("cannot set up the migration lock: %w", err)
	}
	return goose.WithSessionLocker(locker), nil
}

// Migrate applies all pending migrations to whatever schema the handle's search_path points
// at, and reports how many it applied.
//
// It takes a *sql.DB rather than the pgxpool because goose speaks database/sql. That is also
// why this function lives here and not in a migration package of its own: database/sql is on
// the list of imports the architecture test confines to internal/store, and confining it is
// the point.
//
// Unlocked, unlike MigrateUpDSN. This is the entry point the integration harness uses, where
// every test migrates a private schema and there is nothing to serialise — while the advisory
// lock is database-wide, so it would serialise all of them against each other. That is not
// merely wasteful: goose polls a lock it cannot get, so a suite of parallel schemas would pay
// a poll interval per test for a collision that cannot happen.
func Migrate(ctx context.Context, sqlDB *sql.DB) (int, error) {
	provider, err := newProvider(sqlDB)
	if err != nil {
		return 0, err
	}

	results, err := provider.Up(ctx)
	if err != nil {
		return 0, fmt.Errorf("cannot apply migrations: %w", err)
	}
	return len(results), nil
}

// MigrateUpDSN opens its own connection, applies every pending migration and closes again.
//
// It exists so that the server can migrate at startup without bootstrap importing
// database/sql — which the architecture test confines to this package, and rightly: the
// moment another package holds a sql.DB, it can also run a query, and the visibility policy
// stops being unavoidable.
//
// A separate connection rather than the pgxpool the server runs on, because goose speaks
// database/sql and because a migration should not be holding a pooled connection that request
// handling is waiting for.
//
// This is the path a starting server takes, and therefore the one that takes the lock — see
// exclusively. Migrate stays unlocked for the harness that migrates private schemas.
func MigrateUpDSN(ctx context.Context, dsn string) (int, error) {
	db, err := openMigrationConn(ctx, dsn)
	if err != nil {
		return 0, err
	}
	defer func() { _ = db.Close() }()

	locked, err := exclusively()
	if err != nil {
		return 0, err
	}
	provider, err := newProvider(db, locked)
	if err != nil {
		return 0, err
	}

	results, err := provider.Up(ctx)
	if err != nil {
		return 0, fmt.Errorf("cannot apply migrations: %w", err)
	}
	return len(results), nil
}

// openMigrationConn is the database/sql handle goose needs, and the reason both DSN-taking
// functions above and below can exist without bootstrap ever holding one.
func openMigrationConn(ctx context.Context, dsn string) (*sql.DB, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("cannot open a migration connection: %w", err)
	}

	// goose runs one statement after another; a second connection would only add the question
	// of which session a statement landed in.
	db.SetMaxOpenConns(1)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("cannot reach the database for migrations: %w", err)
	}
	return db, nil
}

// MigrateDown rolls back every applied migration, down to an empty schema.
//
// Exists for tests — the reversibility test in this package, and any harness that wants to
// prove a migration pair is symmetric before it is released. Never called by the server: a
// production process that can undo its own schema is one signal handler away from an
// incident.
func MigrateDown(ctx context.Context, sqlDB *sql.DB) error {
	provider, err := newProvider(sqlDB)
	if err != nil {
		return err
	}

	if _, err := provider.DownTo(ctx, 0); err != nil {
		return fmt.Errorf("cannot roll back migrations: %w", err)
	}
	return nil
}

// MigrateDownTo rolls back until the given version is the newest applied one.
//
// The same "exists for tests" reading as MigrateDown, and for a case that one cannot cover: a
// migration that *moves data* has an Up path with rows in it, and the only way to run that path
// is to be one version below it with the old shape populated. Reversibility says the pair is
// symmetric; this says the backfill in the middle does what its comment claims.
func MigrateDownTo(ctx context.Context, sqlDB *sql.DB, version int64) error {
	provider, err := newProvider(sqlDB)
	if err != nil {
		return err
	}

	if _, err := provider.DownTo(ctx, version); err != nil {
		return fmt.Errorf("cannot roll back migrations to %d: %w", version, err)
	}
	return nil
}

// MigrationStatus is what the database has, and what the binary is carrying that it does not.
//
// Both halves, not just the pending ones, because the two questions asked in front of a
// production database are different. "What is about to happen if I restart this container"
// needs Pending. "Which schema is this database on" — the question that decides whether an
// older image can still be started against it — needs Applied.
type MigrationStatus struct {
	// Applied are the versions the database has recorded, in order.
	Applied []int64
	// Pending are the versions embedded in this binary that the database does not have yet.
	Pending []int64
}

// String renders the status for somebody reading a terminal, not for a parser.
func (s MigrationStatus) String() string {
	var b strings.Builder

	fmt.Fprintf(&b, "applied: %d", len(s.Applied))
	if n := len(s.Applied); n > 0 {
		fmt.Fprintf(&b, " (up to %d)", s.Applied[n-1])
	}
	fmt.Fprintf(&b, "\npending: %d", len(s.Pending))
	for _, v := range s.Pending {
		fmt.Fprintf(&b, "\n  %d", v)
	}
	if len(s.Pending) == 0 {
		b.WriteString("\nThe database is on the schema this binary expects.")
	} else {
		// Said here rather than in the runbook, because this is where somebody is standing
		// when the question comes up.
		b.WriteString("\nStarting this image applies them. They are not undone by a rollback:" +
			"\npinning an older tag leaves this schema in place, so the previous image has to" +
			"\nbe able to run against it.")
	}
	return b.String()
}

// StatusDSN reports the migration status over a connection of its own.
//
// The read-only counterpart of MigrateUpDSN, and the one the -migrate-status flag uses: it
// answers the question without being able to change the answer. Anything that inspects
// production before a deploy should have that property.
func StatusDSN(ctx context.Context, dsn string) (MigrationStatus, error) {
	db, err := openMigrationConn(ctx, dsn)
	if err != nil {
		return MigrationStatus{}, err
	}
	defer func() { _ = db.Close() }()

	return Status(ctx, db)
}

// Status reports which migrations are applied and which are pending.
//
// Unlocked deliberately, even though MigrateUpDSN is not: this only reads, and waiting behind
// a migration in progress would be the wrong behaviour for the flag that exists to be run in
// front of a production database. Reading during a deploy answers "somebody is mid-migration",
// which is information, rather than hanging until they are done.
func Status(ctx context.Context, sqlDB *sql.DB) (MigrationStatus, error) {
	provider, err := newProvider(sqlDB)
	if err != nil {
		return MigrationStatus{}, err
	}

	sources, err := provider.Status(ctx)
	if err != nil {
		return MigrationStatus{}, fmt.Errorf("cannot read migration status: %w", err)
	}

	status := MigrationStatus{Applied: []int64{}, Pending: []int64{}}
	for _, s := range sources {
		if s.State == goose.StatePending {
			status.Pending = append(status.Pending, s.Source.Version)
			continue
		}
		status.Applied = append(status.Applied, s.Source.Version)
	}
	return status, nil
}
