package bootstrap_test

import (
	"encoding/json"
	"testing"

	"github.com/obcode/tallox.go/internal/graphqltest"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// Merging cohorts after the wish round, through the API.
//
// Browser door only, and that is the door rule rather than a gap: every demand mutation is
// @interactiveOnly (TestATokenCannotProbeWhichInstancesAreWishedFor), so there is no token path to
// assert the same thing on.

const wishesWithTrackQuery = `query($s: String!) {
	wishes(semester: $s) { person { mail } instance { track } }
}`

// The case from the report: a programme lead adds a second cohort after the wish round, people
// register interest in both, and the lead merges them back into one — in the assignment, with the
// wish round long closed. Nothing is refused and nobody's interest is lost.
func TestAProgrammeLeadMergesCohortsAfterTheWishRound(t *testing.T) {
	t.Parallel()

	f := wishHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Zwei, []string{"LECTURER"}},
		grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}},
	)
	f.leadProgramme(t, testdata.Vier, f.programme)
	lead := graphqltest.New(f.handler).AsUser(testdata.Vier.Mail)
	module := moduleIDOf(t, f.schema, storetest.FixtureModuleOrdinary).String()

	plan := func(tracks ...string) planReport {
		t.Helper()
		entries := make([]map[string]any, 0, len(tracks))
		for _, tr := range tracks {
			entries = append(entries, map[string]any{"track": tr, "groups": 1})
		}
		var out planReport
		lead.MustQuery(t, planMutation, map[string]any{
			"s": f.semester, "p": f.programme, "d": false,
			"e": []map[string]any{{"moduleId": module, "tracks": entries}},
		}, &out)
		return out
	}

	// Eins registers on the single cohort; it becomes A when B is added beside it.
	f.register(t, testdata.Eins)
	plan("A", "B")

	var b string
	if err := f.schema.Pool.QueryRow(t.Context(),
		`SELECT id::text FROM course_instance WHERE track = 'B'`).Scan(&b); err != nil {
		t.Fatalf("cannot find cohort B: %v", err)
	}
	var registered struct{ SetWish struct{ ID string } }
	graphqltest.New(f.handler).AsUser(testdata.Zwei.Mail).MustQuery(t, setWishMutation,
		map[string]any{"p": b, "prio": "HAPPY_TO", "note": nil}, &registered)

	// The wish round is over; the faculty is assigning.
	if _, err := f.schema.Pool.Exec(t.Context(),
		`UPDATE semester SET phase = 'ASSIGNMENT' WHERE code = $1`, f.semester); err != nil {
		t.Fatalf("cannot move the semester on: %v", err)
	}

	report := plan("A")
	if len(report.PlanDemand.Refused) != 0 {
		t.Fatalf("merging after the wish round was refused: %+v", report.PlanDemand.Refused)
	}
	if w := report.PlanDemand.Withdrawn; len(w) != 1 || w[0].Track != "B" {
		t.Errorf("merging reports %+v withdrawn, want B", w)
	}

	// The report says what happened to the demand and nothing about who wanted it: not a name,
	// not a count of moved wishes.
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("cannot render the report: %v", err)
	}
	graphqltest.AssertNoLeak(t, string(raw),
		append(graphqltest.DatabaseNoise(), testdata.Mails(testdata.Others(testdata.Vier))...)...)

	// Both wishes are on A now, as the lead — who may read them — sees them.
	var out struct {
		Wishes []struct {
			Person   struct{ Mail string }
			Instance struct{ Track string }
		}
	}
	lead.MustQuery(t, wishesWithTrackQuery, map[string]any{"s": f.semester}, &out)
	if len(out.Wishes) != 2 {
		t.Fatalf("the lead sees %d wishes after the merge, want both", len(out.Wishes))
	}
	for _, w := range out.Wishes {
		if w.Instance.Track != "A" {
			t.Errorf("%s's wish is on cohort %q after the merge, want A", w.Person.Mail, w.Instance.Track)
		}
	}
}
