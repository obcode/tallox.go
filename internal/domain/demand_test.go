package domain_test

import (
	"context"
	"errors"
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
)

// fakeDemandStore records what it was asked to do and answers plausibly.
type fakeDemandStore struct {
	instances       map[uuid.UUID]domain.CourseInstance
	partOwner       map[uuid.UUID]uuid.UUID
	created         []domain.NewCourseInstance
	planned         []domain.DemandEntry
	plannedSemester string
	writes          int
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

func (f *fakeDemandStore) ShareInstancePartAcrossTracks(ctx context.Context, partID uuid.UUID) (*domain.CourseInstance, error) {
	f.writes++
	return f.CourseInstanceByPartID(ctx, partID)
}

func (f *fakeDemandStore) SplitInstancePartAcrossTracks(ctx context.Context, partID uuid.UUID) (*domain.CourseInstance, error) {
	f.writes++
	return f.CourseInstanceByPartID(ctx, partID)
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

// fakeSemesterStore is the semester half, and it records what was ensured.
//
// That last part is the point of it: a refused declaration must not leave a semester row behind,
// and the row is created by the very act the refusal is about.
type fakeSemesterStore struct {
	rows    map[string]domain.Semester
	ensured []string
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

// fakeCatalogueReader answers the one question the demand asks of the catalogue.
type fakeCatalogueReader struct{}

func (fakeCatalogueReader) Programmes(context.Context) ([]domain.Programme, error) { return nil, nil }

func (fakeCatalogueReader) ProgrammesByID(context.Context, []uuid.UUID) ([]domain.Programme, error) {
	return nil, nil
}

func (fakeCatalogueReader) ProgrammeByCode(_ context.Context, code string) (*domain.Programme, error) {
	switch code {
	case "PA":
		return &domain.Programme{ID: demandProgramme, Code: "PA"}, nil
	case "PB":
		return &domain.Programme{ID: otherProgramme, Code: "PB"}, nil
	default:
		return nil, nil
	}
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

func (fakeCatalogueReader) SetModuleComponents(context.Context, uuid.UUID,
	[]domain.ModuleComponent, uuid.UUID,
) (*domain.Module, error) {
	return nil, nil
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
func TestDemandCanBeDeclaredInEveryPhase(t *testing.T) {
	t.Parallel()

	for _, phase := range policy.AllPhases() {
		t.Run(string(phase), func(t *testing.T) {
			t.Parallel()

			f := newDemandService(t, phase)
			if _, err := f.service.Declare(t.Context(),
				lead(principal.KindInteractive, demandProgramme), declaration()); err != nil {
				t.Errorf("declaring in %s gave %v — a late instance is a correction", phase, err)
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
