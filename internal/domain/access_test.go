package domain_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/obcode/tallox.go/internal/domain"
)

// fakeAccessStore records what it was asked, so the service's own behaviour can be observed
// without a database. The queries themselves are tested against real PostgreSQL in
// internal/store — a fake there would pass while the shipped SQL leaked.
type fakeAccessStore struct {
	summary domain.AccessSummary
	pruned  int64

	summarisedFrom  time.Time
	summarisedUntil time.Time
	prunedBefore    time.Time
	pruneCalls      int

	summaryErr error
	pruneErr   error
}

func (f *fakeAccessStore) Record(context.Context, domain.AccessRecord) error { return nil }

func (f *fakeAccessStore) Entries(context.Context, domain.AccessFilter) ([]domain.AccessEntry, error) {
	return nil, nil
}

func (f *fakeAccessStore) Summary(_ context.Context, from, until time.Time) (domain.AccessSummary, error) {
	f.summarisedFrom, f.summarisedUntil = from, until
	return f.summary, f.summaryErr
}

func (f *fakeAccessStore) Prune(_ context.Context, cutoff time.Time) (int64, error) {
	f.pruneCalls++
	f.prunedBefore = cutoff
	return f.pruned, f.pruneErr
}

// TestTheNightlyRunAlsoPrunes.
//
// Both in one call, and therefore in one crontab line. A separate prune job would be a second
// line somebody can forget on a new host, and the failure mode of forgetting it is a table that
// keeps a year of colleagues' movements while everyone believes it keeps ninety days.
func TestTheNightlyRunAlsoPrunes(t *testing.T) {
	t.Parallel()

	store := &fakeAccessStore{pruned: 42}
	service := domain.NewAccessService(store)

	now := time.Date(2026, 8, 24, 3, 45, 0, 0, time.UTC)
	report, err := service.Report(t.Context(), now, 24*time.Hour)
	if err != nil {
		t.Fatalf("cannot produce the report: %v", err)
	}

	if !store.summarisedFrom.Equal(now.Add(-24 * time.Hour)) {
		t.Errorf("summarised from %v, want %v", store.summarisedFrom, now.Add(-24*time.Hour))
	}
	if !store.summarisedUntil.Equal(now) {
		t.Errorf("summarised until %v, want %v", store.summarisedUntil, now)
	}
	if store.pruneCalls != 1 {
		t.Errorf("pruned %d times, want once", store.pruneCalls)
	}
	if want := now.Add(-domain.AccessLogRetention); !store.prunedBefore.Equal(want) {
		t.Errorf("pruned before %v, want %v", store.prunedBefore, want)
	}
	if report.Pruned != 42 {
		t.Errorf("report says %d pruned, want 42", report.Pruned)
	}
}

// TestAQuietNightStillPrunes. "Nothing happened last night" is not a reason to keep what is
// older than ninety days — and a prune that only ran on busy nights would keep the oldest
// entries longest, which is precisely backwards.
func TestAQuietNightStillPrunes(t *testing.T) {
	t.Parallel()

	store := &fakeAccessStore{}
	service := domain.NewAccessService(store)

	if _, err := service.Report(t.Context(), time.Now(), 24*time.Hour); err != nil {
		t.Fatalf("cannot produce the report: %v", err)
	}
	if store.pruneCalls != 1 {
		t.Errorf("an empty window pruned %d times, want once", store.pruneCalls)
	}
}

// TestAFailedPruneIsReported. Silence here is the failure that matters: the report would look
// exactly like a successful one while the retention period quietly stopped being enforced.
func TestAFailedPruneIsReported(t *testing.T) {
	t.Parallel()

	store := &fakeAccessStore{pruneErr: errors.New("no space left on device")}
	service := domain.NewAccessService(store)

	if _, err := service.Report(t.Context(), time.Now(), 24*time.Hour); err == nil {
		t.Fatal("a failed prune produced no error")
	}
}

// TestWhatMakesANightWorthAMail.
//
// Volumes never do. A busy Tuesday is not news; somebody being turned away is, and so is
// somebody changing something. That is what the cron wrapper's quiet mode keys on.
func TestWhatMakesANightWorthAMail(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		summary domain.AccessSummary
		want    bool
	}{
		{
			name:    "four hundred reads and nothing else",
			summary: domain.AccessSummary{Counts: domain.AccessCounts{Total: 400, Interactive: 400}},
			want:    false,
		},
		{
			name: "somebody was turned away",
			summary: domain.AccessSummary{
				Refused: []domain.RefusedSignIn{{Mail: "niemand@example.org"}},
			},
			want: true,
		},
		{
			name: "somebody changed something",
			summary: domain.AccessSummary{
				Mutations: []domain.MutationCount{{Field: "setPersonRoles"}},
			},
			want: true,
		},
		{
			name: "a token asked for more than it was minted for",
			summary: domain.AccessSummary{
				Counts: domain.AccessCounts{Total: 3, RefusedScope: 3},
			},
			want: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := domain.AccessReport{Summary: tc.summary}
			if got := report.Notable(); got != tc.want {
				t.Errorf("Notable = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestAPageIsAlwaysBounded. An audit trail is the one table where "select everything and filter
// afterwards" is not merely slow but wrong: an unbounded read is how one support question turns
// into a term of colleagues' movements in a single response.
func TestAPageIsAlwaysBounded(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		in   int
		want int
	}{
		{"unset", 0, domain.DefaultAccessLimit},
		{"negative", -5, domain.DefaultAccessLimit},
		{"reasonable", 25, 25},
		{"beyond the ceiling", 100000, domain.MaxAccessLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := domain.AccessFilter{Limit: tc.in}.Normalised().Limit
			if got != tc.want {
				t.Errorf("limit %d normalised to %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}
