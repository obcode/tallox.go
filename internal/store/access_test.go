package store_test

import (
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/store"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// record is one entry, spelled out enough for a test to change only what it cares about.
func record(p testdata.Persona, outcome domain.AccessOutcome, fields ...string) domain.AccessRecord {
	id := p.ID()
	return domain.AccessRecord{
		ActorID:   &id,
		ActorMail: p.Mail,
		Door:      domain.AccessDoorInteractive,
		Roles:     []string{string(policy.RoleLecturer)},
		Operation: "Test",
		Fields:    fields,
		Outcome:   outcome,
		Duration:  7 * time.Millisecond,
	}
}

func TestAccessLogRecordsAndReadsBack(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	storetest.SeedPerson(t, s, testdata.Eins, string(policy.RoleLecturer))

	access := store.NewAccess(s.Pool)

	rec := record(testdata.Eins, domain.AccessOK, "myWishes")
	rec.SourceIP = netip.MustParseAddr("10.0.0.7")
	rec.NarrowedFrom = []string{string(policy.RoleLecturer), string(policy.RoleAdmin)}
	if err := access.Record(t.Context(), rec); err != nil {
		t.Fatalf("cannot record: %v", err)
	}

	entries, err := access.Entries(t.Context(), domain.AccessFilter{})
	if err != nil {
		t.Fatalf("cannot read: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}

	got := entries[0]
	if got.ActorMail != testdata.Eins.Mail {
		t.Errorf("mail = %q, want %q", got.ActorMail, testdata.Eins.Mail)
	}
	// Resolved through the join, so the page can show a name without the log storing one.
	if got.ActorName != testdata.Eins.Name {
		t.Errorf("name = %q, want %q", got.ActorName, testdata.Eins.Name)
	}
	if len(got.Fields) != 1 || got.Fields[0] != "myWishes" {
		t.Errorf("fields = %v, want [myWishes]", got.Fields)
	}
	if got.Duration != 7*time.Millisecond {
		t.Errorf("duration = %v, want 7ms", got.Duration)
	}
	if got.SourceIP.String() != "10.0.0.7" {
		t.Errorf("source ip = %v, want 10.0.0.7", got.SourceIP)
	}
	if len(got.NarrowedFrom) != 2 {
		t.Errorf("narrowedFrom = %v, want two roles", got.NarrowedFrom)
	}
}

// TestAccessLogRecordsARefusedSignIn is the case with no person row — somebody with an HM
// account who is not in this installation's directory. It is the entry an administrator most
// wants to see, and the one a design that keys on person_id cannot store at all.
func TestAccessLogRecordsARefusedSignIn(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	access := store.NewAccess(s.Pool)

	err := access.Record(t.Context(), domain.AccessRecord{
		ActorMail: "niemand@example.org",
		Door:      domain.AccessDoorInteractive,
		Outcome:   domain.AccessRefusedAuth,
		ErrorCode: "UNKNOWN_USER",
		SourceIP:  netip.MustParseAddr("10.0.0.9"),
	})
	if err != nil {
		t.Fatalf("cannot record a refusal: %v", err)
	}

	entries, err := access.Entries(t.Context(), domain.AccessFilter{OnlyRefused: true})
	if err != nil {
		t.Fatalf("cannot read: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d refusals, want 1", len(entries))
	}
	if entries[0].ActorID != nil {
		t.Errorf("actor id = %v, want nil — there is no person row", entries[0].ActorID)
	}
	if entries[0].ErrorCode != "UNKNOWN_USER" {
		t.Errorf("error code = %q, want UNKNOWN_USER", entries[0].ErrorCode)
	}
}

// TestAccessLogFiltersAreAndedTogether pins that the filters narrow rather than replace each
// other. Written because the query is a chain of "argument IS NULL OR …" clauses, which is the
// shape where one forgotten pair of brackets turns a filter into a no-op that still returns
// plausible rows.
func TestAccessLogFiltersAreAndedTogether(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	storetest.SeedPerson(t, s, testdata.Eins, string(policy.RoleLecturer))
	storetest.SeedPerson(t, s, testdata.Zwei, string(policy.RoleLecturer))

	access := store.NewAccess(s.Pool)

	einsRead := record(testdata.Eins, domain.AccessOK, "semesters")
	einsWrite := record(testdata.Eins, domain.AccessOK, "setPersonRoles")
	einsWrite.Mutation = true
	zweiRead := record(testdata.Zwei, domain.AccessOK, "semesters")
	zweiRead.Door = domain.AccessDoorToken

	for _, rec := range []domain.AccessRecord{einsRead, einsWrite, zweiRead} {
		if err := access.Record(t.Context(), rec); err != nil {
			t.Fatalf("cannot record: %v", err)
		}
	}

	einsID := testdata.Eins.ID()

	for _, tc := range []struct {
		name   string
		filter domain.AccessFilter
		want   int
	}{
		{"everything", domain.AccessFilter{}, 3},
		{"one person", domain.AccessFilter{ActorID: &einsID}, 2},
		{"one door", domain.AccessFilter{Door: domain.AccessDoorToken}, 1},
		{"only changes", domain.AccessFilter{OnlyMutations: true}, 1},
		{"person and changes", domain.AccessFilter{ActorID: &einsID, OnlyMutations: true}, 1},
		{"the other person's changes", domain.AccessFilter{
			ActorID: ptrOf(testdata.Zwei.ID()), OnlyMutations: true,
		}, 0},
		{"a substring of the address", domain.AccessFilter{Mail: "zwei@"}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries, err := access.Entries(t.Context(), tc.filter)
			if err != nil {
				t.Fatalf("cannot read: %v", err)
			}
			if len(entries) != tc.want {
				t.Errorf("got %d entries, want %d", len(entries), tc.want)
			}
		})
	}
}

// TestAccessLogPagesWithoutSkippingOrRepeating is why the cursor carries the id as well as the
// timestamp. Three entries written in one batch can share a microsecond, and a cursor on the
// timestamp alone drops whichever of them landed second.
func TestAccessLogPagesWithoutSkippingOrRepeating(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	storetest.SeedPerson(t, s, testdata.Eins, string(policy.RoleLecturer))

	access := store.NewAccess(s.Pool)

	const total = 7
	for range total {
		if err := access.Record(t.Context(), record(testdata.Eins, domain.AccessOK, "semesters")); err != nil {
			t.Fatalf("cannot record: %v", err)
		}
	}

	seen := map[uuid.UUID]bool{}
	var cursor *uuid.UUID
	for range total { // an upper bound on the number of pages, so a broken cursor cannot hang
		page, err := access.Entries(t.Context(), domain.AccessFilter{Limit: 3, Before: cursor})
		if err != nil {
			t.Fatalf("cannot read a page: %v", err)
		}
		if len(page) == 0 {
			break
		}
		for _, entry := range page {
			if seen[entry.ID] {
				t.Fatalf("entry %s appeared on two pages", entry.ID)
			}
			seen[entry.ID] = true
		}
		last := page[len(page)-1].ID
		cursor = &last
	}

	if len(seen) != total {
		t.Errorf("paged through %d entries, want %d", len(seen), total)
	}
}

func TestAccessLogSummaryCountsWhatHappened(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	storetest.SeedPerson(t, s, testdata.Eins, string(policy.RoleLecturer))
	storetest.SeedPerson(t, s, testdata.Sechs, string(policy.RoleAdmin))

	access := store.NewAccess(s.Pool)

	read := record(testdata.Eins, domain.AccessOK, "semesters")

	grant := record(testdata.Sechs, domain.AccessOK, "setPersonRoles")
	grant.Roles = []string{string(policy.RoleAdmin)}
	grant.Mutation = true

	viaToken := record(testdata.Eins, domain.AccessRefusedScope, "people")
	viaToken.Door = domain.AccessDoorToken
	viaToken.ErrorCode = "INSUFFICIENT_SCOPE"

	refused := domain.AccessRecord{
		ActorMail: "niemand@example.org",
		Door:      domain.AccessDoorInteractive,
		Outcome:   domain.AccessRefusedAuth,
		ErrorCode: "UNKNOWN_USER",
	}
	// Twice, so that the grouping is exercised: somebody who cannot get in tries again.
	for _, rec := range []domain.AccessRecord{read, grant, viaToken, refused, refused} {
		if err := access.Record(t.Context(), rec); err != nil {
			t.Fatalf("cannot record: %v", err)
		}
	}

	from := time.Now().Add(-time.Hour)
	summary, err := access.Summary(t.Context(), from, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("cannot summarise: %v", err)
	}

	if summary.Counts.Total != 5 {
		t.Errorf("total = %d, want 5", summary.Counts.Total)
	}
	if summary.Counts.Token != 1 {
		t.Errorf("token = %d, want 1", summary.Counts.Token)
	}
	if summary.Counts.Mutations != 1 {
		t.Errorf("mutations = %d, want 1", summary.Counts.Mutations)
	}
	if summary.Counts.RefusedAuth != 2 {
		t.Errorf("refusedAuth = %d, want 2", summary.Counts.RefusedAuth)
	}
	if summary.Counts.RefusedScope != 1 {
		t.Errorf("refusedScope = %d, want 1", summary.Counts.RefusedScope)
	}
	if summary.Counts.People != 2 {
		t.Errorf("people = %d, want 2 — the refusals have no person row", summary.Counts.People)
	}

	// Two identical refusals are one line with a count, not two lines.
	if len(summary.Refused) != 1 {
		t.Fatalf("got %d refusal groups, want 1: %+v", len(summary.Refused), summary.Refused)
	}
	if summary.Refused[0].Attempts != 2 {
		t.Errorf("attempts = %d, want 2", summary.Refused[0].Attempts)
	}
	if summary.Refused[0].Mail != "niemand@example.org" {
		t.Errorf("mail = %q, want niemand@example.org", summary.Refused[0].Mail)
	}

	if len(summary.Mutations) != 1 {
		t.Fatalf("got %d changes, want 1: %+v", len(summary.Mutations), summary.Mutations)
	}
	if summary.Mutations[0].Field != "setPersonRoles" {
		t.Errorf("field = %q, want setPersonRoles", summary.Mutations[0].Field)
	}
	if summary.Mutations[0].Mail != testdata.Sechs.Mail {
		t.Errorf("mail = %q, want %q", summary.Mutations[0].Mail, testdata.Sechs.Mail)
	}

	roles := map[string]int64{}
	for _, r := range summary.Roles {
		roles[r.Role] = r.Operations
	}
	// Two: the read and the scope refusal. The two refused sign-ins carry no roles at all,
	// which is the point of the constraint that forbids them.
	if roles[string(policy.RoleLecturer)] != 2 {
		t.Errorf("LECTURER = %d, want 2", roles[string(policy.RoleLecturer)])
	}
	if roles[string(policy.RoleAdmin)] != 1 {
		t.Errorf("ADMIN = %d, want 1", roles[string(policy.RoleAdmin)])
	}
}

// TestAccessLogSummaryRespectsItsWindow guards the half-open interval. A window that included
// its upper bound would count the same entry in two consecutive nightly reports.
func TestAccessLogSummaryRespectsItsWindow(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	storetest.SeedPerson(t, s, testdata.Eins, string(policy.RoleLecturer))

	access := store.NewAccess(s.Pool)
	if err := access.Record(t.Context(), record(testdata.Eins, domain.AccessOK, "semesters")); err != nil {
		t.Fatalf("cannot record: %v", err)
	}

	entries, err := access.Entries(t.Context(), domain.AccessFilter{})
	if err != nil {
		t.Fatalf("cannot read: %v", err)
	}
	at := entries[0].At

	summary, err := access.Summary(t.Context(), at, at)
	if err != nil {
		t.Fatalf("cannot summarise: %v", err)
	}
	if summary.Counts.Total != 0 {
		t.Errorf("an empty window counted %d entries, want 0", summary.Counts.Total)
	}

	summary, err = access.Summary(t.Context(), at, at.Add(time.Millisecond))
	if err != nil {
		t.Fatalf("cannot summarise: %v", err)
	}
	if summary.Counts.Total != 1 {
		t.Errorf("the window holding the entry counted %d, want 1", summary.Counts.Total)
	}
}

func TestAccessLogPruneDeletesOnlyWhatIsOlder(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	storetest.SeedPerson(t, s, testdata.Eins, string(policy.RoleLecturer))

	access := store.NewAccess(s.Pool)
	if err := access.Record(t.Context(), record(testdata.Eins, domain.AccessOK, "semesters")); err != nil {
		t.Fatalf("cannot record: %v", err)
	}

	// An entry from beyond the retention period. Written by hand because Record deliberately
	// has no way to backdate one — the timestamp is the database's.
	old := time.Now().Add(-domain.AccessLogRetention - time.Hour)
	if _, err := s.Pool.Exec(t.Context(),
		`INSERT INTO access_log (at, actor_mail, door, mutation, outcome)
		 VALUES ($1, $2, 'INTERACTIVE', false, 'OK')`, old, testdata.Eins.Mail); err != nil {
		t.Fatalf("cannot insert an old entry: %v", err)
	}

	deleted, err := access.Prune(t.Context(), time.Now().Add(-domain.AccessLogRetention))
	if err != nil {
		t.Fatalf("cannot prune: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted %d entries, want 1", deleted)
	}

	entries, err := access.Entries(t.Context(), domain.AccessFilter{})
	if err != nil {
		t.Fatalf("cannot read: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("%d entries survived, want 1", len(entries))
	}
}

// TestAccessLogRefusesRolesOnARefusedSignIn pins the CHECK constraint. A refusal happened
// before there were any roles, so an entry carrying both is a write that filled the row in
// from a half-built actor — and that is worth catching in the database rather than in review.
func TestAccessLogRefusesRolesOnARefusedSignIn(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	access := store.NewAccess(s.Pool)

	err := access.Record(t.Context(), domain.AccessRecord{
		ActorMail: "niemand@example.org",
		Door:      domain.AccessDoorInteractive,
		Roles:     []string{string(policy.RoleAdmin)},
		Outcome:   domain.AccessRefusedAuth,
	})
	if err == nil {
		t.Fatal("a refused sign-in with roles was accepted, want a constraint violation")
	}
}

func ptrOf(id uuid.UUID) *uuid.UUID { return &id }
