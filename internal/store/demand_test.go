package store_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/store"
	"github.com/obcode/tallox.go/internal/store/storetest"
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
	plan, err := f.demand.PlanDemand(ctx, f.semester.ID, f.programme,
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
	plan, err = f.demand.PlanDemand(ctx, f.semester.ID, f.programme,
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
	plan, err = f.demand.PlanDemand(ctx, f.semester.ID, f.programme,
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

	if _, err := f.demand.PlanDemand(ctx, f.semester.ID, f.programme, []domain.DemandEntry{
		planEntry(f.module, track("", 1)),
		planEntry(other, track("", 1)),
	}, uuid.Nil, false); err != nil {
		t.Fatalf("planning gave %v", err)
	}

	// A second save that mentions only one of them — the other must survive it untouched, the
	// way a filtered screen leaves the rest of the catalogue alone.
	if _, err := f.demand.PlanDemand(ctx, f.semester.ID, f.programme,
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

	if _, err := f.demand.PlanDemand(ctx, f.semester.ID, f.programme,
		[]domain.DemandEntry{planEntry(f.module, track("A", 2), track("B", 2))},
		uuid.Nil, false); err != nil {
		t.Fatalf("planning gave %v", err)
	}

	dry, err := f.demand.PlanDemand(ctx, f.semester.ID, f.programme,
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

	if _, err := f.demand.PlanDemand(ctx, f.semester.ID, f.programme, []domain.DemandEntry{
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
	if _, err := f.demand.PlanDemand(ctx, f.semester.ID, f.programme,
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
