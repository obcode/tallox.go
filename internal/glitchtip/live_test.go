package glitchtip

import (
	"errors"
	"os"
	"testing"

	"github.com/rs/zerolog"
)

// TestLiveIngest sends real events to a real collector. It is skipped unless
// GLITCHTIP_SMOKE_DSN is set, so it never runs in CI or on a laptop by accident:
//
//	GLITCHTIP_SMOKE_DSN='https://<key>@glitchtip.example.edu/1' \
//	    go test ./internal/glitchtip/ -run TestLiveIngest -v
//
// Expected in the UI afterwards: THREE events in TWO issues -- the two "student not
// found" lines share a call site and therefore an issue, the third does not. That is
// the grouping decision this package exists for, checked against the deployment
// rather than against a unit test's idea of it.
func TestLiveIngest(t *testing.T) {
	dsn := os.Getenv("GLITCHTIP_SMOKE_DSN")
	if dsn == "" {
		t.Skip("GLITCHTIP_SMOKE_DSN not set")
	}

	flush, err := Init(Config{DSN: dsn, Environment: "smoketest", Release: "smoketest"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer flush()

	// Same as the server does in bootstrap -- without it the caller, and therefore
	// the fingerprint, would carry this machine's absolute path.
	zerolog.CallerMarshalFunc = ShortCallerMarshalFunc

	logger := zerolog.New(zerolog.MultiLevelWriter(
		zerolog.NewTestWriter(t),
		Writer(zerolog.ErrorLevel),
	)).With().Caller().Timestamp().Logger()

	logger.Info().Msg("must not be reported")
	for _, student := range []string{"42", "43"} {
		logger.Error().Str("wish", student).Msg("wish not found")
	}
	logger.Error().
		Err(errors.New("pgx: no rows in result set")).
		Str("semester", "2026-WS").
		Msg("cannot import modules")

	t.Log("sent; expect 3 events in 2 issues, and no info-level event at all")
}
