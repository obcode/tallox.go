package bootstrap_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/obcode/tallox.go/internal/graphqltest"
	"github.com/obcode/tallox.go/internal/testdata"
)

const bigCourseQuery = `query($y: Int!) {
	bigCourseOverview(studyYear: $y) {
		studyYear provisional
		rows {
			professor { personId teacherId name }
			winterHours summerHours averageHours target actual status
			courses { semester moduleName programme { code } teachingHours reason }
		}
	}
}`

type bigCourseOverview struct {
	StudyYear   int
	Provisional bool
	Rows        []struct {
		Professor struct {
			PersonID  *string
			TeacherID *string
			Name      string
		}
		WinterHours, SummerHours, AverageHours float64
		Target, Actual                         int
		Status                                 string
		Courses                                []struct {
			Semester      string
			ModuleName    string
			Programme     struct{ Code string }
			TeachingHours float64
			Reason        string
		}
	}
}

func readBigCourses(t *testing.T, c *graphqltest.Client) bigCourseOverview {
	t.Helper()
	var out struct{ BigCourseOverview bigCourseOverview }
	c.MustQuery(t, bigCourseQuery, map[string]any{"y": 2028}, &out)
	return out.BigCourseOverview
}

func bigCourseHolders(o bigCourseOverview) string {
	ids := []string{}
	for _, r := range o.Rows {
		if r.Professor.PersonID != nil {
			ids = append(ids, *r.Professor.PersonID)
		} else {
			ids = append(ids, "no account")
		}
	}
	sort.Strings(ids)
	return strings.Join(ids, ",")
}

// The overview through every door: the leads and the dean's office read every professor in the
// browser and with a full-reach token, already during the planning; a plain token, whoever holds
// it, reads its owner's row; a colleague reads their own.
func TestTheBigCourseOverviewThroughEveryDoor(t *testing.T) {
	t.Parallel()

	f := assignmentHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Zwei, []string{"LECTURER"}},
		grants{testdata.Drei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
		grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}},
		grants{testdata.Fuenf, []string{"DEANS_OFFICE"}},
	)
	f.leadGroup(t, testdata.Drei, f.group)
	f.leadProgramme(t, testdata.Vier, f.programme)
	// Zwei is a professor as well, holding nothing.
	if _, err := f.schema.Pool.Exec(t.Context(),
		`INSERT INTO teacher (mail, full_name, short_name, is_professor, zpa_teacher_ref)
		 VALUES ($1, 'Zwei', 'Zwei, Prof.', true, 9902)`, testdata.Zwei.Mail); err != nil {
		t.Fatalf("cannot record Zwei as a professor: %v", err)
	}
	// Eins, a professor, holds the lecture of the ordinary compulsory module's single cohort.
	f.fill(t, testdata.Drei, f.lecture, testdata.Eins)

	eins, zwei := testdata.Eins.ID().String(), testdata.Zwei.ID().String()
	everybody := strings.Join(sortedStrings(eins, zwei), ",")

	for _, lead := range []testdata.Persona{testdata.Drei, testdata.Vier, testdata.Fuenf} {
		browser := readBigCourses(t, graphqltest.New(f.handler).AsUser(lead.Mail))
		if got := bigCourseHolders(browser); got != everybody {
			t.Errorf("%s reads %s in the browser, want every professor %s", lead.Name, got, everybody)
		}
		full := readBigCourses(t, graphqltest.New(f.handler).WithToken(fullReachToken(t, f.schema, lead)))
		if got := bigCourseHolders(full); got != everybody {
			t.Errorf("%s reads %s with a full-reach token, want what the browser reads", lead.Name, got)
		}
		plain := readBigCourses(t, graphqltest.New(f.handler).WithToken(lead.Token))
		if len(plain.Rows) != 0 {
			t.Errorf("%s, no professor, reads %d rows with a plain token, want none", lead.Name, len(plain.Rows))
		}
	}

	o := readBigCourses(t, graphqltest.New(f.handler).AsUser(testdata.Fuenf.Mail))
	if o.StudyYear != 2028 || !o.Provisional {
		t.Errorf("study year %d, provisional %v; want 2028 and provisional, the summer is not assigned",
			o.StudyYear, o.Provisional)
	}
	for _, r := range o.Rows {
		if r.Professor.PersonID == nil || *r.Professor.PersonID != eins {
			continue
		}
		if r.WinterHours != 2 || r.AverageHours != 1 || r.Target != 0 || r.Actual != 1 ||
			r.Status != "NOT_APPLICABLE" || len(r.Courses) != 1 || r.Courses[0].Reason != "BY_COHORT" ||
			r.Courses[0].Programme.Code != f.programme || r.Courses[0].Semester != f.semester {
			t.Errorf("Eins's row reads %+v", r)
		}
	}

	// A colleague reads their own row, through either door, and nothing of Eins's.
	graphqltest.EachDoor(t, f.handler, testdata.Zwei.Mail, testdata.Zwei.Token,
		func(t *testing.T, c *graphqltest.Client) {
			got := readBigCourses(t, c)
			if bigCourseHolders(got) != zwei || got.Rows[0].WinterHours != 0 {
				t.Errorf("Zwei reads %+v, want only their own empty row", got.Rows)
			}
		})
	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			got := readBigCourses(t, c)
			if bigCourseHolders(got) != eins || got.Rows[0].Actual != 1 {
				t.Errorf("Eins reads %+v, want their own row with one big course", got.Rows)
			}
		})

	// A study year out of reach is refused, not answered empty.
	resp := graphqltest.New(f.handler).AsUser(testdata.Fuenf.Mail).
		Do(t, bigCourseQuery, map[string]any{"y": 1990})
	if len(resp.Messages()) == 0 || hasCode(resp, "INTERNAL") {
		t.Errorf("a study year far in the past was answered with %v, want a refusal with a code",
			resp.Messages())
	}
}

func sortedStrings(s ...string) []string {
	sort.Strings(s)
	return s
}
