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
// What has to be right is the defaults, and they point in opposite directions. An absent window
// means **open**: the opposite of every other default in this schema, and what makes "in principle
// always possible" true. An absent release means **shut**: since 2026-10-31 nobody registers
// interest in a programme's instances until its demand is announced as settled. Both are the kind
// of thing a fake store gets right by accident while the shipped query returns NULL.

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
	storetest.ReleaseWishes(t, f.schema, f.semester.Code, storetest.FixtureProgrammeA)

	if _, err := wishes.Set(ctx, owner, f.instance, domain.WishHappyTo, "", false, nil); err != nil {
		t.Fatalf("registering interest with an open window failed: %v", err)
	}

	if _, err := m.SetWishWindow(ctx, f.semester.Code, f.group, false, testdata.Drei.ID()); err != nil {
		t.Fatalf("cannot shut the window: %v", err)
	}

	_, err := wishes.Set(ctx, owner, f.instance, domain.WishFirstChoice, "", false, nil)
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
	if _, err := wishes.Set(ctx, owner, f.instance, domain.WishFirstChoice, "", false, nil); err != nil {
		t.Errorf("registering interest after reopening failed: %v", err)
	}
}

// TestAnAbsentReleaseIsShut is the other default, read through the query the write path uses —
// and the programme it counts is the instance's own.
func TestAnAbsentReleaseIsShut(t *testing.T) {
	t.Parallel()

	f, m := marks(t)
	ctx := t.Context()

	released := func() bool {
		t.Helper()
		where, err := f.wishes.WishWriteContext(ctx, f.instance)
		if err != nil || !where.Found() {
			t.Fatalf("cannot read the context: %v (found %v)", err, where.Found())
		}
		return where.WishesReleased
	}

	if released() {
		t.Error("a programme nobody has released takes wishes — it must be shut")
	}

	// Another programme's release opens nothing here.
	if _, err := m.AnnounceDemandComplete(ctx, f.semester.Code, storetest.FixtureProgrammeB,
		testdata.Vier.ID()); err != nil {
		t.Fatalf("cannot release the other programme: %v", err)
	}
	if released() {
		t.Error("the release of another programme opened this programme's instance")
	}

	if _, err := m.AnnounceDemandComplete(ctx, f.semester.Code, storetest.FixtureProgrammeA,
		testdata.Vier.ID()); err != nil {
		t.Fatalf("cannot release the programme: %v", err)
	}
	if !released() {
		t.Error("the instance's own programme released it, and it is still shut")
	}

	if _, err := m.WithdrawDemandComplete(ctx, f.semester.Code, storetest.FixtureProgrammeA); err != nil {
		t.Fatalf("cannot withdraw the release: %v", err)
	}
	if released() {
		t.Error("a withdrawn release still lets wishes in")
	}
}

// TestTheReleaseGatesEveryWishWrite is the rule through the service: registering, bounding and
// withdrawing all wait for the programme, and a withdrawn release keeps what was entered.
func TestTheReleaseGatesEveryWishWrite(t *testing.T) {
	t.Parallel()

	f, m := marks(t)
	ctx := t.Context()

	wishes := domain.NewWishService(f.wishes,
		domain.NewSemesterService(store.NewSemesters(f.schema.Pool), nil))
	owner := testdata.Eins.Actor(principal.KindInteractive, string(policy.RoleLecturer))
	limit := 1

	if _, err := wishes.Set(ctx, owner, f.instance, domain.WishHappyTo, "", false, nil); !errors.Is(err,
		domain.ErrWishesNotReleased) {
		t.Errorf("registering interest before the release gave %v, want ErrWishesNotReleased", err)
	}
	if err := wishes.SetTrackLimit(ctx, owner, f.instance, &limit); !errors.Is(err,
		domain.ErrWishesNotReleased) {
		t.Errorf("bounding before the release gave %v, want ErrWishesNotReleased", err)
	}

	if _, err := m.AnnounceDemandComplete(ctx, f.semester.Code, storetest.FixtureProgrammeA,
		testdata.Vier.ID()); err != nil {
		t.Fatalf("cannot release the programme: %v", err)
	}
	if _, err := wishes.Set(ctx, owner, f.instance, domain.WishHappyTo, "", false, nil); err != nil {
		t.Fatalf("registering interest after the release failed: %v", err)
	}

	if _, err := m.WithdrawDemandComplete(ctx, f.semester.Code, storetest.FixtureProgrammeA); err != nil {
		t.Fatalf("cannot withdraw the release: %v", err)
	}

	// What was entered stays, and stays the owner's to read.
	mine, err := wishes.Mine(ctx, owner, f.semester.Code)
	if err != nil || len(mine) != 1 {
		t.Fatalf("the wish entered while released is gone: %v (%d rows)", err, len(mine))
	}

	// But it is not changed, bounded or withdrawn until the programme releases again.
	if _, err := wishes.Set(ctx, owner, f.instance, domain.WishFirstChoice, "", false, nil); !errors.Is(
		err, domain.ErrWishesNotReleased) {
		t.Errorf("correcting after the release was withdrawn gave %v, want ErrWishesNotReleased", err)
	}
	if err := wishes.SetTrackLimit(ctx, owner, f.instance, &limit); !errors.Is(err,
		domain.ErrWishesNotReleased) {
		t.Errorf("bounding after the release was withdrawn gave %v, want ErrWishesNotReleased", err)
	}
	if err := wishes.Withdraw(ctx, owner, mine[0].ID); !errors.Is(err, domain.ErrWishesNotReleased) {
		t.Errorf("withdrawing after the release was withdrawn gave %v, want ErrWishesNotReleased", err)
	}

	// Released again with the subject's round shut: the release comes first, the window answers.
	if _, err := m.AnnounceDemandComplete(ctx, f.semester.Code, storetest.FixtureProgrammeA,
		testdata.Vier.ID()); err != nil {
		t.Fatalf("cannot release the programme again: %v", err)
	}
	if _, err := m.SetWishWindow(ctx, f.semester.Code, f.group, false, testdata.Drei.ID()); err != nil {
		t.Fatalf("cannot shut the window: %v", err)
	}
	if _, err := wishes.Set(ctx, owner, f.instance, domain.WishFirstChoice, "", false, nil); !errors.Is(
		err, domain.ErrWishWindowClosed) {
		t.Errorf("released with a shut window gave %v, want ErrWishWindowClosed", err)
	}
}

// TestWishRoundsCountTheGroupsPublishingWaitsFor is the fraction under the phase bar: subject
// groups with instances in the semester, and how many of them have shut their round.
//
// The groups have to be OpenWishWindowsOfSemester's, or "3 of 5 closed" and the refusal of
// publishWishes would disagree about how many are still open.
func TestWishRoundsCountTheGroupsPublishingWaitsFor(t *testing.T) {
	t.Parallel()

	f, m := marks(t)
	ctx := t.Context()

	count := func() domain.WishRounds {
		t.Helper()
		got, err := m.WishRounds(ctx, f.semester.Code)
		if err != nil {
			t.Fatalf("cannot count: %v", err)
		}
		return got
	}
	shut := func(group uuid.UUID, open bool) {
		t.Helper()
		if _, err := m.SetWishWindow(ctx, f.semester.Code, group, open, uuid.Nil); err != nil {
			t.Fatalf("cannot switch the window: %v", err)
		}
	}

	// MATHE has the fixture's instance, SWE has none: one group, nobody has shut anything.
	if got := count(); got != (domain.WishRounds{Closed: 0, Groups: 1}) {
		t.Errorf("before any decision: %+v, want 0 of 1", got)
	}

	// A group with no instance in the semester holds nothing up and closes nothing.
	shut(f.otherGroup, false)
	if got := count(); got != (domain.WishRounds{Closed: 0, Groups: 1}) {
		t.Errorf("after shutting a group without instances: %+v, want 0 of 1", got)
	}

	// A second module in SWE, with an instance: now two groups, of which SWE is shut.
	second := moduleID(t, f.schema, storetest.FixtureModuleDutyDiffers)
	modules := store.NewModules(f.schema.Pool)
	if _, err := modules.SetModuleComponents(ctx, second, []domain.ModuleComponent{
		{Kind: domain.PartKindLecture, TeachingHours: 2, Position: 0},
	}, uuid.Nil); err != nil {
		t.Fatalf("cannot state the second module's split: %v", err)
	}
	if _, err := f.schema.Pool.Exec(ctx,
		`INSERT INTO module_subject_group (module_id, subject_group_id) VALUES ($1, $2)`,
		second, f.otherGroup); err != nil {
		t.Fatalf("cannot put the second module in a subject group: %v", err)
	}
	if _, err := store.NewDemand(f.schema.Pool, modules).CreateCourseInstance(ctx,
		domain.NewCourseInstance{
			SemesterID: f.semester.ID, ModuleID: second, ProgrammeID: f.programme,
		}); err != nil {
		t.Fatalf("cannot declare the second instance: %v", err)
	}
	if got := count(); got != (domain.WishRounds{Closed: 1, Groups: 2}) {
		t.Errorf("with a second, shut group: %+v, want 1 of 2", got)
	}

	shut(f.group, false)
	if got := count(); got != (domain.WishRounds{Closed: 2, Groups: 2}) {
		t.Errorf("with both shut: %+v, want 2 of 2", got)
	}

	// Reopening keeps the row and counts as open again.
	shut(f.group, true)
	if got := count(); got != (domain.WishRounds{Closed: 1, Groups: 2}) {
		t.Errorf("after reopening one: %+v, want 1 of 2", got)
	}

	// The same set publishing waits for: the open ones are groups - closed.
	waiting, err := store.NewSemesters(f.schema.Pool).OpenWishWindows(ctx, f.semester.ID)
	if err != nil {
		t.Fatalf("cannot list the open rounds: %v", err)
	}
	if got := count(); len(waiting) != got.Groups-got.Closed {
		t.Errorf("publishing waits for %d groups, the count says %d of %d are closed",
			len(waiting), got.Closed, got.Groups)
	}

	// An inactive group is not one anybody runs a round for.
	if _, err := f.schema.Pool.Exec(ctx,
		`UPDATE subject_group SET active = false WHERE id = $1`, f.group); err != nil {
		t.Fatalf("cannot retire the group: %v", err)
	}
	if got := count(); got != (domain.WishRounds{Closed: 1, Groups: 1}) {
		t.Errorf("with the open group retired: %+v, want 1 of 1", got)
	}

	// Another semester's rounds are not this one's.
	other, err := m.WishRounds(ctx, "2031-SS")
	if err != nil {
		t.Fatalf("cannot count another semester: %v", err)
	}
	if other != (domain.WishRounds{}) {
		t.Errorf("a semester without instances counts %+v", other)
	}
}
