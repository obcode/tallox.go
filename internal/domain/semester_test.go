package domain_test

import (
	"context"
	"testing"
	"time"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
	"github.com/obcode/tallox.go/internal/testdata"
)

// What the list of semesters offers, and what it refuses to drop.
//
// Against the fake store, because the question here is not what a statement returns — that is
// internal/store's job — but which of three sources wins where they disagree: the planning
// mark, the calendar window and the rows somebody has decided something about.

// listedAt is the codes List returns for a clock and a store, in the order it returns them.
func listedAt(t *testing.T, store *fakeSemesterStore, at time.Time) []string {
	t.Helper()

	service := domain.NewSemesterService(store, func() time.Time { return at })
	semesters, err := service.List(context.Background(),
		testdata.Eins.Actor(principal.KindInteractive, string(policy.RoleLecturer)))
	if err != nil {
		t.Fatalf("listing semesters: %v", err)
	}

	codes := make([]string, 0, len(semesters))
	for _, semester := range semesters {
		codes = append(codes, semester.Code)
	}
	return codes
}

func contains(codes []string, want string) bool {
	for _, code := range codes {
		if code == want {
			return true
		}
	}
	return false
}

// The list starts at the planning semester: nothing before it is offered, because before it
// there is nothing left to plan.
func TestListStartsAtThePlanningSemester(t *testing.T) {
	t.Parallel()

	store := newFakeSemesterStore(policy.PhaseDemandPlanning)
	store.planning = "2027-SS"

	// August 2026 is in the summer semester of 2026, so the untouched window reaches back to
	// 2025-SS and forward past 2029.
	codes := listedAt(t, store, time.Date(2026, time.August, 24, 12, 0, 0, 0, time.Local))

	for _, gone := range []string{"2025-SS", "2025-WS", "2026-SS", "2026-WS"} {
		if contains(codes, gone) {
			t.Errorf("%s is before the planning semester and should not be offered, got %v",
				gone, codes)
		}
	}
	if codes[len(codes)-1] != "2027-SS" {
		t.Errorf("the oldest offered semester is %q, want the planning semester 2027-SS (%v)",
			codes[len(codes)-1], codes)
	}
	if !contains(codes, "2027-WS") {
		t.Errorf("the semesters after the planning one are still offered, got %v", codes)
	}
}

// A semester somebody has decided something about is offered however old it is. That is the
// half that keeps a finished plan reachable — and it is also what covers "every semester with
// demand", because declaring demand records the semester in the same transaction.
func TestListKeepsRecordedSemestersBeforeIt(t *testing.T) {
	t.Parallel()

	store := newFakeSemesterStore(policy.PhaseDemandPlanning)
	store.planning = "2027-SS"
	if _, err := store.EnsureSemester(context.Background(), "2024-WS"); err != nil {
		t.Fatalf("recording a decision about an old semester: %v", err)
	}

	codes := listedAt(t, store, time.Date(2026, time.August, 24, 12, 0, 0, 0, time.Local))

	if !contains(codes, "2024-WS") {
		t.Errorf("a recorded semester fell out of the list, got %v", codes)
	}
	if contains(codes, "2025-SS") {
		t.Errorf("an untouched semester before the planning one is still offered, got %v", codes)
	}
}

// The planning semester is in the list even when the calendar window does not reach it. A list
// that offered everything except the semester being worked on would be the one thing it must
// not be.
func TestListAlwaysContainsThePlanningSemester(t *testing.T) {
	t.Parallel()

	store := newFakeSemesterStore(policy.PhaseDemandPlanning)
	if _, err := store.EnsureSemester(context.Background(), "2039-WS"); err != nil {
		t.Fatalf("recording the far semester: %v", err)
	}
	store.planning = "2039-WS"

	codes := listedAt(t, store, time.Date(2026, time.August, 24, 12, 0, 0, 0, time.Local))

	if !contains(codes, "2039-WS") {
		t.Errorf("a planning semester beyond the window fell out of the list, got %v", codes)
	}
	// Everything the calendar would have offered is before the mark, so nothing untouched is
	// left. What remains beside it are the recorded rows, which are never dropped.
	if contains(codes, "2028-SS") {
		t.Errorf("an untouched semester before the planning one is offered, got %v", codes)
	}
}

// Without a mark the whole calendar window stands, which is what this did before the mark
// existed. A fresh installation and a rolled-back schema are both in that state, and neither
// should meet an empty list.
func TestListFallsBackToTheWindowWithoutOne(t *testing.T) {
	t.Parallel()

	store := newFakeSemesterStore(policy.PhaseDemandPlanning)

	codes := listedAt(t, store, time.Date(2026, time.August, 24, 12, 0, 0, 0, time.Local))

	for _, want := range []string{"2025-SS", "2026-SS", "2029-SS"} {
		if !contains(codes, want) {
			t.Errorf("without a planning semester the window is offered whole, %s missing from %v",
				want, codes)
		}
	}
}

// Saying which semester is being planned is the dean's office's, and it is a decision about a
// semester — so it brings the row into being, exactly as advancing a phase does.
func TestSettingThePlanningSemesterRecordsTheSemester(t *testing.T) {
	t.Parallel()

	store := newFakeSemesterStore(policy.PhaseDemandPlanning)
	service := domain.NewSemesterService(store,
		func() time.Time { return time.Date(2026, time.August, 24, 12, 0, 0, 0, time.Local) })

	deansOffice := testdata.Vier.Actor(principal.KindInteractive, string(policy.RoleDeansOffice))
	set, err := service.SetPlanningSemester(context.Background(), deansOffice, "2028-ws")
	if err != nil {
		t.Fatalf("setting the planning semester: %v", err)
	}

	if set.Code != "2028-WS" {
		t.Errorf("the code came back as %q, want it upper-cased to 2028-WS", set.Code)
	}
	if !set.IsPlanning {
		t.Error("the semester that was just set does not carry the mark")
	}
	if store.planning != "2028-WS" {
		t.Errorf("the store holds %q as the planning semester, want 2028-WS", store.planning)
	}
}

// A lecturer may read which semester is being planned — every screen needs it — and may not
// move it.
func TestOnlyTheDeansOfficeMovesThePlanningSemester(t *testing.T) {
	t.Parallel()

	store := newFakeSemesterStore(policy.PhaseDemandPlanning)
	store.planning = "2027-SS"
	service := domain.NewSemesterService(store,
		func() time.Time { return time.Date(2026, time.August, 24, 12, 0, 0, 0, time.Local) })

	lecturer := testdata.Eins.Actor(principal.KindInteractive, string(policy.RoleLecturer))

	planning, err := service.PlanningSemester(context.Background(), lecturer)
	if err != nil {
		t.Fatalf("a lecturer reading the planning semester: %v", err)
	}
	if planning.Code != "2027-SS" {
		t.Errorf("a lecturer read %q as the planning semester, want 2027-SS", planning.Code)
	}

	if _, err := service.SetPlanningSemester(context.Background(), lecturer, "2028-SS"); err == nil {
		t.Error("a lecturer moved the planning semester")
	}
	if store.planning != "2027-SS" {
		t.Errorf("the refused call moved the mark to %q", store.planning)
	}
}
