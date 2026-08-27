package store_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/store"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// The demand against a real database.
//
// Every rule under test here is a statement about rows — the parts are made from the split in
// the same transaction that writes the instance, the identity is a unique key, a withdrawal is
// refused by a foreign key, a copy is all or nothing. A fake store passes all four while the
// shipped statements do something else, which is why none of this is tested against one.

// demandFixture is a projected catalogue with a split on the ordinary module and two semesters.
type demandFixture struct {
	schema    *storetest.Schema
	demand    *store.Demand
	semester  domain.Semester
	previous  domain.Semester
	programme uuid.UUID
	// module is the ordinary module, split into a two-hour lecture and a two-hour laboratory.
	module uuid.UUID
	// withoutHours is the module the examination office states no hours for. Nothing can be
	// split and nothing can be proposed, so it is what is left of the precondition.
	withoutHours uuid.UUID
}

func newDemandFixture(t *testing.T) demandFixture {
	t.Helper()

	s := storetest.New(t)
	storetest.SeedZPACatalogue(t, s)
	project(t, s)

	ctx := t.Context()
	modules := store.NewModules(s.Pool)
	semesters := store.NewSemesters(s.Pool)

	current, err := semesters.EnsureSemester(ctx, "2027-SS")
	if err != nil {
		t.Fatalf("cannot record the semester: %v", err)
	}
	previous, err := semesters.EnsureSemester(ctx, "2026-WS")
	if err != nil {
		t.Fatalf("cannot record the previous semester: %v", err)
	}

	fixture := demandFixture{
		schema:       s,
		demand:       store.NewDemand(s.Pool, modules),
		semester:     current,
		previous:     previous,
		programme:    programmeID(t, s, storetest.FixtureProgrammeA),
		module:       moduleID(t, s, storetest.FixtureModuleOrdinary),
		withoutHours: moduleID(t, s, storetest.FixtureModuleWithoutHours),
	}

	if _, err := modules.SetModuleComponents(ctx, fixture.module, []domain.ModuleComponent{
		{Kind: domain.PartKindLecture, TeachingHours: 2, Position: 0},
		{Kind: domain.PartKindLab, TeachingHours: 2, Position: 1},
	}, uuid.Nil); err != nil {
		t.Fatalf("cannot state the module's split: %v", err)
	}

	return fixture
}

func programmeID(t *testing.T, s *storetest.Schema, code string) uuid.UUID {
	t.Helper()

	var id uuid.UUID
	if err := s.Pool.QueryRow(t.Context(),
		`SELECT id FROM programme WHERE code = $1`, code).Scan(&id); err != nil {
		t.Fatalf("cannot find programme %s: %v", code, err)
	}
	return id
}

func moduleID(t *testing.T, s *storetest.Schema, zpaID int64) uuid.UUID {
	t.Helper()

	var id uuid.UUID
	if err := s.Pool.QueryRow(t.Context(),
		`SELECT id FROM module WHERE zpa_module_ref = $1`, zpaID).Scan(&id); err != nil {
		t.Fatalf("cannot find module %d: %v", zpaID, err)
	}
	return id
}

func (f demandFixture) declare(t *testing.T, track string) *domain.CourseInstance {
	t.Helper()

	instance, err := f.demand.CreateCourseInstance(t.Context(), domain.NewCourseInstance{
		SemesterID:  f.semester.ID,
		ModuleID:    f.module,
		ProgrammeID: f.programme,
		Track:       track,
	})
	if err != nil {
		t.Fatalf("cannot declare the instance: %v", err)
	}
	return instance
}

// partsOf is the parts of the one instance of a module in the fixture's semester.
func (f demandFixture) partsOf(t *testing.T, moduleID uuid.UUID) []domain.InstancePart {
	t.Helper()

	instances, err := f.demand.CourseInstances(t.Context(), domain.DemandFilter{
		SemesterCode: f.semester.Code, Module: moduleID,
	})
	if err != nil {
		t.Fatalf("cannot read the demand of one module: %v", err)
	}
	if len(instances) != 1 {
		t.Fatalf("the module runs in %d instances, want 1", len(instances))
	}
	return instances[0].Parts
}

// instances is the demand of the fixture's semester and programme, in the order the screen
// shows it.
func (f demandFixture) instances(t *testing.T) []domain.CourseInstance {
	t.Helper()

	instances, err := f.demand.CourseInstances(t.Context(), domain.DemandFilter{
		SemesterCode: f.semester.Code,
		Programme:    storetest.FixtureProgrammeA,
	})
	if err != nil {
		t.Fatalf("cannot read the demand: %v", err)
	}
	return instances
}

func kinds(parts []domain.InstancePart) []domain.InstancePartKind {
	out := make([]domain.InstancePartKind, 0, len(parts))
	for _, p := range parts {
		out = append(out, p.Kind)
	}
	return out
}

// The precondition of the whole feature, and the thing that makes the module split a work list
// rather than a decoration.
func TestDeclaringAnInstanceMakesItsPartsFromTheSplit(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	instance := f.declare(t, "A")

	if got := kinds(instance.Parts); len(got) != 2 || got[0] != domain.PartKindLecture || got[1] != domain.PartKindLab {
		t.Fatalf("the instance holds %v, want one lecture and one laboratory — one part per "+
			"unit of the module's split, in its order", got)
	}
	for _, p := range instance.Parts {
		if p.TeachingHours == nil || *p.TeachingHours != 2 {
			t.Errorf("part %s is credited with %v hours, want the 2 the split states",
				p.Kind, p.TeachingHours)
		}
		if p.SharedAcrossTracks {
			t.Errorf("part %s is shared across cohorts on creation — the ordinary case is that "+
				"every cohort holds its own, and sharing is a deliberate act", p.Kind)
		}
	}
	if instance.TeachingHours() != 4 {
		t.Errorf("the instance costs %v hours, want 4 — the sum over its parts, not the "+
			"module's own figure", instance.TeachingHours())
	}
	if instance.Module.Name == "" {
		t.Error("the instance's module arrived without its catalogue entry")
	}
	if len(instance.Module.Components) != 2 {
		t.Errorf("the instance's module carries %d components — a Module assembled without them "+
			"reports every module as undeclarable", len(instance.Module.Components))
	}
}

// What is left of the precondition once a split can be estimated: a module the examination
// office states no hours for. Twelve real modules are in that state, and for those the repair is
// to enter the split by hand.
func TestAModuleWithoutHoursCannotBeDeclared(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()

	_, err := f.demand.CreateCourseInstance(ctx, domain.NewCourseInstance{
		SemesterID:  f.semester.ID,
		ModuleID:    f.withoutHours,
		ProgrammeID: f.programme,
	})
	if !errors.Is(err, domain.ErrModuleNotDecomposed) {
		t.Fatalf("declaring a module with no hours gave %v, want ErrModuleNotDecomposed", err)
	}

	// And nothing was written. The check is inside the transaction, so a refused declaration
	// leaves no instance with no parts behind.
	var instances int
	if err := f.schema.Pool.QueryRow(ctx,
		`SELECT count(*) FROM course_instance WHERE module_id = $1`, f.withoutHours).Scan(&instances); err != nil {
		t.Fatalf("cannot count the instances: %v", err)
	}
	if instances != 0 {
		t.Errorf("%d instance(s) survived the refusal — the precondition is checked outside the "+
			"transaction that writes", instances)
	}
}

func TestTheSameCohortCannotBeDeclaredTwice(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	f.declare(t, "A")

	_, err := f.demand.CreateCourseInstance(t.Context(), domain.NewCourseInstance{
		SemesterID:  f.semester.ID,
		ModuleID:    f.module,
		ProgrammeID: f.programme,
		Track:       "A",
	})
	if !errors.Is(err, domain.ErrTrackTaken) {
		t.Fatalf("declaring the same cohort twice gave %v, want ErrTrackTaken", err)
	}
}

// The cohort year is seeded from the regulations and is a decision afterwards — the migration's
// argument for storing it rather than deriving it on every read.
func TestTheCohortYearIsSeededFromTheRegulations(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	instance := f.declare(t, "")

	var stated *int
	if err := f.schema.Pool.QueryRow(t.Context(),
		`SELECT min(o.min_programme_semester)
		   FROM module_offering o JOIN spo s ON s.id = o.spo_id
		  WHERE o.module_id = $1 AND s.programme_id = $2`,
		f.module, f.programme).Scan(&stated); err != nil {
		t.Fatalf("cannot read what the regulations say: %v", err)
	}
	if stated == nil {
		t.Skip("the fixture's regulations state no earliest semester for this module")
	}
	if instance.ProgrammeSemester == nil || *instance.ProgrammeSemester != *stated {
		t.Errorf("the cohort year is %v, want the %d the regulations state",
			instance.ProgrammeSemester, *stated)
	}
}

// Splitting one cohort into two is one act: the source is renamed in the same transaction that
// declares the sibling, so the two never exist as a pair that does not look like one.
func TestDuplicatingCopiesThePartsAndRenamesTheSource(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()
	source := f.declare(t, "")

	second, err := f.demand.DuplicateCourseInstance(ctx, source.ID, "B", "A", uuid.Nil)
	if err != nil {
		t.Fatalf("cannot duplicate the instance: %v", err)
	}

	if second.Track != "B" {
		t.Errorf("the new cohort is %q, want B", second.Track)
	}
	if got := kinds(second.Parts); len(got) != 2 {
		t.Errorf("the new cohort holds %v, want its own copy of both parts", got)
	}
	if len(second.BorrowedParts) != 0 {
		t.Errorf("the new cohort borrows %d part(s); nothing was shared, so it should hold "+
			"everything itself", len(second.BorrowedParts))
	}

	renamed, err := f.demand.CourseInstanceByID(ctx, source.ID)
	if err != nil {
		t.Fatalf("cannot re-read the source: %v", err)
	}
	if renamed.Track != "A" {
		t.Errorf("the source cohort is %q, want A — a cohort splitting in two renames the "+
			"original in the same transaction, or every label is wrong in between", renamed.Track)
	}
}

// The shared lecture: one row, held once, counted once, and rendered by both cohorts.
func TestSharingAPartAcrossCohorts(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()

	first := f.declare(t, "A")
	second, err := f.demand.DuplicateCourseInstance(ctx, first.ID, "B", "", uuid.Nil)
	if err != nil {
		t.Fatalf("cannot duplicate the instance: %v", err)
	}

	lecture := partOfKind(t, first, domain.PartKindLecture)
	shared, err := f.demand.ShareInstancePartAcrossTracks(ctx, lecture.ID)
	if err != nil {
		t.Fatalf("cannot share the lecture: %v", err)
	}

	if got := kinds(shared.Parts); len(got) != 2 {
		t.Errorf("the owning cohort holds %v, want its lecture and its laboratory", got)
	}
	if !partOfKind(t, shared, domain.PartKindLecture).SharedAcrossTracks {
		t.Error("the lecture is not marked as held for the sibling cohorts")
	}

	sibling, err := f.demand.CourseInstanceByID(ctx, second.ID)
	if err != nil {
		t.Fatalf("cannot re-read the sibling: %v", err)
	}
	if got := kinds(sibling.Parts); len(got) != 1 || got[0] != domain.PartKindLab {
		t.Errorf("the sibling holds %v, want only its laboratory — its own lecture goes when "+
			"the other one starts serving it, or the cohort attends two lectures", got)
	}
	if len(sibling.BorrowedParts) != 1 || sibling.BorrowedParts[0].Part.Kind != domain.PartKindLecture {
		t.Fatalf("the sibling borrows %v, want the shared lecture — a cohort rendered with "+
			"laboratories and no lecture looks like a planning mistake", sibling.BorrowedParts)
	}
	if sibling.BorrowedParts[0].FromTrack != "A" {
		t.Errorf("the borrowed lecture comes from cohort %q, want A",
			sibling.BorrowedParts[0].FromTrack)
	}
	if sibling.TeachingHours() != 2 {
		t.Errorf("the sibling costs %v hours, want 2 — a lecture held once is counted once, "+
			"at the cohort that owns the row", sibling.TeachingHours())
	}
}

func TestSharingNeedsASiblingCohort(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	only := f.declare(t, "")

	_, err := f.demand.ShareInstancePartAcrossTracks(t.Context(),
		partOfKind(t, only, domain.PartKindLecture).ID)
	if !errors.Is(err, domain.ErrNoSiblingTracks) {
		t.Fatalf("sharing a part with nobody to share it with gave %v, want ErrNoSiblingTracks", err)
	}
}

// The inverse, and it has to be as easy as the merge: sharing is a judgement that gets revised.
func TestSplittingGivesEveryCohortItsOwnPartAgain(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()

	first := f.declare(t, "A")
	second, err := f.demand.DuplicateCourseInstance(ctx, first.ID, "B", "", uuid.Nil)
	if err != nil {
		t.Fatalf("cannot duplicate the instance: %v", err)
	}
	lecture := partOfKind(t, first, domain.PartKindLecture)
	if _, err := f.demand.ShareInstancePartAcrossTracks(ctx, lecture.ID); err != nil {
		t.Fatalf("cannot share the lecture: %v", err)
	}

	back, err := f.demand.SplitInstancePartAcrossTracks(ctx, lecture.ID)
	if err != nil {
		t.Fatalf("cannot stop sharing the lecture: %v", err)
	}
	if partOfKind(t, back, domain.PartKindLecture).SharedAcrossTracks {
		t.Error("the lecture is still marked as shared")
	}

	sibling, err := f.demand.CourseInstanceByID(ctx, second.ID)
	if err != nil {
		t.Fatalf("cannot re-read the sibling: %v", err)
	}
	if got := kinds(sibling.Parts); len(got) != 2 {
		t.Fatalf("the sibling holds %v, want its own lecture back alongside its laboratory", got)
	}
	if len(sibling.BorrowedParts) != 0 {
		t.Errorf("the sibling still borrows %d part(s)", len(sibling.BorrowedParts))
	}
	if hours := partOfKind(t, sibling, domain.PartKindLecture).TeachingHours; hours == nil || *hours != 2 {
		t.Errorf("the sibling's own lecture is credited with %v hours, want the shared one's 2", hours)
	}

	// Undoing it twice must not give anybody a second lecture.
	if _, err := f.demand.SplitInstancePartAcrossTracks(ctx, lecture.ID); !errors.Is(err, domain.ErrNotSharedAcrossTracks) {
		t.Errorf("undoing an unshared part gave %v, want ErrNotSharedAcrossTracks", err)
	}
}

func TestWithdrawingAnInstanceTakesItsParts(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()
	instance := f.declare(t, "A")

	if err := f.demand.DeleteCourseInstance(ctx, instance.ID); err != nil {
		t.Fatalf("cannot withdraw the instance: %v", err)
	}

	var parts int
	if err := f.schema.Pool.QueryRow(ctx,
		`SELECT count(*) FROM instance_part WHERE course_instance_id = $1`,
		instance.ID).Scan(&parts); err != nil {
		t.Fatalf("cannot count the parts: %v", err)
	}
	if parts != 0 {
		t.Errorf("%d part(s) outlived their instance", parts)
	}

	if err := f.demand.DeleteCourseInstance(ctx, instance.ID); !errors.Is(err, domain.ErrInstanceNotFound) {
		t.Errorf("withdrawing it twice gave %v, want ErrInstanceNotFound", err)
	}
}

func TestPartsCanBeAddedRemovedAndCorrected(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()
	instance := f.declare(t, "A")

	hours := 2.0
	withGroup, err := f.demand.AddInstancePart(ctx, instance.ID, domain.PartKindLab, &hours)
	if err != nil {
		t.Fatalf("cannot add a laboratory group: %v", err)
	}
	if len(withGroup.Parts) != 3 {
		t.Fatalf("the instance holds %d parts, want three — the second laboratory group is one "+
			"click, which is how the multiplicity is expressed", len(withGroup.Parts))
	}
	if withGroup.TeachingHours() != 6 {
		t.Errorf("the instance costs %v hours, want 6 — a four-hour module running one lecture "+
			"and two laboratory groups", withGroup.TeachingHours())
	}

	added := withGroup.Parts[len(withGroup.Parts)-1]
	corrected := 3.0
	changed, err := f.demand.UpdateInstancePart(ctx, added.ID, domain.PartKindExercise, &corrected)
	if err != nil {
		t.Fatalf("cannot correct the part: %v", err)
	}
	last := changed.Parts[len(changed.Parts)-1]
	if last.Kind != domain.PartKindExercise || last.TeachingHours == nil || *last.TeachingHours != 3 {
		t.Errorf("the corrected part is %s with %v hours, want an exercise with 3",
			last.Kind, last.TeachingHours)
	}

	removed, err := f.demand.DeleteInstancePart(ctx, added.ID)
	if err != nil {
		t.Fatalf("cannot remove the part: %v", err)
	}
	if len(removed.Parts) != 2 {
		t.Errorf("the instance holds %d parts, want two again", len(removed.Parts))
	}
}

func TestChangingACohortRefusesOneThatExists(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()
	first := f.declare(t, "A")
	second := f.declare(t, "B")

	if _, err := f.demand.UpdateCourseInstance(ctx, second.ID, "A", nil); !errors.Is(err, domain.ErrTrackTaken) {
		t.Fatalf("renaming B onto A gave %v, want ErrTrackTaken", err)
	}

	year := 3
	changed, err := f.demand.UpdateCourseInstance(ctx, first.ID, "A", &year)
	if err != nil {
		t.Fatalf("cannot correct the cohort year: %v", err)
	}
	if changed.ProgrammeSemester == nil || *changed.ProgrammeSemester != 3 {
		t.Errorf("the cohort year is %v, want 3", changed.ProgrammeSemester)
	}
}

// Copying is what makes the second semester cheap, and what it must never do is undo work in
// the semester it copies into.
func TestCopyingASemesterSkipsWhatIsAlreadyDeclared(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()

	// Last semester's demand: two cohorts, one of them with an extra laboratory group.
	previous := f.previous
	first, err := f.demand.CreateCourseInstance(ctx, domain.NewCourseInstance{
		SemesterID: previous.ID, ModuleID: f.module, ProgrammeID: f.programme, Track: "A",
	})
	if err != nil {
		t.Fatalf("cannot declare last semester's demand: %v", err)
	}
	if _, err := f.demand.DuplicateCourseInstance(ctx, first.ID, "B", "", uuid.Nil); err != nil {
		t.Fatalf("cannot declare the second cohort: %v", err)
	}
	hours := 2.0
	if _, err := f.demand.AddInstancePart(ctx, first.ID, domain.PartKindLab, &hours); err != nil {
		t.Fatalf("cannot add last semester's second laboratory group: %v", err)
	}

	counts, err := f.demand.CopyDemand(ctx, previous, f.semester, f.programme, uuid.Nil)
	if err != nil {
		t.Fatalf("cannot copy the demand: %v", err)
	}
	if counts.Created != 2 || counts.Skipped != 0 || counts.PartsCreated != 5 {
		t.Errorf("the copy reports %+v, want two instances, nothing skipped and five parts — "+
			"the number of laboratory groups is the decision nobody wants to enter again", counts)
	}

	copied, err := f.demand.CourseInstances(ctx, domain.DemandFilter{
		SemesterCode: f.semester.Code, Programme: storetest.FixtureProgrammeA,
	})
	if err != nil {
		t.Fatalf("cannot read the copied demand: %v", err)
	}
	if len(copied) != 2 {
		t.Fatalf("the target semester holds %d instances, want 2", len(copied))
	}

	// Somebody corrects the copy, and then presses the button again.
	year := 5
	if _, err := f.demand.UpdateCourseInstance(ctx, copied[0].ID, copied[0].Track, &year); err != nil {
		t.Fatalf("cannot correct the copied instance: %v", err)
	}

	again, err := f.demand.CopyDemand(ctx, previous, f.semester, f.programme, uuid.Nil)
	if err != nil {
		t.Fatalf("cannot copy the demand again: %v", err)
	}
	if again.Created != 0 || again.Skipped != 2 || again.PartsCreated != 0 {
		t.Errorf("the second copy reports %+v, want nothing created and two skipped", again)
	}

	unchanged, err := f.demand.CourseInstanceByID(ctx, copied[0].ID)
	if err != nil {
		t.Fatalf("cannot re-read the corrected instance: %v", err)
	}
	if unchanged.ProgrammeSemester == nil || *unchanged.ProgrammeSemester != 5 {
		t.Errorf("the correction is gone (cohort year %v, want 5) — a copy overwrote work in "+
			"the semester it was copying into", unchanged.ProgrammeSemester)
	}
}

// Sharing survives a copy, unlike a duplication — the sibling comes along, so the lecture that
// was held once for both is held once for both again.
func TestCopyingKeepsASharedLectureShared(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()

	first, err := f.demand.CreateCourseInstance(ctx, domain.NewCourseInstance{
		SemesterID: f.previous.ID, ModuleID: f.module, ProgrammeID: f.programme, Track: "A",
	})
	if err != nil {
		t.Fatalf("cannot declare last semester's demand: %v", err)
	}
	if _, err := f.demand.DuplicateCourseInstance(ctx, first.ID, "B", "", uuid.Nil); err != nil {
		t.Fatalf("cannot declare the second cohort: %v", err)
	}
	if _, err := f.demand.ShareInstancePartAcrossTracks(ctx,
		partOfKind(t, first, domain.PartKindLecture).ID); err != nil {
		t.Fatalf("cannot share the lecture: %v", err)
	}

	if _, err := f.demand.CopyDemand(ctx, f.previous, f.semester, f.programme, uuid.Nil); err != nil {
		t.Fatalf("cannot copy the demand: %v", err)
	}

	copied, err := f.demand.CourseInstances(ctx, domain.DemandFilter{
		SemesterCode: f.semester.Code, Programme: storetest.FixtureProgrammeA,
	})
	if err != nil {
		t.Fatalf("cannot read the copied demand: %v", err)
	}

	var hours float64
	for _, instance := range copied {
		hours += instance.TeachingHours()
	}
	if hours != 6 {
		t.Errorf("the copied semester costs %v hours, want 6 — one shared lecture and two "+
			"laboratories. Dropping the sharing on a copy silently doubles the faculty's "+
			"hours every time somebody presses the button", hours)
	}
}

func TestTheDemandListIsNarrowedAndCarriesEverythingAScreenNeeds(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()
	f.declare(t, "A")
	f.declare(t, "B")

	all, err := f.demand.CourseInstances(ctx, domain.DemandFilter{SemesterCode: f.semester.Code})
	if err != nil {
		t.Fatalf("cannot read the demand: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("the semester holds %d instances, want 2", len(all))
	}
	if all[0].Track != "A" || all[1].Track != "B" {
		t.Errorf("the cohorts arrive as %q, %q — sibling cohorts sit next to each other in "+
			"cohort order", all[0].Track, all[1].Track)
	}
	if all[0].SemesterPhase == "" {
		t.Error("the instance arrived without its semester's phase, which every write to it " +
			"is judged against")
	}

	other, err := f.demand.CourseInstances(ctx, domain.DemandFilter{
		SemesterCode: f.semester.Code, Programme: storetest.FixtureProgrammeB,
	})
	if err != nil {
		t.Fatalf("cannot read the other programme's demand: %v", err)
	}
	if len(other) != 0 {
		t.Errorf("the other programme's demand holds %d instances, want none", len(other))
	}

	byModule, err := f.demand.CourseInstances(ctx, domain.DemandFilter{
		SemesterCode: f.semester.Code, Module: f.withoutHours,
	})
	if err != nil {
		t.Fatalf("cannot read the demand of one module: %v", err)
	}
	if len(byModule) != 0 {
		t.Errorf("filtering by a module nobody declared gave %d instances", len(byModule))
	}
}

func partOfKind(t *testing.T, instance *domain.CourseInstance, kind domain.InstancePartKind) domain.InstancePart {
	t.Helper()

	for _, p := range instance.Parts {
		if p.Kind == kind {
			return p
		}
	}
	t.Fatalf("the instance holds no %s", kind)
	return domain.InstancePart{}
}

// The change that makes a semester plannable in October: a module nobody has split yet is
// declared from the proposal, and its parts are exactly the ones the interface showed.
func TestAnInstanceCanBeDeclaredFromTheProposedSplit(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()

	// The second fixture module — four hours, lecture plus exercise — and nobody has stated how
	// they divide.
	unsplit := moduleID(t, f.schema, storetest.FixtureModuleDutyDiffers)

	instance, err := f.demand.CreateCourseInstance(ctx, domain.NewCourseInstance{
		SemesterID:  f.semester.ID,
		ModuleID:    unsplit,
		ProgrammeID: f.programme,
	})
	if err != nil {
		t.Fatalf("declaring from a proposal gave %v", err)
	}

	if got := kinds(instance.Parts); len(got) != 2 ||
		got[0] != domain.PartKindLecture || got[1] != domain.PartKindExercise {
		t.Fatalf("the instance holds %v, want the proposal's lecture and exercise", got)
	}
	if instance.TeachingHours() != 4 {
		t.Errorf("the instance costs %v hours, want the 4 the catalogue states",
			instance.TeachingHours())
	}

	// And the module still says nobody has stated its split — declaring from a guess must not
	// quietly turn the guess into the faculty's own statement.
	var stated int
	if err := f.schema.Pool.QueryRow(ctx,
		`SELECT count(*) FROM module_component WHERE module_id = $1`, unsplit).Scan(&stated); err != nil {
		t.Fatalf("cannot count the split: %v", err)
	}
	if stated != 0 {
		t.Errorf("declaring wrote %d component(s) — a proposal is not a statement", stated)
	}
}

// planEntry is the shorthand the reconciliation tests read in.
func planEntry(moduleID uuid.UUID, tracks ...domain.DemandTrack) domain.DemandEntry {
	return domain.DemandEntry{ModuleID: moduleID, Tracks: tracks}
}

func track(letter string, groups int) domain.DemandTrack {
	return domain.DemandTrack{Track: letter, Groups: groups}
}

// The four directions of the reconciliation, in the order somebody actually does them: declare a
// module, give it a second cohort, give the cohorts different numbers of groups, take one away.
func TestPlanningReconcilesAWholeScreen(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()

	// One module, one cohort, two laboratory groups.
	plan, err := f.demand.PlanDemand(ctx, f.semester.Code, f.programme,
		[]domain.DemandEntry{planEntry(f.module, track("", 2))}, uuid.Nil, false)
	if err != nil {
		t.Fatalf("planning gave %v", err)
	}
	if len(plan.Created) != 1 || len(plan.Withdrawn) != 0 {
		t.Fatalf("the first plan reports %+v, want one instance created", plan)
	}

	instances := f.instances(t)
	if len(instances) != 1 {
		t.Fatalf("the semester holds %d instances, want 1", len(instances))
	}
	if got := len(instances[0].Parts); got != 3 {
		t.Errorf("the cohort holds %d parts, want a lecture and two laboratory groups", got)
	}

	// A second cohort, and the first one gets its letter in the same act — the instance that was
	// there is renamed rather than withdrawn and rebuilt.
	before := instances[0].ID
	plan, err = f.demand.PlanDemand(ctx, f.semester.Code, f.programme,
		[]domain.DemandEntry{planEntry(f.module, track("A", 3), track("B", 2))}, uuid.Nil, false)
	if err != nil {
		t.Fatalf("planning the second cohort gave %v", err)
	}
	if len(plan.Created) != 1 || len(plan.Withdrawn) != 0 {
		t.Errorf("adding a cohort reports %+v, want one created and none withdrawn — the first "+
			"cohort is renamed, not replaced", plan)
	}

	instances = f.instances(t)
	if len(instances) != 2 {
		t.Fatalf("the module runs in %d cohorts, want 2", len(instances))
	}
	if instances[0].ID != before {
		t.Errorf("the original instance was replaced instead of renamed — its parts and, later, " +
			"the wishes on them would have gone with it")
	}
	if instances[0].Track != "A" || instances[1].Track != "B" {
		t.Fatalf("the cohorts are %q and %q, want A and B", instances[0].Track, instances[1].Track)
	}
	// Different numbers of groups per cohort, which is the case the table has to allow: one
	// lecture and three laboratories against one lecture and two.
	if got := len(instances[0].Parts); got != 4 {
		t.Errorf("cohort A holds %d parts, want a lecture and three groups", got)
	}
	if got := len(instances[1].Parts); got != 3 {
		t.Errorf("cohort B holds %d parts, want a lecture and two groups", got)
	}

	// Back to one cohort: B goes, A stays and keeps its letter.
	plan, err = f.demand.PlanDemand(ctx, f.semester.Code, f.programme,
		[]domain.DemandEntry{planEntry(f.module, track("A", 1))}, uuid.Nil, false)
	if err != nil {
		t.Fatalf("planning back to one cohort gave %v", err)
	}
	if len(plan.Withdrawn) != 1 || plan.Withdrawn[0].Track != "B" {
		t.Errorf("reducing the cohorts reports %+v, want B withdrawn", plan.Withdrawn)
	}
	if len(plan.Changed) == 0 {
		t.Error("the group count went from three to one and nothing was reported")
	}

	instances = f.instances(t)
	if len(instances) != 1 || len(instances[0].Parts) != 2 {
		t.Fatalf("what is left is %d instance(s) with %d parts, want one with a lecture and one "+
			"group", len(instances), len(instances[0].Parts))
	}

	// And the module named nowhere in the plan is not touched by any of this.
	if plan.Empty() {
		t.Error("a plan that withdrew a cohort reports itself as empty")
	}
	if !plan.Destructive() {
		t.Error("a plan that withdrew a cohort does not report itself as destructive")
	}
}

// The property the interface's filters rest on: what is not on the screen is not in the plan, and
// what is not in the plan is not touched.
func TestPlanningLeavesUnnamedModulesAlone(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()

	other := moduleID(t, f.schema, storetest.FixtureModuleDutyDiffers)

	if _, err := f.demand.PlanDemand(ctx, f.semester.Code, f.programme, []domain.DemandEntry{
		planEntry(f.module, track("", 1)),
		planEntry(other, track("", 1)),
	}, uuid.Nil, false); err != nil {
		t.Fatalf("planning gave %v", err)
	}

	// A second save that mentions only one of them — the other must survive it untouched, the
	// way a filtered screen leaves the rest of the catalogue alone.
	if _, err := f.demand.PlanDemand(ctx, f.semester.Code, f.programme,
		[]domain.DemandEntry{planEntry(f.module, track("", 2))}, uuid.Nil, false); err != nil {
		t.Fatalf("the second plan gave %v", err)
	}

	if got := len(f.instances(t)); got != 2 {
		t.Errorf("%d instances survive, want 2 — a plan that does not name a module must not "+
			"withdraw it", got)
	}
}

// A tick taken away is shown before it is acted on, so the preview has to be exactly what the
// save would do — and it has to leave nothing behind.
func TestADryRunReportsEverythingAndWritesNothing(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()

	if _, err := f.demand.PlanDemand(ctx, f.semester.Code, f.programme,
		[]domain.DemandEntry{planEntry(f.module, track("A", 2), track("B", 2))},
		uuid.Nil, false); err != nil {
		t.Fatalf("planning gave %v", err)
	}

	dry, err := f.demand.PlanDemand(ctx, f.semester.Code, f.programme,
		[]domain.DemandEntry{planEntry(f.module)}, uuid.Nil, true)
	if err != nil {
		t.Fatalf("the dry run gave %v", err)
	}
	if !dry.DryRun || len(dry.Withdrawn) != 2 {
		t.Fatalf("the dry run reports %+v, want both cohorts as withdrawn", dry)
	}

	if got := len(f.instances(t)); got != 2 {
		t.Errorf("%d instances survive the dry run, want both — a preview that writes is not a "+
			"preview", got)
	}
}

// What a "group" multiplies, and where it multiplies nothing.
//
// The figure applies to the practical unit of the split — the laboratory, the exercise, and yes
// the seminar of a module that is nothing else, because those do run in parallel groups. A module
// that is nothing but a lecture has no such unit, and there the figure has to be without effect
// rather than an error: a screen sending a uniform 2 for every row must not fail on it, and
// parallel lectures are not what anybody means.
func TestGroupsMultiplyThePracticalUnitAndNothingElse(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()

	seminar := moduleID(t, f.schema, storetest.FixtureModuleTwoSlots)
	lectureOnly := moduleID(t, f.schema, storetest.FixtureModuleWithoutName)

	if _, err := f.demand.PlanDemand(ctx, f.semester.Code, f.programme, []domain.DemandEntry{
		planEntry(seminar, track("", 3)),
		planEntry(lectureOnly, track("", 3)),
	}, uuid.Nil, false); err != nil {
		t.Fatalf("planning gave %v", err)
	}

	seminarParts := f.partsOf(t, seminar)
	if len(seminarParts) != 3 {
		t.Errorf("the seminar holds %d parts, want three parallel seminar groups",
			len(seminarParts))
	}
	for _, p := range seminarParts {
		if p.Kind != domain.PartKindSeminar {
			t.Errorf("a seminar group came out as %s", p.Kind)
		}
	}

	lectureParts := f.partsOf(t, lectureOnly)
	if len(lectureParts) != 1 || lectureParts[0].Kind != domain.PartKindLecture {
		t.Errorf("the lecture-only module holds %v, want one lecture — a number of groups has "+
			"nothing to multiply there", kinds(lectureParts))
	}
}

// A shared lecture serves the cohort that appears beside it, so the new one must not get a
// lecture of its own — the same rule duplicating an instance follows, applied where a plan
// creates one.
func TestPlanningDoesNotGiveANewCohortAnAlreadySharedLecture(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()

	first := f.declare(t, "A")
	second, err := f.demand.DuplicateCourseInstance(ctx, first.ID, "B", "", uuid.Nil)
	if err != nil {
		t.Fatalf("cannot declare the second cohort: %v", err)
	}
	if _, err := f.demand.ShareInstancePartAcrossTracks(ctx,
		partOfKind(t, first, domain.PartKindLecture).ID); err != nil {
		t.Fatalf("cannot share the lecture: %v", err)
	}
	if err := f.demand.DeleteCourseInstance(ctx, second.ID); err != nil {
		t.Fatalf("cannot withdraw the second cohort: %v", err)
	}

	// And now a plan brings the second cohort back.
	if _, err := f.demand.PlanDemand(ctx, f.semester.Code, f.programme,
		[]domain.DemandEntry{planEntry(f.module, track("A", 1), track("B", 1))},
		uuid.Nil, false); err != nil {
		t.Fatalf("planning gave %v", err)
	}

	instances := f.instances(t)
	if len(instances) != 2 {
		t.Fatalf("the module runs in %d cohorts, want 2", len(instances))
	}
	back := instances[1]
	for _, p := range back.Parts {
		if p.Kind == domain.PartKindLecture {
			t.Errorf("the new cohort holds a lecture of its own while the sibling's is held for " +
				"everybody — that is the same teaching counted twice")
		}
	}
	if len(back.BorrowedParts) != 1 {
		t.Errorf("the new cohort borrows %d part(s), want the shared lecture",
			len(back.BorrowedParts))
	}
}

// A plan over rows that were not made by a plan.
//
// The demand of a real installation is not only what this table wrote: cohorts declared one by
// one, a lecture shared across them, a part added by hand, a module whose split somebody cleared
// afterwards. Reconciling *that* is the case a fixture built by the reconciliation itself never
// reaches — and the one a person meets on the day they open the screen for a semester somebody
// else started.
//
// What this asserts is not a particular outcome for each row. It is that no arrangement of them
// costs more than its own row: the plan comes back, the other rows are applied, and whatever
// could not be done is named.
func TestPlanningReconcilesWhatOtherPathsLeftBehind(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()

	// One module, declared as two cohorts the old way — the second by duplication — with the
	// lecture shared and an extra group added by hand.
	first := f.declare(t, "")
	second, err := f.demand.DuplicateCourseInstance(ctx, first.ID, "A", "", uuid.Nil)
	if err != nil {
		t.Fatalf("cannot duplicate: %v", err)
	}
	if _, err := f.demand.ShareInstancePartAcrossTracks(ctx,
		partOfKind(t, first, domain.PartKindLecture).ID); err != nil {
		t.Fatalf("cannot share the lecture: %v", err)
	}
	hours := 2.0
	if _, err := f.demand.AddInstancePart(ctx, second.ID, domain.PartKindExercise, &hours); err != nil {
		t.Fatalf("cannot add a part by hand: %v", err)
	}

	// A second module whose split somebody cleared after declaring it.
	other := moduleID(t, f.schema, storetest.FixtureModuleDutyDiffers)
	if _, err := f.demand.CreateCourseInstance(ctx, domain.NewCourseInstance{
		SemesterID: f.semester.ID, ModuleID: other, ProgrammeID: f.programme,
	}); err != nil {
		t.Fatalf("cannot declare the second module: %v", err)
	}
	if _, err := f.schema.Pool.Exec(ctx,
		`DELETE FROM module_component WHERE module_id = $1`, other); err != nil {
		t.Fatalf("cannot clear the split: %v", err)
	}

	// And now the screen saves: the first module in two cohorts with two groups each, the second
	// as it is, and a module that does not exist at all — which is what a load from a moment ago
	// and a deletion in between look like from here.
	plan, err := f.demand.PlanDemand(ctx, f.semester.Code, f.programme, []domain.DemandEntry{
		planEntry(f.module, track("", 2), track("A", 2)),
		planEntry(other, track("", 1)),
		planEntry(uuid.New(), track("", 1)),
	}, uuid.Nil, false)
	if err != nil {
		t.Fatalf("planning over a demand somebody else built gave %v — one awkward row must "+
			"cost its own row and not the screen", err)
	}

	if len(plan.Refused) != 1 || plan.Refused[0].Code != "MODULE_NOT_FOUND" {
		t.Errorf("the plan refuses %+v, want the module that is not there", plan.Refused)
	}

	// The shared lecture is still shared, and still counted once.
	instances := f.instances(t)
	var shared int
	for _, instance := range instances {
		for _, p := range instance.Parts {
			if p.SharedAcrossTracks {
				shared++
			}
		}
	}
	if shared != 1 {
		t.Errorf("%d shared parts after the plan, want the one that was there", shared)
	}
}

// The first thing anybody plans in a semester nobody has touched.
//
// This is the case that failed in production, and it failed at the one place where a preview and
// a write differ: the preview must not record the semester, so it used to be handed no semester
// at all — and wrote instances pointing at nothing. A foreign key said so, and the person got
// "Das hat nicht geklappt."
//
// Both halves are asserted here, because they are one property seen from two sides: the dry run
// reports what would happen and leaves no row behind, and the save does the same thing and keeps
// it.
func TestPlanningTheFirstDemandOfAnUntouchedSemester(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()

	// A semester with no row at all: nobody has decided anything about it.
	const untouched = "2033-WS"
	if recorded(t, f.schema, untouched) {
		t.Fatalf("%s is already recorded — this test is about a semester that is not", untouched)
	}

	entries := []domain.DemandEntry{planEntry(f.module, track("", 2))}

	dry, err := f.demand.PlanDemand(ctx, untouched, f.programme, entries, uuid.Nil, true)
	if err != nil {
		t.Fatalf("the dry run gave %v", err)
	}
	if len(dry.Created) != 1 {
		t.Errorf("the dry run reports %+v, want the one cohort it would declare", dry.Created)
	}
	if recorded(t, f.schema, untouched) {
		t.Error("the dry run recorded the semester — the row is the record of a decision, and " +
			"looking is not one")
	}

	if _, err := f.demand.PlanDemand(ctx, untouched, f.programme, entries, uuid.Nil, false); err != nil {
		t.Fatalf("the save gave %v", err)
	}
	if !recorded(t, f.schema, untouched) {
		t.Error("the save did not record the semester it planned in")
	}

	instances, err := f.demand.CourseInstances(ctx, domain.DemandFilter{
		SemesterCode: untouched, Programme: storetest.FixtureProgrammeA,
	})
	if err != nil {
		t.Fatalf("cannot read the demand: %v", err)
	}
	if len(instances) != 1 || len(instances[0].Parts) != 3 {
		t.Fatalf("the semester holds %d instance(s), want one with a lecture and two groups",
			len(instances))
	}
}

func recorded(t *testing.T, s *storetest.Schema, code string) bool {
	t.Helper()

	var exists bool
	if err := s.Pool.QueryRow(t.Context(),
		`SELECT EXISTS (SELECT 1 FROM semester WHERE code = $1)`, code).Scan(&exists); err != nil {
		t.Fatalf("cannot look for the semester: %v", err)
	}
	return exists
}

// A programme may declare a module that is at home somewhere else.
//
// The identity of an instance names a programme and a module and says nothing about the two
// belonging together — and that is deliberate rather than an omission. Modules are borrowed
// across programmes and across faculties; the difference between where a module is at home and
// who declares it *is* the dean's office's import/export figure, so a schema that refused the
// case would refuse the thing it exists to measure. It is also the escape hatch the faculty
// needs: a module that has to be offered and is not in this programme's catalogue.
//
// Written down as a test because nothing else says it. There is no constraint to read, no
// comment on a WHERE clause — the rule is the absence of a predicate, and an absence is what
// somebody adds back while tidying up.
func TestAModuleOfAnotherProgrammeCanBeDeclared(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()

	// Its home is PZ; the demand is PA's.
	foreign := moduleID(t, f.schema, storetest.FixtureModuleOfProgrammeZ)

	instance, err := f.demand.CreateCourseInstance(ctx, domain.NewCourseInstance{
		SemesterID:  f.semester.ID,
		ProgrammeID: f.programme,
		ModuleID:    foreign,
	})
	if err != nil {
		t.Fatalf("declaring a module of another programme: %v", err)
	}
	if instance.Programme.Code != storetest.FixtureProgrammeA || instance.Module.ID != foreign {
		t.Errorf("the instance is %s's offering of %s, want PA's demand for PZ's module",
			instance.Programme.Code, instance.Module.Name)
	}
	if instance.Module.HomeProgramme.Code != storetest.FixtureProgrammeZ {
		t.Errorf("the module's home is %s, so this test is no longer about a foreign one",
			instance.Module.HomeProgramme.Code)
	}
	if len(instance.Parts) == 0 {
		t.Error("the instance has no parts — it was built from neither a split nor a proposal")
	}

	// And it comes back on the programme's own demand, which is the half that makes it usable:
	// a row nobody can see is a row nobody can take back.
	held, err := f.demand.CourseInstances(ctx, domain.DemandFilter{
		SemesterCode: f.semester.Code,
		Programme:    storetest.FixtureProgrammeA,
	})
	if err != nil {
		t.Fatalf("reading the demand: %v", err)
	}
	found := false
	for _, i := range held {
		if i.Module.ID == foreign {
			found = true
		}
	}
	if !found {
		t.Error("the foreign module is not in the programme's demand")
	}
}

// declareIn declares the fixture's ordinary module for a named programme and cohort.
//
// The coverage tests need two programmes' demand for one module, which is the case the whole
// mechanism is about and the one the plain declare helper cannot express.
func (f demandFixture) declareIn(t *testing.T, programme, track string) *domain.CourseInstance {
	t.Helper()

	instance, err := f.demand.CreateCourseInstance(t.Context(), domain.NewCourseInstance{
		SemesterID:  f.semester.ID,
		ModuleID:    f.module,
		ProgrammeID: programmeID(t, f.schema, programme),
		Track:       track,
	})
	if err != nil {
		t.Fatalf("cannot declare the instance for %s: %v", programme, err)
	}
	return instance
}

// The case the whole mechanism exists for: two programmes need the module and hold it once.
func TestAcceptingCoverageTakesTheGuestsParts(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()

	host := f.declareIn(t, storetest.FixtureProgrammeA, "")
	guest := f.declareIn(t, storetest.FixtureProgrammeB, "")

	// Both hold their own teaching until somebody says otherwise, which is the safe default:
	// coverage by accident would make the faculty's hours look smaller than they are.
	if len(guest.Parts) != 2 {
		t.Fatalf("the guest was declared with %d parts, want 2", len(guest.Parts))
	}
	before := host.TeachingHours() + guest.TeachingHours()
	if before != 8 {
		t.Fatalf("two cohorts of a 4-hour module cost %v hours, want 8", before)
	}

	asked, err := f.demand.RequestInstanceCoverage(ctx, guest.ID, host.ID, uuid.Nil)
	if err != nil {
		t.Fatalf("cannot ask to be covered: %v", err)
	}

	// Asking changes nothing. That is the whole of the two-sided handshake: the other programme
	// has not agreed, so it still holds only its own event and this one still holds its own.
	if asked.CoveredBy == nil {
		t.Fatal("the request was not recorded")
	}
	if asked.CoveredBy.Accepted() {
		t.Error("a request counted as an agreement, which is one side deciding for two")
	}
	if len(asked.Parts) != 2 {
		t.Errorf("asking removed %d of the guest's parts; asking must change nothing",
			2-len(asked.Parts))
	}

	agreed, err := f.demand.AcceptInstanceCoverage(ctx, guest.ID, uuid.Nil)
	if err != nil {
		t.Fatalf("cannot agree to cover: %v", err)
	}

	if !agreed.CoveredBy.Accepted() {
		t.Error("the agreement was not recorded")
	}
	if len(agreed.Parts) != 0 {
		t.Errorf("the covered cohort still holds %d parts of its own", len(agreed.Parts))
	}
	if got := agreed.TeachingHours(); got != 0 {
		t.Errorf("the covered cohort costs %v hours, want 0 — the event is held once and "+
			"costs once, at the programme that holds it", got)
	}

	// It attends the teaching it no longer owns, or its screen would show a cohort with nothing
	// at all and read as a planning mistake.
	if len(agreed.BorrowedParts) != 2 {
		t.Fatalf("the covered cohort borrows %d parts, want 2", len(agreed.BorrowedParts))
	}
	for _, b := range agreed.BorrowedParts {
		if b.FromProgramme != storetest.FixtureProgrammeA {
			t.Errorf("a borrowed part names programme %q, want %s",
				b.FromProgramme, storetest.FixtureProgrammeA)
		}
	}

	// And the faculty now spends half as much on this module, which is the point.
	after := 0.0
	for _, instance := range f.allInstances(t) {
		after += instance.TeachingHours()
	}
	if after != 4 {
		t.Errorf("one event held for two programmes costs the faculty %v hours, want 4", after)
	}
}

// allInstances is every programme's demand in the fixture's semester.
func (f demandFixture) allInstances(t *testing.T) []domain.CourseInstance {
	t.Helper()

	instances, err := f.demand.CourseInstances(t.Context(), domain.DemandFilter{
		SemesterCode: f.semester.Code,
	})
	if err != nil {
		t.Fatalf("cannot read the demand: %v", err)
	}
	return instances
}

// The host's own screen has to show the agreement, or it exists only in the other programme's
// table and the lead who made it cannot find it again.
func TestAHostSeesTheDemandsItCovers(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()

	host := f.declareIn(t, storetest.FixtureProgrammeA, "")
	guest := f.declareIn(t, storetest.FixtureProgrammeB, "")

	if _, err := f.demand.RequestInstanceCoverage(ctx, guest.ID, host.ID, uuid.Nil); err != nil {
		t.Fatalf("cannot ask to be covered: %v", err)
	}

	read, err := f.demand.CourseInstanceByID(ctx, host.ID)
	if err != nil {
		t.Fatalf("cannot read the host: %v", err)
	}
	if len(read.Covers) != 1 {
		t.Fatalf("the host shows %d covered demands, want 1 — an unanswered request is "+
			"exactly what this side needs to see", len(read.Covers))
	}
	if read.Covers[0].Accepted() {
		t.Error("an unanswered request reads as agreed on the host's side")
	}
	if got := read.Covers[0].Instance.Programme.Code; got != storetest.FixtureProgrammeB {
		t.Errorf("the covered demand belongs to %q, want %s", got, storetest.FixtureProgrammeB)
	}

	if _, err := f.demand.AcceptInstanceCoverage(ctx, guest.ID, uuid.Nil); err != nil {
		t.Fatalf("cannot agree: %v", err)
	}
	read, err = f.demand.CourseInstanceByID(ctx, host.ID)
	if err != nil {
		t.Fatalf("cannot re-read the host: %v", err)
	}
	if !read.Covers[0].Accepted() {
		t.Error("the agreement is not visible on the side that made it")
	}

	// The host keeps its own teaching throughout. Coverage takes parts from the guest, never
	// from the cohort that holds the event.
	if len(read.Parts) != 2 {
		t.Errorf("the host holds %d parts, want 2", len(read.Parts))
	}
}

// The transaction assertion, and the one that would pass against a fake.
//
// Agreeing removes the guest's parts. A part somebody already holds refuses to go, and then
// *nothing* happens — not the acceptance either, or the database would say the demand is covered
// while the cohort still holds its own teaching.
func TestAcceptingCoverageIsRefusedWhenAGuestPartIsAssigned(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()

	host := f.declareIn(t, storetest.FixtureProgrammeA, "")
	guest := f.declareIn(t, storetest.FixtureProgrammeB, "")

	if _, err := f.demand.RequestInstanceCoverage(ctx, guest.ID, host.ID, uuid.Nil); err != nil {
		t.Fatalf("cannot ask to be covered: %v", err)
	}

	// Somebody is already teaching one of the guest's parts.
	storetest.SeedPerson(t, f.schema, testdata.Eins, "LECTURER")
	if _, err := f.schema.Pool.Exec(ctx,
		`INSERT INTO assignment (instance_part_id, person_id) VALUES ($1, $2)`,
		guest.Parts[0].ID, testdata.Eins.ID()); err != nil {
		t.Fatalf("cannot fill a part of the guest: %v", err)
	}

	_, err := f.demand.AcceptInstanceCoverage(ctx, guest.ID, uuid.Nil)
	if !errors.Is(err, domain.ErrPartAssigned) {
		t.Fatalf("agreeing over a staffed part answered %v, want ErrPartAssigned", err)
	}

	// Nothing moved. Both halves, because a half-written agreement is the state this is a
	// transaction to prevent.
	read, err := f.demand.CourseInstanceByID(ctx, guest.ID)
	if err != nil {
		t.Fatalf("cannot re-read the guest: %v", err)
	}
	if read.CoveredBy.Accepted() {
		t.Error("the agreement was written even though the parts could not go — the cohort " +
			"now reads as covered while still holding its own teaching")
	}
	if len(read.Parts) != 2 {
		t.Errorf("the guest holds %d parts after a refused agreement, want 2", len(read.Parts))
	}
}

// The inverse has to exist and has to be as easy, because coverage is a judgement that gets
// revised — the colleague who was going to hold it for both is on sabbatical.
func TestReleasingCoverageGivesTheGuestItsTeachingBack(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()

	host := f.declareIn(t, storetest.FixtureProgrammeA, "")
	guest := f.declareIn(t, storetest.FixtureProgrammeB, "")

	if _, err := f.demand.RequestInstanceCoverage(ctx, guest.ID, host.ID, uuid.Nil); err != nil {
		t.Fatalf("cannot ask: %v", err)
	}
	if _, err := f.demand.AcceptInstanceCoverage(ctx, guest.ID, uuid.Nil); err != nil {
		t.Fatalf("cannot agree: %v", err)
	}

	released, err := f.demand.ReleaseInstanceCoverage(ctx, guest.ID)
	if err != nil {
		t.Fatalf("cannot end the coverage: %v", err)
	}
	if released.CoveredBy != nil {
		t.Error("the link survived the release")
	}

	// One part per unit of the module's split. What does NOT come back is the number of
	// laboratory groups: the split states one unit per kind, and the multiplicity was a planning
	// decision that went with the parts. Inventing three groups because there were three before
	// would be inventing teaching.
	if got := kinds(released.Parts); len(got) != 2 {
		t.Fatalf("the cohort got %v back, want a lecture and a laboratory", got)
	}
	if got := released.TeachingHours(); got != 4 {
		t.Errorf("the cohort costs %v hours again, want 4", got)
	}
	if len(released.BorrowedParts) != 0 {
		t.Errorf("it still borrows %d parts after holding its own again",
			len(released.BorrowedParts))
	}
}

// Ending a request nobody answered is the same operation, and it must not hand out teaching the
// cohort never stopped holding.
func TestReleasingAnUnansweredRequestChangesNoParts(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()

	host := f.declareIn(t, storetest.FixtureProgrammeA, "")
	guest := f.declareIn(t, storetest.FixtureProgrammeB, "")

	if _, err := f.demand.RequestInstanceCoverage(ctx, guest.ID, host.ID, uuid.Nil); err != nil {
		t.Fatalf("cannot ask: %v", err)
	}

	released, err := f.demand.ReleaseInstanceCoverage(ctx, guest.ID)
	if err != nil {
		t.Fatalf("cannot withdraw the request: %v", err)
	}
	if released.CoveredBy != nil {
		t.Error("the request survived being withdrawn")
	}
	if len(released.Parts) != 2 {
		t.Errorf("withdrawing a request left the cohort with %d parts, want the 2 it never "+
			"stopped holding", len(released.Parts))
	}
}

// The refusals, each named so the caller can act on it.
func TestCoverageRefusalsNameWhatIsWrong(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()

	host := f.declareIn(t, storetest.FixtureProgrammeA, "")
	sibling := f.declareIn(t, storetest.FixtureProgrammeA, "B")
	guest := f.declareIn(t, storetest.FixtureProgrammeB, "")

	if _, err := f.demand.RequestInstanceCoverage(ctx, host.ID, host.ID, uuid.Nil); !errors.Is(
		err, domain.ErrCoverageSelf) {
		t.Errorf("covering itself answered %v, want ErrCoverageSelf", err)
	}

	// The same programme is what the shared lecture across parallel cohorts is for, and the
	// refusal says so rather than leaving somebody to guess.
	if _, err := f.demand.RequestInstanceCoverage(ctx, sibling.ID, host.ID, uuid.Nil); !errors.Is(
		err, domain.ErrCoverageSameProgramme) {
		t.Errorf("coverage inside one programme answered %v, want ErrCoverageSameProgramme", err)
	}

	// Accepting and releasing need a request to act on.
	if _, err := f.demand.AcceptInstanceCoverage(ctx, guest.ID, uuid.Nil); !errors.Is(
		err, domain.ErrCoverageNotRequested) {
		t.Errorf("agreeing with no request answered %v, want ErrCoverageNotRequested", err)
	}
	if _, err := f.demand.ReleaseInstanceCoverage(ctx, guest.ID); !errors.Is(
		err, domain.ErrCoverageNotRequested) {
		t.Errorf("ending nothing answered %v, want ErrCoverageNotRequested", err)
	}

	if _, err := f.demand.RequestInstanceCoverage(ctx, guest.ID, host.ID, uuid.Nil); err != nil {
		t.Fatalf("cannot ask: %v", err)
	}

	// Pointing a standing request somewhere else is ending it and asking again, which is two
	// decisions and reads as two.
	if _, err := f.demand.RequestInstanceCoverage(ctx, guest.ID, sibling.ID, uuid.Nil); !errors.Is(
		err, domain.ErrCoverageAlreadySet) {
		t.Errorf("re-pointing a request answered %v, want ErrCoverageAlreadySet", err)
	}

	if _, err := f.demand.AcceptInstanceCoverage(ctx, guest.ID, uuid.Nil); err != nil {
		t.Fatalf("cannot agree: %v", err)
	}
	if _, err := f.demand.AcceptInstanceCoverage(ctx, guest.ID, uuid.Nil); !errors.Is(
		err, domain.ErrCoverageAlreadyAccepted) {
		t.Errorf("agreeing twice answered %v, want ErrCoverageAlreadyAccepted", err)
	}
}

// A covered cohort holds no teaching, and every path that could hand it some has to say so rather
// than quietly obliging. These are the regressions this feature risks.
func TestNothingGivesACoveredCohortTeachingBack(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()

	host := f.declareIn(t, storetest.FixtureProgrammeA, "")
	guest := f.declareIn(t, storetest.FixtureProgrammeB, "")

	if _, err := f.demand.RequestInstanceCoverage(ctx, guest.ID, host.ID, uuid.Nil); err != nil {
		t.Fatalf("cannot ask: %v", err)
	}
	if _, err := f.demand.AcceptInstanceCoverage(ctx, guest.ID, uuid.Nil); err != nil {
		t.Fatalf("cannot agree: %v", err)
	}

	hours := 2.0
	if _, err := f.demand.AddInstancePart(ctx, guest.ID, domain.PartKindLab, &hours); !errors.Is(
		err, domain.ErrInstanceCovered) {
		t.Errorf("adding a part to a covered cohort answered %v, want ErrInstanceCovered", err)
	}

	// Duplicating would produce a second cohort with no teaching and no explanation — the row
	// this whole mechanism exists to prevent.
	if _, err := f.demand.DuplicateCourseInstance(ctx, guest.ID, "B", "", uuid.Nil); !errors.Is(
		err, domain.ErrInstanceCovered) {
		t.Errorf("duplicating a covered cohort answered %v, want ErrInstanceCovered", err)
	}

	// And the host cannot be withdrawn while another programme's demand hangs off it. Named,
	// unlike the opaque refusal a wish earns: a coverage link is a declaration of demand, and
	// the demand is not confidential.
	if err := f.demand.DeleteCourseInstance(ctx, host.ID); !errors.Is(
		err, domain.ErrInstanceCoversOthers) {
		t.Errorf("withdrawing a host answered %v, want ErrInstanceCoversOthers", err)
	}
}

// The worst of the regressions, because it is silent: planDemand sends a group count for the
// cohort, the covered cohort holds no practical parts, and the reconciliation would helpfully
// insert some.
func TestPlanningDoesNotGiveACoveredCohortItsPartsBack(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()

	host := f.declareIn(t, storetest.FixtureProgrammeA, "")
	guest := f.declareIn(t, storetest.FixtureProgrammeB, "")

	if _, err := f.demand.RequestInstanceCoverage(ctx, guest.ID, host.ID, uuid.Nil); err != nil {
		t.Fatalf("cannot ask: %v", err)
	}
	if _, err := f.demand.AcceptInstanceCoverage(ctx, guest.ID, uuid.Nil); err != nil {
		t.Fatalf("cannot agree: %v", err)
	}

	plan, err := f.demand.PlanDemand(ctx, f.semester.Code,
		programmeID(t, f.schema, storetest.FixtureProgrammeB),
		[]domain.DemandEntry{planEntry(f.module, track("", 3))}, uuid.Nil, false)
	if err != nil {
		t.Fatalf("cannot plan the covered programme's demand: %v", err)
	}

	read, err := f.demand.CourseInstanceByID(ctx, guest.ID)
	if err != nil {
		t.Fatalf("cannot re-read the guest: %v", err)
	}
	if len(read.Parts) != 0 {
		t.Errorf("planning gave the covered cohort %d parts back — the joint event's "+
			"laboratories now count twice", len(read.Parts))
	}

	// Reported, not skipped: somebody moved a stepper and is owed an answer.
	var told bool
	for _, r := range plan.Refused {
		if r.Code == "INSTANCE_COVERED" {
			told = true
		}
	}
	if !told {
		t.Error("planning silently ignored the group count for a covered cohort; a save that " +
			"does nothing and says nothing reads as a save that did not stick")
	}
}

// A copy carries the request and never the agreement: the other programme's lead agreed about
// *that* semester, and an agreement carried forward is a decision nobody made.
func TestCopyingASemesterAsksToBeCoveredAgain(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()

	host := f.declareIn(t, storetest.FixtureProgrammeA, "")
	guest := f.declareIn(t, storetest.FixtureProgrammeB, "")

	if _, err := f.demand.RequestInstanceCoverage(ctx, guest.ID, host.ID, uuid.Nil); err != nil {
		t.Fatalf("cannot ask: %v", err)
	}
	if _, err := f.demand.AcceptInstanceCoverage(ctx, guest.ID, uuid.Nil); err != nil {
		t.Fatalf("cannot agree: %v", err)
	}

	// The holding programme's demand has to exist in the target first, or there is nothing to
	// point at — which is the other half of this rule and is counted separately.
	hostCounts, err := f.demand.CopyDemand(ctx, f.semester, f.previous,
		programmeID(t, f.schema, storetest.FixtureProgrammeA), uuid.Nil)
	if err != nil {
		t.Fatalf("cannot copy the holding programme's demand: %v", err)
	}
	if hostCounts.Created == 0 {
		t.Fatal("the holding programme's demand was not copied")
	}

	counts, err := f.demand.CopyDemand(ctx, f.semester, f.previous,
		programmeID(t, f.schema, storetest.FixtureProgrammeB), uuid.Nil)
	if err != nil {
		t.Fatalf("cannot copy the covered programme's demand: %v", err)
	}
	if counts.CoverageRequested != 1 {
		t.Errorf("the copy asked to be covered %d times, want 1", counts.CoverageRequested)
	}

	copied, err := f.demand.CourseInstances(ctx, domain.DemandFilter{
		SemesterCode: f.previous.Code, Programme: storetest.FixtureProgrammeB,
	})
	if err != nil {
		t.Fatalf("cannot read the copied demand: %v", err)
	}
	if len(copied) != 1 {
		t.Fatalf("the copy produced %d instances, want 1", len(copied))
	}
	if copied[0].CoveredBy == nil {
		t.Fatal("the copied cohort asks nobody to cover it, so its teaching silently reappeared")
	}
	if copied[0].CoveredBy.Accepted() {
		t.Error("the copy carried the agreement forward — that is the other programme's " +
			"decision about a semester nobody has planned yet")
	}
}
