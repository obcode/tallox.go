// Package glitchtip forwards zerolog error events to a GlitchTip instance, which
// speaks the Sentry protocol.
//
// It attaches as a zerolog.LevelWriter beside the console writer, so every existing
// log.Error() reports without a single call site changing.
//
// # Grouping
//
// tallox logs its errors through one writer, so the stack trace of every event is the
// same handful of frames inside this package. Left to GlitchTip's default grouping,
// every log.Error() site in the code base would fold into a single issue. The
// fingerprint is therefore the zerolog "caller" field -- the file:line the log call
// sits on, which the logger already attaches via .With().Caller().
//
// Measured against GlitchTip 6.2.6: three events, two of them sharing a caller,
// produce two issues.
//
// # Disabled by default
//
// With an empty DSN every function here is a no-op, so a local run needs no
// collector and no configuration.
package glitchtip

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/rs/zerolog"
)

// flushTimeout bounds how long a shutdown -- or a log.Fatal() -- waits for events
// still in the transport queue.
const flushTimeout = 5 * time.Second

// Config is the deployment-supplied part. Everything is optional; an empty DSN
// disables reporting entirely.
type Config struct {
	// DSN as issued by GlitchTip, e.g. https://<key>@glitchtip.example.edu/1.
	DSN string
	// Environment separates production from a test deployment in the UI.
	Environment string
	// Release is stamped on every event so an issue can be tied to a build.
	Release string
}

// Init starts the reporter and returns a flush function to defer. The flush is
// always safe to call, including when reporting is disabled.
func Init(cfg Config) (flush func(), err error) {
	if cfg.DSN == "" {
		return func() {}, nil
	}

	err = sentry.Init(sentry.ClientOptions{
		Dsn:         cfg.DSN,
		Environment: cfg.Environment,
		Release:     cfg.Release,
		// The stack trace would be this package's write path, identical for every
		// event and useless for reading. The call site travels in "caller" instead.
		AttachStacktrace: false,
	})
	if err != nil {
		return func() {}, fmt.Errorf("cannot initialise glitchtip reporting: %w", err)
	}

	return func() { sentry.Flush(flushTimeout) }, nil
}

// ShortCallerMarshalFunc renders a call site as "package/file.go:line" instead of the
// absolute path zerolog reports by default. Assign it to zerolog.CallerMarshalFunc
// wherever the logger is configured.
//
// It is not cosmetic. The fingerprint IS the caller, so an absolute path ties the
// grouping to the machine that built the binary: the same log.Error() arrives as
// "/build/tallox/wishes.go:245" from the container image and as
// "/Users/someone/tallox.go/tallox/wishes.go:245" from a laptop, and GlitchTip files
// those as two unrelated issues. Two path elements are enough to tell the
// call sites apart and depend on nothing but the code.
func ShortCallerMarshalFunc(_ uintptr, file string, line int) string {
	short := file
	if slash := strings.LastIndexByte(file, '/'); slash >= 0 {
		if prev := strings.LastIndexByte(file[:slash], '/'); prev >= 0 {
			short = file[prev+1:]
		}
	}
	return short + ":" + strconv.Itoa(line)
}

// Writer returns a zerolog writer that reports events of minLevel and above. Add it
// beside the console writer with zerolog.MultiLevelWriter; it never writes output of
// its own.
func Writer(minLevel zerolog.Level) zerolog.LevelWriter {
	return levelWriter{minLevel: minLevel}
}

type levelWriter struct {
	minLevel zerolog.Level
}

// Write accepts and drops. zerolog only reaches this path for a logger whose writer
// is not level-aware, and without a level there is nothing sensible to report -- the
// console writer beside us has already printed the line either way.
func (w levelWriter) Write(p []byte) (int, error) {
	return len(p), nil
}

func (w levelWriter) WriteLevel(level zerolog.Level, p []byte) (int, error) {
	if level < w.minLevel {
		return len(p), nil
	}

	var fields map[string]any
	if json.Unmarshal(p, &fields) != nil {
		// A log line we cannot parse is not worth losing the process over, and
		// reporting the parse failure through the logger would recurse. Swallowed on
		// purpose: there is no caller left to hand an error back to, and a short write
		// would make zerolog treat every unparsable line as a logging failure.
		//nolint:nilerr // dropping the event is the intended behaviour here
		return len(p), nil
	}

	sentry.CaptureEvent(eventFrom(level, fields))

	// log.Fatal() calls os.Exit(1) as soon as this returns, and log.Panic() panics.
	// Without flushing here the one event that mattered most is the one that never
	// leaves the process.
	if level >= zerolog.FatalLevel {
		sentry.Flush(flushTimeout)
	}

	return len(p), nil
}

// eventFrom turns a decoded zerolog line into a Sentry event. Recognised fields
// become first-class attributes; everything else the call site logged is kept as
// extra data, because that is usually the identifier one needs to reproduce.
func eventFrom(level zerolog.Level, fields map[string]any) *sentry.Event {
	event := sentry.NewEvent()
	event.Level = sentryLevel(level)
	event.Logger = "zerolog"
	event.Timestamp = time.Now()

	if msg, ok := fields[zerolog.MessageFieldName].(string); ok {
		event.Message = msg
	}

	if caller, ok := fields[zerolog.CallerFieldName].(string); ok && caller != "" {
		// The whole point of this package -- see the package comment.
		event.Fingerprint = []string{caller}
		event.Tags[zerolog.CallerFieldName] = caller
	} else if event.Message != "" {
		// No caller means no stable key, so fall back to the message rather than
		// silently grouping unrelated events together.
		event.Fingerprint = []string{event.Message}
	}

	// Whatever else the call site logged -- ids, semester, file name -- is usually
	// what one needs to reproduce the failure. sentry-go dropped Event.Extra, so it
	// travels as a context; GlitchTip renders that as its own block on the issue.
	rest := sentry.Context{}
	for key, value := range fields {
		switch key {
		case zerolog.LevelFieldName, zerolog.MessageFieldName,
			zerolog.CallerFieldName, zerolog.TimestampFieldName:
			continue
		default:
			rest[key] = value
		}
	}
	if len(rest) > 0 {
		event.Contexts["zerolog"] = rest
	}

	return event
}

func sentryLevel(level zerolog.Level) sentry.Level {
	switch level {
	case zerolog.ErrorLevel:
		return sentry.LevelError
	case zerolog.FatalLevel, zerolog.PanicLevel:
		return sentry.LevelFatal
	case zerolog.WarnLevel:
		return sentry.LevelWarning
	default:
		return sentry.LevelInfo
	}
}
