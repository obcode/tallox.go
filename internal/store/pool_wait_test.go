package store_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/obcode/tallox.go/internal/store"
)

// unreachableDSN points at a port nothing listens on, so every attempt fails immediately with
// "connection refused" rather than hanging. Port 1 is reserved and never a database.
const unreachableDSN = "postgres://tallox:tallox@127.0.0.1:1/tallox?sslmode=disable"

// TestWaitReadyGivesUpAndSaysWhy covers the case the caller actually has to survive: a database
// that never appears. It must return within the budget rather than block a starting container
// forever, and it must carry the underlying error out so the log line names the real cause and
// not just "timeout".
//
// No PostgreSQL needed, deliberately — the failure path is the one that is hard to reproduce
// against a real server, and it is the one a reboot exercises.
func TestWaitReadyGivesUpAndSaysWhy(t *testing.T) {
	t.Parallel()

	const budget = 1500 * time.Millisecond

	waits := 0
	start := time.Now()
	err := store.WaitReady(context.Background(), unreachableDSN, budget, func(error) { waits++ })
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("WaitReady returned nil for a database that is not there")
	}
	if !strings.Contains(err.Error(), "not reachable within") {
		t.Errorf("error should say the budget ran out, got: %v", err)
	}
	// The wrapped cause has to survive: without it the operator sees a timeout and cannot tell
	// a refused connection from a wrong password.
	if !strings.Contains(err.Error(), "connect") && !strings.Contains(err.Error(), "refused") {
		t.Errorf("error should carry the connection failure, got: %v", err)
	}

	// Generous upper bound: this asserts that it stops, not how precisely it stops. A tight
	// bound here would fail on a loaded CI runner and teach everyone to ignore it.
	if elapsed > 4*budget {
		t.Errorf("took %s, which is well past the %s budget", elapsed, budget)
	}

	// Once, not once per attempt: the callback exists to say why the wait started, and a line
	// every 500 ms would bury the rest of the startup log.
	if waits != 1 {
		t.Errorf("onWait called %d times, want exactly 1", waits)
	}
}

// TestWaitReadyStopsWhenTheContextDoes makes sure a shutdown during startup is reported as a
// shutdown. Returning "database not reachable" there would send whoever reads the log looking
// at Postgres for a problem that was a SIGTERM.
func TestWaitReadyStopsWhenTheContextDoes(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	err := store.WaitReady(ctx, unreachableDSN, time.Minute, nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("WaitReady returned nil for a cancelled context")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("error should name the cancellation, got: %v", err)
	}
	// It must not sit out the minute-long budget it was given.
	if elapsed > 10*time.Second {
		t.Errorf("took %s; a cancelled context should return promptly", elapsed)
	}
}

// TestWaitReadyRejectsAGarbledDSN keeps the parse failure distinguishable from the wait. There
// is nothing to wait for when the URL itself is wrong, and retrying it for 90 s would turn a
// typo in .env into a container that looks slow instead of misconfigured.
func TestWaitReadyRejectsAGarbledDSN(t *testing.T) {
	t.Parallel()

	start := time.Now()
	err := store.WaitReady(context.Background(), "postgres://%zz", time.Minute, nil)
	if err == nil {
		t.Fatal("WaitReady accepted a DSN that cannot be parsed")
	}
	if !strings.Contains(err.Error(), "cannot parse database url") {
		t.Errorf("error should name the parse failure, got: %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Error("a parse failure should be immediate, not retried")
	}
}
