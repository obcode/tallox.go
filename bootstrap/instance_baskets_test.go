package bootstrap_test

import (
	"slices"
	"testing"

	"github.com/obcode/tallox.go/internal/graphqltest"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// CourseInstance.baskets is Module.baskets asked with the cohort's own programme, for the screens
// that list cohorts of several programmes side by side. Catalogue data, readable through every
// door like the rest of the demand.
func TestACohortNamesTheBasketsOfItsOwnProgrammeThroughEveryDoor(t *testing.T) {
	t.Parallel()

	f := demandHandler(t,
		map[string][]string{
			testdata.Vier.Mail: {storetest.FixtureProgrammeA, storetest.FixtureProgrammeB},
		},
		grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}},
		grants{testdata.Eins, []string{"LECTURER"}})

	lead := graphqltest.New(f.handler).AsUser(testdata.Vier.Mail).On(graphqltest.Browser)
	declare := func(programme, module string) {
		t.Helper()
		lead.MustQuery(t, declareMutation, map[string]any{"in": map[string]any{
			"semester":  "2027-SS",
			"programme": programme,
			"moduleId":  module,
			"track":     "",
		}}, &struct{ DeclareCourseInstance struct{ ID string } }{})
	}
	// The same module in two programmes, each with its own basket, and a module at home in a
	// programme that is gone, which sits in no basket of A.
	declare(storetest.FixtureProgrammeA, f.module.String())
	declare(storetest.FixtureProgrammeB, f.module.String())
	declare(storetest.FixtureProgrammeA, f.foreign.String())

	read := func(t *testing.T, c *graphqltest.Client) {
		t.Helper()
		var out struct {
			CourseInstances []struct {
				Programme struct{ Code string }
				Module    struct{ ID string }
				Baskets   []string
			}
		}
		c.MustQuery(t, `query($s: String!) {
			courseInstances(semester: $s) {
				programme { code }
				module { id }
				baskets
			}
		}`, map[string]any{"s": "2027-SS"}, &out)

		if len(out.CourseInstances) != 3 {
			t.Fatalf("the semester holds %d instances, want 3", len(out.CourseInstances))
		}
		for _, i := range out.CourseInstances {
			var want []string
			switch {
			case i.Module.ID == f.foreign.String():
				want = []string{}
			case i.Programme.Code == storetest.FixtureProgrammeA:
				want = []string{"PA: Pflicht"}
			default:
				want = []string{"PB: Pflicht"}
			}
			if i.Baskets == nil || !slices.Equal(i.Baskets, want) {
				t.Errorf("the cohort of %s in %s names the baskets %v, want %v",
					i.Module.ID, i.Programme.Code, i.Baskets, want)
			}
		}
	}
	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token, read)
	t.Run("full reach", func(t *testing.T) {
		read(t, graphqltest.New(f.handler).WithToken(fullReachToken(t, f.schema, testdata.Eins)).
			On(graphqltest.Token))
	})
}
