package bootstrap_test

import (
	"testing"

	"github.com/obcode/tallox.go/internal/graphqltest"
	"github.com/obcode/tallox.go/internal/testdata"
)

const setDesired = `mutation($s: String!, $h: Float, $n: String) {
	setDesiredTeachingHours(semester: $s, hours: $h, note: $n) { hours note person { id } }
}`

func desiredSeen(t *testing.T, c *graphqltest.Client, semester string) map[string]float64 {
	t.Helper()
	var out struct {
		DesiredTeachingHours []struct {
			Hours  float64
			Person struct{ ID string }
		}
	}
	c.MustQuery(t, `query($s: String!) {
		desiredTeachingHours(semester: $s) { hours person { id } }
	}`, map[string]any{"s": semester}, &out)
	seen := map[string]float64{}
	for _, d := range out.DesiredTeachingHours {
		seen[d.Person.ID] = d.Hours
	}
	return seen
}

// One's own figure, written and read through either door — it is one's own data, like a wish.
func TestDesiredHoursAreOnesOwnThroughBothDoors(t *testing.T) {
	t.Parallel()

	f := wishHandler(t, grants{testdata.Eins, []string{"LECTURER"}})

	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var set struct {
				SetDesiredTeachingHours *struct {
					Hours float64
					Note  string
				}
			}
			c.MustQuery(t, setDesired, map[string]any{"s": f.semester, "h": 12.5, "n": "gern Block"}, &set)
			if set.SetDesiredTeachingHours == nil || set.SetDesiredTeachingHours.Hours != 12.5 {
				t.Fatalf("stating 12.5 hours answered %+v", set.SetDesiredTeachingHours)
			}

			var mine struct {
				MyDesiredTeachingHours []struct {
					Semester string
					Hours    float64
				}
			}
			c.MustQuery(t, `{ myDesiredTeachingHours { semester hours } }`, nil, &mine)
			if len(mine.MyDesiredTeachingHours) != 1 || mine.MyDesiredTeachingHours[0].Semester != f.semester {
				t.Errorf("my own figures across semesters read %+v", mine.MyDesiredTeachingHours)
			}

			c.MustQuery(t, setDesired, map[string]any{"s": f.semester, "h": nil}, &set)
			if set.SetDesiredTeachingHours != nil {
				t.Errorf("taking it back answered %+v, want null", set.SetDesiredTeachingHours)
			}
		})
}

// The lead of the person's subject group reads the figure in the browser and not through a token;
// a colleague reads nothing of it, and asking is an empty list rather than a refusal.
func TestDesiredHoursAreReadByTheLeadInTheBrowserOnly(t *testing.T) {
	t.Parallel()

	f := wishHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Zwei, []string{"LECTURER"}},
		grants{testdata.Drei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
	)
	f.leadGroup(t, testdata.Drei, f.group)
	if _, err := f.schema.Pool.Exec(t.Context(),
		`INSERT INTO person_subject_group (person_id, subject_group_id) VALUES ($1, $2)`,
		testdata.Eins.ID(), f.group); err != nil {
		t.Fatalf("cannot put Eins into the group: %v", err)
	}

	var ignored struct{}
	graphqltest.New(f.handler).AsUser(testdata.Eins.Mail).
		MustQuery(t, setDesired, map[string]any{"s": f.semester, "h": 14}, &ignored)

	if got := desiredSeen(t, graphqltest.New(f.handler).AsUser(testdata.Drei.Mail), f.semester); got[testdata.Eins.ID().String()] != 14 {
		t.Errorf("the lead of Eins's group sees %v in the browser, want Eins's 14", got)
	}
	if got := desiredSeen(t, graphqltest.New(f.handler).WithToken(testdata.Drei.Token), f.semester); len(got) != 0 {
		t.Errorf("the same lead sees %v through a token, want nothing", got)
	}
	if got := desiredSeen(t, graphqltest.New(f.handler).AsUser(testdata.Zwei.Mail), f.semester); len(got) != 0 {
		t.Errorf("a colleague sees %v, want nothing", got)
	}
}

// The refusals the interface branches on, by code.
func TestDesiredHoursRefusalsHaveCodes(t *testing.T) {
	t.Parallel()

	f := wishHandler(t, grants{testdata.Eins, []string{"LECTURER"}})
	c := graphqltest.New(f.handler).AsUser(testdata.Eins.Mail)

	mustRefuseWith(t, c, setDesired, map[string]any{"s": f.semester, "h": 7.3}, "DESIRED_HOURS_OUT_OF_RANGE")

	if _, err := f.schema.Pool.Exec(t.Context(),
		`UPDATE semester SET phase = 'FINAL' WHERE code = $1`, f.semester); err != nil {
		t.Fatalf("cannot close the semester: %v", err)
	}
	mustRefuseWith(t, c, setDesired, map[string]any{"s": f.semester, "h": 8}, "DESIRED_HOURS_PHASE_CLOSED")
}

func mustRefuseWith(t *testing.T, c *graphqltest.Client, query string, variables map[string]any, code string) {
	t.Helper()
	resp := c.Do(t, query, variables)
	if !resp.Failed() {
		t.Fatalf("expected %s, the call succeeded: %s", code, resp.Body)
	}
	if got := resp.Errors[0].Extensions["code"]; got != code {
		t.Errorf("refused with %v, want %s: %s", got, code, resp.Body)
	}
}
