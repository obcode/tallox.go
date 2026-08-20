package store_test

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/store"
	"github.com/obcode/tallox.go/internal/store/storetest"
)

func semesters(t *testing.T) (*store.Semesters, *storetest.Schema) {
	t.Helper()

	s := storetest.New(t)
	return store.NewSemesters(s.Pool), s
}

func TestCreateAndReadSemester(t *testing.T) {
	t.Parallel()

	semesters, _ := semesters(t)

	created, err := semesters.CreateSemester(t.Context(), "2027-SS")
	if err != nil {
		t.Fatalf("cannot create: %v", err)
	}

	switch {
	case created.Code != "2027-SS":
		t.Errorf("code = %q, want 2027-SS", created.Code)
	case created.Phase != policy.PhaseDemandPlanning:
		t.Errorf("phase = %s, want %s — a new semester is at the start of the process",
			created.Phase, policy.PhaseDemandPlanning)
	case !created.WishesPublishedAt.IsZero():
		t.Errorf("wishesPublishedAt = %v, want the zero time", created.WishesPublishedAt)
	case created.ID == uuid.Nil:
		t.Error("the id is nil")
	}

	found, err := semesters.SemesterByID(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("cannot read back: %v", err)
	}
	if found.ID != created.ID || found.Code != created.Code {
		t.Errorf("read back %+v, want %+v", found, created)
	}
}

func TestSemesterByIDReportsAMissingRow(t *testing.T) {
	t.Parallel()

	semesters, _ := semesters(t)

	_, err := semesters.SemesterByID(t.Context(), uuid.New())
	if !errors.Is(err, domain.ErrNoSuchSemester) {
		t.Errorf("err = %v, want ErrNoSuchSemester", err)
	}
}

func TestDuplicateSemesterIsRefusedWithoutTheDriverMessage(t *testing.T) {
	t.Parallel()

	semesters, _ := semesters(t)

	if _, err := semesters.CreateSemester(t.Context(), "2027-SS"); err != nil {
		t.Fatalf("cannot create: %v", err)
	}

	_, err := semesters.CreateSemester(t.Context(), "2027-SS")
	if !errors.Is(err, domain.ErrSemesterExists) {
		t.Fatalf("err = %v, want ErrSemesterExists", err)
	}

	// The habit, established where it is harmless. Which semesters exist is not confidential;
	// which colleagues have registered interest is, and that write path raises the same
	// SQLSTATE. A verbatim constraint name here would be the pattern to copy there.
	for _, noise := range []string{"23505", "duplicate key", "semester_code_key", "SQLSTATE"} {
		if strings.Contains(err.Error(), noise) {
			t.Errorf("the refusal carries %q from the driver:\n  %v", noise, err)
		}
	}
}

// TestSemesterCodeFormatIsEnforcedByTheDatabase covers the half of the rule that
// internal/domain cannot: an import, a migration or a hand-written INSERT does not go through
// the service, and a single malformed code breaks the chronological sort for the whole table.
func TestSemesterCodeFormatIsEnforcedByTheDatabase(t *testing.T) {
	t.Parallel()

	_, s := semesters(t)
	q := s.Queries()

	// "2027S" is in the list because it is the shape this column held until migration 7: the
	// old form has to be refused now, or the two spellings would coexist and the sort below
	// would be lexicographic without being chronological.
	for _, code := range []string{
		"2027S", "WS 2026", "2026 WS", "SS2027", "2027", "27S", "2027X", "2026-ws", "",
		"2026-W", "2026-SW", "2026_WS",
	} {
		if _, err := q.CreateSemester(t.Context(), code); err == nil {
			t.Errorf("the database accepted the code %q", code)
		}
	}
}

// TestSemestersSortChronologically is the property the whole system's ORDER BY relies on, and
// the reason the code has the shape it has.
//
// Lexicographic on the code is chronological because the year leads and SS precedes WS within
// a year — which is also the order the terms happen in. Seeded out of order on purpose.
func TestSemestersSortChronologically(t *testing.T) {
	t.Parallel()

	semesters, _ := semesters(t)

	for _, code := range []string{"2027-SS", "2025-WS", "2026-WS", "2027-WS", "2026-SS"} {
		if _, err := semesters.CreateSemester(t.Context(), code); err != nil {
			t.Fatalf("cannot create %s: %v", code, err)
		}
	}

	list, err := semesters.Semesters(t.Context())
	if err != nil {
		t.Fatalf("cannot list: %v", err)
	}

	got := make([]string, 0, len(list))
	for _, s := range list {
		got = append(got, s.Code)
	}

	want := []string{"2027-WS", "2027-SS", "2026-WS", "2026-SS", "2025-WS"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v (newest first)", got, want)
	}
}

func TestAdvanceSemesterPhase(t *testing.T) {
	t.Parallel()

	semesters, _ := semesters(t)

	created, err := semesters.CreateSemester(t.Context(), "2027-SS")
	if err != nil {
		t.Fatalf("cannot create: %v", err)
	}

	moved, err := semesters.AdvanceSemesterPhase(t.Context(), created.ID,
		policy.PhaseDemandPlanning, policy.PhaseWishes)
	if err != nil {
		t.Fatalf("cannot advance: %v", err)
	}
	if moved.Phase != policy.PhaseWishes {
		t.Errorf("phase = %s, want %s", moved.Phase, policy.PhaseWishes)
	}
	if !moved.UpdatedAt.After(created.UpdatedAt) {
		t.Errorf("updatedAt did not move: %v then %v", created.UpdatedAt, moved.UpdatedAt)
	}
}

// TestAdvanceRefusesWhenTheSemesterMovedOn is the compare-and-set, asserted directly.
//
// The second caller believes the semester is still in DEMAND_PLANNING. Without the predicate
// in the UPDATE, their write would land and the semester would arrive in ASSIGNMENT having
// skipped WISHES — a phase nobody chose, and indistinguishable afterwards from one somebody
// did.
func TestAdvanceRefusesWhenTheSemesterMovedOn(t *testing.T) {
	t.Parallel()

	semesters, _ := semesters(t)

	created, err := semesters.CreateSemester(t.Context(), "2027-SS")
	if err != nil {
		t.Fatalf("cannot create: %v", err)
	}

	if _, err := semesters.AdvanceSemesterPhase(t.Context(), created.ID,
		policy.PhaseDemandPlanning, policy.PhaseWishes); err != nil {
		t.Fatalf("the first switch failed: %v", err)
	}

	_, err = semesters.AdvanceSemesterPhase(t.Context(), created.ID,
		policy.PhaseDemandPlanning, policy.PhaseAssignment)
	if !errors.Is(err, domain.ErrPhaseMovedOn) {
		t.Fatalf("err = %v, want ErrPhaseMovedOn", err)
	}

	// And nothing changed.
	after, err := semesters.SemesterByID(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("cannot read back: %v", err)
	}
	if after.Phase != policy.PhaseWishes {
		t.Errorf("phase = %s, want %s — the refused switch wrote anyway",
			after.Phase, policy.PhaseWishes)
	}
}

// TestConcurrentAdvancesProduceOneWinner is the same rule under the conditions it exists for.
//
// Two people from the dean's office clicking at the same moment is the situation a phase
// switch happens in. Exactly one of these two must land, and the loser has to be told rather
// than silently overwritten.
func TestConcurrentAdvancesProduceOneWinner(t *testing.T) {
	t.Parallel()

	semesters, _ := semesters(t)

	created, err := semesters.CreateSemester(t.Context(), "2027-SS")
	if err != nil {
		t.Fatalf("cannot create: %v", err)
	}

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		winners int
		losers  int
	)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()

			_, err := semesters.AdvanceSemesterPhase(t.Context(), created.ID,
				policy.PhaseDemandPlanning, policy.PhaseWishes)

			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				winners++
			case errors.Is(err, domain.ErrPhaseMovedOn):
				losers++
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()

	if winners != 1 || losers != 1 {
		t.Errorf("winners = %d, losers = %d, want 1 and 1", winners, losers)
	}
}

func TestAdvanceReportsAMissingSemester(t *testing.T) {
	t.Parallel()

	semesters, _ := semesters(t)

	_, err := semesters.AdvanceSemesterPhase(t.Context(), uuid.New(),
		policy.PhaseDemandPlanning, policy.PhaseWishes)
	if !errors.Is(err, domain.ErrNoSuchSemester) {
		t.Errorf("err = %v, want ErrNoSuchSemester — a broken link and a stale page need "+
			"different answers", err)
	}
}

// TestPublishingIsIdempotentAndKeepsTheFirstTimestamp covers both halves of the query's
// COALESCE.
//
// The second call must not be an error — the caller wanted the wishes published and they are —
// and it must not move the timestamp, because when publication happened is a fact about the
// process. updatedAt stays put too, so a repeated call does not make the row look edited.
func TestPublishingIsIdempotentAndKeepsTheFirstTimestamp(t *testing.T) {
	t.Parallel()

	semesters, _ := semesters(t)

	created, err := semesters.CreateSemester(t.Context(), "2027-SS")
	if err != nil {
		t.Fatalf("cannot create: %v", err)
	}

	first, err := semesters.PublishSemesterWishes(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("cannot publish: %v", err)
	}
	if first.WishesPublishedAt.IsZero() {
		t.Fatal("wishesPublishedAt is still zero after publishing")
	}

	second, err := semesters.PublishSemesterWishes(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("publishing twice is not an error, but it failed: %v", err)
	}
	if !second.WishesPublishedAt.Equal(first.WishesPublishedAt) {
		t.Errorf("the second call moved the timestamp: %v then %v",
			first.WishesPublishedAt, second.WishesPublishedAt)
	}
	if !second.UpdatedAt.Equal(first.UpdatedAt) {
		t.Errorf("the second call touched updatedAt: %v then %v",
			first.UpdatedAt, second.UpdatedAt)
	}
}

// TestPublishingIsIndependentOfThePhase is the schema half of the decision the policy makes.
//
// No constraint ties the two together, deliberately: the wish phase can close without
// publishing, and publication can happen while the assignment is already running. A test here
// because the tempting simplification is a CHECK, and a CHECK would be discovered by a member
// of the dean's office on the day they need the other order.
func TestPublishingIsIndependentOfThePhase(t *testing.T) {
	t.Parallel()

	semesters, _ := semesters(t)

	for _, phase := range policy.AllPhases() {
		created, err := semesters.CreateSemester(t.Context(), codeFor(t, phase))
		if err != nil {
			t.Fatalf("cannot create: %v", err)
		}

		// Walk it to the phase under test, one legal step at a time. AllPhases is in process
		// order, so stepping along it is the forward path — and for DEMAND_PLANNING that is
		// no steps at all, which is why the check comes before the move.
		path := policy.AllPhases()
		current := path[0]
		for _, step := range path[1:] {
			if current == phase {
				break
			}
			if _, err := semesters.AdvanceSemesterPhase(t.Context(), created.ID, current, step); err != nil {
				t.Fatalf("cannot step %s -> %s: %v", current, step, err)
			}
			current = step
		}

		published, err := semesters.PublishSemesterWishes(t.Context(), created.ID)
		if err != nil {
			t.Errorf("cannot publish while in %s: %v", phase, err)
			continue
		}
		if published.Phase != phase {
			t.Errorf("phase = %s, want %s — publishing changed it", published.Phase, phase)
		}
	}
}

// codeFor gives each phase in the loop above its own semester, since the code is unique.
func codeFor(t *testing.T, phase policy.Phase) string {
	t.Helper()

	for i, known := range policy.AllPhases() {
		if known == phase {
			return "202" + string(rune('0'+i)) + "-SS"
		}
	}
	t.Fatalf("unknown phase %s", phase)
	return ""
}

// TestDatabaseAndPolicyAgreeOnPhases is the same triangle as the roles one: the CHECK
// constraint decides what can be stored, internal/policy decides what it means, and the two
// cannot import each other.
//
// Drift is unpleasant in both directions. A phase the policy knows and the database rejects
// makes the switch fail with a constraint violation in front of whoever is running the
// process; a phase the database accepts and the policy does not know is a row this build
// refuses to act on at all.
func TestDatabaseAndPolicyAgreeOnPhases(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)

	// Scoped to this test's schema — every parallel test has a constraint of the same name,
	// and they come and go while this query runs.
	var definition string
	err := s.Pool.QueryRow(t.Context(),
		`SELECT pg_get_constraintdef(c.oid)
		   FROM pg_constraint c
		   JOIN pg_class t ON t.oid = c.conrelid
		   JOIN pg_namespace n ON n.oid = t.relnamespace
		  WHERE c.conname = 'semester_phase_is_known' AND n.nspname = current_schema()`,
	).Scan(&definition)
	if err != nil {
		t.Fatalf("cannot read the phase constraint — has it been renamed? %v", err)
	}

	for _, phase := range policy.AllPhases() {
		if !strings.Contains(definition, "'"+string(phase)+"'") {
			t.Errorf("policy knows the phase %s and the constraint does not list it:\n  %s",
				phase, definition)
		}
	}

	for _, literal := range strings.Split(definition, "'") {
		if literal == "" || strings.ContainsAny(literal, " (),=:") {
			continue // SQL between the quoted literals
		}
		if _, ok := policy.ParsePhase(literal); !ok {
			t.Errorf("the database accepts the phase %q, which internal/policy does not know",
				literal)
		}
	}
}

// TestUnknownPhasesCannotBeStored is the write side of that agreement.
func TestUnknownPhasesCannotBeStored(t *testing.T) {
	t.Parallel()

	semesters, s := semesters(t)
	q := s.Queries()

	created, err := semesters.CreateSemester(t.Context(), "2027-SS")
	if err != nil {
		t.Fatalf("cannot create: %v", err)
	}

	_, err = q.AdvanceSemesterPhase(t.Context(), store.AdvanceSemesterPhaseParams{
		ID:      created.ID,
		Phase:   "PLANNING_RETREAT",
		Phase_2: string(policy.PhaseDemandPlanning),
	})
	if err == nil {
		t.Error("the database accepted a phase internal/policy does not know")
	}
}
