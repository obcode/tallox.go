package store_test

import (
	"errors"
	"slices"
	"strconv"
	"testing"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/store"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// Carrying a corrected split over to existing instances, against a real database.
//
// The case that was reported: a module's split was corrected, and its instances in the planning
// semester kept the old parts — the demand page multiplied the new split, the controlling summed
// the old parts. What has to hold is about rows: which parts change, which stay because somebody
// holds them, and that the answer to a caller who cannot read assignments does not depend on them.

func mayAlwaysRemove(domain.SplitInstanceScope) bool { return true }
func mayNeverRemove(domain.SplitInstanceScope) bool  { return false }

// restate replaces the fixture module's split, the way setModuleComponents does.
func (f demandFixture) restate(t *testing.T, components ...domain.ModuleComponent) {
	t.Helper()
	for i := range components {
		components[i].Position = i
	}
	if _, err := store.NewModules(f.schema.Pool).SetModuleComponents(t.Context(), f.module,
		components, uuid.Nil); err != nil {
		t.Fatalf("cannot restate the split: %v", err)
	}
}

func (f demandFixture) reread(t *testing.T, id uuid.UUID) *domain.CourseInstance {
	t.Helper()
	instance, err := f.demand.CourseInstanceByID(t.Context(), id)
	if err != nil || instance == nil {
		t.Fatalf("cannot read the instance: %v", err)
	}
	return instance
}

func (f demandFixture) addGroup(t *testing.T, id uuid.UUID, kind domain.InstancePartKind, hours float64) {
	t.Helper()
	if _, err := f.demand.AddInstancePart(t.Context(), id, kind, &hours); err != nil {
		t.Fatalf("cannot add a group: %v", err)
	}
}

// staff puts somebody on each of these parts.
func (f demandFixture) staff(t *testing.T, parts ...uuid.UUID) {
	t.Helper()
	storetest.SeedPerson(t, f.schema, testdata.Drei, "LECTURER")
	for _, part := range parts {
		if _, err := store.NewAssignments(f.schema.Pool).FillPart(t.Context(), part,
			domain.Assignee{PersonID: testdata.Drei.ID()}, "", testdata.Drei.ID()); err != nil {
			t.Fatalf("cannot staff the part: %v", err)
		}
	}
}

func component(kind domain.InstancePartKind, hours float64) domain.ModuleComponent {
	return domain.ModuleComponent{Kind: kind, TeachingHours: hours}
}

// shape renders an instance's parts as kind:hours, pieces marked, in order.
func shape(t *testing.T, instance *domain.CourseInstance) []string {
	t.Helper()
	out := make([]string, 0, len(instance.Parts))
	for _, p := range instance.Parts {
		s := string(p.Kind)
		if p.DividedFrom != uuid.Nil {
			s = "+" + s
		}
		out = append(out, s+":"+strconv.FormatFloat(hoursOf(t, p), 'f', -1, 64))
	}
	return out
}

func expectShape(t *testing.T, instance *domain.CourseInstance, want ...string) {
	t.Helper()
	if got := shape(t, instance); !slices.Equal(got, want) {
		t.Errorf("the parts are %v, want %v", got, want)
	}
}

// The reported case: the hours move, the dry run shows it and writes nothing, the group count
// somebody chose stays.
func TestApplyingASplitCorrectsTheHoursAndKeepsTheGroups(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()
	instance := f.declare(t, "")
	f.addGroup(t, instance.ID, domain.PartKindLab, 2)

	f.restate(t, component(domain.PartKindLecture, 3), component(domain.PartKindLab, 1))

	preview, err := f.demand.ApplyModuleSplit(ctx, f.module, mayAlwaysRemove, true)
	if err != nil {
		t.Fatalf("cannot preview: %v", err)
	}
	if !preview.DryRun || len(preview.Changed) != 1 {
		t.Fatalf("the preview is %+v, want one changed instance", preview)
	}
	if c := preview.Changed[0]; c.HoursBefore != 6 || c.HoursAfter != 5 {
		t.Errorf("the preview says %v → %v hours, want 6 → 5", c.HoursBefore, c.HoursAfter)
	}
	expectShape(t, f.reread(t, instance.ID), "LECTURE:2", "LAB:2", "LAB:2")

	applied, err := f.demand.ApplyModuleSplit(ctx, f.module, mayAlwaysRemove, false)
	if err != nil {
		t.Fatalf("cannot apply: %v", err)
	}
	if applied.DryRun || len(applied.Changed) != 1 || len(applied.Refused) != 0 {
		t.Fatalf("the save reported %+v, want one changed instance and nothing refused", applied)
	}
	expectShape(t, f.reread(t, instance.ID), "LECTURE:3", "LAB:1", "LAB:1")

	again, err := f.demand.ApplyModuleSplit(ctx, f.module, mayAlwaysRemove, false)
	if err != nil {
		t.Fatalf("cannot apply a second time: %v", err)
	}
	if len(again.Changed) != 0 || again.Unchanged != 1 {
		t.Errorf("applying twice reported %+v, want one unchanged instance", again)
	}
}

// A laboratory that became an exercise: the old groups go, as many new ones come, the lecture
// stays first.
func TestApplyingASplitCarriesTheGroupCountToANewPracticalKind(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	instance := f.declare(t, "")
	f.addGroup(t, instance.ID, domain.PartKindLab, 2)
	f.addGroup(t, instance.ID, domain.PartKindLab, 2)

	f.restate(t, component(domain.PartKindLecture, 2), component(domain.PartKindExercise, 2))
	if _, err := f.demand.ApplyModuleSplit(t.Context(), f.module, mayAlwaysRemove, false); err != nil {
		t.Fatalf("cannot apply: %v", err)
	}
	expectShape(t, f.reread(t, instance.ID), "LECTURE:2", "EXERCISE:2", "EXERCISE:2", "EXERCISE:2")
}

// A held part is not removed, and is not doubled either; its hours still follow the split.
func TestApplyingASplitKeepsAHeldPart(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	instance := f.declare(t, "")
	f.addGroup(t, instance.ID, domain.PartKindLab, 2)
	f.staff(t, partOfKind(t, instance, domain.PartKindLab).ID,
		partOfKind(t, instance, domain.PartKindLecture).ID)

	f.restate(t, component(domain.PartKindLecture, 3), component(domain.PartKindExercise, 1))
	applied, err := f.demand.ApplyModuleSplit(t.Context(), f.module, mayAlwaysRemove, false)
	if err != nil {
		t.Fatalf("cannot apply: %v", err)
	}
	if len(applied.Refused) != 1 || applied.Refused[0].Code != "PART_ASSIGNED" ||
		applied.Refused[0].Kind != domain.PartKindLab {
		t.Fatalf("the refusals are %+v, want PART_ASSIGNED for the laboratory", applied.Refused)
	}
	// One laboratory went, so one exercise came; the held one stays beside it.
	expectShape(t, f.reread(t, instance.ID), "LECTURE:3", "LAB:2", "EXERCISE:1")
}

// For a caller who cannot read the assignments, nothing is removed, and the answer is the same
// whether somebody holds the part or not — otherwise the report is the unpublished assignment.
func TestApplyingASplitBlindTellsNothingAboutAssignments(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	held := f.declare(t, "A")
	free := f.declare(t, "B")
	f.staff(t, partOfKind(t, held, domain.PartKindLab).ID)

	f.restate(t, component(domain.PartKindLecture, 3), component(domain.PartKindExercise, 2))
	applied, err := f.demand.ApplyModuleSplit(t.Context(), f.module, mayNeverRemove, false)
	if err != nil {
		t.Fatalf("cannot apply: %v", err)
	}

	answers := map[uuid.UUID]string{}
	for _, r := range applied.Refused {
		answers[r.Instance.ID] = r.Code + "/" + string(r.Kind)
	}
	if answers[held.ID] != "SPLIT_LEFT_TO_PROGRAMME/LAB" || answers[free.ID] != answers[held.ID] {
		t.Errorf("the answers are %v, want the same SPLIT_LEFT_TO_PROGRAMME for both cohorts", answers)
	}
	for _, id := range []uuid.UUID{held.ID, free.ID} {
		expectShape(t, f.reread(t, id), "LECTURE:3", "LAB:2")
	}
}

// A lecture held for both cohorts gets the new hours once, and the cohort that borrows it does
// not get a lecture of its own. A kind the split adds reaches both.
func TestApplyingASplitRespectsASharedLecture(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	a := f.declare(t, "A")
	b := f.declare(t, "B")
	if _, err := f.demand.ShareInstancePartAcrossTracks(t.Context(),
		partOfKind(t, a, domain.PartKindLecture).ID); err != nil {
		t.Fatalf("cannot share the lecture: %v", err)
	}

	f.restate(t, component(domain.PartKindLecture, 4), component(domain.PartKindLab, 2),
		component(domain.PartKindSeminar, 1))
	if _, err := f.demand.ApplyModuleSplit(t.Context(), f.module, mayAlwaysRemove, false); err != nil {
		t.Fatalf("cannot apply: %v", err)
	}

	sharedA := f.reread(t, a.ID)
	expectShape(t, sharedA, "LECTURE:4", "LAB:2", "SEMINAR:1")
	if !partOfKind(t, sharedA, domain.PartKindLecture).SharedAcrossTracks {
		t.Error("the lecture is no longer held for both cohorts")
	}
	expectShape(t, f.reread(t, b.ID), "LAB:2", "SEMINAR:1")
}

// A split that names the lecture twice divides it, as a new instance would be divided.
func TestApplyingASplitDividesWhereTheSplitDoes(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	instance := f.declare(t, "")

	f.restate(t, component(domain.PartKindLecture, 2), component(domain.PartKindLecture, 2),
		component(domain.PartKindLab, 2))
	if _, err := f.demand.ApplyModuleSplit(t.Context(), f.module, mayAlwaysRemove, false); err != nil {
		t.Fatalf("cannot apply: %v", err)
	}
	expectShape(t, f.reread(t, instance.ID), "LECTURE:2", "+LECTURE:2", "LAB:2")
}

// A part divided by hand, otherwise than the split says, is an arrangement — with a colleague or
// another faculty — and stays as it is.
func TestApplyingASplitLeavesAHandMadeDivision(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	instance, lecture, _ := f.fourAndFour(t, "")
	if _, err := f.demand.DivideInstancePart(t.Context(), lecture, 2); err != nil {
		t.Fatalf("cannot divide the lecture: %v", err)
	}

	f.restate(t, component(domain.PartKindLecture, 4), component(domain.PartKindLab, 4))
	applied, err := f.demand.ApplyModuleSplit(t.Context(), f.module, mayAlwaysRemove, false)
	if err != nil {
		t.Fatalf("cannot apply: %v", err)
	}
	if len(applied.Refused) != 1 || applied.Refused[0].Code != "PART_DIVIDED" {
		t.Fatalf("the refusals are %+v, want PART_DIVIDED for the lecture", applied.Refused)
	}
	expectShape(t, f.reread(t, instance.ID), "LECTURE:2", "+LECTURE:2", "LAB:4")
}

// A semester that is over is history.
func TestApplyingASplitLeavesAFinalSemester(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	past, err := f.demand.CreateCourseInstance(t.Context(), domain.NewCourseInstance{
		SemesterID: f.previous.ID, ModuleID: f.module, ProgrammeID: f.programme,
	})
	if err != nil {
		t.Fatalf("cannot declare the past instance: %v", err)
	}
	if _, err := f.schema.Pool.Exec(t.Context(),
		`UPDATE semester SET phase = 'FINAL' WHERE id = $1`, f.previous.ID); err != nil {
		t.Fatalf("cannot close the semester: %v", err)
	}

	f.restate(t, component(domain.PartKindLecture, 3), component(domain.PartKindLab, 1))
	applied, err := f.demand.ApplyModuleSplit(t.Context(), f.module, mayAlwaysRemove, false)
	if err != nil {
		t.Fatalf("cannot apply: %v", err)
	}
	if len(applied.Changed) != 0 || applied.Unchanged != 0 {
		t.Errorf("the report is %+v, want the FINAL semester not even looked at", applied)
	}
	expectShape(t, f.reread(t, past.ID), "LECTURE:2", "LAB:2")
}

func TestApplyingNoSplitIsNotDecomposed(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	_, err := f.demand.ApplyModuleSplit(t.Context(), f.withoutHours, mayAlwaysRemove, true)
	if !errors.Is(err, domain.ErrModuleNotDecomposed) {
		t.Errorf("applying a module without a split gave %v, want ErrModuleNotDecomposed", err)
	}
}

// A cohort held by another programme has no parts to correct, and is neither changed nor counted.
func TestApplyingASplitSkipsACoveredCohort(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	f.declare(t, "")
	guest := f.declareIn(t, storetest.FixtureProgrammeB, "")
	if guest.CoveredBy == nil || len(guest.Parts) != 0 {
		t.Fatalf("the second programme's cohort is not held by the first: %+v", guest)
	}

	f.restate(t, component(domain.PartKindLecture, 3), component(domain.PartKindLab, 1))
	applied, err := f.demand.ApplyModuleSplit(t.Context(), f.module, mayAlwaysRemove, false)
	if err != nil {
		t.Fatalf("cannot apply: %v", err)
	}
	if len(applied.Changed) != 1 || applied.Changed[0].Instance.ID == guest.ID {
		t.Errorf("the report is %+v, want only the holding cohort changed", applied)
	}
	if parts := f.reread(t, guest.ID).Parts; len(parts) != 0 {
		t.Errorf("the covered cohort holds %d parts now, want none", len(parts))
	}
}
