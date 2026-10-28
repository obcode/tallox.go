package bootstrap_test

import (
	"strings"
	"testing"

	"github.com/obcode/tallox.go/internal/graphqltest"
	"github.com/obcode/tallox.go/internal/testdata"
)

const setDesiredRange = `mutation($s: String!, $min: Float, $max: Float, $n: String) {
	setDesiredTeachingHours(semester: $s, minHours: $min, maxHours: $max, note: $n) {
		minHours maxHours hours note correctedBy
	}
}`

const correctDesired = `mutation($s: String!, $p: ID!, $min: Float, $max: Float, $n: String) {
	correctDesiredTeachingHours(semester: $s, person: $p, minHours: $min, maxHours: $max, note: $n) {
		minHours maxHours note correctedBy
	}
}`

type desiredRow struct {
	MinHours    float64
	MaxHours    float64
	Note        string
	CorrectedBy string
	Person      struct{ ID string }
}

func desiredRows(t *testing.T, c *graphqltest.Client, semester string) map[string]desiredRow {
	t.Helper()
	var out struct{ DesiredTeachingHours []desiredRow }
	c.MustQuery(t, `query($s: String!) {
		desiredTeachingHours(semester: $s) { minHours maxHours note correctedBy person { id } }
	}`, map[string]any{"s": semester}, &out)
	seen := map[string]desiredRow{}
	for _, d := range out.DesiredTeachingHours {
		seen[d.Person.ID] = d
	}
	return seen
}

type overviewRow struct {
	Person        struct{ ID string }
	SubjectGroups []struct{ Code string }
	Stated        bool
	MinHours      *float64
	MaxHours      *float64
}

func overviewRows(t *testing.T, c *graphqltest.Client, semester string) map[string]overviewRow {
	t.Helper()
	var out struct{ DesiredHoursOverview []overviewRow }
	c.MustQuery(t, `query($s: String!) {
		desiredHoursOverview(semester: $s) {
			person { id } subjectGroups { code } stated minHours maxHours
		}
	}`, map[string]any{"s": semester}, &out)
	seen := map[string]overviewRow{}
	for _, r := range out.DesiredHoursOverview {
		seen[r.Person.ID] = r
	}
	return seen
}

// The whole process, through the interface it is run from: state, freeze, correct, publish — and
// at each step who reads what, through both doors.
func TestTheDesiredHoursProcessThroughBothDoors(t *testing.T) {
	t.Parallel()

	f := wishHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Zwei, []string{"LECTURER"}},
		grants{testdata.Fuenf, []string{"LECTURER", "DEANS_OFFICE"}},
	)
	eins := testdata.Eins.ID().String()
	deans := graphqltest.New(f.handler).AsUser(testdata.Fuenf.Mail)

	var ignored struct{}
	graphqltest.New(f.handler).AsUser(testdata.Eins.Mail).MustQuery(t, setDesiredRange,
		map[string]any{"s": f.semester, "min": 6, "max": 10, "n": "Forschungssemester im Herbst"},
		&ignored)

	// Before publication a colleague reads nothing — neither the rows, through either door, nor
	// a has-stated flag in the overview, which is the browser's alone.
	graphqltest.EachDoor(t, f.handler, testdata.Zwei.Mail, testdata.Zwei.Token,
		func(t *testing.T, c *graphqltest.Client) {
			if got := desiredRows(t, c, f.semester); len(got) != 0 {
				t.Errorf("a colleague reads %+v before publication", got)
			}
		})
	colleague := graphqltest.New(f.handler).AsUser(testdata.Zwei.Mail)
	row, listed := overviewRows(t, colleague, f.semester)[eins]
	if !listed {
		t.Fatal("the overview does not list Eins at all")
	}
	if row.Stated || row.MinHours != nil || row.MaxHours != nil {
		t.Errorf("the overview tells a colleague about Eins's figure: %+v", row)
	}
	mustRefuseWith(t, graphqltest.New(f.handler).WithToken(testdata.Zwei.Token),
		`query($s: String!) { desiredHoursOverview(semester: $s) { person { id } } }`,
		map[string]any{"s": f.semester}, "FULL_REACH_REQUIRED")

	// Publishing before the freeze is refused, and so is freezing through a token.
	mustRefuseWith(t, deans, `mutation($s: String!) { publishDesiredHours(code: $s) { code } }`,
		map[string]any{"s": f.semester}, "DESIRED_HOURS_NOT_FROZEN")
	mustRefuseWith(t, graphqltest.New(f.handler).WithToken(testdata.Fuenf.Token),
		`mutation($s: String!) { freezeDesiredHours(code: $s) { code } }`,
		map[string]any{"s": f.semester}, "FULL_REACH_REQUIRED")

	var frozen struct {
		FreezeDesiredHours struct{ DesiredHoursFrozenAt *string }
	}
	deans.MustQuery(t, `mutation($s: String!) { freezeDesiredHours(code: $s) { desiredHoursFrozenAt } }`,
		map[string]any{"s": f.semester}, &frozen)
	if frozen.FreezeDesiredHours.DesiredHoursFrozenAt == nil {
		t.Fatal("freezing left desiredHoursFrozenAt null")
	}

	// Frozen: the person is refused through either door, with a sentence that names nobody.
	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			mustRefuseWith(t, c, setDesiredRange,
				map[string]any{"s": f.semester, "min": 2, "max": 4}, "DESIRED_HOURS_FROZEN")
		})

	// The dean's office corrects, in the browser only.
	mustRefuseWith(t, graphqltest.New(f.handler).WithToken(testdata.Fuenf.Token), correctDesired,
		map[string]any{"s": f.semester, "p": eins, "min": 8, "max": 12}, "FORBIDDEN")
	var corrected struct {
		CorrectDesiredTeachingHours *desiredRow
	}
	deans.MustQuery(t, correctDesired,
		map[string]any{"s": f.semester, "p": eins, "min": 8, "max": 12, "n": "abgesprochen"}, &corrected)
	if c := corrected.CorrectDesiredTeachingHours; c == nil || c.MinHours != 8 || c.MaxHours != 12 ||
		c.CorrectedBy == "" {
		t.Fatalf("the correction answered %+v", corrected.CorrectDesiredTeachingHours)
	}

	var published struct {
		PublishDesiredHours struct{ DesiredHoursPublishedAt *string }
	}
	deans.MustQuery(t, `mutation($s: String!) { publishDesiredHours(code: $s) { desiredHoursPublishedAt } }`,
		map[string]any{"s": f.semester}, &published)
	if published.PublishDesiredHours.DesiredHoursPublishedAt == nil {
		t.Fatal("publishing left desiredHoursPublishedAt null")
	}

	// Published: the range is everybody's in the browser, the note and who corrected it are not.
	// The doors differ on purpose: through a token a colleague's figure stays out of reach, so
	// that the faculty's teaching plans are not one script call away.
	published0, ok := desiredRows(t, colleague, f.semester)[eins]
	if !ok || published0.MinHours != 8 || published0.MaxHours != 12 {
		t.Errorf("after publication a colleague reads %+v, want 8–12", published0)
	}
	if published0.Note != "" || published0.CorrectedBy != "" {
		t.Errorf("a colleague reads the note %q and the corrector %q", published0.Note, published0.CorrectedBy)
	}
	over := overviewRows(t, colleague, f.semester)[eins]
	if !over.Stated || over.MaxHours == nil || *over.MaxHours != 12 {
		t.Errorf("the overview after publication reads %+v", over)
	}
	byToken := graphqltest.New(f.handler).WithToken(testdata.Zwei.Token)
	if got := desiredRows(t, byToken, f.semester); len(got) != 0 {
		t.Errorf("after publication a token reads %+v of somebody else", got)
	}
	mustRefuseWith(t, byToken,
		`query($s: String!) { desiredHoursOverview(semester: $s) { person { id } } }`,
		map[string]any{"s": f.semester}, "FULL_REACH_REQUIRED")

	// The person sees their own note and who corrected it, through either door.
	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			own := desiredRows(t, c, f.semester)[eins]
			if own.Note != "abgesprochen" || own.CorrectedBy == "" {
				t.Errorf("the person reads their own figure as %+v", own)
			}
		})
}

// The write path says nothing about anybody else: not who is in the table, not what the database
// thinks of the input.
func TestDesiredHoursRefusalsLeakNothing(t *testing.T) {
	t.Parallel()

	f := wishHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Zwei, []string{"LECTURER"}},
		grants{testdata.Fuenf, []string{"DEANS_OFFICE"}},
	)
	var ignored struct{}
	graphqltest.New(f.handler).AsUser(testdata.Zwei.Mail).MustQuery(t, setDesiredRange,
		map[string]any{"s": f.semester, "min": 4, "max": 6}, &ignored)

	c := graphqltest.New(f.handler).AsUser(testdata.Eins.Mail)
	forbidden := append(graphqltest.DatabaseNoise(), testdata.Mails(testdata.Others(testdata.Eins))...)
	for _, tc := range []struct {
		vars map[string]any
		code string
	}{
		{map[string]any{"s": f.semester, "min": 12, "max": 10}, "DESIRED_HOURS_RANGE"},
		{map[string]any{"s": f.semester, "min": 41}, "DESIRED_HOURS_OUT_OF_RANGE"},
	} {
		resp := c.Do(t, setDesiredRange, tc.vars)
		if !resp.Failed() || resp.Errors[0].Extensions["code"] != tc.code {
			t.Errorf("%v answered %s, want %s", tc.vars, resp.Body, tc.code)
			continue
		}
		graphqltest.AssertNoLeak(t, strings.Join(resp.Messages(), " "), forbidden...)
	}

	// A lecturer correcting somebody else's figure is refused without learning whether there is one.
	resp := c.Do(t, correctDesired, map[string]any{"s": f.semester, "p": testdata.Zwei.ID().String(),
		"min": 1, "max": 2})
	if !resp.Failed() || resp.Errors[0].Extensions["code"] != "FORBIDDEN" {
		t.Errorf("a lecturer correcting a colleague answered %s, want FORBIDDEN", resp.Body)
	}
	graphqltest.AssertNoLeak(t, strings.Join(resp.Messages(), " "), forbidden...)
}
