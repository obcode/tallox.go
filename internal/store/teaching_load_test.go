package store_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/store"
	"github.com/obcode/tallox.go/internal/store/storetest"
)

// The controlling view against a real database.
//
// The rule is a SUM over parts grouped by the module's home, and every way that sum could be
// wrong is a property of rows: a lecture shared by two cohorts is one part or two, a covered
// cohort holds parts or does not, a part without hours is NULL. A fake store would add up
// whatever it was told.

// loadOf is one home programme's entry, or fails the test.
func loadOf(t *testing.T, loads []domain.TeachingLoad, code string) domain.TeachingLoad {
	t.Helper()

	for _, l := range loads {
		if l.HomeProgramme.Code == code {
			return l
		}
	}
	t.Fatalf("the teaching load has no entry for %s", code)
	return domain.TeachingLoad{}
}

func (f demandFixture) teachingLoad(t *testing.T) []domain.TeachingLoad {
	t.Helper()

	loads, err := f.demand.TeachingLoad(t.Context(), f.semester.Code)
	if err != nil {
		t.Fatalf("cannot read the teaching load: %v", err)
	}
	return loads
}

// A planned programme nobody has declared anything for is the row the dean's office looks for,
// so it is there, at zero, rather than missing.
func TestTeachingLoadListsAPlannedProgrammeWithNothingDeclared(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)

	load := loadOf(t, f.teachingLoad(t), storetest.FixtureProgrammeA)
	if len(load.Shares) != 0 || load.TeachingHours() != 0 {
		t.Errorf("an empty semester costs PA %v hours in %d shares, want nothing",
			load.TeachingHours(), len(load.Shares))
	}
	if load.HomeProgramme.PlanningStatus != domain.ProgrammePlanned {
		t.Errorf("PA reads as %q, want PLANNED", load.HomeProgramme.PlanningStatus)
	}
}

// The case the view exists for: a module of PA taught for PB counts at PA, and the share shows
// where it went.
func TestTeachingLoadCountsAModuleAtItsHomeProgramme(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)

	f.declareIn(t, storetest.FixtureProgrammeA, "")
	f.declareSeparatelyIn(t, storetest.FixtureProgrammeB, "")

	loads := f.teachingLoad(t)
	pa := loadOf(t, loads, storetest.FixtureProgrammeA)
	if got := pa.TeachingHours(); got != 8 {
		t.Errorf("two cohorts of PA's 2+2-hour module cost PA %v hours, want 8", got)
	}
	if got := pa.OwnHours(); got != 4 {
		t.Errorf("PA teaches %v of them itself, want 4", got)
	}
	if len(pa.Shares) != 2 || pa.Shares[1].Programme.Code != storetest.FixtureProgrammeB ||
		pa.Shares[1].TeachingHours != 4 || pa.Shares[1].Instances != 1 {
		t.Errorf("PA's shares are %+v, want 4 hours in its own and 4 in one PB instance", pa.Shares)
	}

	if pb := loadOf(t, loads, storetest.FixtureProgrammeB); pb.TeachingHours() != 0 {
		t.Errorf("PB is charged %v hours for a module that is not its own", pb.TeachingHours())
	}
}

// A lecture held once for two cohorts is one part, so it is counted once.
func TestTeachingLoadCountsASharedLectureOnce(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)

	first := f.declare(t, "A")
	f.declare(t, "B")
	if got := loadOf(t, f.teachingLoad(t), storetest.FixtureProgrammeA).TeachingHours(); got != 8 {
		t.Fatalf("two cohorts cost %v hours before sharing, want 8", got)
	}

	lecture := first.Parts[0]
	if lecture.Kind != domain.PartKindLecture {
		t.Fatalf("the first part is a %s, want the lecture", lecture.Kind)
	}
	if _, err := f.demand.ShareInstancePartAcrossTracks(t.Context(), lecture.ID); err != nil {
		t.Fatalf("cannot share the lecture: %v", err)
	}

	if got := loadOf(t, f.teachingLoad(t), storetest.FixtureProgrammeA).TeachingHours(); got != 6 {
		t.Errorf("one lecture and two laboratories cost %v hours, want 6", got)
	}
}

// A covered cohort holds no parts, so it costs nothing, and its demand is still counted as an
// instance — the import/export figure needs the denominator.
func TestTeachingLoadCountsACoveredCohortAtZero(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)

	host := f.declareIn(t, storetest.FixtureProgrammeA, "")
	guest := f.declareSeparatelyIn(t, storetest.FixtureProgrammeB, "")
	if _, err := f.demand.RequestInstanceCoverage(t.Context(), guest.ID, host.ID, uuid.Nil); err != nil {
		t.Fatalf("cannot ask to be covered: %v", err)
	}
	if _, err := f.demand.AcceptInstanceCoverage(t.Context(), guest.ID, uuid.Nil); err != nil {
		t.Fatalf("cannot agree to cover: %v", err)
	}

	pa := loadOf(t, f.teachingLoad(t), storetest.FixtureProgrammeA)
	if got := pa.TeachingHours(); got != 4 {
		t.Errorf("one event held for two programmes costs PA %v hours, want 4", got)
	}
	if got := pa.Instances(); got != 2 {
		t.Errorf("PA's module is in demand %d times, want 2 — covering is not withdrawing", got)
	}
}

// A part nobody stated hours for counts zero, and says so: an unfinished split must not read as
// a light semester.
func TestTeachingLoadCountsThePartsWithoutHours(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)

	instance := f.declare(t, "")
	if _, err := f.demand.AddInstancePart(t.Context(), instance.ID, domain.PartKindLab, nil); err != nil {
		t.Fatalf("cannot add a part without hours: %v", err)
	}

	pa := loadOf(t, f.teachingLoad(t), storetest.FixtureProgrammeA)
	if pa.TeachingHours() != 4 || pa.PartsWithoutHours() != 1 {
		t.Errorf("PA costs %v hours with %d parts without hours, want 4 and 1",
			pa.TeachingHours(), pa.PartsWithoutHours())
	}
}

// "We need three FWP" is three cohorts of a placeholder, at the programme that made it.
func TestTeachingLoadCountsElectivePlaceholders(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()
	pb := programmeID(t, f.schema, storetest.FixtureProgrammeB)

	placeholder, err := store.NewModules(f.schema.Pool).CreateLocalModule(ctx, domain.NewLocalModule{
		HomeProgrammeID: pb,
		Name:            "FWP (technisch)",
		Kind:            domain.ModuleKindFwpPlaceholder,
		CourseType:      domain.CourseTypeSU,
		Frequency:       domain.FrequencyOnAnnouncement,
		Active:          true,
		Components:      []domain.ModuleComponent{{Kind: domain.PartKindLecture, TeachingHours: 4}},
	}, uuid.Nil)
	if err != nil {
		t.Fatalf("cannot create the placeholder: %v", err)
	}
	for _, track := range []string{"A", "B", "C"} {
		if _, err := f.demand.CreateCourseInstance(ctx, domain.NewCourseInstance{
			SemesterID: f.semester.ID, ModuleID: placeholder.ID, ProgrammeID: pb, Track: track,
		}); err != nil {
			t.Fatalf("cannot declare placeholder cohort %s: %v", track, err)
		}
	}
	f.declare(t, "")

	loads := f.teachingLoad(t)
	load := loadOf(t, loads, storetest.FixtureProgrammeB)
	if load.FwpInstances() != 3 || load.FwpHours() != 12 || load.TeachingHours() != 12 {
		t.Errorf("PB has %d FWP cohorts costing %v of %v hours, want 3, 12 and 12",
			load.FwpInstances(), load.FwpHours(), load.TeachingHours())
	}
	if pa := loadOf(t, loads, storetest.FixtureProgrammeA); pa.FwpInstances() != 0 {
		t.Errorf("an ordinary module counted as %d FWP cohorts", pa.FwpInstances())
	}
}

// Another semester's demand is not this semester's load.
func TestTeachingLoadIsPerSemester(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)

	if _, err := f.demand.CreateCourseInstance(t.Context(), domain.NewCourseInstance{
		SemesterID: f.previous.ID, ModuleID: f.module, ProgrammeID: f.programme,
	}); err != nil {
		t.Fatalf("cannot declare in the previous semester: %v", err)
	}

	if got := loadOf(t, f.teachingLoad(t), storetest.FixtureProgrammeA).TeachingHours(); got != 0 {
		t.Errorf("the previous semester's demand costs this one %v hours", got)
	}

	before, err := f.demand.TeachingLoad(t.Context(), f.previous.Code)
	if err != nil {
		t.Fatalf("cannot read the previous semester's load: %v", err)
	}
	if got := loadOf(t, before, storetest.FixtureProgrammeA).TeachingHours(); got != 4 {
		t.Errorf("the previous semester costs PA %v hours, want 4", got)
	}
}
