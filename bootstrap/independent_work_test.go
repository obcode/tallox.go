package bootstrap_test

import (
	"testing"

	"github.com/obcode/tallox.go/internal/graphqltest"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// Independent work — a thesis, the practical semester, the bachelor seminar — is not planned at
// all: students arrange it directly with a teacher. These tests take an ordinary fixture module and
// turn it into one by its course type, because that is the only thing that decides it; the module
// keeps its confirmed split, so that every refusal below is about the course type and not about a
// missing split.

func makeIndependentWork(t *testing.T, s *storetest.Schema, module string) {
	t.Helper()
	if _, err := s.Pool.Exec(t.Context(),
		`UPDATE module SET course_type = 'SELF_STUDY' WHERE id = $1`, module); err != nil {
		t.Fatalf("cannot make the module independent work: %v", err)
	}
}

// The catalogue says so through both doors, and such a module is never plannable.
func TestIndependentWorkIsMarkedInTheCatalogueThroughBothDoors(t *testing.T) {
	t.Parallel()

	f := demandHandler(t, nil, grants{testdata.Eins, []string{"LECTURER"}})

	const query = `query($id: ID!) { module(id: $id) { independentWork plannable } }`
	type answer struct {
		Module struct{ IndependentWork, Plannable bool }
	}

	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var out answer
			c.MustQuery(t, query, map[string]any{"id": f.module.String()}, &out)
			if out.Module.IndependentWork || !out.Module.Plannable {
				t.Errorf("an ordinary module with a split reads %+v, want plannable", out.Module)
			}
		})

	makeIndependentWork(t, f.schema, f.module.String())
	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var out answer
			c.MustQuery(t, query, map[string]any{"id": f.module.String()}, &out)
			if !out.Module.IndependentWork || out.Module.Plannable {
				t.Errorf("a thesis with a split reads %+v, want independent work, not plannable",
					out.Module)
			}
		})
}

// No instance of it, neither one at a time nor as a row of the demand screen.
func TestIndependentWorkTakesNoDemand(t *testing.T) {
	t.Parallel()

	f := demandHandler(t,
		map[string][]string{testdata.Vier.Mail: {storetest.FixtureProgrammeA}},
		grants{testdata.Vier, []string{"PROGRAMME_LEAD"}})
	makeIndependentWork(t, f.schema, f.module.String())

	inTheBrowser(t, f.handler, testdata.Vier.Mail,
		func(t *testing.T, c *graphqltest.Client) {
			resp := c.Do(t, declareMutation,
				declareInput(f, storetest.FixtureProgrammeA, "2027-SS", ""))
			if code := errorCode(t, resp); code != "MODULE_NOT_PLANNED" {
				t.Errorf("declaring a thesis was refused with %s, want MODULE_NOT_PLANNED", code)
			}

			var out planReport
			c.MustQuery(t, planMutation, map[string]any{
				"s": "2027-SS", "p": storetest.FixtureProgrammeA, "d": false,
				"e": []map[string]any{{
					"moduleId": f.module.String(),
					"tracks":   []map[string]any{{"track": "", "groups": 1}},
				}},
			}, &out)
			if len(out.PlanDemand.Created) != 0 || len(out.PlanDemand.Refused) != 1 ||
				out.PlanDemand.Refused[0].Code != "MODULE_NOT_PLANNED" {
				t.Errorf("planning a thesis reports %+v, want one MODULE_NOT_PLANNED refusal",
					out.PlanDemand)
			}
		})
}

// An instance from before the rule stays where it is, and a copy leaves it behind and says so.
func TestCopyingLeavesIndependentWorkBehind(t *testing.T) {
	t.Parallel()

	f := demandHandler(t,
		map[string][]string{testdata.Vier.Mail: {storetest.FixtureProgrammeA}},
		grants{testdata.Vier, []string{"PROGRAMME_LEAD"}})

	c := graphqltest.New(f.handler).AsUser(testdata.Vier.Mail).On(graphqltest.Browser)
	var declared struct {
		DeclareCourseInstance struct{ ID string }
	}
	c.MustQuery(t, declareMutation,
		declareInput(f, storetest.FixtureProgrammeA, "2026-WS", ""), &declared)
	makeIndependentWork(t, f.schema, f.module.String())

	var out struct {
		CopyDemandFromSemester struct {
			Created, Skipped, NotPlanned int
			Instances                    []struct{ Track string }
		}
	}
	c.MustQuery(t, `mutation($from: String!, $to: String!, $p: String!) {
		copyDemandFromSemester(from: $from, to: $to, programme: $p) {
			created skipped notPlanned instances { track }
		}
	}`, map[string]any{"from": "2026-WS", "to": "2027-SS", "p": storetest.FixtureProgrammeA}, &out)

	got := out.CopyDemandFromSemester
	if got.Created != 0 || got.Skipped != 0 || got.NotPlanned != 1 || len(got.Instances) != 0 {
		t.Errorf("copying a thesis reports %+v, want nothing created and one not planned", got)
	}
}

// Nobody states a competence for it, and it is not one of the compulsory modules counted towards
// the minimum — the way three theses used to stand in for three compulsory subjects.
func TestIndependentWorkTakesNoCompetenceThroughBothDoors(t *testing.T) {
	t.Parallel()

	f := competenceHandler(t, grants{testdata.Eins, []string{"LECTURER"}})
	f.join(t, testdata.Eins)
	// A statement from before the rule, so that the count has something to leave out.
	f.state(t, testdata.Eins, "CAN_TEACH")
	makeIndependentWork(t, f.schema, f.module)

	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			resp := c.Do(t, setMyCompetenceMutation, map[string]any{"m": f.module, "l": "CAN_TEACH"})
			if code := errorCode(t, resp); code != "MODULE_NOT_PLANNED" {
				t.Errorf("a competence for a thesis was refused with %s, want MODULE_NOT_PLANNED", code)
			}

			var out struct {
				MyCompetenceTotal  struct{ CanTeachCompulsory, CompulsoryModules int }
				MyCompetenceStatus []struct{ CanTeachCompulsory int }
			}
			c.MustQuery(t, `query {
				myCompetenceTotal { canTeachCompulsory compulsoryModules }
				myCompetenceStatus { canTeachCompulsory }
			}`, nil, &out)
			if out.MyCompetenceTotal.CanTeachCompulsory != 0 {
				t.Errorf("the total counts %d compulsory modules taught, want the thesis left out",
					out.MyCompetenceTotal.CanTeachCompulsory)
			}
			for _, s := range out.MyCompetenceStatus {
				if s.CanTeachCompulsory != 0 {
					t.Errorf("a subject group counts %d, want the thesis left out", s.CanTeachCompulsory)
				}
			}
		})
}

// Nothing to note down for it either.
func TestIndependentWorkTakesNoAdvanceWishThroughBothDoors(t *testing.T) {
	t.Parallel()

	f := wishHandler(t, grants{testdata.Eins, []string{"LECTURER"}})
	module := moduleIDOfFixture(t, f).String()
	makeIndependentWork(t, f.schema, module)

	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			resp := c.Do(t, setAdvanceWishMutation, map[string]any{
				"s": advanceSemester, "m": module, "p": f.programme, "prio": "FIRST_CHOICE",
			})
			if code := errorCode(t, resp); code != "MODULE_NOT_PLANNED" {
				t.Errorf("an advance wish for a thesis was refused with %s, want MODULE_NOT_PLANNED",
					code)
			}
		})
}
