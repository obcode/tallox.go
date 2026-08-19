package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/store"
	"github.com/obcode/tallox.go/internal/zpa"
)

// abandonedRunAge is how long a RUNNING row may live before a start decides its process died.
//
// Generous on purpose: a real run takes about eleven seconds, so anything above a few minutes
// is already beyond doubt, and an hour leaves room for a slow day at the other end without ever
// marking a live sync as failed.
const abandonedRunAge = time.Hour

// runZPASync is the -zpa-sync entry point: fetch, apply, report, exit.
//
// A separate process rather than a timer inside the server. The whole argument is in the flag's
// comment; the consequence here is that this function owns its own pool and closes it, and that
// its result reaches the operator as an exit code and a line on stdout rather than as a metric
// nobody is collecting.
// The exit code is returned rather than taken here, because log.Fatal skips deferred calls and
// this function owns a connection pool. A process that exits without closing it is harmless in
// practice and exactly the habit that is not harmless in the next function somebody writes.
func runZPASync(ctx context.Context, cfg Config, dsn string, dryRun bool) {
	os.Exit(zpaSyncExitCode(ctx, cfg, dsn, dryRun))
}

func zpaSyncExitCode(ctx context.Context, cfg Config, dsn string, dryRun bool) int {
	if !cfg.ZPA.Configured() {
		log.Error().Msg("the zpa import is not configured — see zpa.baseurl and zpa.token")
		return 2
	}

	client, err := zpa.New(zpa.Config{BaseURL: cfg.ZPA.BaseURL, Token: cfg.ZPA.Token})
	if err != nil {
		log.Error().Err(err).Msg("cannot build the zpa client")
		return 2
	}

	if dryRun {
		// No pool, no lock, no writes — not even the run row. This is the command somebody runs
		// against production to find out what the catalogue looks like today, and it must not
		// be able to change anything while answering.
		return reportDryRun(ctx, client)
	}

	pool, err := store.Open(ctx, dsn)
	if err != nil {
		log.Error().Err(err).Msg("cannot reach the database")
		return 2
	}
	defer pool.Close()

	cache := store.NewZPA(pool)
	if failed, err := cache.FailAbandonedRuns(ctx, abandonedRunAge); err != nil {
		log.Warn().Err(err).Msg("cannot clear abandoned sync runs")
	} else if failed > 0 {
		log.Warn().Int("runs", failed).Msg("marked abandoned sync runs as failed")
	}

	service := domain.NewZPASyncService(cache, client, store.NewZPALock(pool))

	// The lock is inside Sync, so this path and the button in the interface cannot end up with
	// different concurrency behaviour.
	run, err := service.Sync(ctx, domain.ZPASyncTriggerSchedule, nil)

	if errors.Is(err, store.ErrZPASyncLocked) {
		// Not a failure. Somebody pressed the button a minute ago, or the previous night's run
		// is still going. Exit zero so the crontab does not mail about it.
		log.Info().Msg("another sync is already running — nothing to do")
		return 0
	}
	if err != nil {
		log.Error().Err(err).Msg("the zpa sync failed")
		return 1
	}

	// stdout, in the shape the cron wrapper turns into a mail. Deliberately one line per
	// endpoint plus one summary: a report that has to be scrolled is a report nobody reads, and
	// the wrapper only sends mail when there is something in it.
	printRunReport(run)

	if run.Status == domain.ZPASyncFailed {
		return 1
	}
	return 0
}

// reportDryRun fetches everything and says what is there, writing nothing.
func reportDryRun(ctx context.Context, source domain.ZPASource) int {
	fmt.Println("ZPA dry run — nothing is written.")

	code := 0
	for _, kind := range domain.AllZPAKinds() {
		started := time.Now()
		objects, err := source.Fetch(ctx, kind)
		if err != nil {
			fmt.Printf("  %-7s FEHLER: %v\n", kind, err)
			code = 1
			continue
		}

		fmt.Printf("  %-7s %5d Objekte, %s, IDs %d..%d\n",
			kind, len(objects), time.Since(started).Round(time.Millisecond),
			minID(objects), maxID(objects))
		// The key names, not the values: this is the line that answers "what shape is it now"
		// after they change something, and it can be pasted into a mail to the maintainer.
		fmt.Printf("          Felder: %s\n", keysOf(objects[0].Payload))
	}

	return code
}

func printRunReport(run domain.ZPASyncRun) {
	fmt.Printf("ZPA-Abgleich %s: %s\n", run.ID, run.Status)
	for _, kind := range run.Kinds {
		if kind.Error != "" {
			fmt.Printf("  %-7s FEHLER: %s\n", kind.Kind, kind.Error)
			continue
		}
		fmt.Printf("  %-7s %5d Objekte\n", kind.Kind, kind.Fetched)
	}
	fmt.Printf("  neu %d, geändert %d, entfallen %d\n",
		run.Appeared, run.Changed, run.Disappeared)
}

func minID(objects []domain.ZPAObject) int64 {
	smallest := objects[0].ZpaID
	for _, o := range objects {
		if o.ZpaID < smallest {
			smallest = o.ZpaID
		}
	}
	return smallest
}

func maxID(objects []domain.ZPAObject) int64 {
	largest := objects[0].ZpaID
	for _, o := range objects {
		if o.ZpaID > largest {
			largest = o.ZpaID
		}
	}
	return largest
}

// keysOf lists an object's top-level field names. Names only — they are schema and safe to
// share; the values are data from another institution's system.
func keysOf(payload json.RawMessage) string {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return "(kein Objekt)"
	}
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)

	out := ""
	for i, name := range names {
		if i > 0 {
			out += ", "
		}
		out += name
	}
	return out
}
