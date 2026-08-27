package store_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
	"github.com/obcode/tallox.go/internal/store"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// The two planning marks against a real database.
//
// The one that has to be right is the default: an absent row means **open**. It is the opposite of
// every other default in this schema, it is what makes "in principle always possible" true, and it
// is the kind of thing a fake store gets right by accident while the shipped query returns NULL.

// marks builds a wish fixture plus the store the marks live in.
func marks(t *testing.T) (wishFixture, *store.PlanningMarks) {
	t.Helper()

	f := newWishFixture(t)
	return f, store.NewPlanningMarks(f.schema.Pool)
}

// TestAnAbsentWishWindowIsOpen is the default, read through the query the write path uses.
func TestAnAbsentWishWindowIsOpen(t *testing.T) {
	t.Parallel()

	f, _ := marks(t)

	where, err := f.wishes.WishWriteContext(t.Context(), f.instance)
	if err != nil {
		t.Fatalf("cannot read the context: %v", err)
	}
	if !where.Found() {
		t.Fatal("the instance was not found")
	}
	if !where.WindowOpen {
		t.Error("a subject group nobody has decided anything about is closed — it must be open, " +
			"or the deploy of migration 18 would have shut every group in the faculty")
	}
	if where.SubjectGroupID != f.group {
		t.Errorf("the context carries subject group %s, want %s", where.SubjectGroupID, f.group)
	}
}

// TestAModuleInNoSubjectGroupHasAnOpenWindow is the second half of the same default.
//
// The LEFT JOIN chain reaches no window at all here, so the COALESCE is what answers — and it has
// to answer open, because a module nobody has sorted is the ordinary state until the catalogue is
// worked through, not a module whose wishes are shut.
func TestAModuleInNoSubjectGroupHasAnOpenWindow(t *testing.T) {
	t.Parallel()

	f, m := marks(t)

	// Shut the group the module is in, then take the module out of it. The window still exists and
	// says shut; this instance must no longer be reached by it.
	if _, err := m.SetWishWindow(t.Context(), f.semester.Code, f.group, false, uuid.Nil); err != nil {
		t.Fatalf("cannot shut the window: %v", err)
	}
	if _, err := f.schema.Pool.Exec(t.Context(),
		`DELETE FROM module_subject_group WHERE module_id = $1`, f.module); err != nil {
		t.Fatalf("cannot unsort the module: %v", err)
	}

	where, err := f.wishes.WishWriteContext(t.Context(), f.instance)
	if err != nil {
		t.Fatalf("cannot read the context: %v", err)
	}
	if !where.WindowOpen {
		t.Error("a module in no subject group inherited a shut window from the group it left")
	}
}

// TestShuttingAndReopeningAWishWindowKeepsTheRow is the difference from the announcement.
//
// A window that was shut and opened again is not the same as one nobody ever touched: somebody
// took two decisions, and the second one is worth being able to see.
func TestShuttingAndReopeningAWishWindowKeepsTheRow(t *testing.T) {
	t.Parallel()

	f, m := marks(t)
	ctx := t.Context()

	shut, err := m.SetWishWindow(ctx, f.semester.Code, f.group, false, testdata.Drei.ID())
	if err != nil {
		t.Fatalf("cannot shut: %v", err)
	}
	if shut.Open {
		t.Error("shutting the window left it open")
	}

	where, err := f.wishes.WishWriteContext(ctx, f.instance)
	if err != nil {
		t.Fatalf("cannot read the context: %v", err)
	}
	if where.WindowOpen {
		t.Error("the write path still sees the window as open after it was shut")
	}

	reopened, err := m.SetWishWindow(ctx, f.semester.Code, f.group, true, testdata.Drei.ID())
	if err != nil {
		t.Fatalf("cannot reopen: %v", err)
	}
	if !reopened.Open {
		t.Error("reopening the window left it shut")
	}

	windows, err := m.WishWindows(ctx, f.semester.Code)
	if err != nil {
		t.Fatalf("cannot list the windows: %v", err)
	}
	if len(windows) != 1 {
		t.Fatalf("after shutting and reopening there are %d rows, want 1 — the row is kept so "+
			"that the second decision is visible", len(windows))
	}
	if !windows[0].Open || windows[0].ChangedBy != testdata.Drei.ID() {
		t.Errorf("the kept row is %+v, want open and attributed", windows[0])
	}
}

// TestWishWindowsListsOnlyTheExceptions is the shape callers have to read correctly.
func TestWishWindowsListsOnlyTheExceptions(t *testing.T) {
	t.Parallel()

	f, m := marks(t)

	windows, err := m.WishWindows(t.Context(), f.semester.Code)
	if err != nil {
		t.Fatalf("cannot list the windows: %v", err)
	}
	if len(windows) != 0 {
		t.Errorf("a semester nobody has switched anything in lists %d windows, want none — this "+
			"list is the exceptions and not the state of every group", len(windows))
	}
}

// TestAnnouncingTheDemandTwiceRefreshesRatherThanDuplicating is the announcement's own shape.
//
// The opposite of a publication mark, which keeps its first timestamp because it records something
// irreversible. Here a reader wants to know how fresh the statement is.
func TestAnnouncingTheDemandTwiceRefreshesRatherThanDuplicating(t *testing.T) {
	t.Parallel()

	f, m := marks(t)
	ctx := t.Context()

	first, err := m.AnnounceDemandComplete(ctx, f.semester.Code, storetest.FixtureProgrammeA, testdata.Vier.ID())
	if err != nil {
		t.Fatalf("cannot announce: %v", err)
	}

	again, err := m.AnnounceDemandComplete(ctx, f.semester.Code, storetest.FixtureProgrammeA, testdata.Vier.ID())
	if err != nil {
		t.Fatalf("cannot re-announce: %v", err)
	}
	if !again.CompletedAt.After(first.CompletedAt) && !again.CompletedAt.Equal(first.CompletedAt) {
		t.Errorf("re-announcing moved the timestamp backwards: %v then %v",
			first.CompletedAt, again.CompletedAt)
	}

	list, err := m.DemandCompletions(ctx, f.semester.Code)
	if err != nil {
		t.Fatalf("cannot list: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("announcing twice left %d rows, want 1", len(list))
	}
}

// TestWithdrawingAnAnnouncementRemovesTheRow is the other difference from the window.
//
// "Never announced" and "announced, then withdrawn" are the same state — the demand is not settled
// — so keeping the difference would be keeping something nobody reads.
func TestWithdrawingAnAnnouncementRemovesTheRow(t *testing.T) {
	t.Parallel()

	f, m := marks(t)
	ctx := t.Context()

	if _, err := m.AnnounceDemandComplete(ctx, f.semester.Code, storetest.FixtureProgrammeA, uuid.Nil); err != nil {
		t.Fatalf("cannot announce: %v", err)
	}

	removed, err := m.WithdrawDemandComplete(ctx, f.semester.Code, storetest.FixtureProgrammeA)
	if err != nil {
		t.Fatalf("cannot withdraw: %v", err)
	}
	if !removed {
		t.Error("withdrawing an announcement that was there reported nothing removed")
	}

	list, err := m.DemandCompletions(ctx, f.semester.Code)
	if err != nil {
		t.Fatalf("cannot list: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("after withdrawing there are %d announcements, want none", len(list))
	}

	// Withdrawing what is not there is not an error, only nothing.
	again, err := m.WithdrawDemandComplete(ctx, f.semester.Code, storetest.FixtureProgrammeA)
	if err != nil {
		t.Fatalf("withdrawing twice failed: %v", err)
	}
	if again {
		t.Error("withdrawing twice reported something removed the second time")
	}
}

// TestAShutWindowRefusesAWishAndAReopenedOneTakesItAgain is the door, through the service.
//
// The whole point of the correction of 2026-08-28: this is what ends a wish round, and it is held
// by the subject group rather than by the faculty's phase.
func TestAShutWindowRefusesAWishAndAReopenedOneTakesItAgain(t *testing.T) {
	t.Parallel()

	f, m := marks(t)
	ctx := t.Context()

	wishes := domain.NewWishService(f.wishes,
		domain.NewSemesterService(store.NewSemesters(f.schema.Pool), nil))
	owner := testdata.Eins.Actor(principal.KindInteractive, string(policy.RoleLecturer))

	if _, err := wishes.Set(ctx, owner, f.instance, domain.WishHappyTo, ""); err != nil {
		t.Fatalf("registering interest with an open window failed: %v", err)
	}

	if _, err := m.SetWishWindow(ctx, f.semester.Code, f.group, false, testdata.Drei.ID()); err != nil {
		t.Fatalf("cannot shut the window: %v", err)
	}

	_, err := wishes.Set(ctx, owner, f.instance, domain.WishFirstChoice, "")
	if !errors.Is(err, domain.ErrWishWindowClosed) {
		t.Errorf("registering interest with a shut window gave %v, want ErrWishWindowClosed", err)
	}

	// Correcting is bound by the same door, because a list that may be added to but not corrected
	// is worse than a closed one.
	mine, err := wishes.Mine(ctx, owner, f.semester.Code)
	if err != nil || len(mine) != 1 {
		t.Fatalf("cannot read my own wish back: %v (%d rows)", err, len(mine))
	}
	if err := wishes.Withdraw(ctx, owner, mine[0].ID); !errors.Is(err, domain.ErrWishWindowClosed) {
		t.Errorf("withdrawing with a shut window gave %v, want ErrWishWindowClosed", err)
	}

	if _, err := m.SetWishWindow(ctx, f.semester.Code, f.group, true, testdata.Drei.ID()); err != nil {
		t.Fatalf("cannot reopen: %v", err)
	}
	if _, err := wishes.Set(ctx, owner, f.instance, domain.WishFirstChoice, ""); err != nil {
		t.Errorf("registering interest after reopening failed: %v", err)
	}
}
