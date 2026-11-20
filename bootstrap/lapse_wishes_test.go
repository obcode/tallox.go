package bootstrap_test

import (
	"slices"
	"testing"

	"github.com/obcode/tallox.go/internal/graphqltest"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// Withdrawing a cohort whose wishes lapse (planDemand with dropWishes), and the refusal that says
// what is in the way — through all three doors.

const planDemandDrop = `mutation($s: String!, $p: String!, $m: ID!, $drop: Boolean, $dry: Boolean!) {
  planDemand(semester: $s, programme: $p, dryRun: $dry,
             entries: [{moduleId: $m, tracks: [], dropWishes: $drop}]) {
    withdrawn { track }
    refused { code message cause }
  }
}`

type planDemandDropResult struct {
	PlanDemand struct {
		Withdrawn []struct{ Track string }
		Refused   []struct {
			Code    string
			Message string
			Cause   *string
		}
	}
}

func (f assignmentAPIFixture) moduleOfInstance(t *testing.T) string {
	t.Helper()
	var id string
	if err := f.schema.Pool.QueryRow(t.Context(),
		`SELECT module_id::text FROM course_instance WHERE id = $1::uuid`, f.instance).Scan(&id); err != nil {
		t.Fatalf("cannot read the module: %v", err)
	}
	return id
}

func (f assignmentAPIFixture) instanceExists(t *testing.T) bool {
	t.Helper()
	var exists bool
	if err := f.schema.Pool.QueryRow(t.Context(),
		`SELECT EXISTS (SELECT 1 FROM course_instance WHERE id = $1::uuid)`, f.instance).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	return exists
}

// A plain token gets neither the cause nor the withdrawal; a full-reach token and the browser get
// both — and the refusal names the kind of thing, never the person.
func TestDropWishesThroughEveryDoor(t *testing.T) {
	t.Parallel()

	f := assignmentHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Vier, []string{"PROGRAMME_LEAD"}},
	)
	f.leadProgramme(t, testdata.Vier, f.programme)
	f.wishFor(t, testdata.Eins)
	module := f.moduleOfInstance(t)

	vars := func(drop, dry bool) map[string]any {
		return map[string]any{"s": f.semester, "p": f.programme, "m": module, "drop": drop, "dry": dry}
	}

	plain := graphqltest.New(f.handler).WithToken(testdata.Vier.Token).On(graphqltest.Token)
	assertRefusal(t, plain.Do(t, planDemandDrop, vars(true, false)), "FULL_REACH_REQUIRED")
	if !f.instanceExists(t) {
		t.Fatal("a plain token withdrew the cohort")
	}

	full := graphqltest.New(f.handler).WithToken(fullReachToken(t, f.schema, testdata.Vier)).
		On(graphqltest.Token)
	browser := graphqltest.New(f.handler).AsUser(testdata.Vier.Mail)

	for _, door := range []struct {
		name string
		c    *graphqltest.Client
	}{{"full reach token", full}, {"browser", browser}} {
		c := door.c
		t.Run(door.name, func(t *testing.T) {
			var refused planDemandDropResult
			c.MustQuery(t, planDemandDrop, vars(false, true), &refused)
			r := refused.PlanDemand.Refused
			if len(r) != 1 || r[0].Code != "INSTANCE_IN_USE" || r[0].Cause == nil || *r[0].Cause != "WISHES" {
				t.Fatalf("without dropWishes: refused %+v, want INSTANCE_IN_USE with cause WISHES", r)
			}
			graphqltest.AssertNoLeak(t, r[0].Message,
				append(graphqltest.DatabaseNoise(), testdata.Mails(testdata.Others(testdata.Vier))...)...)
			// The pairing: whoever is told "wishes" reads them.
			if got := wishers(t, c, f.semester); !slices.Contains(got, testdata.Eins.Mail) {
				t.Errorf("the cause reached a caller that cannot read the wishes: %v", got)
			}

			var dry planDemandDropResult
			c.MustQuery(t, planDemandDrop, vars(true, true), &dry)
			if len(dry.PlanDemand.Withdrawn) != 1 || len(dry.PlanDemand.Refused) != 0 {
				t.Errorf("dry run with dropWishes reports %+v, want one withdrawn", dry.PlanDemand)
			}
		})
	}

	if !f.instanceExists(t) {
		t.Fatal("a dry run withdrew the cohort")
	}

	var saved planDemandDropResult
	browser.MustQuery(t, planDemandDrop, vars(true, false), &saved)
	if len(saved.PlanDemand.Withdrawn) != 1 || f.instanceExists(t) {
		t.Fatalf("saving with dropWishes reports %+v, want the cohort gone", saved.PlanDemand)
	}
	var lapsed int
	if err := f.schema.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM lapsed_wish WHERE person_id = $1`, testdata.Eins.ID()).Scan(&lapsed); err != nil {
		t.Fatal(err)
	}
	if lapsed != 1 {
		t.Errorf("%d lapsed wishes recorded for Eins, want 1", lapsed)
	}
}

// Nobody who is not allowed to write the programme's demand learns a cause: the lead of another
// programme, with full reach, is refused for the programme and told nothing about the cohort.
func TestTheCauseReachesNobodyOutsideTheProgramme(t *testing.T) {
	t.Parallel()

	f := assignmentHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Zwei, []string{"PROGRAMME_LEAD"}},
	)
	f.leadProgramme(t, testdata.Zwei, storetest.FixtureProgrammeB)
	f.wishFor(t, testdata.Eins)

	other := graphqltest.New(f.handler).AsUser(testdata.Zwei.Mail)
	resp := other.Do(t, planDemandDrop, map[string]any{
		"s": f.semester, "p": f.programme, "m": f.moduleOfInstance(t), "drop": true, "dry": true,
	})
	assertRefusal(t, resp, "NOT_YOUR_PROGRAMME")
	if !f.instanceExists(t) {
		t.Fatal("the lead of another programme withdrew the cohort")
	}
}
