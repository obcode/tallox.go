package store_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/store"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// Advance wishes ("Vormerkungen") against a real database: the conversion is three data-modifying
// statements in one, and the filter is a WHERE clause — neither is anything a fake would test.

// advanceSemester has no row and no instance until a test gives it one.
const advanceSemester = "2028-SS"

type advanceFixture struct {
	wishFixture
	demand *store.Demand
	marks  *store.PlanningMarks
}

func newAdvanceFixture(t *testing.T) advanceFixture {
	t.Helper()
	f := newWishFixture(t)
	return advanceFixture{
		wishFixture: f,
		demand:      store.NewDemand(f.schema.Pool, store.NewModules(f.schema.Pool)),
		marks:       store.NewPlanningMarks(f.schema.Pool),
	}
}

func (f advanceFixture) note(t *testing.T, who testdata.Persona, programme uuid.UUID,
	priority domain.WishPriority, note string, maxTracks *int) uuid.UUID {
	t.Helper()
	id, err := f.wishes.SetAdvanceWish(t.Context(), who.ID(), programme, domain.NewAdvanceWish{
		SemesterCode: advanceSemester, ModuleID: f.module, Priority: priority, Note: note,
		MaxTracks: maxTracks,
	})
	if err != nil {
		t.Fatalf("cannot note down the advance wish: %v", err)
	}
	return id
}

func (f advanceFixture) semester(t *testing.T) domain.Semester {
	t.Helper()
	s, err := store.NewSemesters(f.schema.Pool).EnsureSemester(t.Context(), advanceSemester)
	if err != nil {
		t.Fatalf("cannot record the semester: %v", err)
	}
	return s
}

func (f advanceFixture) cohort(t *testing.T, programme uuid.UUID, track string) uuid.UUID {
	t.Helper()
	instance, err := f.demand.CreateCourseInstance(t.Context(), domain.NewCourseInstance{
		SemesterID: f.semester(t).ID, ModuleID: f.module, ProgrammeID: programme, Track: track,
	})
	if err != nil {
		t.Fatalf("cannot declare the cohort: %v", err)
	}
	return instance.ID
}

func (f advanceFixture) release(t *testing.T, programme string) {
	t.Helper()
	f.semester(t)
	if _, err := f.marks.AnnounceDemandComplete(t.Context(), advanceSemester, programme,
		uuid.Nil); err != nil {
		t.Fatalf("cannot release the demand: %v", err)
	}
}

func (f advanceFixture) wishesOf(t *testing.T, who testdata.Persona) []domain.Wish {
	t.Helper()
	wishes, err := f.wishes.Wishes(t.Context(),
		domain.WishQuery{SemesterCode: advanceSemester, Person: who.ID()},
		policy.WishFilter{Scope: policy.WishScopeAll})
	if err != nil {
		t.Fatalf("cannot read the wishes: %v", err)
	}
	return wishes
}

func (f advanceFixture) advanceOf(t *testing.T, who testdata.Persona) []domain.AdvanceWish {
	t.Helper()
	rows, err := f.wishes.AdvanceWishes(t.Context(), domain.AdvanceWishQuery{Person: who.ID()},
		policy.WishFilter{Scope: policy.WishScopeAll})
	if err != nil {
		t.Fatalf("cannot read the advance wishes: %v", err)
	}
	return rows
}

func TestAnAdvanceWishBecomesAWishOnEveryCohortWhenReleased(t *testing.T) {
	t.Parallel()
	f := newAdvanceFixture(t)

	one := 1
	f.note(t, testdata.Eins, f.programme, domain.WishFirstChoice, "nur die Vorlesung", &one)
	a := f.cohort(t, f.programme, "A")
	b := f.cohort(t, f.programme, "B")

	if got := f.wishesOf(t, testdata.Eins); len(got) != 0 {
		t.Fatalf("before the release there are %d wishes, want none: the cohorts take none yet", len(got))
	}
	if got := f.advanceOf(t, testdata.Eins); len(got) != 1 || got[0].Status() != domain.AdvanceWishPending {
		t.Fatalf("before the release the advance wish reads %+v, want one pending", got)
	}

	f.release(t, storetest.FixtureProgrammeA)

	got := f.wishesOf(t, testdata.Eins)
	if len(got) != 2 {
		t.Fatalf("after the release there are %d wishes, want one on each of the two cohorts", len(got))
	}
	on := map[uuid.UUID]bool{}
	for _, w := range got {
		on[w.Instance.ID] = true
		if w.Priority != domain.WishFirstChoice || w.Note != "nur die Vorlesung" || w.TrackLimit != 1 {
			t.Errorf("the converted wish reads %s/%q/limit %d, want FIRST_CHOICE, the note and limit 1",
				w.Priority, w.Note, w.TrackLimit)
		}
	}
	if !on[a] || !on[b] {
		t.Errorf("the wishes are on %v, want both cohorts", on)
	}
	if rows := f.advanceOf(t, testdata.Eins); len(rows) != 1 || rows[0].Status() != domain.AdvanceWishConverted {
		t.Errorf("after the release the advance wish reads %+v, want converted", rows)
	}
}

func TestAReleaseWithoutACohortSaysNotOfferedUntilOneComes(t *testing.T) {
	t.Parallel()
	f := newAdvanceFixture(t)

	f.note(t, testdata.Eins, f.programme, domain.WishHappyTo, "", nil)
	f.release(t, storetest.FixtureProgrammeA)

	if rows := f.advanceOf(t, testdata.Eins); len(rows) != 1 || rows[0].Status() != domain.AdvanceWishNotOffered {
		t.Fatalf("a release without a cohort reads %+v, want NOT_OFFERED", rows)
	}

	// A cohort declared late, in a programme already released: the wish follows.
	cohort := f.cohort(t, f.programme, "")
	got := f.wishesOf(t, testdata.Eins)
	if len(got) != 1 || got[0].Instance.ID != cohort {
		t.Fatalf("after a late cohort the wishes are %+v, want one on it", got)
	}
	if rows := f.advanceOf(t, testdata.Eins); rows[0].Status() != domain.AdvanceWishConverted {
		t.Errorf("after a late cohort the advance wish reads %s, want converted", rows[0].Status())
	}
}

func TestAnotherProgrammesReleaseConvertsNothing(t *testing.T) {
	t.Parallel()
	f := newAdvanceFixture(t)

	f.note(t, testdata.Eins, f.programme, domain.WishHappyTo, "", nil)
	f.cohort(t, f.programme, "")
	f.cohort(t, f.otherProgramme, "")
	f.release(t, storetest.FixtureProgrammeB)

	if got := f.wishesOf(t, testdata.Eins); len(got) != 0 {
		t.Errorf("another programme's release made %d wishes, want none", len(got))
	}
}

func TestAnAdvanceWishConvertsOnce(t *testing.T) {
	t.Parallel()
	f := newAdvanceFixture(t)

	f.note(t, testdata.Eins, f.programme, domain.WishHappyTo, "", nil)
	f.cohort(t, f.programme, "")
	f.release(t, storetest.FixtureProgrammeA)

	got := f.wishesOf(t, testdata.Eins)
	if len(got) != 1 {
		t.Fatalf("want one converted wish, got %d", len(got))
	}
	if err := f.wishes.WithdrawWish(t.Context(), got[0].ID, testdata.Eins.ID()); err != nil {
		t.Fatalf("cannot withdraw the wish: %v", err)
	}

	// Re-announcing after a late change is the ordinary act; it must not bring the wish back.
	f.release(t, storetest.FixtureProgrammeA)
	f.cohort(t, f.programme, "B")
	if got := f.wishesOf(t, testdata.Eins); len(got) != 0 {
		t.Errorf("a withdrawn converted wish came back as %d wishes", len(got))
	}

	// And a converted one is changed as a wish, not as an advance wish.
	_, err := f.wishes.SetAdvanceWish(t.Context(), testdata.Eins.ID(), f.programme, domain.NewAdvanceWish{
		SemesterCode: advanceSemester, ModuleID: f.module, Priority: domain.WishIfNeeded,
	})
	if !errors.Is(err, domain.ErrAdvanceWishConverted) {
		t.Errorf("changing a converted advance wish answered %v, want ErrAdvanceWishConverted", err)
	}
}

func TestConversionKeepsTheStrongerWishAndBothNotes(t *testing.T) {
	t.Parallel()
	f := newAdvanceFixture(t)

	f.note(t, testdata.Eins, f.programme, domain.WishIfNeeded, "lieber dienstags", nil)
	cohort := f.cohort(t, f.programme, "")
	// A wish that is already there — entered while an earlier release held, then withdrawn.
	if _, err := f.wishes.SetWish(t.Context(), cohort, testdata.Eins.ID(), domain.WishFirstChoice,
		"nur die Vorlesung", false); err != nil {
		t.Fatalf("cannot register the wish: %v", err)
	}
	f.release(t, storetest.FixtureProgrammeA)

	got := f.wishesOf(t, testdata.Eins)
	if len(got) != 1 {
		t.Fatalf("want the one wish, got %d", len(got))
	}
	if got[0].Priority != domain.WishFirstChoice {
		t.Errorf("the merged wish is %s, want the stronger FIRST_CHOICE", got[0].Priority)
	}
	if got[0].Note != "nur die Vorlesung · lieber dienstags" {
		t.Errorf("the merged note is %q, want both", got[0].Note)
	}
}

func TestAFinishedSemesterConvertsNothing(t *testing.T) {
	t.Parallel()
	f := newAdvanceFixture(t)

	f.note(t, testdata.Eins, f.programme, domain.WishHappyTo, "", nil)
	f.cohort(t, f.programme, "")
	if _, err := f.schema.Pool.Exec(t.Context(),
		`UPDATE semester SET phase = 'FINAL' WHERE code = $1`, advanceSemester); err != nil {
		t.Fatalf("cannot finish the semester: %v", err)
	}
	f.release(t, storetest.FixtureProgrammeA)

	if got := f.wishesOf(t, testdata.Eins); len(got) != 0 {
		t.Errorf("a finished semester took %d wishes, want none", len(got))
	}
}

// The rule itself, at the layer where it is a WHERE clause: an advance wish is read by whoever
// would read the wish — through the programme it names and the module's subject group.
func TestAdvanceWishesFollowTheWishFilter(t *testing.T) {
	t.Parallel()
	f := newAdvanceFixture(t)
	f.note(t, testdata.Eins, f.programme, domain.WishHappyTo, "", nil)

	for _, tc := range []struct {
		name   string
		filter policy.WishFilter
		want   int
	}{
		{"a colleague", policy.WishFilter{Scope: policy.WishScopeOwn, OwnerID: testdata.Zwei.ID()}, 0},
		{"the owner", policy.WishFilter{Scope: policy.WishScopeOwn, OwnerID: testdata.Eins.ID()}, 1},
		{"the lead of the programme it names", policy.WishFilter{
			Scope: policy.WishScopeOwnOrScoped, OwnerID: testdata.Vier.ID(),
			ProgrammeIDs: []uuid.UUID{f.programme},
		}, 1},
		{"the lead of another programme", policy.WishFilter{
			Scope: policy.WishScopeOwnOrScoped, OwnerID: testdata.Vier.ID(),
			ProgrammeIDs: []uuid.UUID{f.otherProgramme},
		}, 0},
		{"the lead of the module's subject group", policy.WishFilter{
			Scope: policy.WishScopeOwnOrScoped, OwnerID: testdata.Vier.ID(),
			SubjectGroupIDs: []uuid.UUID{f.group},
		}, 1},
		{"the lead of another subject group", policy.WishFilter{
			Scope: policy.WishScopeOwnOrScoped, OwnerID: testdata.Vier.ID(),
			SubjectGroupIDs: []uuid.UUID{f.otherGroup},
		}, 0},
		{"nobody", policy.WishFilter{Scope: policy.WishScopeNone}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := f.wishes.AdvanceWishes(t.Context(),
				domain.AdvanceWishQuery{SemesterCode: advanceSemester}, tc.filter)
			if err != nil {
				t.Fatalf("cannot read: %v", err)
			}
			if len(rows) != tc.want {
				t.Errorf("sees %d advance wishes, want %d", len(rows), tc.want)
			}
		})
	}
}

func TestNotingDownAnAdvanceWishRecordsNoSemester(t *testing.T) {
	t.Parallel()
	f := newAdvanceFixture(t)
	f.note(t, testdata.Eins, f.programme, domain.WishHappyTo, "", nil)

	var rows int
	if err := f.schema.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM semester WHERE code = $1`, advanceSemester).Scan(&rows); err != nil {
		t.Fatalf("cannot look: %v", err)
	}
	if rows != 0 {
		t.Error("an advance wish recorded a semester row: it is no decision about the semester")
	}
}
