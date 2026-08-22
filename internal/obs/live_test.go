package obs

import (
	"errors"
	"os"
	"testing"

	"github.com/rs/zerolog"
)

// TestLiveIngest sends real events to a real collector. Skipped unless
// GLITCHTIP_SMOKE_DSN is set, so it never runs in CI or on a laptop by accident:
//
//	GLITCHTIP_SMOKE_DSN='https://<key>@glitchtip.example.edu/1' \
//	    go test ./internal/obs/ -run TestLiveIngest -v
//
// Expected in the UI afterwards: THREE events in TWO issues — the two lines that
// share a call site share an issue — and on every one of them a `semester` tag
// but NO `email` tag, and a message with [email] in place of the address. That
// is the scrubber checked against the deployment rather than against a unit
// test's idea of it.
func TestLiveIngest(t *testing.T) {
	dsn := os.Getenv("GLITCHTIP_SMOKE_DSN")
	if dsn == "" {
		t.Skip("GLITCHTIP_SMOKE_DSN not set")
	}

	writer, err := Init(Config{DSN: dsn, Environment: "smoketest", Release: "smoketest"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if writer == nil {
		t.Fatal("Init returned no writer for a non-empty DSN")
	}
	defer Flush()

	// Same as bootstrap does — without it the caller, and therefore the fingerprint,
	// would carry this machine's absolute path.
	zerolog.CallerMarshalFunc = RepoRelativeCaller

	logger := zerolog.New(zerolog.MultiLevelWriter(zerolog.NewTestWriter(t), writer)).
		With().Caller().Timestamp().Logger()

	logger.Info().Msg("must not be reported")
	for _, module := range []string{"IF7", "IF8"} {
		logger.Error().
			Str("semester", "2026-WS").
			Str("module", module).
			Str("email", "oliver.braun@hm.edu"). // must be scrubbed away
			Msg("cannot resolve the module")
	}
	logger.Error().
		Err(errors.New("no person for oliver.braun@hm.edu")).
		Msg("cannot import the demand")

	t.Log("sent; expect 3 events in 2 issues, no email tag anywhere, [email] in the text")
}
