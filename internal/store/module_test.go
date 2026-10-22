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

// teacherByMail returns the fixture teacher with this address, or fails the test.
func teacherByMail(t *testing.T, teachers []domain.Teacher, mail string) domain.Teacher {
	t.Helper()

	for _, teacher := range teachers {
		if teacher.Mail == mail {
			return teacher
		}
	}
	t.Fatalf("the teacher list does not contain %s", mail)
	return domain.Teacher{}
}

// Teacher.isUser answers "may somebody of this address sign in here", and deactivating is how
// this installation takes everything away at once — tokens included. So a deactivated account
// is not an answer of yes.
//
// The teacher stays in the list either way. Who teaches at the faculty is the examination
// office's statement and does not change when this installation withdraws an account; the two
// facts sit in the same row and mean different things.
func TestAnInactiveAccountIsNotSomebodyWhoMaySignIn(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	ctx := t.Context()

	// Before the projection, so that the derived link has something to find.
	storetest.SeedPerson(t, s, testdata.Eins, "LECTURER")
	storetest.SeedZPACatalogue(t, s)
	project(t, s)

	modules := store.NewModules(s.Pool)
	filter := domain.TeacherFilter{IncludeInactive: true}

	before, err := modules.Teachers(ctx, filter)
	if err != nil {
		t.Fatalf("cannot read the teachers: %v", err)
	}
	if !teacherByMail(t, before, testdata.Eins.Mail).IsUser {
		t.Fatal("the seeded persona does not read as a user, so the link is not exercised")
	}

	if err := store.NewPeople(s.Pool).SetPersonActive(ctx, testdata.Eins.ID(), false); err != nil {
		t.Fatalf("cannot deactivate the account: %v", err)
	}

	after, err := modules.Teachers(ctx, filter)
	if err != nil {
		t.Fatalf("cannot read the teachers: %v", err)
	}
	if len(after) != len(before) {
		t.Errorf("the list lost %d rows when an account was deactivated, want the same people",
			len(before)-len(after))
	}
	if teacherByMail(t, after, testdata.Eins.Mail).IsUser {
		t.Error("a deactivated account still reads as somebody who may sign in")
	}
}

// moduleFixture is a projected catalogue with the store that reads and writes it.
type moduleFixture struct {
	modules   *store.Modules
	programme uuid.UUID
	// module is an imported one, for the paths that must never touch those.
	module uuid.UUID
}

func newModuleFixture(t *testing.T) moduleFixture {
	t.Helper()

	s := storetest.New(t)
	storetest.SeedZPACatalogue(t, s)
	project(t, s)

	return moduleFixture{
		modules:   store.NewModules(s.Pool),
		programme: programmeID(t, s, storetest.FixtureProgrammeA),
		module:    moduleID(t, s, storetest.FixtureModuleOrdinary),
	}
}

// A course the faculty enters itself is a catalogue row like any other, and appears where its
// home programme's catalogue is asked for.
//
// Nothing in ListModules had to change for that: the programme predicate has had its
// home-programme half since the beginning — twenty-six real modules are reachable only through
// it — and a local row has exactly one home.
func TestALocalModuleIsInItsHomeProgrammesCatalogue(t *testing.T) {
	t.Parallel()

	f := newModuleFixture(t)
	ctx := t.Context()

	created, err := f.modules.CreateLocalModule(ctx, domain.NewLocalModule{
		HomeProgrammeID: f.programme,
		Name:            "FWP-Platzhalter (technisch)",
		Kind:            domain.ModuleKindFwpPlaceholder,
		CourseType:      domain.CourseTypeSU,
		Frequency:       domain.FrequencyOnAnnouncement,
		Active:          true,
		Components: []domain.ModuleComponent{
			{Kind: domain.PartKindLecture, TeachingHours: 4, Position: 0},
		},
	}, uuid.Nil)
	if err != nil {
		t.Fatalf("cannot record the local course: %v", err)
	}

	switch {
	case created.Source != domain.ModuleSourceLocal:
		t.Errorf("the source is %q, want LOCAL", created.Source)
	case created.Kind != domain.ModuleKindFwpPlaceholder:
		t.Errorf("the kind is %q, want FWP_PLACEHOLDER", created.Kind)
	case created.ZpaID != nil:
		t.Errorf("the local course carries the ZPA reference %d", *created.ZpaID)
	case len(created.Components) != 1:
		t.Errorf("the split holds %d parts, want the one it was given",
			len(created.Components))
	}

	// A stated split makes it plannable, which is the whole point: an instance is built from it.
	if created.SplitIsEstimated() {
		t.Error("the split it was given is being reported as an estimate")
	}

	listed, err := f.modules.Modules(ctx, domain.ModuleFilter{
		Programme: storetest.FixtureProgrammeA,
		Frequency: domain.AllFrequencies(),
	})
	if err != nil {
		t.Fatalf("cannot list the catalogue: %v", err)
	}
	found := false
	for _, m := range listed {
		if m.ID == created.ID {
			found = true
		}
	}
	if !found {
		t.Error("the local course is not in its home programme's catalogue")
	}
}

// Two clicks on "anlegen" are one row, and the second one says so rather than raising a
// constraint violation at the caller.
func TestASecondLocalModuleOfTheSameNameIsRefusedByName(t *testing.T) {
	t.Parallel()

	f := newModuleFixture(t)
	ctx := t.Context()

	spec := domain.NewLocalModule{
		HomeProgrammeID: f.programme,
		Name:            "FWP-Platzhalter (technisch)",
		Kind:            domain.ModuleKindFwpPlaceholder,
		CourseType:      domain.CourseTypeSU,
		Frequency:       domain.FrequencyOnAnnouncement,
		Active:          true,
	}
	if _, err := f.modules.CreateLocalModule(ctx, spec, uuid.Nil); err != nil {
		t.Fatalf("cannot record the local course: %v", err)
	}
	if _, err := f.modules.CreateLocalModule(ctx, spec, uuid.Nil); !errors.Is(err,
		domain.ErrLocalModuleNameTaken) {
		t.Errorf("the second course of the same name gave %v, want ErrLocalModuleNameTaken", err)
	}
}

// Deactivating is how a local course is retired — there is no delete, because instances and
// later wishes point at it — and it drops out of the lists a planner sees.
func TestADeactivatedLocalModuleLeavesTheCatalogue(t *testing.T) {
	t.Parallel()

	f := newModuleFixture(t)
	ctx := t.Context()

	spec := domain.NewLocalModule{
		HomeProgrammeID: f.programme,
		Name:            "Eigene Lehrveranstaltung",
		Kind:            domain.ModuleKindModule,
		CourseType:      domain.CourseTypeSU,
		Frequency:       domain.FrequencyOnAnnouncement,
		Active:          true,
	}
	created, err := f.modules.CreateLocalModule(ctx, spec, uuid.Nil)
	if err != nil {
		t.Fatalf("cannot record the local course: %v", err)
	}

	spec.Active = false
	changed, err := f.modules.UpdateLocalModule(ctx, created.ID, spec, uuid.Nil)
	if err != nil {
		t.Fatalf("cannot deactivate the local course: %v", err)
	}
	if changed.Active {
		t.Error("the course is still active")
	}
	if changed.RetiredAt != nil {
		t.Error("deactivating set retired_at, which is the import's word and not this one")
	}

	listed, err := f.modules.Modules(ctx, domain.ModuleFilter{
		Programme: storetest.FixtureProgrammeA,
		Frequency: domain.AllFrequencies(),
	})
	if err != nil {
		t.Fatalf("cannot list the catalogue: %v", err)
	}
	for _, m := range listed {
		if m.ID == created.ID {
			t.Error("a deactivated course is still offered in the catalogue")
		}
	}
}

// An imported row is not this path's to edit, and answers the same as no row at all.
func TestUpdateLocalModuleNeverTouchesAnImportedOne(t *testing.T) {
	t.Parallel()

	f := newModuleFixture(t)

	if _, err := f.modules.UpdateLocalModule(t.Context(), f.module, domain.NewLocalModule{
		HomeProgrammeID: f.programme,
		Name:            "Umbenannt",
		Kind:            domain.ModuleKindModule,
		CourseType:      domain.CourseTypeSU,
		Frequency:       domain.FrequencyOnAnnouncement,
		Active:          true,
	}, uuid.Nil); !errors.Is(err, domain.ErrModuleNotFound) {
		t.Errorf("editing an imported module through the local path gave %v, want "+
			"ErrModuleNotFound", err)
	}
}

// The picker's list leaves out what the faculty does not plan; the catalogue still holds it.
func TestProgrammesLeaveOutWhatTheFacultyDoesNotPlan(t *testing.T) {
	t.Parallel()

	f := newModuleFixture(t)
	ctx := t.Context()

	// A new programme is planned — appearing in a picker grants nothing, and one that is
	// silently missing is a support question whose answer is invisible from the screen.
	planned, err := f.modules.Programmes(ctx, false)
	if err != nil {
		t.Fatalf("cannot list the programmes: %v", err)
	}
	for _, p := range planned {
		if p.PlanningStatus != domain.ProgrammePlanned {
			t.Errorf("%s comes out of a fresh projection as %q, want PLANNED",
				p.Code, p.PlanningStatus)
		}
	}

	changed, err := f.modules.SetProgrammePlanningStatus(ctx, storetest.FixtureProgrammeZ,
		domain.ProgrammeNotOurs, uuid.Nil)
	if err != nil {
		t.Fatalf("cannot record the planning status: %v", err)
	}
	if changed.PlanningStatus != domain.ProgrammeNotOurs {
		t.Errorf("the status reads %q, want NOT_OURS", changed.PlanningStatus)
	}

	planned, err = f.modules.Programmes(ctx, false)
	if err != nil {
		t.Fatalf("cannot list the programmes: %v", err)
	}
	for _, p := range planned {
		if p.Code == storetest.FixtureProgrammeZ {
			t.Error("a programme the faculty does not plan is still in the picker's list")
		}
	}

	all, err := f.modules.Programmes(ctx, true)
	if err != nil {
		t.Fatalf("cannot list every programme: %v", err)
	}
	found := false
	for _, p := range all {
		if p.Code == storetest.FixtureProgrammeZ {
			found = true
		}
	}
	if !found {
		t.Error("includeUnplanned left it out anyway — the catalogue still holds it")
	}

	// And by code it answers whatever its status: reading the demand of a programme that has run
	// out is the record of what the faculty did.
	one, err := f.modules.ProgrammeByCode(ctx, storetest.FixtureProgrammeZ)
	if err != nil {
		t.Fatalf("cannot read the programme: %v", err)
	}
	if one == nil {
		t.Fatal("a programme the faculty does not plan cannot be read by code at all")
	}

	// A code that names nothing is (nil, nil), not an error.
	missing, err := f.modules.SetProgrammePlanningStatus(ctx, "GIBTESNICHT",
		domain.ProgrammePlanned, uuid.Nil)
	if err != nil || missing != nil {
		t.Errorf("an unknown code gave (%v, %v), want (nil, nil)", missing, err)
	}
}

// The degree is recorded, read back through the lookup, and taken back — and who did it stays on
// the row even after taking it back.
func TestADegreeIsRecordedAndTakenBack(t *testing.T) {
	t.Parallel()

	f := newModuleFixture(t)
	ctx := t.Context()

	none, err := f.modules.ProgrammeDegrees(ctx)
	if err != nil {
		t.Fatalf("cannot read the degrees: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("a fresh projection has degrees %v, want none: nothing may guess them", none)
	}

	master := domain.DegreeMaster
	changed, err := f.modules.SetProgrammeDegree(ctx, storetest.FixtureProgrammeB, &master,
		uuid.Nil)
	if err != nil {
		t.Fatalf("cannot record the degree: %v", err)
	}
	if changed == nil || changed.Code != storetest.FixtureProgrammeB {
		t.Fatalf("recording answered %+v, want programme %s", changed, storetest.FixtureProgrammeB)
	}

	degrees, err := f.modules.ProgrammeDegrees(ctx)
	if err != nil {
		t.Fatalf("cannot read the degrees: %v", err)
	}
	if len(degrees) != 1 || degrees[storetest.FixtureProgrammeB] != domain.DegreeMaster {
		t.Errorf("the degrees read %v, want only %s = MASTER", degrees, storetest.FixtureProgrammeB)
	}

	if _, err := f.modules.SetProgrammeDegree(ctx, storetest.FixtureProgrammeB, nil,
		uuid.Nil); err != nil {
		t.Fatalf("cannot take the degree back: %v", err)
	}
	degrees, err = f.modules.ProgrammeDegrees(ctx)
	if err != nil {
		t.Fatalf("cannot read the degrees: %v", err)
	}
	if len(degrees) != 0 {
		t.Errorf("after taking it back the degrees read %v, want none", degrees)
	}

	missing, err := f.modules.SetProgrammeDegree(ctx, "GIBTESNICHT", &master, uuid.Nil)
	if err != nil || missing != nil {
		t.Errorf("an unknown code gave (%v, %v), want (nil, nil)", missing, err)
	}
}

// The catalogue's degree filter has the programme filter's two halves, over every programme of
// the degree — and a programme nobody recorded a degree for matches neither.
func TestTheCatalogueFiltersByDegree(t *testing.T) {
	t.Parallel()

	f := newModuleFixture(t)
	ctx := t.Context()
	bachelor, master := domain.DegreeBachelor, domain.DegreeMaster

	refs := func(degree *domain.Degree) map[int64]bool {
		t.Helper()
		modules, err := f.modules.Modules(ctx, domain.ModuleFilter{Degree: degree})
		if err != nil {
			t.Fatalf("cannot list the catalogue: %v", err)
		}
		out := map[int64]bool{}
		for _, m := range modules {
			if m.ZpaID != nil {
				out[*m.ZpaID] = true
			}
		}
		return out
	}

	// Before anybody records anything, a filter by degree finds nothing — not everything.
	if got := refs(&master); len(got) != 0 {
		t.Errorf("with no degree recorded, MASTER found %v, want nothing", got)
	}

	for code, degree := range map[string]*domain.Degree{
		storetest.FixtureProgrammeA: &bachelor,
		storetest.FixtureProgrammeB: &master,
	} {
		if _, err := f.modules.SetProgrammeDegree(ctx, code, degree, uuid.Nil); err != nil {
			t.Fatalf("cannot record the degree of %s: %v", code, err)
		}
	}

	masters := refs(&master)
	// 101 is at home in PA and counts in PB: the offering half finds it.
	if len(masters) != 1 || !masters[storetest.FixtureModuleOrdinary] {
		t.Errorf("MASTER found %v, want only %d, which counts in PB", masters,
			storetest.FixtureModuleOrdinary)
	}

	bachelors := refs(&bachelor)
	for _, want := range []int64{storetest.FixtureModuleOrdinary, storetest.FixtureModuleDutyDiffers} {
		if !bachelors[want] {
			t.Errorf("BACHELOR is missing %d, which is at home in PA", want)
		}
	}
	// 107 is PZ's, and nobody said what PZ leads to.
	if bachelors[storetest.FixtureModuleOfProgrammeZ] || masters[storetest.FixtureModuleOfProgrammeZ] {
		t.Errorf("module %d of a programme without a degree matched a degree",
			storetest.FixtureModuleOfProgrammeZ)
	}

	if all := refs(nil); len(all) <= len(bachelors) {
		t.Errorf("without a degree the catalogue holds %d modules, no more than BACHELOR's %d",
			len(all), len(bachelors))
	}
}
