package storetest

import (
	"context"
	"fmt"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// containerImage is pinned to the same major the production host and the DevContainer run.
//
// Not "postgres:latest", and not floating: an integration suite whose database version drifts
// away from production tests a system nobody is running. Bump this in the same commit that
// bumps the compose files.
const containerImage = "postgres:18-alpine"

// startContainer boots a throwaway PostgreSQL and returns its DSN.
//
// Called at most once per test binary (see the sync.OnceValues in storetest.go) — note that
// ./internal/store/... is two of them, so a parallel `go test` boots two containers. The
// container is deliberately never terminated from Go: Testcontainers' Ryuk sidecar reaps it
// when the test process exits, including when that process is killed or panics — which is
// exactly the case a t.Cleanup would miss.
//
// CI is the exception and sets TESTCONTAINERS_RYUK_DISABLED: the runner is thrown away
// anyway, and Ryuk is a per-session singleton that two binaries starting at once race for.
func startContainer() (string, error) {
	// Generous, because this budget covers pulling the image on a cold CI runner, not just
	// starting it. Too tight here and the first run of the day fails for a reason that looks
	// like a database problem and is really a network one.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	container, err := tcpostgres.Run(ctx, containerImage,
		tcpostgres.WithDatabase("tallox_test"),
		tcpostgres.WithUsername("tallox"),
		tcpostgres.WithPassword("tallox"),
		// Durability off. This database is thrown away when the test process exits, so fsync
		// buys nothing and costs a large fraction of the suite's runtime — migrations and
		// fixture inserts are exactly the commit-heavy workload it slows down.
		testcontainers.WithCmdArgs("-c", "fsync=off", "-c", "full_page_writes=off",
			"-c", "synchronous_commit=off"),
		// The module's own strategy rather than a hand-rolled wait.ForLog. initdb starts the
		// server once before restarting it for real, so "ready to accept connections" appears
		// twice — waiting for the first occurrence is the classic Testcontainers flake, and
		// this is the maintained answer to it.
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		return "", fmt.Errorf("cannot start %s: %w", containerImage, err)
	}

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return "", fmt.Errorf("cannot read connection string from %s: %w", containerImage, err)
	}
	return dsn, nil
}
