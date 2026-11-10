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

// localModuleInGroup is a minimal local course, filed under the given group on creation.
func localModuleInGroup(programme uuid.UUID, name string, group *uuid.UUID) domain.NewLocalModule {
	return domain.NewLocalModule{
		HomeProgrammeID: programme,
		Name:            name,
		Kind:            domain.ModuleKindFwpPlaceholder,
		CourseType:      domain.CourseTypeSU,
		Frequency:       domain.FrequencyOnAnnouncement,
		Active:          true,
		Components: []domain.ModuleComponent{
			{Kind: domain.PartKindLecture, TeachingHours: 4, Position: 0},
		},
		SubjectGroupID: group,
	}
}

// localModuleGroupFixture is a projected catalogue, a person who enters courses, and one active and
// one retired subject group.
type localModuleGroupFixture struct {
	schema    *storetest.Schema
	modules   *store.Modules
	programme uuid.UUID
	active    uuid.UUID
	retired   uuid.UUID
}

func newLocalModuleGroupFixture(t *testing.T) localModuleGroupFixture {
	t.Helper()

	s := storetest.New(t)
	storetest.SeedPerson(t, s, testdata.Vier, "PROGRAMME_LEAD")
	storetest.SeedZPACatalogue(t, s)
	project(t, s)

	groups := store.NewSubjectGroups(s.Pool)
	active, err := groups.CreateSubjectGroup(t.Context(), "SWE", "Softwarefächer")
	if err != nil {
		t.Fatalf("cannot create a subject group: %v", err)
	}
	retired, err := groups.CreateSubjectGroup(t.Context(), "ALT", "Stillgelegt")
	if err != nil {
		t.Fatalf("cannot create a subject group: %v", err)
	}
	if _, err := groups.SetSubjectGroupActive(t.Context(), retired.ID, false); err != nil {
		t.Fatalf("cannot retire the subject group: %v", err)
	}

	return localModuleGroupFixture{
		schema:    s,
		modules:   store.NewModules(s.Pool),
		programme: programmeID(t, s, storetest.FixtureProgrammeA),
		active:    active.ID,
		retired:   retired.ID,
	}
}

// localModuleRows counts the local rows of this name, so a refusal can be shown to have left
// nothing behind rather than merely to have reported an error.
func (f localModuleGroupFixture) localModuleRows(t *testing.T, name string) int {
	t.Helper()

	var n int
	if err := f.schema.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM module WHERE source = 'LOCAL' AND name = $1`, name).Scan(&n); err != nil {
		t.Fatalf("cannot count the local modules: %v", err)
	}
	return n
}

// The group named on creation is written with the module, and the person who entered it is the
// one recorded as having filed it.
func TestALocalModuleIsFiledUnderItsGroupOnCreation(t *testing.T) {
	t.Parallel()

	f := newLocalModuleGroupFixture(t)
	creator := testdata.Vier.ID()

	created, err := f.modules.CreateLocalModule(t.Context(),
		localModuleInGroup(f.programme, "FWP-Platzhalter (technisch)", &f.active), creator)
	if err != nil {
		t.Fatalf("cannot create the course with a group: %v", err)
	}
	if created.SubjectGroup == nil || created.SubjectGroup.ID != f.active {
		t.Fatalf("the new course is filed under %+v, want the group it was created with",
			created.SubjectGroup)
	}

	var assignedBy uuid.NullUUID
	if err := f.schema.Pool.QueryRow(t.Context(),
		`SELECT assigned_by FROM module_subject_group WHERE module_id = $1`,
		created.ID).Scan(&assignedBy); err != nil {
		t.Fatalf("cannot read who filed the course: %v", err)
	}
	if !assignedBy.Valid || assignedBy.UUID != creator {
		t.Errorf("the filing is recorded as by %v, want the creator %v", assignedBy, creator)
	}
}

// Without a group the course is created unfiled, exactly as before the argument existed.
func TestALocalModuleWithoutAGroupStaysUnfiled(t *testing.T) {
	t.Parallel()

	f := newLocalModuleGroupFixture(t)

	created, err := f.modules.CreateLocalModule(t.Context(),
		localModuleInGroup(f.programme, "Ohne Fachgruppe", nil), testdata.Vier.ID())
	if err != nil {
		t.Fatalf("cannot create the course: %v", err)
	}
	if created.SubjectGroup != nil {
		t.Errorf("a course created without a group is filed under %+v", created.SubjectGroup)
	}
}

// A group that does not exist, or has been retired, refuses the whole creation: no module row is
// left behind, and no row lands on the list of unsorted modules that nobody chose to put there.
func TestAnUnknownOrRetiredGroupLeavesNoModuleRow(t *testing.T) {
	t.Parallel()

	f := newLocalModuleGroupFixture(t)
	unknown := uuid.New()
	nilGroup := uuid.Nil

	for name, group := range map[string]*uuid.UUID{
		"unknown": &unknown,
		"retired": &f.retired,
		"nil id":  &nilGroup,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			course := "Fach mit Fachgruppe " + name
			_, err := f.modules.CreateLocalModule(t.Context(),
				localModuleInGroup(f.programme, course, group), testdata.Vier.ID())
			if !errors.Is(err, domain.ErrSubjectGroupNotFound) {
				t.Fatalf("got %v, want ErrSubjectGroupNotFound", err)
			}
			if n := f.localModuleRows(t, course); n != 0 {
				t.Errorf("a refused creation left %d module rows behind", n)
			}
		})
	}
}
