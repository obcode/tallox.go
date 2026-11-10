package store_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/store"
)

// Demand for a local course that was taken out of the lists, against a real database.
//
// The other direction of TestALocalModuleStillInDemandCannotBeDeactivated: a deactivated course
// is gone from the catalogue and the demand page, so no path may declare an instance of it again
// — declaring, duplicating, planning, or copying an earlier semester.

// retiredPlaceholder is a placeholder with one cohort in the previous semester, which is then
// finished, and the placeholder taken out of the lists. A finished semester keeps its instances,
// so this is the state in which every path below could otherwise bring it back.
func (f demandFixture) retiredPlaceholder(t *testing.T) (*domain.Module, *domain.CourseInstance) {
	t.Helper()

	ctx := t.Context()
	created, instance := f.placeholderIn(t, f.previous)
	if _, err := f.schema.Pool.Exec(ctx,
		`UPDATE semester SET phase = 'FINAL' WHERE id = $1`, f.previous.ID); err != nil {
		t.Fatalf("cannot finish the semester: %v", err)
	}
	if _, err := store.NewModules(f.schema.Pool).UpdateLocalModule(ctx, created.ID,
		placeholderSpec(f.programme, false), uuid.Nil); err != nil {
		t.Fatalf("cannot take the placeholder out of the lists: %v", err)
	}
	return created, instance
}

func TestADeactivatedLocalModuleCannotBeDeclared(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()
	created, _ := f.retiredPlaceholder(t)

	spec := domain.NewCourseInstance{
		SemesterID:  f.semester.ID,
		ModuleID:    created.ID,
		ProgrammeID: f.programme,
		Track:       "A",
	}
	if _, err := f.demand.CreateCourseInstance(ctx, spec); !errors.Is(err, domain.ErrModuleInactive) {
		t.Fatalf("declaring a deactivated placeholder gave %v, want ErrModuleInactive", err)
	}

	// Taken back into the lists, it can be planned again.
	if _, err := store.NewModules(f.schema.Pool).UpdateLocalModule(ctx, created.ID,
		placeholderSpec(f.programme, true), uuid.Nil); err != nil {
		t.Fatalf("cannot take the placeholder back in: %v", err)
	}
	if _, err := f.demand.CreateCourseInstance(ctx, spec); err != nil {
		t.Errorf("declaring it after taking it back in gave %v", err)
	}
}

// The source cohort is a finished semester's, which survived the deactivation.
func TestADeactivatedLocalModuleCannotBeDuplicated(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	_, instance := f.retiredPlaceholder(t)

	if _, err := f.demand.DuplicateCourseInstance(t.Context(), instance.ID, "B", "",
		uuid.Nil); !errors.Is(err, domain.ErrModuleInactive) {
		t.Errorf("duplicating a cohort of a deactivated placeholder gave %v, want ErrModuleInactive",
			err)
	}
}

// A plan reports the refusal per row and goes on with the rest, as with every other refusal.
func TestPlanningRefusesADeactivatedLocalModuleAndKeepsTheRest(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	created, _ := f.retiredPlaceholder(t)

	plan, err := f.demand.PlanDemand(t.Context(), f.semester.Code, f.programme,
		[]domain.DemandEntry{
			planEntry(created.ID, track("A", 0)),
			planEntry(f.module, track("", 1)),
		}, uuid.Nil, false)
	if err != nil {
		t.Fatalf("planning gave %v", err)
	}
	if len(plan.Refused) != 1 || plan.Refused[0].Code != "MODULE_INACTIVE" ||
		plan.Refused[0].ModuleID != created.ID {
		t.Errorf("the plan refused %+v, want one MODULE_INACTIVE for the placeholder", plan.Refused)
	}
	if len(plan.Created) != 1 || plan.Created[0].ModuleID != f.module {
		t.Errorf("the plan created %+v, want the ordinary module declared", plan.Created)
	}
}

// Copying last year's demand leaves the deactivated course behind and says so.
func TestCopyingDemandLeavesADeactivatedLocalModuleBehind(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()
	created, _ := f.retiredPlaceholder(t)

	counts, err := f.demand.CopyDemand(ctx, f.previous, f.semester, f.programme, uuid.Nil)
	if err != nil {
		t.Fatalf("copying gave %v", err)
	}
	if counts.Inactive != 1 || counts.Created != 0 {
		t.Errorf("the copy created %d and left %d behind as inactive, want 0 and 1",
			counts.Created, counts.Inactive)
	}
	var n int
	if err := f.schema.Pool.QueryRow(ctx,
		`SELECT count(*) FROM course_instance WHERE semester_id = $1 AND module_id = $2`,
		f.semester.ID, created.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("the target semester holds %d cohorts of the deactivated placeholder, want none", n)
	}
}
