package store_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/store"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// The first release moves the semester on to the wishes (2026-11-10), against a real database.
//
// What has to be right is the compare-and-set: only DEMAND_PLANNING moves, only to WISHES, and a
// release racing the dean's office still ends exactly one step on.

// phaseOf reads the phase back from the row, not from anything the call returned.
func phaseOf(t *testing.T, s *storetest.Schema, code string) policy.Phase {
	t.Helper()

	semester, err := store.NewSemesters(s.Pool).SemesterByCode(t.Context(), code)
	if err != nil {
		t.Fatalf("cannot read the semester back: %v", err)
	}
	return semester.Phase
}

// moveTo walks the semester forward one step at a time to the phase a case needs.
func moveTo(t *testing.T, s *storetest.Schema, semester domain.Semester, to policy.Phase) {
	t.Helper()

	steps := []policy.Phase{policy.PhaseDemandPlanning, policy.PhaseWishes, policy.PhaseAssignment,
		policy.PhaseReview, policy.PhaseFinal}
	semesters := store.NewSemesters(s.Pool)
	for i := 1; i < len(steps) && steps[i-1] != to; i++ {
		if _, err := semesters.AdvanceSemesterPhase(t.Context(), semester.ID, steps[i-1],
			steps[i]); err != nil {
			t.Fatalf("cannot move the semester to %s: %v", steps[i], err)
		}
	}
	if got := phaseOf(t, s, semester.Code); got != to {
		t.Fatalf("the semester is in %s, want %s to start the case from", got, to)
	}
}

// TestTheFirstReleaseMovesTheSemesterOnToTheWishes is the step itself, and that it is the only one.
func TestTheFirstReleaseMovesTheSemesterOnToTheWishes(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		from, want policy.Phase
	}{
		{policy.PhaseDemandPlanning, policy.PhaseWishes},
		{policy.PhaseWishes, policy.PhaseWishes},
		{policy.PhaseAssignment, policy.PhaseAssignment},
		{policy.PhaseReview, policy.PhaseReview},
		{policy.PhaseFinal, policy.PhaseFinal},
	} {
		t.Run(string(tc.from), func(t *testing.T) {
			t.Parallel()

			f, m := marks(t)
			moveTo(t, f.schema, f.semester, tc.from)

			if _, err := m.AnnounceDemandComplete(t.Context(), f.semester.Code,
				storetest.FixtureProgrammeA, testdata.Vier.ID()); err != nil {
				t.Fatalf("cannot release: %v", err)
			}
			if got := phaseOf(t, f.schema, f.semester.Code); got != tc.want {
				t.Errorf("a release in %s left the semester in %s, want %s", tc.from, got, tc.want)
			}
		})
	}
}

// TestWithdrawingTheReleaseKeepsTheWishes is the one-way street: withdrawing never moves back.
func TestWithdrawingTheReleaseKeepsTheWishes(t *testing.T) {
	t.Parallel()

	f, m := marks(t)
	ctx := t.Context()

	if _, err := m.AnnounceDemandComplete(ctx, f.semester.Code, storetest.FixtureProgrammeA,
		testdata.Vier.ID()); err != nil {
		t.Fatalf("cannot release: %v", err)
	}
	if _, err := m.WithdrawDemandComplete(ctx, f.semester.Code, storetest.FixtureProgrammeA); err != nil {
		t.Fatalf("cannot withdraw: %v", err)
	}
	if got := phaseOf(t, f.schema, f.semester.Code); got != policy.PhaseWishes {
		t.Errorf("withdrawing the only release moved the semester to %s, want it to stay in WISHES", got)
	}
}

// TestAReleaseAfterSwitchingBackMovesOnAgain: the dean's office may go back to the demand
// planning, and the next release is again the start of the wishes.
func TestAReleaseAfterSwitchingBackMovesOnAgain(t *testing.T) {
	t.Parallel()

	f, m := marks(t)
	ctx := t.Context()

	if _, err := m.AnnounceDemandComplete(ctx, f.semester.Code, storetest.FixtureProgrammeA,
		testdata.Vier.ID()); err != nil {
		t.Fatalf("cannot release: %v", err)
	}
	if _, err := store.NewSemesters(f.schema.Pool).AdvanceSemesterPhase(ctx, f.semester.ID,
		policy.PhaseWishes, policy.PhaseDemandPlanning); err != nil {
		t.Fatalf("cannot switch back: %v", err)
	}
	if _, err := m.AnnounceDemandComplete(ctx, f.semester.Code, storetest.FixtureProgrammeB,
		testdata.Vier.ID()); err != nil {
		t.Fatalf("cannot release the other programme: %v", err)
	}
	if got := phaseOf(t, f.schema, f.semester.Code); got != policy.PhaseWishes {
		t.Errorf("a release after switching back left the semester in %s, want WISHES", got)
	}
}

// TestAWrittenMarkMovesNoPhase is why the step is in Go and not a trigger: a fixture or a repair
// that writes the mark directly leaves the phase where it is.
func TestAWrittenMarkMovesNoPhase(t *testing.T) {
	t.Parallel()

	f, _ := marks(t)
	storetest.ReleaseWishes(t, f.schema, f.semester.Code, storetest.FixtureProgrammeA)

	if got := phaseOf(t, f.schema, f.semester.Code); got != policy.PhaseDemandPlanning {
		t.Errorf("a mark inserted directly moved the semester to %s", got)
	}
}

// TestAReleaseRacingTheDeansOfficeIsOneStep: both want WISHES at the same moment. Whoever wins,
// the semester ends in WISHES — never ASSIGNMENT, which would be a step nobody chose — and the
// loser of a manual switch is told the phase moved on rather than overwriting anything.
func TestAReleaseRacingTheDeansOfficeIsOneStep(t *testing.T) {
	t.Parallel()

	f, m := marks(t)
	ctx := t.Context()
	semesters := store.NewSemesters(f.schema.Pool)

	for round := range 10 {
		code := fmt.Sprintf("%d-WS", 2028+round)
		semester, err := semesters.EnsureSemester(ctx, code)
		if err != nil {
			t.Fatalf("cannot record %s: %v", code, err)
		}

		var wg sync.WaitGroup
		var releaseErr, switchErr error
		start := make(chan struct{})
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, releaseErr = m.AnnounceDemandComplete(ctx, code, storetest.FixtureProgrammeA,
				testdata.Vier.ID())
		}()
		go func() {
			defer wg.Done()
			<-start
			_, switchErr = semesters.AdvanceSemesterPhase(ctx, semester.ID,
				policy.PhaseDemandPlanning, policy.PhaseWishes)
		}()
		close(start)
		wg.Wait()

		if releaseErr != nil {
			t.Fatalf("%s: the release failed: %v", code, releaseErr)
		}
		if switchErr != nil && !errors.Is(switchErr, domain.ErrPhaseMovedOn) {
			t.Fatalf("%s: the manual switch failed with %v, want success or ErrPhaseMovedOn",
				code, switchErr)
		}
		if got := phaseOf(t, f.schema, code); got != policy.PhaseWishes {
			t.Errorf("%s: after the race the semester is in %s, want exactly one step to WISHES",
				code, got)
		}
	}
}

// TestDemandReleaseCountsOnlyPlannedProgrammes is the fraction the GUI shows, on both sides.
func TestDemandReleaseCountsOnlyPlannedProgrammes(t *testing.T) {
	t.Parallel()

	f, m := marks(t)
	ctx := t.Context()

	count := func() domain.DemandRelease {
		t.Helper()
		got, err := m.DemandRelease(ctx, f.semester.Code)
		if err != nil {
			t.Fatalf("cannot count: %v", err)
		}
		return got
	}

	var planned int
	if err := f.schema.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM programme WHERE planning_status = 'PLANNED'`).Scan(&planned); err != nil {
		t.Fatalf("cannot count the planned programmes: %v", err)
	}
	if planned < 2 {
		t.Fatalf("the fixture plans %d programmes, the case needs at least two", planned)
	}

	if got := count(); got != (domain.DemandRelease{Released: 0, Planned: planned}) {
		t.Errorf("before any release: %+v, want 0 of %d", got, planned)
	}

	for _, code := range []string{storetest.FixtureProgrammeA, storetest.FixtureProgrammeB} {
		if _, err := m.AnnounceDemandComplete(ctx, f.semester.Code, code, testdata.Vier.ID()); err != nil {
			t.Fatalf("cannot release %s: %v", code, err)
		}
	}
	if got := count(); got != (domain.DemandRelease{Released: 2, Planned: planned}) {
		t.Errorf("after two releases: %+v, want 2 of %d", got, planned)
	}

	// The faculty stops planning one of them: it leaves both sides, release or not.
	if _, err := f.schema.Pool.Exec(ctx,
		`UPDATE programme SET planning_status = 'DISCONTINUED' WHERE code = $1`,
		storetest.FixtureProgrammeB); err != nil {
		t.Fatalf("cannot stop planning the programme: %v", err)
	}
	if got := count(); got != (domain.DemandRelease{Released: 1, Planned: planned - 1}) {
		t.Errorf("with a released programme no longer planned: %+v, want 1 of %d", got, planned-1)
	}

	// Another semester's releases are not this one's.
	other, err := m.DemandRelease(ctx, "2031-SS")
	if err != nil {
		t.Fatalf("cannot count another semester: %v", err)
	}
	if other.Released != 0 {
		t.Errorf("a semester nobody released anything in counts %d releases", other.Released)
	}
}
