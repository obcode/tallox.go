package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/obcode/tallox.go/internal/domain"
)

// Access is the persistence of the access log.
type Access struct {
	q *Queries
}

// NewAccess binds the access-log queries to a pool or transaction.
func NewAccess(db DBTX) *Access { return &Access{q: New(db)} }

var _ domain.AccessStore = (*Access)(nil)

// Record appends one entry.
//
// Called on the request path for every operation, so it does exactly one round trip and reads
// nothing back. Its caller treats a failure as a log line and not as a reason to fail the
// request — see graph.RecordAccess for why that is the right way round.
func (a *Access) Record(ctx context.Context, rec domain.AccessRecord) error {
	params := RecordAccessParams{
		ActorID:      nullUUID(rec.ActorID),
		ActorMail:    textOrNil(rec.ActorMail),
		Door:         string(rec.Door),
		TokenID:      textOrNil(rec.TokenID),
		Roles:        nonNilStrings(rec.Roles),
		NarrowedFrom: rec.NarrowedFrom,
		Operation:    textOrNil(rec.Operation),
		Fields:       nonNilStrings(rec.Fields),
		Mutation:     rec.Mutation,
		Outcome:      string(rec.Outcome),
		ErrorCode:    textOrNil(rec.ErrorCode),
	}
	if rec.Duration > 0 {
		ms := int32(rec.Duration.Milliseconds())
		params.DurationMs = &ms
	}
	if rec.SourceIP.IsValid() {
		ip := rec.SourceIP
		params.SourceIp = &ip
	}

	if err := a.q.RecordAccess(ctx, params); err != nil {
		return fmt.Errorf("cannot record access: %w", err)
	}
	return nil
}

// Entries reads one page of the log, newest first.
func (a *Access) Entries(ctx context.Context, filter domain.AccessFilter) ([]domain.AccessEntry, error) {
	filter = filter.Normalised()

	params := AccessLogEntriesParams{
		ActorID:       nullUUID(filter.ActorID),
		Mail:          textOrNil(filter.Mail),
		Door:          textOrNil(string(filter.Door)),
		OnlyRefused:   filter.OnlyRefused,
		OnlyMutations: filter.OnlyMutations,
		From:          timestamp(filter.From),
		Until:         timestamp(filter.Until),
		BeforeID:      nullUUID(filter.Before),
		Lim:           int32(filter.Limit),
	}

	rows, err := a.q.AccessLogEntries(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("cannot read the access log: %w", err)
	}

	entries := make([]domain.AccessEntry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, entryFrom(row))
	}
	return entries, nil
}

// Summary assembles the figures for one window: [from, until).
//
// Four queries rather than one. They aggregate over different groupings and a single statement
// would need three lateral joins to produce a shape that Go then has to take apart again — and
// this runs once a night and once per page load, not per request.
func (a *Access) Summary(ctx context.Context, from, until time.Time) (domain.AccessSummary, error) {
	summary := domain.AccessSummary{From: from, Until: until}

	counts, err := a.q.AccessLogCounts(ctx, AccessLogCountsParams{From: from, Until: until})
	if err != nil {
		return domain.AccessSummary{}, fmt.Errorf("cannot count the access log: %w", err)
	}
	summary.Counts = domain.AccessCounts{
		Total:              counts.Total,
		Interactive:        counts.Interactive,
		Token:              counts.ViaToken,
		Mutations:          counts.Mutations,
		Errors:             counts.Errors,
		RefusedAuth:        counts.RefusedAuth,
		RefusedScope:       counts.RefusedScope,
		RefusedInteractive: counts.RefusedInteractive,
		People:             counts.People,
	}

	roles, err := a.q.AccessLogByRole(ctx, AccessLogByRoleParams{From: from, Until: until})
	if err != nil {
		return domain.AccessSummary{}, fmt.Errorf("cannot count the access log by role: %w", err)
	}
	for _, row := range roles {
		summary.Roles = append(summary.Roles, domain.AccessRoleCount{
			Role:       row.Role,
			Operations: row.Operations,
		})
	}

	refused, err := a.q.AccessLogRefusedSignIns(ctx, AccessLogRefusedSignInsParams{From: from, Until: until})
	if err != nil {
		return domain.AccessSummary{}, fmt.Errorf("cannot read refused sign-ins: %w", err)
	}
	for _, row := range refused {
		summary.Refused = append(summary.Refused, domain.RefusedSignIn{
			Mail:     row.Mail,
			TokenID:  row.TokenID,
			Reason:   row.Reason,
			Door:     domain.AccessDoor(row.Door),
			Attempts: row.Attempts,
			LastAt:   row.LastAt,
		})
	}

	mutations, err := a.q.AccessLogMutations(ctx, AccessLogMutationsParams{From: from, Until: until})
	if err != nil {
		return domain.AccessSummary{}, fmt.Errorf("cannot read the changes made: %w", err)
	}
	for _, row := range mutations {
		summary.Mutations = append(summary.Mutations, domain.MutationCount{
			Mail:   row.Mail,
			Field:  row.Field,
			Calls:  row.Calls,
			LastAt: row.LastAt,
		})
	}

	return summary, nil
}

// Prune deletes everything older than the cutoff and reports how much that was.
func (a *Access) Prune(ctx context.Context, cutoff time.Time) (int64, error) {
	deleted, err := a.q.PruneAccessLog(ctx, cutoff)
	if err != nil {
		return 0, fmt.Errorf("cannot prune the access log: %w", err)
	}
	return deleted, nil
}

func entryFrom(row AccessLogEntriesRow) domain.AccessEntry {
	entry := domain.AccessEntry{
		ID:           row.ID,
		At:           row.At,
		Door:         domain.AccessDoor(row.Door),
		Roles:        row.Roles,
		NarrowedFrom: row.NarrowedFrom,
		Fields:       row.Fields,
		Mutation:     row.Mutation,
		Outcome:      domain.AccessOutcome(row.Outcome),
	}
	if row.ActorID.Valid {
		id := row.ActorID.UUID
		entry.ActorID = &id
	}
	entry.ActorMail = deref(row.ActorMail)
	entry.ActorName = deref(row.ActorName)
	entry.TokenID = deref(row.TokenID)
	entry.Operation = deref(row.Operation)
	entry.ErrorCode = deref(row.ErrorCode)
	if row.DurationMs != nil {
		entry.Duration = time.Duration(*row.DurationMs) * time.Millisecond
	}
	if row.SourceIp != nil {
		entry.SourceIP = *row.SourceIp
	}
	return entry
}

// textOrNil turns the empty string into SQL NULL.
//
// The columns it feeds are all "absent or a value", never "present and empty": there is no such
// thing as a request from the empty mail address or through the empty door. Storing ” for them
// would give every later query two ways to spell absent.
func textOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// nonNilStrings keeps a NOT NULL text[] column out of the hands of a nil slice.
//
// pgx would send nil as NULL, and the column refuses it. The empty array is what "no roles" and
// "no root fields" actually mean, and both happen — an anonymous caller has no roles, and a
// refused sign-in has no fields.
func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func timestamp(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}
