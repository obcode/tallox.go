package glitchtip

import (
	"testing"

	"github.com/getsentry/sentry-go"
	"github.com/rs/zerolog"
)

// The fingerprint is the whole reason this package exists: tallox logs every error
// through one writer, so without it GlitchTip folds every log.Error() site into
// a single issue.
func TestFingerprintIsTheCallerSoDistinctSitesStayDistinct(t *testing.T) {
	a := eventFrom(zerolog.ErrorLevel, map[string]any{
		"caller":  "tallox/wishes.go:245",
		"message": "wish not found",
	})
	b := eventFrom(zerolog.ErrorLevel, map[string]any{
		"caller":  "tallox/wishes.go:245",
		"message": "wish not found",
		"wish":    "42",
	})
	c := eventFrom(zerolog.ErrorLevel, map[string]any{
		"caller":  "tallox/modules.go:99",
		"message": "cannot import modules",
	})

	if got := a.Fingerprint; len(got) != 1 || got[0] != "tallox/wishes.go:245" {
		t.Fatalf("fingerprint = %v, want [tallox/wishes.go:245]", got)
	}
	// Same call site, different payload: still one issue.
	if a.Fingerprint[0] != b.Fingerprint[0] {
		t.Errorf("same caller produced different fingerprints: %v vs %v", a.Fingerprint, b.Fingerprint)
	}
	if a.Fingerprint[0] == c.Fingerprint[0] {
		t.Errorf("different callers shared a fingerprint: %v", a.Fingerprint)
	}
}

// A logger without .With().Caller() must not group unrelated errors together.
func TestWithoutCallerItFallsBackToTheMessage(t *testing.T) {
	event := eventFrom(zerolog.ErrorLevel, map[string]any{"message": "no caller here"})

	if got := event.Fingerprint; len(got) != 1 || got[0] != "no caller here" {
		t.Errorf("fingerprint = %v, want [no caller here]", got)
	}
	if _, ok := event.Tags["caller"]; ok {
		t.Error("tagged a caller that was not in the line")
	}
}

func TestTheLoggedFieldsSurviveAsContext(t *testing.T) {
	event := eventFrom(zerolog.ErrorLevel, map[string]any{
		"caller":   "tallox/zpa.go:17",
		"message":  "cannot read module",
		"time":     1787407009,
		"level":    "error",
		"error":    "pgx: no rows in result set",
		"semester": "2026-WS",
	})

	ctx, ok := event.Contexts["zerolog"]
	if !ok {
		t.Fatal("no zerolog context on the event")
	}
	if ctx["semester"] != "2026-WS" || ctx["error"] != "pgx: no rows in result set" {
		t.Errorf("context lost fields: %v", ctx)
	}
	// The four recognised fields became first-class attributes and must not be
	// repeated in the context block.
	for _, key := range []string{"caller", "message", "time", "level"} {
		if _, dup := ctx[key]; dup {
			t.Errorf("%q duplicated into the context", key)
		}
	}
}

func TestLevelMapping(t *testing.T) {
	for _, tc := range []struct {
		in   zerolog.Level
		want sentry.Level
	}{
		{zerolog.ErrorLevel, sentry.LevelError},
		{zerolog.FatalLevel, sentry.LevelFatal},
		{zerolog.PanicLevel, sentry.LevelFatal},
		{zerolog.WarnLevel, sentry.LevelWarning},
	} {
		if got := sentryLevel(tc.in); got != tc.want {
			t.Errorf("sentryLevel(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// Below the threshold nothing is reported, and the write must still be accounted
// for -- zerolog treats a short write as an error.
func TestWriteLevelIgnoresQuietLevelsAndReportsFullLength(t *testing.T) {
	w := Writer(zerolog.ErrorLevel)
	line := []byte(`{"level":"info","message":"nichts zu melden"}`)

	n, err := w.WriteLevel(zerolog.InfoLevel, line)
	if err != nil || n != len(line) {
		t.Errorf("WriteLevel = (%d, %v), want (%d, nil)", n, err, len(line))
	}
}

// A line that is not JSON must not take the process down, and must not recurse
// through the logger.
func TestWriteLevelSurvivesUnparsableLines(t *testing.T) {
	w := Writer(zerolog.ErrorLevel)
	line := []byte("das ist kein JSON")

	n, err := w.WriteLevel(zerolog.ErrorLevel, line)
	if err != nil || n != len(line) {
		t.Errorf("WriteLevel = (%d, %v), want (%d, nil)", n, err, len(line))
	}
}

// With no DSN the package must stay out of the way entirely -- that is what makes a
// local run work without a collector.
func TestInitWithoutDSNIsANoOp(t *testing.T) {
	flush, err := Init(Config{})
	if err != nil {
		t.Fatalf("Init() = %v, want nil", err)
	}
	flush() // must not block or panic
}

func TestShortCallerMarshalFuncDropsTheBuildPath(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"/build/tallox/wishes.go", "tallox/wishes.go:245"},
		{"/Users/someone/plexams.go/tallox/wishes.go", "tallox/wishes.go:245"},
		{"rooms.go", "rooms.go:245"},
		{"tallox/wishes.go", "tallox/wishes.go:245"},
	} {
		if got := ShortCallerMarshalFunc(0, tc.in, 245); got != tc.want {
			t.Errorf("ShortCallerMarshalFunc(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Two build environments, one call site: the grouping key must be the same.
func TestTheGroupingKeyDoesNotDependOnTheBuildMachine(t *testing.T) {
	container := ShortCallerMarshalFunc(0, "/build/tallox/wishes.go", 245)
	laptop := ShortCallerMarshalFunc(0, "/Users/someone/plexams.go/tallox/wishes.go", 245)

	if container != laptop {
		t.Errorf("same call site, different keys: %q vs %q", container, laptop)
	}
}
