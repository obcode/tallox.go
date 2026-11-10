package bootstrap

import (
	"io"
	"time"

	"github.com/obcode/tallox.go/internal/domain"
)

// Test-only exports.
//
// The report renderer is unexported because nothing outside this package should be printing
// it, and it is tested from bootstrap_test because that is where every other test of this
// package lives — driving the assembled server rather than a hand-wired one. This file is the
// standard Go way to have both.

// PrintAccessReportForTest renders the nightly report. See printAccessReport.
func PrintAccessReportForTest(out io.Writer, report domain.AccessReport, since time.Duration) {
	printAccessReport(out, report, since, nil)
}

// PrintAccessReportWithMailForTest renders the nightly report with the mail figures.
func PrintAccessReportWithMailForTest(out io.Writer, report domain.AccessReport, since time.Duration,
	mail domain.MailCounts) {
	printAccessReport(out, report, since, &mail)
}

// PrintProjectionWarningsForTest renders the projection lines of the nightly ZPA report. See
// printProjectionWarnings.
func PrintProjectionWarningsForTest(out io.Writer, run domain.ZPASyncRun, projections []domain.CatalogueProjection) {
	printProjectionWarnings(out, run, projections)
}
