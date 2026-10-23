package store_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// Merging cohorts after the wish round.
//
// A wish points at a cohort, ON DELETE RESTRICT, and that used to hold the demand still from the
// first wish onwards: reducing two cohorts to one was refused as INSTANCE_IN_USE. The faculty
// plans iteratively, so the wishes on a cohort that goes now move to a sibling that stays. Every
// assertion here is about what the statements do to rows, which is why none of it is tested
// against a fake.

// wishRow is a wish as the table holds it.
type wishRow struct {
	instance uuid.UUID
	priority int
	note     string
}

// wish registers interest the way the wish screen would, straight into the table.
func (f demandFixture) wish(t *testing.T, instance uuid.UUID, who testdata.Persona, priority int, note string) {
	t.Helper()

	if _, err := f.schema.Pool.Exec(t.Context(),
		`INSERT INTO wish (course_instance_id, person_id, priority, note) VALUES ($1, $2, $3, $4)`,
		instance, who.ID(), priority, note); err != nil {
		t.Fatalf("cannot register interest: %v", err)
	}
}

// wishesOf is every wish one person holds, in any cohort.
func (f demandFixture) wishesOf(t *testing.T, who testdata.Persona) []wishRow {
	t.Helper()

	rows, err := f.schema.Pool.Query(t.Context(),
		`SELECT course_instance_id, priority, note FROM wish WHERE person_id = $1 ORDER BY priority`,
		who.ID())
	if err != nil {
		t.Fatalf("cannot read the wishes: %v", err)
	}
	defer rows.Close()

	var out []wishRow
	for rows.Next() {
		var w wishRow
		if err := rows.Scan(&w.instance, &w.priority, &w.note); err != nil {
			t.Fatalf("cannot scan a wish: %v", err)
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("cannot read the wishes: %v", err)
	}
	return out
}

// twoCohorts plans A and B and returns them in that order.
func (f demandFixture) twoCohorts(t *testing.T) (a, b domain.CourseInstance) {
	t.Helper()

	if _, err := f.demand.PlanDemand(t.Context(), f.semester.Code, f.programme,
		[]domain.DemandEntry{planEntry(f.module, track("A", 1), track("B", 1))}, uuid.Nil, false); err != nil {
		t.Fatalf("cannot plan two cohorts: %v", err)
	}
	instances := f.instances(t)
	if len(instances) != 2 {
		t.Fatalf("the module runs in %d cohorts, want 2", len(instances))
	}
	return instances[0], instances[1]
}

// The case from the report: two cohorts, somebody wished for B, the lead merges them into A.
func TestMergingCohortsHandsTheWishesToTheOneThatStays(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()
	a, b := f.twoCohorts(t)

	storetest.SeedPerson(t, f.schema, testdata.Eins, "LECTURER")
	f.wish(t, b.ID, testdata.Eins, 1, "gern dienstags")

	plan, err := f.demand.PlanDemand(ctx, f.semester.Code, f.programme,
		[]domain.DemandEntry{planEntry(f.module, track("A", 1))}, uuid.Nil, false)
	if err != nil {
		t.Fatalf("merging gave %v", err)
	}
	if len(plan.Refused) != 0 {
		t.Fatalf("merging was refused: %+v — the wish on B held the demand still", plan.Refused)
	}
	if len(plan.Withdrawn) != 1 || plan.Withdrawn[0].Track != "B" {
		t.Errorf("merging reports %+v withdrawn, want B", plan.Withdrawn)
	}

	got := f.wishesOf(t, testdata.Eins)
	want := []wishRow{{instance: a.ID, priority: 1, note: "gern dienstags"}}
	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("the wish is %+v after the merge, want %+v — moved to A, unchanged", got, want)
	}
}

// Somebody who wished for both cohorts keeps one wish, the stronger one, with both notes.
func TestMergingCohortsKeepsOneWishPerPersonWithBothNotes(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name         string
		onA, onB     int
		wantPriority int
		wantNote     string
	}{
		// The survivor is the stronger row, and its note comes first, whichever cohort it was on.
		{"stronger on the cohort that goes", 2, 1, 1, "B-Notiz · A-Notiz"},
		{"stronger on the cohort that stays", 1, 3, 1, "A-Notiz · B-Notiz"},
		// A tie goes to the older entry, which is A's: it was registered first.
		{"tie", 2, 2, 2, "A-Notiz · B-Notiz"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			f := newDemandFixture(t)
			a, b := f.twoCohorts(t)

			storetest.SeedPerson(t, f.schema, testdata.Eins, "LECTURER")
			f.wish(t, a.ID, testdata.Eins, c.onA, "A-Notiz")
			f.wish(t, b.ID, testdata.Eins, c.onB, "B-Notiz")

			plan, err := f.demand.PlanDemand(t.Context(), f.semester.Code, f.programme,
				[]domain.DemandEntry{planEntry(f.module, track("A", 1))}, uuid.Nil, false)
			if err != nil {
				t.Fatalf("merging gave %v", err)
			}
			if len(plan.Refused) != 0 {
				t.Fatalf("merging was refused: %+v", plan.Refused)
			}

			got := f.wishesOf(t, testdata.Eins)
			want := wishRow{instance: a.ID, priority: c.wantPriority, note: c.wantNote}
			if len(got) != 1 || got[0] != want {
				t.Errorf("the wishes are %+v, want exactly %+v", got, want)
			}
		})
	}
}

// Other people's wishes on the cohort that stays are not touched by a merge.
func TestMergingCohortsLeavesOtherPeoplesWishesAlone(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	a, b := f.twoCohorts(t)

	storetest.SeedPerson(t, f.schema, testdata.Eins, "LECTURER")
	storetest.SeedPerson(t, f.schema, testdata.Zwei, "LECTURER")
	f.wish(t, a.ID, testdata.Eins, 3, "nur Praktikum")
	f.wish(t, b.ID, testdata.Zwei, 1, "")

	if _, err := f.demand.PlanDemand(t.Context(), f.semester.Code, f.programme,
		[]domain.DemandEntry{planEntry(f.module, track("A", 1))}, uuid.Nil, false); err != nil {
		t.Fatalf("merging gave %v", err)
	}

	if got := f.wishesOf(t, testdata.Eins); len(got) != 1 ||
		got[0] != (wishRow{instance: a.ID, priority: 3, note: "nur Praktikum"}) {
		t.Errorf("Eins's wish on A is %+v after the merge, want it unchanged", got)
	}
	if got := f.wishesOf(t, testdata.Zwei); len(got) != 1 ||
		got[0] != (wishRow{instance: a.ID, priority: 1, note: ""}) {
		t.Errorf("Zwei's wish is %+v after the merge, want it moved to A unchanged", got)
	}
}

// Taking the module away altogether is not a merge: there is nowhere for the interest to go, and
// the last cohort is refused as it always was. The other cohorts still go, and hand their wishes
// to the one that is refused — nothing is lost either way.
func TestWithdrawingTheModuleIsStillRefusedWhileSomebodyWantsIt(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	_, b := f.twoCohorts(t)

	storetest.SeedPerson(t, f.schema, testdata.Eins, "LECTURER")
	f.wish(t, b.ID, testdata.Eins, 2, "")

	plan, err := f.demand.PlanDemand(t.Context(), f.semester.Code, f.programme,
		[]domain.DemandEntry{planEntry(f.module)}, uuid.Nil, false)
	if err != nil {
		t.Fatalf("withdrawing the module gave %v", err)
	}
	if len(plan.Refused) != 1 || plan.Refused[0].Code != "INSTANCE_IN_USE" {
		t.Errorf("withdrawing a wished-for module reports %+v refused, want INSTANCE_IN_USE", plan.Refused)
	}
	if got := f.wishesOf(t, testdata.Eins); len(got) != 1 {
		t.Errorf("Eins holds %d wishes after a refused withdrawal, want 1 — nothing may be lost", len(got))
	}
}

// A cohort somebody already teaches in is still refused, and its wishes stay where they were: the
// handover runs in the savepoint the refusal rolls back.
func TestMergingIsStillRefusedForAStaffedCohortAndNothingMoves(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	_, b := f.twoCohorts(t)

	storetest.SeedPerson(t, f.schema, testdata.Eins, "LECTURER")
	f.wish(t, b.ID, testdata.Eins, 2, "")
	f.staff(t, b.Parts[0].ID)

	plan, err := f.demand.PlanDemand(t.Context(), f.semester.Code, f.programme,
		[]domain.DemandEntry{planEntry(f.module, track("A", 1))}, uuid.Nil, false)
	if err != nil {
		t.Fatalf("merging gave %v", err)
	}
	if len(plan.Refused) != 1 || plan.Refused[0].Track != "B" {
		t.Fatalf("merging over a staffed cohort reports %+v refused, want B", plan.Refused)
	}
	if got := f.wishesOf(t, testdata.Eins); len(got) != 1 || got[0].instance != b.ID {
		t.Errorf("the wish is %+v after a refused merge, want it still on B", got)
	}
}

// The other direction from the report: adding a cohort after the wish round. The cohort that was
// there is renamed rather than rebuilt, so its wishes stay on it.
func TestAddingACohortAfterTheWishRoundKeepsTheWishes(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()

	if _, err := f.demand.PlanDemand(ctx, f.semester.Code, f.programme,
		[]domain.DemandEntry{planEntry(f.module, track("", 1))}, uuid.Nil, false); err != nil {
		t.Fatalf("cannot plan one cohort: %v", err)
	}
	only := f.instances(t)[0]

	storetest.SeedPerson(t, f.schema, testdata.Eins, "LECTURER")
	f.wish(t, only.ID, testdata.Eins, 1, "")

	plan, err := f.demand.PlanDemand(ctx, f.semester.Code, f.programme,
		[]domain.DemandEntry{planEntry(f.module, track("A", 1), track("B", 1))}, uuid.Nil, false)
	if err != nil {
		t.Fatalf("adding a cohort gave %v", err)
	}
	if len(plan.Refused) != 0 || len(plan.Created) != 1 {
		t.Fatalf("adding a cohort reports %+v, want one created and nothing refused", plan)
	}
	if got := f.wishesOf(t, testdata.Eins); len(got) != 1 || got[0].instance != only.ID {
		t.Errorf("the wish is %+v, want it on the renamed cohort", got)
	}
}

// The single withdrawal is the same rule: a sibling takes the wishes, no sibling refuses.
func TestWithdrawingOneCohortHandsItsWishesToASibling(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()
	a, b := f.twoCohorts(t)

	storetest.SeedPerson(t, f.schema, testdata.Eins, "LECTURER")
	f.wish(t, a.ID, testdata.Eins, 2, "")

	if err := f.demand.DeleteCourseInstance(ctx, a.ID); err != nil {
		t.Fatalf("withdrawing A answered %v, want it withdrawn with its wish handed to B", err)
	}
	if got := f.wishesOf(t, testdata.Eins); len(got) != 1 || got[0].instance != b.ID {
		t.Errorf("the wish is %+v, want it on B", got)
	}

	// B is the last cohort now, and nothing is left to merge into.
	if err := f.demand.DeleteCourseInstance(ctx, b.ID); !errors.Is(err, domain.ErrInstanceInUse) {
		t.Errorf("withdrawing the last wished-for cohort answered %v, want ErrInstanceInUse", err)
	}
}
