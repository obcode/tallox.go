package bootstrap

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/store"
)

// notableMarker is the last line of the report, and the only part of it a machine reads.
//
// The cron wrapper greps for it to decide whether to send mail on an installation that would
// rather not hear from a quiet night. A marker line rather than an exit code, because a nonzero
// exit is how cron itself decides to mail — the two mechanisms would fight, and the loser would
// be the mail that says something failed.
const notableMarker = "Berichtenswert: "

// runAccessReport is the -access-report entry point: summarise, prune, print, exit.
//
// A separate process rather than a timer inside the server, exactly like -zpa-sync and for the
// same three reasons: the schedule can change without a redeploy, a failure is an exit code and
// a line in a file the operator already reads, and switching it off is commenting out a crontab
// line rather than trusting a flag inside a running process.
//
// It does NOT migrate, for the reason -zpa-sync does not: the serving container owns the
// schema, and a cron job that could apply a migration means the schema changes at 03:45 on
// whatever image the crontab happens to invoke.
func runAccessReport(ctx context.Context, dsn string, since time.Duration) {
	os.Exit(accessReportExitCode(ctx, dsn, since, os.Stdout))
}

func accessReportExitCode(ctx context.Context, dsn string, since time.Duration, out io.Writer) int {
	pool, err := store.Open(ctx, dsn)
	if err != nil {
		log.Error().Err(err).Msg("cannot reach the database")
		return 2
	}
	defer pool.Close()

	access := domain.NewAccessService(store.NewAccess(pool))

	report, err := access.Report(ctx, time.Now(), since)
	if err != nil {
		log.Error().Err(err).Msg("cannot produce the access report")
		return 2
	}

	printAccessReport(out, report, since)
	return 0
}

// printAccessReport writes the report a person reads.
//
// German, like everything else a person reads. Figures first, then the two things that are
// events rather than volumes — who was turned away, and what was changed. Deliberately NOT the
// entries themselves: the whole log is one click away behind the VPN and a signed-in session,
// and a nightly mail carrying every colleague's movements is a movement profile leaving the
// system into a mailbox.
func printAccessReport(out io.Writer, report domain.AccessReport, since time.Duration) {
	s := report.Summary
	c := s.Counts
	w := &reportWriter{out: out}

	w.printf("Zugriffe der letzten %s (%s bis %s)\n\n",
		humanDuration(since),
		s.From.Format("2006-01-02 15:04"), s.Until.Format("2006-01-02 15:04"))

	w.printf("  Operationen gesamt    %6d  (%d Personen)\n", c.Total, c.People)
	w.printf("  davon Browser         %6d\n", c.Interactive)
	w.printf("  davon Token           %6d\n", c.Token)
	w.printf("  davon Änderungen      %6d\n", c.Mutations)
	w.printf("  Fehler                %6d\n", c.Errors)
	w.printf("  Abgewiesen: Anmeldung %6d, Scope %d, nur interaktiv %d\n",
		c.RefusedAuth, c.RefusedScope, c.RefusedInteractive)

	if len(s.Roles) > 0 {
		w.printf("\n  Nach Rolle:\n")
		for _, r := range s.Roles {
			w.printf("    %-20s %6d\n", r.Role, r.Operations)
		}
	}

	// The part that names people, and it names them because being turned away is the event.
	// Ordered by the most recent attempt, so the top of the list is what happened last night.
	if len(s.Refused) > 0 {
		w.printf("\nABGEWIESENE ANMELDUNGEN\n")
		for _, r := range s.Refused {
			w.printf("  %-40s %-16s %-12s %2dx  zuletzt %s\n",
				refusedIdentity(r), r.Reason, r.Door, r.Attempts,
				r.LastAt.Format("2006-01-02 15:04"))
		}
		w.printf("\n  Wer hier steht, hat eine HM-Kennung und in Tallox kein Konto — oder ein\n")
		w.printf("  Token, das nicht mehr gilt. Das eine wird unter /verwaltung/personen behoben,\n")
		w.printf("  das andere von der Person selbst.\n")
	}

	if len(s.Mutations) > 0 {
		w.printf("\nÄNDERUNGEN\n")
		for _, m := range s.Mutations {
			w.printf("  %-40s %-28s %2dx  zuletzt %s\n",
				m.Mail, m.Field, m.Calls, m.LastAt.Format("2006-01-02 15:04"))
		}
	}

	w.printf("\nAufbewahrung: %s. Gelöscht wurden %d Einträge vor dem %s.\n",
		humanDuration(domain.AccessLogRetention), report.Pruned,
		report.Cutoff.Format("2006-01-02"))
	w.printf("Einzelheiten unter /verwaltung/zugriffe.\n")

	// Last line, and the only one a machine reads.
	w.printf("%s%s\n", notableMarker, yesNo(report.Notable()))

	if w.err != nil {
		// The report is gone rather than truncated-and-silent. In the nightly run this means
		// the cron wrapper's pipe went away, which is worth a line in the log it also writes.
		log.Error().Err(w.err).Msg("cannot write the access report")
	}
}

// reportWriter is fmt.Fprintf with the error kept rather than dropped.
//
// A whole page of Fprintf calls has a whole page of error returns, and checking each one inline
// would bury the report in error handling for a failure that is the same failure every time:
// the destination went away. The first one is kept, the rest are skipped, and the caller says
// so once.
type reportWriter struct {
	out io.Writer
	err error
}

func (w *reportWriter) printf(format string, a ...any) {
	if w.err != nil {
		return
	}
	_, w.err = fmt.Fprintf(w.out, format, a...)
}

// refusedIdentity names whoever was turned away, with whichever identifier the door had.
//
// Three cases, and the third is not padding: the browser door knows an address, the token door
// knows the token's public half, and a credential too malformed to parse knows neither. The
// third is somebody sending something that is not a Tallox credential at all, and a report that
// rendered it as an empty column would hide the one line that says so.
func refusedIdentity(r domain.RefusedSignIn) string {
	switch {
	case r.Mail != "":
		return r.Mail
	case r.TokenID != "":
		return "Token " + r.TokenID
	default:
		return "(kein Credential lesbar)"
	}
}

// humanDuration renders a whole number of days as days and anything else as Go's own form.
//
// "90 Tage" rather than "2160h0m0s". The retention period is the number somebody has to be able
// to state when asked what this installation keeps, and a report that answers in hours makes
// them do the arithmetic.
func humanDuration(d time.Duration) string {
	if d%(24*time.Hour) == 0 && d >= 24*time.Hour {
		days := int(d / (24 * time.Hour))
		if days == 1 {
			return "24 Stunden"
		}
		return fmt.Sprintf("%d Tage", days)
	}
	if d%time.Hour == 0 {
		return fmt.Sprintf("%d Stunden", int(d/time.Hour))
	}
	return d.String()
}

func yesNo(b bool) string {
	if b {
		return "ja"
	}
	return "nein"
}
