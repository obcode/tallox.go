package bootstrap_test

import (
	"strings"
	"testing"
	"time"

	"github.com/obcode/tallox.go/bootstrap"
	"github.com/obcode/tallox.go/internal/domain"
)

func reportWith(refused []domain.RefusedSignIn, mutations []domain.MutationCount) domain.AccessReport {
	now := time.Date(2026, 8, 24, 3, 45, 0, 0, time.Local)
	return domain.AccessReport{
		Summary: domain.AccessSummary{
			From:  now.Add(-24 * time.Hour),
			Until: now,
			Counts: domain.AccessCounts{
				Total: 412, Interactive: 400, Token: 12,
				Mutations: 9, Errors: 1, RefusedAuth: int64(len(refused)), People: 17,
			},
			Roles:     []domain.AccessRoleCount{{Role: "LECTURER", Operations: 380}},
			Refused:   refused,
			Mutations: mutations,
		},
		Pruned: 1204,
		Cutoff: now.Add(-domain.AccessLogRetention),
	}
}

// TestTheNightlyReportNamesWhoWasTurnedAway.
//
// The figures are volumes and the refusals are events. An administrator scanning this at
// breakfast has to be able to see the second without reading the first, which is why they are a
// block of their own with a heading rather than another counter.
func TestTheNightlyReportNamesWhoWasTurnedAway(t *testing.T) {
	t.Parallel()

	report := reportWith([]domain.RefusedSignIn{{
		Mail: "niemand@example.org", Reason: "UNKNOWN_USER",
		Door: domain.AccessDoorInteractive, Attempts: 3,
		LastAt: time.Date(2026, 8, 24, 2, 15, 0, 0, time.Local),
	}}, nil)

	var out strings.Builder
	bootstrap.PrintAccessReportForTest(&out, report, 24*time.Hour)
	text := out.String()

	for _, want := range []string{
		"niemand@example.org",
		"UNKNOWN_USER",
		"ABGEWIESENE ANMELDUNGEN",
		"/verwaltung/zugriffe",
		"90 Tage",
		"1204",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the report does not mention %q:\n%s", want, text)
		}
	}
}

// TestTheNightlyReportCarriesNoRawEntries.
//
// The decision this pins: the mail carries figures and events, and the entries stay behind the
// VPN and a signed-in session. A nightly mail listing every operation of every colleague is a
// movement profile leaving the system into a mailbox, and it is the thing this report must not
// quietly grow into.
func TestTheNightlyReportCarriesNoRawEntries(t *testing.T) {
	t.Parallel()

	report := reportWith(nil, []domain.MutationCount{{
		Mail: "admin@example.org", Field: "setPersonRoles", Calls: 2,
		LastAt: time.Date(2026, 8, 24, 1, 5, 0, 0, time.Local),
	}})

	var out strings.Builder
	bootstrap.PrintAccessReportForTest(&out, report, 24*time.Hour)
	text := out.String()

	// A change is named — who, which root field, how often. That is the audit half.
	for _, want := range []string{"ÄNDERUNGEN", "admin@example.org", "setPersonRoles"} {
		if !strings.Contains(text, want) {
			t.Errorf("the report does not mention %q:\n%s", want, text)
		}
	}

	// Length is the crude but honest guard: this report is bounded by the number of PEOPLE and
	// FIELDS, never by the number of operations. 412 operations must not be 412 lines.
	if lines := strings.Count(text, "\n"); lines > 40 {
		t.Errorf("the report is %d lines for 412 operations — has it started listing entries?\n%s",
			lines, text)
	}
}

// TestTheReportSaysWhetherItIsWorthReading.
//
// The last line is the only part a machine reads: the cron wrapper greps it to decide whether
// to send mail on a quiet night. A marker line rather than an exit code, because a nonzero exit
// is how cron itself decides to mail and the two would fight.
func TestTheReportSaysWhetherItIsWorthReading(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		report domain.AccessReport
		want   string
	}{
		{"a quiet night", reportWith(nil, nil), "Berichtenswert: nein"},
		{"somebody was turned away", reportWith([]domain.RefusedSignIn{{
			Mail: "niemand@example.org", Reason: "UNKNOWN_USER", Attempts: 1,
		}}, nil), "Berichtenswert: ja"},
		{"something was changed", reportWith(nil, []domain.MutationCount{{
			Mail: "admin@example.org", Field: "setPersonRoles", Calls: 1,
		}}), "Berichtenswert: ja"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out strings.Builder
			bootstrap.PrintAccessReportForTest(&out, tc.report, 24*time.Hour)

			lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
			if got := lines[len(lines)-1]; got != tc.want {
				t.Errorf("last line = %q, want %q", got, tc.want)
			}
		})
	}
}
