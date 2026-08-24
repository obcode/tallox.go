package domain

import (
	"context"
	"fmt"
	"time"
)

// AccessService is the access log as the rest of the program uses it.
//
// Thin on purpose. The rules that matter about this log are in two places that are not here:
// what may be stored is decided by the table (see the migration), and who may read it is
// decided by policy.MayReadAccessLog. What is left for a service is the one piece of behaviour
// the two callers share — the nightly report — so that the page and the mail cannot come to
// different conclusions about the same night.
type AccessService struct {
	store AccessStore
}

// NewAccessService binds the service to its store.
func NewAccessService(store AccessStore) *AccessService {
	return &AccessService{store: store}
}

// Record appends one entry.
//
// Returns the error rather than swallowing it: whether a failure to log is worth failing the
// request over is the caller's decision, and on the request path the answer is no. Deciding it
// here would make that answer the same everywhere, including in the nightly job where a silent
// failure would be wrong.
func (s *AccessService) Record(ctx context.Context, rec AccessRecord) error {
	return s.store.Record(ctx, rec)
}

// Entries reads one page of the log, newest first.
func (s *AccessService) Entries(ctx context.Context, filter AccessFilter) ([]AccessEntry, error) {
	return s.store.Entries(ctx, filter)
}

// Summary assembles the figures for the window [from, until).
func (s *AccessService) Summary(ctx context.Context, from, until time.Time) (AccessSummary, error) {
	return s.store.Summary(ctx, from, until)
}

// AccessReport is one nightly run: what happened, and what was cleared out.
type AccessReport struct {
	Summary AccessSummary
	// Pruned is how many entries fell out of the retention window on this run.
	//
	// Part of the report rather than a silent side effect. A prune that has stopped working
	// looks exactly like a quiet night unless the number is printed, and the whole point of a
	// retention period is that somebody can say it is being kept.
	Pruned int64
	// Cutoff is the moment before which nothing is kept any more.
	Cutoff time.Time
}

// Report produces the nightly report for the window ending at now and covering the given span,
// and prunes what has fallen out of the retention period.
//
// Both in one call, and therefore in one cron line. A separate prune job would be a second line
// somebody can forget to add on a new host — and the failure mode of forgetting it is a table
// that keeps a year of colleagues' movements while everyone believes it keeps ninety days.
//
// The prune runs even when the report window is empty. "Nothing happened last night" is not a
// reason to keep what is older than ninety days.
func (s *AccessService) Report(ctx context.Context, now time.Time, window time.Duration) (AccessReport, error) {
	summary, err := s.store.Summary(ctx, now.Add(-window), now)
	if err != nil {
		return AccessReport{}, err
	}

	cutoff := now.Add(-AccessLogRetention)
	pruned, err := s.store.Prune(ctx, cutoff)
	if err != nil {
		return AccessReport{}, fmt.Errorf("the report is complete but the log was not pruned: %w", err)
	}

	return AccessReport{Summary: summary, Pruned: pruned, Cutoff: cutoff}, nil
}

// Notable reports whether this report has anything in it worth a mail beyond the figures.
//
// Used by the nightly script through the exit code, so that an installation which would rather
// not get a mail every single night can still be sure of getting one on the nights that matter.
// Refused sign-ins and changes are the two, and both are events rather than volumes: somebody
// was turned away, or somebody changed something.
func (r AccessReport) Notable() bool {
	return len(r.Summary.Refused) > 0 || len(r.Summary.Mutations) > 0 ||
		r.Summary.Counts.RefusedScope > 0 || r.Summary.Counts.RefusedInteractive > 0
}
