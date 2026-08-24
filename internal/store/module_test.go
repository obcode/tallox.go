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
