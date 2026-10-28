package store_test

import (
	"strings"
	"testing"

	"github.com/obcode/tallox.go/internal/store"
	"github.com/obcode/tallox.go/internal/store/storetest"
)

// TestEverySessionCarriesTheGuardTimeouts reads the settings back from a live session rather
// than from the config the pool was built with: what protects the pool is what PostgreSQL
// applies, and a runtime parameter the driver silently dropped would look fine in Go.
func TestEverySessionCarriesTheGuardTimeouts(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	ctx := t.Context()

	pool, err := store.Open(ctx, s.DSN)
	if err != nil {
		t.Fatalf("cannot open the pool: %v", err)
	}
	defer pool.Close()

	for key, want := range store.SessionDefaults() {
		var got string
		if err := pool.QueryRow(ctx, "SHOW "+key).Scan(&got); err != nil {
			t.Fatalf("SHOW %s: %v", key, err)
		}
		if got != want {
			t.Errorf("%s = %q in the session, want %q", key, got, want)
		}
	}

	// The DSN wins where it speaks: an operator who needs a longer statement for one job sets
	// it there, and the default does not quietly override them.
	sep := "?"
	if strings.Contains(s.DSN, "?") {
		sep = "&"
	}
	longer, err := store.Open(ctx, s.DSN+sep+"statement_timeout=7s")
	if err != nil {
		t.Fatalf("cannot open the pool with a DSN override: %v", err)
	}
	defer longer.Close()

	var got string
	if err := longer.QueryRow(ctx, "SHOW statement_timeout").Scan(&got); err != nil {
		t.Fatalf("SHOW statement_timeout: %v", err)
	}
	if got != "7s" {
		t.Errorf("statement_timeout = %q with the DSN saying 7s", got)
	}
}
