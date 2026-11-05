package store_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/store"
)

// Taking a local course out of the lists while it is still in demand, against a real database.
//
// A deactivated module disappears from the demand page, and its instances would go on costing
// hours where nobody sees them. So deactivating waits until no semester that is still running has
// an instance of it — and a semester that is over does not count.

func placeholderSpec(programme uuid.UUID, active bool) domain.NewLocalModule {
	return domain.NewLocalModule{
		HomeProgrammeID: programme,
		Name:            "FWP-Platzhalter (technisch)",
		Kind:            domain.ModuleKindFwpPlaceholder,
		CourseType:      domain.CourseTypeSU,
		Frequency:       domain.FrequencyOnAnnouncement,
		Active:          active,
		Components: []domain.ModuleComponent{
			{Kind: domain.PartKindLecture, TeachingHours: 4, Position: 0},
		},
	}
}

// placeholderIn creates a placeholder and declares one cohort of it in the semester.
func (f demandFixture) placeholderIn(t *testing.T, semester domain.Semester,
) (*domain.Module, *domain.CourseInstance) {
	t.Helper()

	modules := store.NewModules(f.schema.Pool)
	created, err := modules.CreateLocalModule(t.Context(), placeholderSpec(f.programme, true),
		uuid.Nil)
	if err != nil {
		t.Fatalf("cannot record the placeholder: %v", err)
	}
	instance, err := f.demand.CreateCourseInstance(t.Context(), domain.NewCourseInstance{
		SemesterID:  semester.ID,
		ModuleID:    created.ID,
		ProgrammeID: f.programme,
		Track:       "A",
	})
	if err != nil {
		t.Fatalf("cannot declare the placeholder: %v", err)
	}
	return created, instance
}

func TestALocalModuleStillInDemandCannotBeDeactivated(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()
	modules := store.NewModules(f.schema.Pool)
	created, instance := f.placeholderIn(t, f.semester)

	// Renamed and deactivated in one request: the refusal has to take back the rename as well.
	spec := placeholderSpec(f.programme, false)
	spec.Name = "FWP-Platzhalter (umbenannt)"
	if _, err := modules.UpdateLocalModule(ctx, created.ID, spec, uuid.Nil); !errors.Is(err,
		domain.ErrModuleInDemand) {
		t.Fatalf("deactivating a placeholder in demand gave %v, want ErrModuleInDemand", err)
	}

	after, err := modules.ModuleByID(ctx, created.ID)
	if err != nil || after == nil {
		t.Fatalf("cannot read the placeholder back: %v", err)
	}
	if !after.Active || after.Name != created.Name {
		t.Errorf("the refused write left it active=%v named %q, want it unchanged",
			after.Active, after.Name)
	}

	// Everything else stays editable while it is in demand.
	renamed := placeholderSpec(f.programme, true)
	renamed.Name = "FWP-Platzhalter (umbenannt)"
	if _, err := modules.UpdateLocalModule(ctx, created.ID, renamed, uuid.Nil); err != nil {
		t.Errorf("renaming a placeholder in demand gave %v, want it to go through", err)
	}

	// Withdrawn from the demand, it may go.
	if err := f.demand.DeleteCourseInstance(ctx, instance.ID); err != nil {
		t.Fatalf("cannot withdraw the cohort: %v", err)
	}
	deactivated, err := modules.UpdateLocalModule(ctx, created.ID, placeholderSpec(f.programme,
		false), uuid.Nil)
	if err != nil {
		t.Fatalf("deactivating after the withdrawal gave %v", err)
	}
	if deactivated.Active {
		t.Error("the placeholder is still active after it was deactivated")
	}
}

// A semester that is over is the record of what was taught, not demand. Its instances stay, and
// they do not keep the course in the lists.
func TestAnInstanceInAFinishedSemesterDoesNotKeepALocalModuleActive(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()
	modules := store.NewModules(f.schema.Pool)
	created, instance := f.placeholderIn(t, f.previous)

	if _, err := f.schema.Pool.Exec(ctx,
		`UPDATE semester SET phase = 'FINAL' WHERE id = $1`, f.previous.ID); err != nil {
		t.Fatalf("cannot finish the semester: %v", err)
	}

	deactivated, err := modules.UpdateLocalModule(ctx, created.ID, placeholderSpec(f.programme,
		false), uuid.Nil)
	if err != nil {
		t.Fatalf("deactivating with only a finished semester's instance gave %v", err)
	}
	if deactivated.Active {
		t.Error("the placeholder is still active after it was deactivated")
	}
	if kept, err := f.demand.CourseInstanceByID(ctx, instance.ID); err != nil || kept == nil {
		t.Errorf("the finished semester's instance is gone (%v); deactivating deletes nothing", err)
	}
}

// Every phase before FINAL is a running semester, REVIEW included.
func TestEveryPhaseBeforeFinalKeepsALocalModuleInDemand(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	modules := store.NewModules(f.schema.Pool)
	created, _ := f.placeholderIn(t, f.semester)

	for _, phase := range []string{"DEMAND_PLANNING", "WISHES", "ASSIGNMENT", "REVIEW"} {
		if _, err := f.schema.Pool.Exec(t.Context(),
			`UPDATE semester SET phase = $2 WHERE id = $1`, f.semester.ID, phase); err != nil {
			t.Fatalf("cannot switch the semester to %s: %v", phase, err)
		}
		if _, err := modules.UpdateLocalModule(t.Context(), created.ID,
			placeholderSpec(f.programme, false), uuid.Nil); !errors.Is(err,
			domain.ErrModuleInDemand) {
			t.Errorf("in %s deactivating gave %v, want ErrModuleInDemand", phase, err)
		}
	}
}
