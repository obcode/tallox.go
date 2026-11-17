package bootstrap_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/graphqltest"
	"github.com/obcode/tallox.go/internal/store"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// CourseInstance.dutyStatus is Module.dutyStatus asked with the cohort's own programme, for the
// assignment page, which orders cohorts of several programmes by it. Catalogue data, readable
// through every door like the rest of the demand.
func TestACohortNamesItsDutyStatusInItsOwnProgrammeThroughEveryDoor(t *testing.T) {
	t.Parallel()

	f := demandHandler(t,
		map[string][]string{
			testdata.Vier.Mail: {storetest.FixtureProgrammeA, storetest.FixtureProgrammeB},
		},
		grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}},
		grants{testdata.Eins, []string{"LECTURER"}})

	// Two more modules of PA need a split before they can be declared: the one whose duty differs
	// between the versions of the regulations, and an elective one.
	modules := store.NewModules(f.schema.Pool)
	moduleID := func(ref int) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := f.schema.Pool.QueryRow(t.Context(),
			`SELECT id FROM module WHERE zpa_module_ref = $1`, ref).Scan(&id); err != nil {
			t.Fatalf("cannot read the id of module %d: %v", ref, err)
		}
		if _, err := modules.SetModuleComponents(t.Context(), id, []domain.ModuleComponent{
			{Kind: domain.PartKindLecture, TeachingHours: 4, Position: 0},
		}, uuid.Nil); err != nil {
			t.Fatalf("cannot state the split of module %d: %v", ref, err)
		}
		return id
	}
	mixed := moduleID(storetest.FixtureModuleDutyDiffers)
	elective := moduleID(storetest.FixtureModuleTwoSlots)

	lead := graphqltest.New(f.handler).AsUser(testdata.Vier.Mail).On(graphqltest.Browser)
	declare := func(programme string, module uuid.UUID) {
		t.Helper()
		lead.MustQuery(t, declareMutation, map[string]any{"in": map[string]any{
			"semester":  "2027-SS",
			"programme": programme,
			"moduleId":  module.String(),
			"track":     "",
		}}, &struct{ DeclareCourseInstance struct{ ID string } }{})
	}
	declare(storetest.FixtureProgrammeA, f.module)
	declare(storetest.FixtureProgrammeB, f.module)
	declare(storetest.FixtureProgrammeA, mixed)
	declare(storetest.FixtureProgrammeA, elective)
	declare(storetest.FixtureProgrammeA, f.foreign)

	// "null" stands for the module that does not appear in the programme's catalogue at all.
	type key struct{ programme, module string }
	want := map[key]string{
		{storetest.FixtureProgrammeA, f.module.String()}:  string(domain.DutyCompulsory),
		{storetest.FixtureProgrammeB, f.module.String()}:  string(domain.DutyCompulsory),
		{storetest.FixtureProgrammeA, mixed.String()}:     string(domain.DutyMixed),
		{storetest.FixtureProgrammeA, elective.String()}:  string(domain.DutyElective),
		{storetest.FixtureProgrammeA, f.foreign.String()}: "null",
	}

	read := func(t *testing.T, c *graphqltest.Client) {
		t.Helper()
		var out struct {
			CourseInstances []struct {
				Programme  struct{ Code string }
				Module     struct{ ID string }
				DutyStatus *string
			}
		}
		c.MustQuery(t, `query($s: String!) {
			courseInstances(semester: $s) {
				programme { code }
				module { id }
				dutyStatus
			}
		}`, map[string]any{"s": "2027-SS"}, &out)

		if len(out.CourseInstances) != len(want) {
			t.Fatalf("the semester holds %d instances, want %d", len(out.CourseInstances), len(want))
		}
		for _, i := range out.CourseInstances {
			w, ok := want[key{i.Programme.Code, i.Module.ID}]
			if !ok {
				t.Fatalf("unexpected cohort of %s in %s", i.Module.ID, i.Programme.Code)
			}
			got := "null"
			if i.DutyStatus != nil {
				got = *i.DutyStatus
			}
			if got != w {
				t.Errorf("the cohort of %s in %s has the duty status %s, want %s",
					i.Module.ID, i.Programme.Code, got, w)
			}
		}
	}
	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token, read)
	t.Run("full reach", func(t *testing.T) {
		read(t, graphqltest.New(f.handler).WithToken(fullReachToken(t, f.schema, testdata.Eins)).
			On(graphqltest.Token))
	})
}
