package domain_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
	"github.com/obcode/tallox.go/internal/testdata"
)

// The demand service, against fakes.
//
// What is tested here is the part that is not a statement about rows: which questions are asked
// in which order, and what a badly shaped request is answered with. The rules that live in SQL —
// the parts made from the split, the identity, the refusal to withdraw something in use — are
// tested against a real database in internal/store, because a fake passes them while the shipped
// statements do something else.

var (
	demandProgramme = uuid.MustParse("33333333-3333-4333-8333-333333333333")
	otherProgramme  = uuid.MustParse("44444444-4444-4444-8444-444444444444")
	// unplannedProgramme has run out: it is in the catalogue and this faculty does not plan it.
	unplannedProgramme = uuid.MustParse("55555555-5555-4555-8555-555555555555")
)

// fakeDemandStore records what it was asked to do and answers plausibly.
type fakeDemandStore struct {
	instances       map[uuid.UUID]domain.CourseInstance
	partOwner       map[uuid.UUID]uuid.UUID
	created         []domain.NewCourseInstance
	planned         []domain.DemandEntry
	plannedSemester string
	writes          int
	// loads is what TeachingLoad answers, quotas what the quota methods keep.
	loads []domain.TeachingLoad
	// loadsOf overrides loads for one semester, for the study-year cases.
	loadsOf    map[string][]domain.TeachingLoad
	quotas     []domain.ProgrammeQuota
	quotaYears []int
	// taughtBy is the last faculty SetInstancePartTaughtBy was handed.
	taughtBy string
}

func newFakeDemandStore() *fakeDemandStore {
	return &fakeDemandStore{
		instances: map[uuid.UUID]domain.CourseInstance{},
		partOwner: map[uuid.UUID]uuid.UUID{},
	}
}

func (f *fakeDemandStore) add(programmeID uuid.UUID, phase policy.Phase, parts int) domain.CourseInstance {
	instance := domain.CourseInstance{
		ID:            uuid.New(),
		SemesterCode:  "2027-SS",
		SemesterPhase: phase,
		Programme:     domain.Programme{ID: programmeID, Code: "PA"},
	}
	for i := 0; i < parts; i++ {
		part := domain.InstancePart{ID: uuid.New(), Kind: domain.PartKindLab, Position: i}
		instance.Parts = append(instance.Parts, part)
		f.partOwner[part.ID] = instance.ID
	}
	f.instances[instance.ID] = instance
	return instance
}

func (f *fakeDemandStore) CourseInstances(context.Context, domain.DemandFilter) ([]domain.CourseInstance, error) {
	out := make([]domain.CourseInstance, 0, len(f.instances))
	for _, i := range f.instances {
		out = append(out, i)
	}
	return out, nil
}

func (f *fakeDemandStore) CourseInstanceByID(_ context.Context, id uuid.UUID) (*domain.CourseInstance, error) {
	instance, ok := f.instances[id]
	if !ok {
		return nil, nil
	}
	return &instance, nil
}

func (f *fakeDemandStore) CourseInstanceByPartID(ctx context.Context, partID uuid.UUID) (*domain.CourseInstance, error) {
	id, ok := f.partOwner[partID]
	if !ok {
		return nil, nil
	}
	return f.CourseInstanceByID(ctx, id)
}

func (f *fakeDemandStore) CreateCourseInstance(_ context.Context, spec domain.NewCourseInstance) (*domain.CourseInstance, error) {
	f.writes++
	f.created = append(f.created, spec)
	instance := domain.CourseInstance{
		ID:                uuid.New(),
		Programme:         domain.Programme{ID: spec.ProgrammeID},
		Track:             spec.Track,
		ProgrammeSemester: spec.ProgrammeSemester,
	}
	f.instances[instance.ID] = instance
	return &instance, nil
}

func (f *fakeDemandStore) DuplicateCourseInstance(ctx context.Context, id uuid.UUID, track, _ string,
	_ uuid.UUID,
) (*domain.CourseInstance, error) {
	f.writes++
	instance, _ := f.CourseInstanceByID(ctx, id)
	instance.Track = track
	return instance, nil
}

func (f *fakeDemandStore) UpdateCourseInstance(ctx context.Context, id uuid.UUID, track string,
	programmeSemester *int,
) (*domain.CourseInstance, error) {
	f.writes++
	instance, _ := f.CourseInstanceByID(ctx, id)
	instance.Track = track
	instance.ProgrammeSemester = programmeSemester
	return instance, nil
}

func (f *fakeDemandStore) SetCourseInstanceFormat(ctx context.Context, id uuid.UUID,
	delivery domain.Delivery, englishOverride *bool,
) (*domain.CourseInstance, error) {
	f.writes++
	instance, _ := f.CourseInstanceByID(ctx, id)
	instance.Delivery = delivery
	instance.EnglishOverride = englishOverride
	return instance, nil
}

func (f *fakeDemandStore) DeleteCourseInstance(_ context.Context, id uuid.UUID) error {
	f.writes++
	delete(f.instances, id)
	return nil
}

func (f *fakeDemandStore) AddInstancePart(ctx context.Context, instanceID uuid.UUID,
	_ domain.InstancePartKind, _ *float64,
) (*domain.CourseInstance, error) {
	f.writes++
	return f.CourseInstanceByID(ctx, instanceID)
}

func (f *fakeDemandStore) UpdateInstancePart(ctx context.Context, partID uuid.UUID,
	_ domain.InstancePartKind, _ *float64,
) (*domain.CourseInstance, error) {
	f.writes++
	return f.CourseInstanceByPartID(ctx, partID)
}

func (f *fakeDemandStore) DeleteInstancePart(ctx context.Context, partID uuid.UUID) (*domain.CourseInstance, error) {
	f.writes++
	return f.CourseInstanceByPartID(ctx, partID)
}

func (f *fakeDemandStore) DivideInstancePart(ctx context.Context, partID uuid.UUID, _ float64) (*domain.CourseInstance, error) {
	f.writes++
	return f.CourseInstanceByPartID(ctx, partID)
}

func (f *fakeDemandStore) RejoinInstancePart(ctx context.Context, partID uuid.UUID) (*domain.CourseInstance, error) {
	f.writes++
	return f.CourseInstanceByPartID(ctx, partID)
}

func (f *fakeDemandStore) SetInstancePartTaughtBy(ctx context.Context, partID uuid.UUID, faculty string) (*domain.CourseInstance, error) {
	f.writes++
	f.taughtBy = faculty
	return f.CourseInstanceByPartID(ctx, partID)
}

func (f *fakeDemandStore) ShareInstancePartAcrossTracks(ctx context.Context, partID uuid.UUID) (*domain.CourseInstance, error) {
	f.writes++
	return f.CourseInstanceByPartID(ctx, partID)
}

func (f *fakeDemandStore) SplitInstancePartAcrossTracks(ctx context.Context, partID uuid.UUID) (*domain.CourseInstance, error) {
	f.writes++
	return f.CourseInstanceByPartID(ctx, partID)
}

// The coverage half. What is tested against this fake is only ever the permission — which side of
// the handshake a caller may write — because everything else about coverage is a statement about
// rows and is tested against a real database in internal/store.

func (f *fakeDemandStore) RequestInstanceCoverage(ctx context.Context, guestID, hostID, by uuid.UUID) (*domain.CourseInstance, error) {
	f.writes++
	return f.CourseInstanceByID(ctx, guestID)
}

func (f *fakeDemandStore) AcceptInstanceCoverage(ctx context.Context, guestID, by uuid.UUID) (*domain.CourseInstance, error) {
	f.writes++
	return f.CourseInstanceByID(ctx, guestID)
}

func (f *fakeDemandStore) ReleaseInstanceCoverage(ctx context.Context, guestID uuid.UUID) (*domain.CourseInstance, error) {
	f.writes++
	return f.CourseInstanceByID(ctx, guestID)
}

func (f *fakeDemandStore) HostCandidates(ctx context.Context, guestID uuid.UUID) ([]domain.CourseInstance, error) {
	return nil, nil
}

// cover links two of the fake's instances, so a permission test has a handshake to act on.
func (f *fakeDemandStore) cover(guestID, hostID uuid.UUID, accepted bool) {
	guest := f.instances[guestID]
	host := f.instances[hostID]
	coverage := &domain.InstanceCoverage{Instance: host, RequestedAt: time.Now()}
	if accepted {
		at := time.Now()
		coverage.AcceptedAt = &at
	}
	guest.CoveredBy = coverage
	f.instances[guestID] = guest
}

func (f *fakeDemandStore) CopyDemand(context.Context, domain.Semester, domain.Semester,
	uuid.UUID, uuid.UUID,
) (domain.CopyCounts, error) {
	f.writes++
	return domain.CopyCounts{Created: 2, PartsCreated: 5}, nil
}

func (f *fakeDemandStore) PlanDemand(_ context.Context, semester string, _ uuid.UUID,
	entries []domain.DemandEntry, _ uuid.UUID, dryRun bool,
) (domain.DemandPlan, error) {
	f.planned = entries
	f.plannedSemester = semester
	if !dryRun {
		f.writes++
	}
	return domain.DemandPlan{DryRun: dryRun}, nil
}

func (f *fakeDemandStore) TeachingLoad(_ context.Context, semester string) ([]domain.TeachingLoad, error) {
	if l, ok := f.loadsOf[semester]; ok {
		return append([]domain.TeachingLoad{}, l...), nil
	}
	// The first semester asked about: a summer's load asks about its winter afterwards.
	if f.plannedSemester == "" {
		f.plannedSemester = semester
	}
	return append([]domain.TeachingLoad{}, f.loads...), nil
}

// ApplyModuleSplit is exercised against PostgreSQL (store.TestApplyModuleSplit…); the service's
// own part is the permission, which bootstrap tests through both doors.
func (f *fakeDemandStore) ApplyModuleSplit(_ context.Context, _ uuid.UUID,
	_ func(domain.SplitInstanceScope) bool, dryRun bool,
) (domain.SplitApplication, error) {
	return domain.SplitApplication{DryRun: dryRun}, nil
}

func (f *fakeDemandStore) ProgrammeQuotas(_ context.Context, studyYear int) ([]domain.ProgrammeQuota, error) {
	f.quotaYears = append(f.quotaYears, studyYear)
	out := []domain.ProgrammeQuota{}
	for _, q := range f.quotas {
		if q.StudyYear == studyYear {
			out = append(out, q)
		}
	}
	return out, nil
}

// SetProgrammeQuota knows the two planned fixture programmes and nothing else, the way the real
// statement inserts nothing for a code that names no programme.
func (f *fakeDemandStore) SetProgrammeQuota(_ context.Context, programme string, studyYear int,
	hours float64, by uuid.UUID,
) (*domain.ProgrammeQuota, error) {
	ids := map[string]uuid.UUID{"PA": demandProgramme, "PB": otherProgramme}
	id, ok := ids[programme]
	if !ok {
		return nil, nil
	}
	f.writes++
	_ = f.ClearProgrammeQuota(context.Background(), programme, studyYear)
	quota := domain.ProgrammeQuota{
		Programme: domain.Programme{ID: id, Code: programme}, StudyYear: studyYear,
		TeachingHours: hours, SetBy: by,
	}
	f.quotas = append(f.quotas, quota)
	return &quota, nil
}

func (f *fakeDemandStore) ClearProgrammeQuota(_ context.Context, programme string, studyYear int) error {
	kept := f.quotas[:0]
	for _, q := range f.quotas {
		if q.Programme.Code != programme || q.StudyYear != studyYear {
			kept = append(kept, q)
		}
	}
	f.quotas = kept
	return nil
}

// fakeSemesterStore is the semester half, and it records what was ensured.
//
// That last part is the point of it: a refused declaration must not leave a semester row behind,
// and the row is created by the very act the refusal is about.
type fakeSemesterStore struct {
	rows    map[string]domain.Semester
	ensured []string
	// planning is the code the mark sits on, or empty while nobody has said. A field rather
	// than a flag on the rows, because at most one row carries it and a map of booleans can
	// hold a state the database cannot.
	planning string
}

func newFakeSemesterStore(phase policy.Phase) *fakeSemesterStore {
	return &fakeSemesterStore{rows: map[string]domain.Semester{
		"2027-SS": {ID: uuid.New(), Code: "2027-SS", Phase: phase},
	}}
}

func (f *fakeSemesterStore) EnsureSemester(_ context.Context, code string) (domain.Semester, error) {
	f.ensured = append(f.ensured, code)
	if row, ok := f.rows[code]; ok {
		return row, nil
	}
	row := domain.Semester{ID: uuid.New(), Code: code, Phase: policy.PhaseDemandPlanning}
	f.rows[code] = row
	return row, nil
}

func (f *fakeSemesterStore) SemesterByCode(_ context.Context, code string) (domain.Semester, error) {
	return f.rows[code], nil
}

func (f *fakeSemesterStore) Semesters(context.Context) ([]domain.Semester, error) {
	out := make([]domain.Semester, 0, len(f.rows))
	for _, row := range f.rows {
		out = append(out, row)
	}
	return out, nil
}

func (f *fakeSemesterStore) AdvanceSemesterPhase(context.Context, uuid.UUID, policy.Phase,
	policy.Phase,
) (domain.Semester, error) {
	return domain.Semester{}, errors.New("not part of this test")
}

func (f *fakeSemesterStore) PublishSemesterWishes(context.Context, uuid.UUID) (domain.Semester, error) {
	return domain.Semester{}, errors.New("not part of this test")
}

func (f *fakeSemesterStore) PublishSemesterAssignments(context.Context, uuid.UUID) (domain.Semester, error) {
	return domain.Semester{}, errors.New("not part of this test")
}

func (f *fakeSemesterStore) PlanningSemester(context.Context) (domain.Semester, error) {
	if f.planning == "" {
		return domain.Semester{}, nil
	}
	row := f.rows[f.planning]
	row.IsPlanning = true
	return row, nil
}

func (f *fakeSemesterStore) SetPlanningSemester(_ context.Context, id, _ uuid.UUID,
) (domain.Semester, error) {
	for code, row := range f.rows {
		if row.ID != id {
			continue
		}
		f.planning = code
		row.IsPlanning = true
		f.rows[code] = row
		return row, nil
	}
	return domain.Semester{}, errors.New("no such semester")
}

// fakeCatalogueReader answers the one question the demand asks of the catalogue.
type fakeCatalogueReader struct{}

func (fakeCatalogueReader) Programmes(context.Context, bool) ([]domain.Programme, error) {
	return nil, nil
}

func (fakeCatalogueReader) ProgrammesByID(context.Context, []uuid.UUID) ([]domain.Programme, error) {
	return nil, nil
}

// PC is the programme this faculty does not plan — every write against it is refused whoever
// holds what, which is the half of the rule that is not a permission.
func (fakeCatalogueReader) ProgrammeByCode(_ context.Context, code string) (*domain.Programme, error) {
	switch code {
	case "PA":
		return &domain.Programme{
			ID: demandProgramme, Code: "PA", PlanningStatus: domain.ProgrammePlanned,
		}, nil
	case "PB":
		return &domain.Programme{
			ID: otherProgramme, Code: "PB", PlanningStatus: domain.ProgrammePlanned,
		}, nil
	case "PC":
		return &domain.Programme{
			ID: unplannedProgramme, Code: "PC", PlanningStatus: domain.ProgrammeDiscontinued,
		}, nil
	default:
		return nil, nil
	}
}

func (fakeCatalogueReader) ProgrammeLeads(context.Context, []string) (map[string][]domain.Person, error) {
	return nil, nil
}

func (fakeCatalogueReader) Modules(context.Context, domain.ModuleFilter) ([]domain.Module, error) {
	return nil, nil
}

func (fakeCatalogueReader) Teachers(context.Context, domain.TeacherFilter) ([]domain.Teacher, error) {
	return nil, nil
}

func (fakeCatalogueReader) ModuleByID(context.Context, uuid.UUID) (*domain.Module, error) {
	return nil, nil
}

func (fakeCatalogueReader) SetProgrammePlanningStatus(context.Context, string,
	domain.ProgrammeStatus, uuid.UUID,
) (*domain.Programme, error) {
	return nil, errors.New("not part of this test")
}

func (fakeCatalogueReader) SetModuleComponents(context.Context, uuid.UUID,
	[]domain.ModuleComponent, uuid.UUID,
) (*domain.Module, error) {
	return nil, nil
}

func (fakeCatalogueReader) SetModuleSemesterSpan(context.Context, uuid.UUID, int) (*domain.Module, error) {
	return nil, nil
}

func (fakeCatalogueReader) SetModuleProjectBased(context.Context, uuid.UUID, bool) (*domain.Module, error) {
	return nil, nil
}

func (fakeCatalogueReader) CreateLocalModule(context.Context, domain.NewLocalModule, uuid.UUID,
) (*domain.Module, error) {
	return nil, errors.New("not part of this test")
}

func (fakeCatalogueReader) UpdateLocalModule(context.Context, uuid.UUID, domain.NewLocalModule,
	uuid.UUID,
) (*domain.Module, error) {
	return nil, errors.New("not part of this test")
}

// demandFixture wires the service to its fakes at a fixed moment, so that "is this semester
// plannable" does not depend on the fortnight the test runs in.
type demandFixture struct {
	service   *domain.DemandService
	store     *fakeDemandStore
	semesters *fakeSemesterStore
}

func newDemandService(t *testing.T, phase policy.Phase) demandFixture {
	t.Helper()

	store := newFakeDemandStore()
	semesters := newFakeSemesterStore(phase)
	at := func() time.Time { return time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC) }

	return demandFixture{
		service:   domain.NewDemandService(store, fakeCatalogueReader{}, domain.NewSemesterService(semesters, at)),
		store:     store,
		semesters: semesters,
	}
}

// lead is a programme lead scoped to the programme the fixtures are about.
func lead(kind principal.Kind, programmes ...uuid.UUID) principal.Actor {
	actor := testdata.Vier.Actor(kind, string(policy.RoleLecturer), string(policy.RoleProgrammeLead))
	for _, id := range programmes {
		actor.RoleScopes = append(actor.RoleScopes, principal.RoleScope{
			Role:        string(policy.RoleProgrammeLead),
			ProgrammeID: id,
		})
	}
	return actor
}

func declaration() domain.DeclareInstance {
	return domain.DeclareInstance{
		SemesterCode: "2027-SS",
		Programme:    "PA",
		ModuleID:     uuid.New(),
	}
}

// A refused declaration must not leave a semester row behind.
//
// The row is the record of a decision about a semester, and a refusal is the absence of one. It
// would also be visible: a semester with a row is a semester the list shows as touched, so the
// refusal would leave a trace that says somebody planned something.
func TestARefusedDeclarationRecordsNothing(t *testing.T) {
	t.Parallel()

	f := newDemandService(t, policy.PhaseDemandPlanning)

	_, err := f.service.Declare(t.Context(), lead(principal.KindInteractive, otherProgramme), declaration())
	if !errors.Is(err, domain.ErrNotYourProgramme) {
		t.Fatalf("declaring for somebody else's programme gave %v, want ErrNotYourProgramme", err)
	}
	if len(f.semesters.ensured) != 0 {
		t.Errorf("the refusal recorded the semester %v — a semester row is the record of a "+
			"decision, and refusing is not one", f.semesters.ensured)
	}
	if f.store.writes != 0 {
		t.Errorf("the refusal wrote %d time(s) to the demand", f.store.writes)
	}
}

// The decision from the faculty, at the level where it is enforced.
func TestDemandCanBeDeclaredUntilTheSemesterIsFinished(t *testing.T) {
	t.Parallel()

	for _, phase := range policy.AllPhases() {
		t.Run(string(phase), func(t *testing.T) {
			t.Parallel()

			f := newDemandService(t, phase)
			_, err := f.service.Declare(t.Context(),
				lead(principal.KindInteractive, demandProgramme), declaration())

			if phase == policy.PhaseFinal {
				// Closed since 2026-08-28, and it is the one cell in the whole matrix that is.
				// A finished semester is the record of what the faculty did; letting the demand
				// alone survive the close would make FINAL mean two things depending on which
				// screen somebody is looking at.
				if !errors.Is(err, domain.ErrPhaseClosed) {
					t.Errorf("declaring in %s gave %v, want ErrPhaseClosed", phase, err)
				}
				return
			}
			if err != nil {
				t.Errorf("declaring in %s gave %v — a late instance is a correction, and a "+
					"refused correction happens outside the tool instead", phase, err)
			}
		})
	}
}

func TestAPhaseThisBuildDoesNotKnowRefusesTheWrite(t *testing.T) {
	t.Parallel()

	f := newDemandService(t, policy.Phase("KLAUSURTAGUNG"))

	_, err := f.service.Declare(t.Context(), lead(principal.KindInteractive, demandProgramme), declaration())
	if !errors.Is(err, domain.ErrPhaseClosed) {
		t.Fatalf("declaring in an unknown phase gave %v, want ErrPhaseClosed", err)
	}
	if f.store.writes != 0 {
		t.Error("the refusal wrote to the demand anyway")
	}
}

// Through a token exactly as far as through a browser: the demand is neither confidential nor
// personnel data, and a colleague planning their own programme from a script is a use this API
// exists for.
func TestTheTokenDoorPlansAsFarAsTheBrowserDoor(t *testing.T) {
	t.Parallel()

	f := newDemandService(t, policy.PhaseDemandPlanning)

	if _, err := f.service.Declare(t.Context(),
		lead(principal.KindToken, demandProgramme), declaration()); err != nil {
		t.Errorf("declaring through a token gave %v", err)
	}
}

func TestWhatIsAcceptedAsACohortAndACohortYear(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name              string
		track             string
		programmeSemester *int
		wantTrack         string
		wantErr           error
	}{
		{name: "a lower-case letter is the same cohort to a person", track: "b", wantTrack: "B"},
		{name: "no cohort at all is the ordinary case", track: "", wantTrack: ""},
		{name: "surrounding space is trimmed", track: " A ", wantTrack: "A"},
		{name: "a word is not a cohort", track: "ABCD", wantErr: domain.ErrTrackInvalid},
		{name: "punctuation is not a cohort", track: "A-1", wantErr: domain.ErrTrackInvalid},
		{
			name:              "a thirteenth semester is not a cohort year",
			programmeSemester: ptr(13),
			wantErr:           domain.ErrProgrammeSemesterInvalid,
		},
		{name: "a third semester is", programmeSemester: ptr(3)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newDemandService(t, policy.PhaseDemandPlanning)
			spec := declaration()
			spec.Track = tc.track
			spec.ProgrammeSemester = tc.programmeSemester

			instance, err := f.service.Declare(t.Context(),
				lead(principal.KindInteractive, demandProgramme), spec)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("got %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected refusal: %v", err)
			}
			if instance.Track != tc.wantTrack {
				t.Errorf("the cohort was stored as %q, want %q", instance.Track, tc.wantTrack)
			}
		})
	}
}

func TestDuplicatingNeedsACohortToDuplicateInto(t *testing.T) {
	t.Parallel()

	f := newDemandService(t, policy.PhaseDemandPlanning)
	instance := f.store.add(demandProgramme, policy.PhaseDemandPlanning, 2)
	actor := lead(principal.KindInteractive, demandProgramme)

	if _, err := f.service.Duplicate(t.Context(), actor, instance.ID, "", ""); !errors.Is(err, domain.ErrTrackInvalid) {
		t.Fatalf("duplicating into no cohort gave %v, want ErrTrackInvalid — without a letter "+
			"the copy is its own source", err)
	}
	if _, err := f.service.Duplicate(t.Context(), actor, instance.ID, "B", "A"); err != nil {
		t.Errorf("duplicating into B gave %v", err)
	}
}

// Every part-level write is judged by the instance the part belongs to, which is how the
// programme scope reaches an id that carries no programme of its own.
func TestPartWritesAreJudgedByTheirInstance(t *testing.T) {
	t.Parallel()

	f := newDemandService(t, policy.PhaseDemandPlanning)
	instance := f.store.add(otherProgramme, policy.PhaseDemandPlanning, 1)
	actor := lead(principal.KindInteractive, demandProgramme)
	part := instance.Parts[0].ID

	hours := 2.0
	for _, call := range []struct {
		name string
		run  func() error
	}{
		{"add", func() error {
			_, err := f.service.AddPart(t.Context(), actor, instance.ID, domain.PartKindLab, &hours)
			return err
		}},
		{"change", func() error {
			_, err := f.service.ChangePart(t.Context(), actor, part, domain.PartKindLab, &hours)
			return err
		}},
		{"remove", func() error {
			_, err := f.service.RemovePart(t.Context(), actor, part)
			return err
		}},
		{"share", func() error {
			_, err := f.service.SharePartAcrossTracks(t.Context(), actor, part)
			return err
		}},
		{"split", func() error {
			_, err := f.service.SplitPartAcrossTracks(t.Context(), actor, part)
			return err
		}},
		{"withdraw", func() error { return f.service.Withdraw(t.Context(), actor, instance.ID) }},
	} {
		if err := call.run(); !errors.Is(err, domain.ErrNotYourProgramme) {
			t.Errorf("%s on another programme's instance gave %v, want ErrNotYourProgramme",
				call.name, err)
		}
	}
	if f.store.writes != 0 {
		t.Errorf("%d refused call(s) wrote anyway", f.store.writes)
	}
}

// Dividing a part and handing it to another faculty are judged by the assignment's rule, not the
// demand's: a programme lead of another programme is not responsible, and a finished semester
// takes nothing. The rule itself is asserted in internal/policy; this is the service asking it
// about the right instance and answering with the assignment's sentinels.
func TestArrangingAPartIsJudgedByTheAssignmentRule(t *testing.T) {
	t.Parallel()

	calls := func(f demandFixture, actor principal.Actor, part uuid.UUID) map[string]func() error {
		return map[string]func() error{
			"divide": func() error {
				_, err := f.service.DividePart(t.Context(), actor, part, 1)
				return err
			},
			"rejoin": func() error {
				_, err := f.service.RejoinPart(t.Context(), actor, part)
				return err
			},
			"taughtBy": func() error {
				_, err := f.service.SetPartTaughtBy(t.Context(), actor, part, "FK 04")
				return err
			},
		}
	}

	t.Run("another programme", func(t *testing.T) {
		t.Parallel()
		f := newDemandService(t, policy.PhaseAssignment)
		instance := f.store.add(otherProgramme, policy.PhaseAssignment, 1)
		for name, run := range calls(f, lead(principal.KindInteractive, demandProgramme), instance.Parts[0].ID) {
			if err := run(); !errors.Is(err, domain.ErrNotYourSubject) {
				t.Errorf("%s gave %v, want ErrNotYourSubject", name, err)
			}
		}
		if f.store.writes != 0 {
			t.Errorf("%d refused call(s) wrote anyway", f.store.writes)
		}
	})

	t.Run("a finished semester", func(t *testing.T) {
		t.Parallel()
		f := newDemandService(t, policy.PhaseFinal)
		instance := f.store.add(demandProgramme, policy.PhaseFinal, 1)
		for name, run := range calls(f, lead(principal.KindInteractive, demandProgramme), instance.Parts[0].ID) {
			if err := run(); !errors.Is(err, domain.ErrAssignmentPhaseClosed) {
				t.Errorf("%s gave %v, want ErrAssignmentPhaseClosed", name, err)
			}
		}
		if f.store.writes != 0 {
			t.Errorf("%d refused call(s) wrote anyway", f.store.writes)
		}
	})

	t.Run("the own programme, during the review", func(t *testing.T) {
		t.Parallel()
		f := newDemandService(t, policy.PhaseReview)
		instance := f.store.add(demandProgramme, policy.PhaseReview, 1)
		for name, run := range calls(f, lead(principal.KindInteractive, demandProgramme), instance.Parts[0].ID) {
			if err := run(); err != nil {
				t.Errorf("%s gave %v, want it written", name, err)
			}
		}
	})

	t.Run("a part that does not exist", func(t *testing.T) {
		t.Parallel()
		f := newDemandService(t, policy.PhaseAssignment)
		for name, run := range calls(f, lead(principal.KindInteractive, demandProgramme), uuid.New()) {
			if err := run(); !errors.Is(err, domain.ErrPartNotFound) {
				t.Errorf("%s gave %v, want ErrPartNotFound", name, err)
			}
		}
	})
}

func TestWhatIsAcceptedWhenArrangingAPart(t *testing.T) {
	t.Parallel()

	f := newDemandService(t, policy.PhaseAssignment)
	instance := f.store.add(demandProgramme, policy.PhaseAssignment, 1)
	actor := lead(principal.KindInteractive, demandProgramme)
	part := instance.Parts[0].ID

	for _, hours := range []float64{0, -1, 21} {
		if _, err := f.service.DividePart(t.Context(), actor, part, hours); !errors.Is(err, domain.ErrPartInvalid) {
			t.Errorf("dividing off %v hours gave %v, want ErrPartInvalid", hours, err)
		}
	}

	if _, err := f.service.SetPartTaughtBy(t.Context(), actor, part, strings.Repeat("x", 81)); !errors.Is(err, domain.ErrTaughtByTooLong) {
		t.Errorf("81 characters gave %v, want ErrTaughtByTooLong", err)
	}
	// Counted in characters, like the note: a faculty's name with umlauts is not longer for it.
	if _, err := f.service.SetPartTaughtBy(t.Context(), actor, part, strings.Repeat("ü", 80)); err != nil {
		t.Errorf("80 characters with umlauts gave %v", err)
	}
	if _, err := f.service.SetPartTaughtBy(t.Context(), actor, part, "  FK 04 \n"); err != nil {
		t.Fatal(err)
	}
	if f.store.taughtBy != "FK 04" {
		t.Errorf("the store was handed %q, want it trimmed to %q", f.store.taughtBy, "FK 04")
	}

	full := f.store.add(demandProgramme, policy.PhaseAssignment, domain.MaxPartsPerInstance)
	if _, err := f.service.DividePart(t.Context(), actor, full.Parts[0].ID, 1); !errors.Is(err, domain.ErrTooManyParts) {
		t.Errorf("dividing a part of a full instance gave %v, want ErrTooManyParts", err)
	}
}

func TestWhatIsAcceptedAsAPart(t *testing.T) {
	t.Parallel()

	f := newDemandService(t, policy.PhaseDemandPlanning)
	instance := f.store.add(demandProgramme, policy.PhaseDemandPlanning, 1)
	actor := lead(principal.KindInteractive, demandProgramme)

	cases := []struct {
		name    string
		kind    domain.InstancePartKind
		hours   *float64
		wantErr error
	}{
		{name: "hours nobody has stated yet", kind: domain.PartKindLab, hours: nil},
		{name: "an ordinary laboratory", kind: domain.PartKindLab, hours: ptrf(2)},
		{name: "no hours at all", kind: domain.PartKindLab, hours: ptrf(0), wantErr: domain.ErrPartInvalid},
		{name: "negative hours", kind: domain.PartKindLab, hours: ptrf(-2), wantErr: domain.ErrPartInvalid},
		{name: "a working week of them", kind: domain.PartKindLab, hours: ptrf(40), wantErr: domain.ErrPartInvalid},
		{name: "a kind this build does not know", kind: "TUTORIUM", hours: ptrf(2), wantErr: domain.ErrPartInvalid},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.service.AddPart(t.Context(), actor, instance.ID, tc.kind, tc.hours)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("got %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestAnInstanceCannotGrowWithoutBound(t *testing.T) {
	t.Parallel()

	f := newDemandService(t, policy.PhaseDemandPlanning)
	instance := f.store.add(demandProgramme, policy.PhaseDemandPlanning, domain.MaxPartsPerInstance)

	hours := 2.0
	_, err := f.service.AddPart(t.Context(), lead(principal.KindInteractive, demandProgramme),
		instance.ID, domain.PartKindLab, &hours)
	if !errors.Is(err, domain.ErrTooManyParts) {
		t.Fatalf("adding the %dth part gave %v, want ErrTooManyParts",
			domain.MaxPartsPerInstance+1, err)
	}
}

func TestCopyingIsJudgedByTheTargetSemester(t *testing.T) {
	t.Parallel()

	f := newDemandService(t, policy.PhaseDemandPlanning)
	ctx := t.Context()

	if _, err := f.service.CopyFrom(ctx, lead(principal.KindInteractive, otherProgramme),
		"2026-WS", "2027-SS", "PA"); !errors.Is(err, domain.ErrNotYourProgramme) {
		t.Errorf("copying another programme's demand gave %v, want ErrNotYourProgramme", err)
	}

	if _, err := f.service.CopyFrom(ctx, lead(principal.KindInteractive, demandProgramme),
		"2027-SS", "2027-SS", "PA"); !errors.Is(err, domain.ErrSameSemester) {
		t.Errorf("copying a semester into itself gave %v, want ErrSameSemester", err)
	}

	report, err := f.service.CopyFrom(ctx, lead(principal.KindInteractive, demandProgramme),
		"2026-WS", "2027-SS", "PA")
	if err != nil {
		t.Fatalf("copying gave %v", err)
	}
	if report.From != "2026-WS" || report.To != "2027-SS" || report.Programme.Code != "PA" {
		t.Errorf("the report describes %+v, want the copy that was asked for", report)
	}
}

// Reading is open to anybody with an account, the same as the catalogue and the semester list:
// the demand is what the wish phase is about, and a lecturer who cannot see the instances has
// nothing to register interest in.
func TestReadingTheDemandNeedsNoRole(t *testing.T) {
	t.Parallel()

	f := newDemandService(t, policy.PhaseDemandPlanning)
	f.store.add(demandProgramme, policy.PhaseDemandPlanning, 1)

	lecturer := testdata.Eins.Actor(principal.KindInteractive, string(policy.RoleLecturer))
	instances, err := f.service.Instances(t.Context(), lecturer, domain.DemandFilter{SemesterCode: "2027-SS"})
	if err != nil {
		t.Fatalf("a lecturer cannot read the demand: %v", err)
	}
	if len(instances) != 1 {
		t.Errorf("a lecturer sees %d instances, want 1", len(instances))
	}

	if _, err := f.service.Instances(t.Context(), principal.Anonymous,
		domain.DemandFilter{SemesterCode: "2027-SS"}); err == nil {
		t.Error("somebody with no account read the demand")
	}
}

func ptr(n int) *int          { return &n }
func ptrs(s string) *string   { return &s }
func ptrf(f float64) *float64 { return &f }

// Planning names the semester and does not record it.
//
// The row for a semester nobody has touched is written inside the store's transaction, because
// that transaction is what a dry run rolls back — a preview that recorded the semester would
// leave the one trace it must not leave, and one that passed no semester at all wrote instances
// pointing at nothing. That is the bug this arrangement replaced; the property itself is asserted
// where it lives, in internal/store.
//
// What belongs here is the layering: this service hands the store a code and records nothing
// itself.
func TestPlanningLeavesTheSemesterRowToTheStore(t *testing.T) {
	t.Parallel()

	f := newDemandService(t, policy.PhaseDemandPlanning)
	actor := lead(principal.KindInteractive, demandProgramme)

	entries := []domain.DemandEntry{{
		ModuleID: uuid.New(),
		Tracks:   []domain.DemandTrack{{Track: "A", Groups: 2}},
	}}

	for _, dryRun := range []bool{true, false} {
		if _, err := f.service.PlanDemand(t.Context(), actor, "2029-WS", "PA", entries, dryRun); err != nil {
			t.Fatalf("planning (dryRun=%v) gave %v", dryRun, err)
		}
		if f.store.plannedSemester != "2029-WS" {
			t.Errorf("the store was asked to plan %q, want the semester the caller named",
				f.store.plannedSemester)
		}
	}

	if len(f.semesters.ensured) != 0 {
		t.Errorf("the service recorded the semester %v itself — the store does it, in the "+
			"transaction a dry run rolls back", f.semesters.ensured)
	}
}

// The shape of a plan, as a person could get it wrong with a stepper and a script could get it
// wrong in a loop.
func TestWhatIsAcceptedAsAPlan(t *testing.T) {
	t.Parallel()

	moduleID := uuid.New()

	cases := []struct {
		name    string
		entries []domain.DemandEntry
		wantErr error
		want    []domain.DemandTrack
	}{
		{
			name:    "a lower-case letter is the same cohort to a person",
			entries: []domain.DemandEntry{{ModuleID: moduleID, Tracks: []domain.DemandTrack{{Track: "b", Groups: 2}}}},
			want:    []domain.DemandTrack{{Track: "B", Groups: 2}},
		},
		{
			name:    "no cohorts at all is the row whose tick was taken away",
			entries: []domain.DemandEntry{{ModuleID: moduleID}},
			want:    nil,
		},
		{
			name:    "no groups is a cohort that runs only its lecture",
			entries: []domain.DemandEntry{{ModuleID: moduleID, Tracks: []domain.DemandTrack{{Track: "", Groups: 0}}}},
			want:    []domain.DemandTrack{{Track: "", Groups: 0}},
		},
		{
			name: "the same module twice",
			entries: []domain.DemandEntry{
				{ModuleID: moduleID, Tracks: []domain.DemandTrack{{Track: "A", Groups: 1}}},
				{ModuleID: moduleID, Tracks: []domain.DemandTrack{{Track: "B", Groups: 1}}},
			},
			wantErr: domain.ErrDuplicateEntry,
		},
		{
			name: "the same cohort twice, which would be two instances of one identity",
			entries: []domain.DemandEntry{{ModuleID: moduleID, Tracks: []domain.DemandTrack{
				{Track: "A", Groups: 1}, {Track: "a", Groups: 2},
			}}},
			wantErr: domain.ErrDuplicateEntry,
		},
		{
			name: "more cohorts than the alphabet offers",
			entries: []domain.DemandEntry{{
				ModuleID: moduleID,
				Tracks:   make([]domain.DemandTrack, domain.MaxTracksPerModule+1),
			}},
			wantErr: domain.ErrTooManyTracks,
		},
		{
			name: "more groups than anybody runs",
			entries: []domain.DemandEntry{{ModuleID: moduleID, Tracks: []domain.DemandTrack{
				{Track: "", Groups: domain.MaxGroupsPerTrack + 1},
			}}},
			wantErr: domain.ErrTooManyGroups,
		},
		{
			name: "a negative number of groups",
			entries: []domain.DemandEntry{{ModuleID: moduleID, Tracks: []domain.DemandTrack{
				{Track: "", Groups: -1},
			}}},
			wantErr: domain.ErrTooManyGroups,
		},
		{
			name: "a thirteenth semester",
			entries: []domain.DemandEntry{{
				ModuleID:          moduleID,
				Tracks:            []domain.DemandTrack{{Track: "", Groups: 1}},
				ProgrammeSemester: ptr(13),
			}},
			wantErr: domain.ErrProgrammeSemesterInvalid,
		},
		{
			name: "a note longer than a sentence beside a row can be",
			entries: []domain.DemandEntry{{
				ModuleID: moduleID,
				Tracks:   []domain.DemandTrack{{Track: "", Groups: 1}},
				Note:     ptrs(strings.Repeat("ä", domain.MaxNoteLength+1)),
			}},
			wantErr: domain.ErrNoteTooLong,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newDemandService(t, policy.PhaseDemandPlanning)
			_, err := f.service.PlanDemand(t.Context(),
				lead(principal.KindInteractive, demandProgramme), "2027-SS", "PA", tc.entries, false)

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("got %v, want %v", err, tc.wantErr)
				}
				if f.store.writes != 0 {
					t.Error("a refused plan was written anyway")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected refusal: %v", err)
			}
			if len(f.store.planned) != 1 {
				t.Fatalf("the store received %d entries, want 1", len(f.store.planned))
			}
			got := f.store.planned[0].Tracks
			if len(got) != len(tc.want) {
				t.Fatalf("the store received %+v, want %+v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("the store received %+v, want %+v", got, tc.want)
					break
				}
			}
		})
	}
}

// The note is trimmed before it is stored, and the bound counts characters rather than bytes:
// a field somebody cleared with a trailing space is a cleared field, and 2000 umlauts are 2000
// characters to the person who typed them. `nil` travels through untouched — it is the row that
// said nothing, and the store has to be able to tell that apart from a cleared note.
func TestANoteIsTrimmedAndCountedInCharacters(t *testing.T) {
	t.Parallel()

	moduleID := uuid.New()
	entry := func(note *string) []domain.DemandEntry {
		return []domain.DemandEntry{{
			ModuleID: moduleID,
			Tracks:   []domain.DemandTrack{{Track: "", Groups: 1}},
			Note:     note,
		}}
	}
	plan := func(t *testing.T, entries []domain.DemandEntry) *domain.DemandEntry {
		t.Helper()
		f := newDemandService(t, policy.PhaseDemandPlanning)
		if _, err := f.service.PlanDemand(t.Context(),
			lead(principal.KindInteractive, demandProgramme), "2027-SS", "PA", entries, false); err != nil {
			t.Fatalf("unexpected refusal: %v", err)
		}
		return &f.store.planned[0]
	}

	if got := plan(t, entry(ptrs("  vier Züge wegen SPO-Wechsel \n"))).Note; got == nil || *got != "vier Züge wegen SPO-Wechsel" {
		t.Errorf("the note reached the store as %q, want it trimmed", deref(got))
	}
	if got := plan(t, entry(ptrs("   "))).Note; got == nil || *got != "" {
		t.Errorf("a note of whitespace reached the store as %q, want the cleared note", deref(got))
	}
	if got := plan(t, entry(nil)).Note; got != nil {
		t.Errorf("a row that said nothing reached the store with a note %q", *got)
	}
	if got := plan(t, entry(ptrs(strings.Repeat("ä", domain.MaxNoteLength)))).Note; got == nil {
		t.Error("a note of exactly the bound, in two-byte characters, was refused")
	}
}

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

// Planning is a write like any other, and it is refused for somebody else's programme before
// anything is looked at.
func TestPlanningIsScopedToTheProgramme(t *testing.T) {
	t.Parallel()

	f := newDemandService(t, policy.PhaseDemandPlanning)

	_, err := f.service.PlanDemand(t.Context(), lead(principal.KindInteractive, otherProgramme),
		"2027-SS", "PA", []domain.DemandEntry{{ModuleID: uuid.New()}}, false)
	if !errors.Is(err, domain.ErrNotYourProgramme) {
		t.Fatalf("planning another programme gave %v, want ErrNotYourProgramme", err)
	}
	if f.store.writes != 0 || len(f.semesters.ensured) != 0 {
		t.Error("the refusal wrote something anyway")
	}
}

// The point of the two-sided handshake, at the level where it is decided.
//
// Two programme leads who each may write exactly one programme can between them express something
// neither could write alone. Neither needs the other's scope, and nobody needs a role that reaches
// both — which matters, because the role that reaches every programme is the dean's office, and
// the faculty does not want its leads holding it.
func TestEachSideOfTheCoverageHandshakeWritesItsOwnProgramme(t *testing.T) {
	t.Parallel()

	f := newDemandService(t, policy.PhaseDemandPlanning)
	ctx := t.Context()

	host := f.store.add(demandProgramme, policy.PhaseDemandPlanning, 2)
	guest := f.store.add(otherProgramme, policy.PhaseDemandPlanning, 2)

	guestLead := lead(principal.KindInteractive, otherProgramme)
	hostLead := lead(principal.KindInteractive, demandProgramme)

	// Asking is a statement about the asker's own declaration, so the asking lead may make it —
	// and may point at an instance of a programme they cannot write at all.
	if _, err := f.service.RequestCoverage(ctx, guestLead, guest.ID, host.ID); err != nil {
		t.Fatalf("the asking programme's lead could not ask: %v", err)
	}
	f.store.cover(guest.ID, host.ID, false)

	// The holding programme's lead may not ask on the other's behalf.
	if _, err := f.service.RequestCoverage(ctx, hostLead, guest.ID, host.ID); !errors.Is(
		err, domain.ErrNotYourProgramme) {
		t.Errorf("the holding programme's lead asked in somebody else's name, answering %v", err)
	}

	// Agreeing is a statement about the holder's own teaching, so only that lead may make it.
	// If the asking lead could agree, the handshake would be one-sided with an extra step.
	if _, err := f.service.AcceptCoverage(ctx, guestLead, guest.ID); !errors.Is(
		err, domain.ErrNotYourProgramme) {
		t.Errorf("the asking programme's lead agreed on the holder's behalf, answering %v", err)
	}
	if _, err := f.service.AcceptCoverage(ctx, hostLead, guest.ID); err != nil {
		t.Errorf("the holding programme's lead could not agree: %v", err)
	}
}

// Ending it is the one both may do: the asking programme because it is their demand, the holding
// one because it is their teaching. A holder who could not walk away could only correct an
// agreement by asking somebody else to.
func TestEitherSideMayEndACoverage(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	for _, who := range []struct {
		name      string
		programme uuid.UUID
	}{
		{"the asking programme's lead", otherProgramme},
		{"the holding programme's lead", demandProgramme},
	} {
		t.Run(who.name, func(t *testing.T) {
			t.Parallel()

			f := newDemandService(t, policy.PhaseDemandPlanning)
			host := f.store.add(demandProgramme, policy.PhaseDemandPlanning, 2)
			guest := f.store.add(otherProgramme, policy.PhaseDemandPlanning, 0)
			f.store.cover(guest.ID, host.ID, true)

			if _, err := f.service.ReleaseCoverage(ctx,
				lead(principal.KindInteractive, who.programme), guest.ID); err != nil {
				t.Errorf("%s could not end the coverage: %v", who.name, err)
			}
		})
	}
}

// A lead of neither programme is refused, and told about the instance they named rather than
// about the one they did not.
func TestALeadOfNeitherProgrammeCannotTouchACoverage(t *testing.T) {
	t.Parallel()

	f := newDemandService(t, policy.PhaseDemandPlanning)
	ctx := t.Context()

	host := f.store.add(demandProgramme, policy.PhaseDemandPlanning, 2)
	guest := f.store.add(otherProgramme, policy.PhaseDemandPlanning, 0)
	f.store.cover(guest.ID, host.ID, true)

	stranger := lead(principal.KindInteractive, unplannedProgramme)

	if _, err := f.service.RequestCoverage(ctx, stranger, guest.ID, host.ID); !errors.Is(
		err, domain.ErrNotYourProgramme) {
		t.Errorf("asking answered %v, want ErrNotYourProgramme", err)
	}
	if _, err := f.service.AcceptCoverage(ctx, stranger, guest.ID); !errors.Is(
		err, domain.ErrNotYourProgramme) {
		t.Errorf("agreeing answered %v, want ErrNotYourProgramme", err)
	}
	if _, err := f.service.ReleaseCoverage(ctx, stranger, guest.ID); !errors.Is(
		err, domain.ErrNotYourProgramme) {
		t.Errorf("ending answered %v, want ErrNotYourProgramme", err)
	}
}

// The dean's office reaches every programme, so it holds both halves — which is what makes it the
// answer when two leads cannot agree between themselves.
func TestTheDeansOfficeHoldsBothHalvesOfTheHandshake(t *testing.T) {
	t.Parallel()

	f := newDemandService(t, policy.PhaseDemandPlanning)
	ctx := t.Context()

	host := f.store.add(demandProgramme, policy.PhaseDemandPlanning, 2)
	guest := f.store.add(otherProgramme, policy.PhaseDemandPlanning, 2)
	dean := testdata.Vier.Actor(principal.KindInteractive, string(policy.RoleDeansOffice))

	if _, err := f.service.RequestCoverage(ctx, dean, guest.ID, host.ID); err != nil {
		t.Fatalf("the dean's office could not ask: %v", err)
	}
	f.store.cover(guest.ID, host.ID, false)
	if _, err := f.service.AcceptCoverage(ctx, dean, guest.ID); err != nil {
		t.Errorf("the dean's office could not agree: %v", err)
	}
}

// Agreeing and ending need something to act on, and neither writes on the way to finding out.
func TestActingOnACoverageThatIsNotThere(t *testing.T) {
	t.Parallel()

	f := newDemandService(t, policy.PhaseDemandPlanning)
	ctx := t.Context()

	guest := f.store.add(otherProgramme, policy.PhaseDemandPlanning, 2)
	guestLead := lead(principal.KindInteractive, otherProgramme)

	if _, err := f.service.AcceptCoverage(ctx, guestLead, guest.ID); !errors.Is(
		err, domain.ErrCoverageNotRequested) {
		t.Errorf("agreeing to nothing answered %v, want ErrCoverageNotRequested", err)
	}
	if _, err := f.service.ReleaseCoverage(ctx, guestLead, guest.ID); !errors.Is(
		err, domain.ErrCoverageNotRequested) {
		t.Errorf("ending nothing answered %v, want ErrCoverageNotRequested", err)
	}
	if f.store.writes != 0 {
		t.Errorf("finding out there was nothing to do wrote %d time(s)", f.store.writes)
	}
}

// The controlling view folds the shares of one home programme. The own share is the one taught
// in the home programme's instances; everything else is what it lends to other programmes.
func TestTeachingLoadSumsItsShares(t *testing.T) {
	t.Parallel()

	home := domain.Programme{ID: uuid.New(), Code: "PA"}
	other := domain.Programme{ID: uuid.New(), Code: "PB"}
	load := domain.TeachingLoad{
		Programme: home,
		Shares: []domain.TeachingLoadShare{
			{HomeProgramme: home, TeachingHours: 12, Instances: 3, FwpInstances: 1, FwpHours: 4},
			{HomeProgramme: other, TeachingHours: 6.5, Instances: 2, PartsWithoutHours: 1},
		},
	}

	cases := []struct {
		name      string
		got, want float64
	}{
		{"teaching hours", load.TeachingHours(), 18.5},
		{"own hours", load.OwnHours(), 12},
		{"instances", float64(load.Instances()), 5},
		{"FWP instances", float64(load.FwpInstances()), 1},
		{"FWP hours", load.FwpHours(), 4},
		{"parts without hours", float64(load.PartsWithoutHours()), 1},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: %v, want %v", c.name, c.got, c.want)
		}
	}

	if empty := (domain.TeachingLoad{Programme: home}); empty.TeachingHours() != 0 ||
		empty.OwnHours() != 0 {
		t.Error("a programme nothing has been declared for costs something")
	}
}

// Reading the load is reading the demand: anybody with an account, a semester code checked and
// normalised the way every other read does it, and nothing recorded on the way.
func TestReadingTheTeachingLoad(t *testing.T) {
	t.Parallel()

	t.Run("a lecturer reads it, and the semester stays untouched", func(t *testing.T) {
		t.Parallel()
		f := newDemandService(t, policy.PhaseDemandPlanning)

		actor := testdata.Eins.Actor(principal.KindToken, string(policy.RoleLecturer))
		if _, err := f.service.TeachingLoad(t.Context(), actor, " 2027-ss "); err != nil {
			t.Fatalf("a lecturer cannot read the teaching load: %v", err)
		}
		if f.store.plannedSemester != "2027-SS" {
			t.Errorf("the store was asked about %q, want 2027-SS", f.store.plannedSemester)
		}
		if len(f.semesters.ensured) != 0 {
			t.Errorf("reading recorded the semester %v", f.semesters.ensured)
		}
	})

	t.Run("nobody without an account", func(t *testing.T) {
		t.Parallel()
		f := newDemandService(t, policy.PhaseDemandPlanning)

		if _, err := f.service.TeachingLoad(t.Context(), principal.Actor{}, "2027-SS"); err == nil {
			t.Error("an anonymous actor read the teaching load")
		}
	})

	t.Run("a code that names no semester", func(t *testing.T) {
		t.Parallel()
		f := newDemandService(t, policy.PhaseDemandPlanning)

		actor := testdata.Eins.Actor(principal.KindInteractive, string(policy.RoleLecturer))
		_, err := f.service.TeachingLoad(t.Context(), actor, "Sommer 27")
		if !errors.Is(err, domain.ErrSemesterCodeInvalid) {
			t.Errorf("an invalid code gave %v, want ErrSemesterCodeInvalid", err)
		}
	})
}

// The quota is a year's figure, and a winter is read against half of it. Without a quota there
// is no figure at all — not zero, which would read as a programme with nothing to spend.
func TestTheBalanceIsHalfTheYearMinusTheLoad(t *testing.T) {
	t.Parallel()

	home := domain.Programme{ID: uuid.New(), Code: "PA"}
	load := domain.TeachingLoad{
		Programme: home,
		Shares:    []domain.TeachingLoadShare{{HomeProgramme: home, TeachingHours: 184}},
	}
	if load.SemesterQuota() != nil || load.Balance() != nil {
		t.Fatal("a programme without a quota has a balance")
	}

	yearly := 425.0
	load.YearlyQuota = &yearly
	if got := *load.SemesterQuota(); got != 212.5 {
		t.Errorf("semester quota %v, want 212.5", got)
	}
	if got := *load.Balance(); got != 28.5 {
		t.Errorf("balance %v, want 28.5", got)
	}

	small := 300.0
	load.YearlyQuota = &small
	if got := *load.Balance(); got != -34 {
		t.Errorf("a programme spending more than half its year has a balance of %v, want -34", got)
	}
}

// The teaching load carries the quota of the study year its semester belongs to: the summer is
// read against the year that began the October before it.
func TestTheTeachingLoadCarriesTheStudyYearsQuota(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		semester string
		year     int
	}{
		{"2026-WS", 2026},
		{"2027-SS", 2026},
		{"2027-WS", 2027},
	} {
		t.Run(c.semester, func(t *testing.T) {
			t.Parallel()
			f := newDemandService(t, policy.PhaseDemandPlanning)
			f.store.loads = []domain.TeachingLoad{
				{Programme: domain.Programme{ID: demandProgramme, Code: "PA"}},
				{Programme: domain.Programme{ID: otherProgramme, Code: "PB"}},
			}
			f.store.quotas = []domain.ProgrammeQuota{
				{Programme: domain.Programme{ID: demandProgramme}, StudyYear: c.year, TeachingHours: 100},
				{Programme: domain.Programme{ID: otherProgramme}, StudyYear: c.year + 1, TeachingHours: 999},
			}

			actor := testdata.Eins.Actor(principal.KindToken, string(policy.RoleLecturer))
			loads, err := f.service.TeachingLoad(t.Context(), actor, c.semester)
			if err != nil {
				t.Fatal(err)
			}
			if loads[0].YearlyQuota == nil || *loads[0].YearlyQuota != 100 {
				t.Errorf("PA in %s has the quota %v, want 100", c.semester, loads[0].YearlyQuota)
			}
			if loads[1].YearlyQuota != nil {
				t.Errorf("PB in %s took the quota of another study year", c.semester)
			}
		})
	}
}

// Entering a quota is the dean's office's, through either door. Not the programme lead, whose
// programme the line is drawn for, and not the administrator.
func TestWhoSetsAProgrammeQuota(t *testing.T) {
	t.Parallel()

	hours := 425.0
	cases := []struct {
		name  string
		actor principal.Actor
		ok    bool
	}{
		{"the dean's office, interactively", testdata.Fuenf.Actor(principal.KindInteractive,
			string(policy.RoleDeansOffice)), true},
		{"the dean's office, by token", testdata.Fuenf.Actor(principal.KindToken,
			string(policy.RoleDeansOffice)), true},
		{"the programme lead", lead(principal.KindInteractive, demandProgramme), false},
		{"the administrator", testdata.Sechs.Actor(principal.KindInteractive,
			string(policy.RoleAdmin)), false},
		{"a lecturer", testdata.Eins.Actor(principal.KindInteractive,
			string(policy.RoleLecturer)), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			f := newDemandService(t, policy.PhaseDemandPlanning)

			quota, err := f.service.SetProgrammeQuota(t.Context(), c.actor, " pa ", 2026, &hours)
			if !c.ok {
				if !errors.Is(err, domain.ErrCatalogueForbidden) {
					t.Errorf("gave %v, want ErrCatalogueForbidden", err)
				}
				if f.store.writes != 0 {
					t.Error("a refused quota was written")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if quota.Programme.Code != "PA" || quota.TeachingHours != 425 || quota.SetBy != c.actor.ID {
				t.Errorf("wrote %+v", quota)
			}
		})
	}
}

func TestWhatAProgrammeQuotaMayBe(t *testing.T) {
	t.Parallel()

	dean := testdata.Fuenf.Actor(principal.KindInteractive, string(policy.RoleDeansOffice))
	hours := func(h float64) *float64 { return &h }

	cases := []struct {
		name      string
		programme string
		year      int
		hours     *float64
		want      error
	}{
		{"negative", "PA", 2026, hours(-1), domain.ErrQuotaInvalid},
		{"absurd", "PA", 2026, hours(10001), domain.ErrQuotaInvalid},
		{"a year nobody plans", "PA", 2099, hours(10), domain.ErrStudyYearOutOfRange},
		{"an unknown programme", "XX", 2026, hours(10), domain.ErrProgrammeNotFound},
		{"taking back for an unknown programme", "XX", 2026, nil, domain.ErrProgrammeNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			f := newDemandService(t, policy.PhaseDemandPlanning)

			_, err := f.service.SetProgrammeQuota(t.Context(), dean, c.programme, c.year, c.hours)
			if !errors.Is(err, c.want) {
				t.Errorf("gave %v, want %v", err, c.want)
			}
		})
	}

	t.Run("zero is a quota, and null takes it back", func(t *testing.T) {
		t.Parallel()
		f := newDemandService(t, policy.PhaseDemandPlanning)

		if _, err := f.service.SetProgrammeQuota(t.Context(), dean, "PA", 2026, hours(0)); err != nil {
			t.Fatalf("zero refused: %v", err)
		}
		quota, err := f.service.SetProgrammeQuota(t.Context(), dean, "PA", 2026, nil)
		if err != nil || quota != nil {
			t.Fatalf("taking back gave (%v, %v)", quota, err)
		}
		if len(f.store.quotas) != 0 {
			t.Errorf("still there: %+v", f.store.quotas)
		}
	})

	t.Run("reading a year nobody plans is refused too", func(t *testing.T) {
		t.Parallel()
		f := newDemandService(t, policy.PhaseDemandPlanning)

		reader := testdata.Eins.Actor(principal.KindToken, string(policy.RoleLecturer))
		if _, err := f.service.ProgrammeQuotas(t.Context(), reader, 1990); !errors.Is(err,
			domain.ErrStudyYearOutOfRange) {
			t.Errorf("gave %v, want ErrStudyYearOutOfRange", err)
		}
	})
}

// costing is a home programme's load of a given number of hours, all taught in its own instances.
func costing(p domain.Programme, hours float64) domain.TeachingLoad {
	return domain.TeachingLoad{
		Programme: p,
		Shares:    []domain.TeachingLoadShare{{HomeProgramme: p, TeachingHours: hours}},
	}
}

// A summer is planned after its winter has happened: what the winter spent is gone, and the
// summer is held to what is left of the year rather than to half of it.
func TestASummerIsReadAgainstWhatTheWinterLeft(t *testing.T) {
	t.Parallel()

	pa := domain.Programme{ID: demandProgramme, Code: "PA"}
	pb := domain.Programme{ID: otherProgramme, Code: "PB"}
	f := newDemandService(t, policy.PhaseDemandPlanning)
	f.store.loadsOf = map[string][]domain.TeachingLoad{
		"2026-WS": {costing(pa, 250)},
		"2027-SS": {costing(pa, 150), costing(pb, 10)},
	}
	f.store.quotas = []domain.ProgrammeQuota{
		{Programme: pa, StudyYear: 2026, TeachingHours: 425},
		{Programme: pb, StudyYear: 2026, TeachingHours: 100},
	}
	actor := testdata.Eins.Actor(principal.KindToken, string(policy.RoleLecturer))

	summer, err := f.service.TeachingLoad(t.Context(), actor, "2027-SS")
	if err != nil {
		t.Fatal(err)
	}
	if got := *summer[0].SemesterQuota(); got != 175 {
		t.Errorf("PA's summer may spend %v, want 425 − 250 = 175", got)
	}
	if got := *summer[0].Balance(); got != 25 {
		t.Errorf("PA's summer balance %v, want 25", got)
	}
	// PB declared nothing in the winter, so the whole year is left for the summer.
	if got := *summer[1].SemesterQuota(); got != 100 {
		t.Errorf("PB's summer may spend %v, want the whole 100", got)
	}

	winter, err := f.service.TeachingLoad(t.Context(), actor, "2026-WS")
	if err != nil {
		t.Fatal(err)
	}
	if winter[0].SpentInWinter != nil || *winter[0].SemesterQuota() != 212.5 {
		t.Errorf("PA's winter may spend %v, want half the year", *winter[0].SemesterQuota())
	}
}

// The study year puts both semesters beside the quota, and lists a programme that appears in
// only one of them.
func TestTheStudyYearLoad(t *testing.T) {
	t.Parallel()

	pa := domain.Programme{ID: demandProgramme, Code: "PA"}
	pb := domain.Programme{ID: otherProgramme, Code: "PB"}
	f := newDemandService(t, policy.PhaseDemandPlanning)
	f.store.loadsOf = map[string][]domain.TeachingLoad{
		"2026-WS": {costing(pb, 30), costing(pa, 250)},
		"2027-SS": {costing(pa, 150)},
	}
	f.store.quotas = []domain.ProgrammeQuota{{Programme: pa, StudyYear: 2026, TeachingHours: 425}}
	actor := testdata.Eins.Actor(principal.KindToken, string(policy.RoleLecturer))

	year, err := f.service.StudyYearLoad(t.Context(), actor, 2026)
	if err != nil {
		t.Fatal(err)
	}
	if len(year) != 2 || year[0].Programme.Code != "PA" || year[1].Programme.Code != "PB" {
		t.Fatalf("listed %+v, want PA and PB by code", year)
	}
	if year[0].WinterHours != 250 || year[0].SummerHours != 150 || *year[0].Remaining() != 25 {
		t.Errorf("PA: winter %v, summer %v, remaining %v; want 250, 150, 25",
			year[0].WinterHours, year[0].SummerHours, year[0].Remaining())
	}
	if year[1].Remaining() != nil || year[1].SummerHours != 0 {
		t.Errorf("PB without a quota has %v remaining and %v in the summer", year[1].Remaining(),
			year[1].SummerHours)
	}

	if _, err := f.service.StudyYearLoad(t.Context(), actor, 2090); !errors.Is(err,
		domain.ErrStudyYearOutOfRange) {
		t.Errorf("a year nobody plans gave %v", err)
	}
	if _, err := f.service.StudyYearLoad(t.Context(), principal.Actor{}, 2026); err == nil {
		t.Error("an anonymous actor read the study year")
	}
}
