package bootstrap_test

import (
	"testing"

	"github.com/obcode/tallox.go/internal/graphqltest"
	"github.com/obcode/tallox.go/internal/testdata"
)

const assignedLoadQuery = `query($s: String!) {
	assignedTeachingHours(semester: $s) {
		person { id } total bySubjectGroup { subjectGroup { code } hours }
	}
}`

type loadRow struct {
	Person         struct{ ID string }
	Total          float64
	BySubjectGroup []struct {
		SubjectGroup *struct{ Code string }
		Hours        float64
	}
}

func assignedLoads(t *testing.T, c *graphqltest.Client, semester string) map[string]loadRow {
	t.Helper()
	var out struct{ AssignedTeachingHours []loadRow }
	c.MustQuery(t, assignedLoadQuery, map[string]any{"s": semester}, &out)
	seen := map[string]loadRow{}
	for _, l := range out.AssignedTeachingHours {
		seen[l.Person.ID] = l
	}
	return seen
}

// The exception, through the interface: the lead of SWE2 reads that a member of SWE2 already holds
// two hours in MATHE2 — and not the assignment itself. Through a token the same lead reads only
// their own; a colleague reads nothing until the assignments are published, then through either
// door.
func TestTheAssignedLoadThroughBothDoors(t *testing.T) {
	t.Parallel()

	f := assignmentHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Zwei, []string{"LECTURER"}},
		grants{testdata.Vier, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
		grants{testdata.Fuenf, []string{"DEANS_OFFICE"}},
	)
	f.leadGroup(t, testdata.Vier, f.other)
	for _, group := range []any{f.group, f.other} {
		if _, err := f.schema.Pool.Exec(t.Context(),
			`INSERT INTO person_subject_group (person_id, subject_group_id) VALUES ($1, $2)`,
			testdata.Eins.ID(), group); err != nil {
			t.Fatalf("cannot put Eins into a group: %v", err)
		}
	}
	f.fill(t, testdata.Fuenf, f.lecture, testdata.Eins)
	eins := testdata.Eins.ID().String()

	lead := graphqltest.New(f.handler).AsUser(testdata.Vier.Mail)
	load, ok := assignedLoads(t, lead, f.semester)[eins]
	if !ok || load.Total != 2 || len(load.BySubjectGroup) != 1 ||
		load.BySubjectGroup[0].SubjectGroup == nil || load.BySubjectGroup[0].SubjectGroup.Code != "MATHE2" {
		t.Errorf("the lead of SWE2 reads Eins's load as %+v, want MATHE2 2", load)
	}
	if got := held(t, lead, f.semester); len(got) != 0 {
		t.Errorf("the lead of SWE2 reads the MATHE2 assignment itself: %v", got)
	}

	// The overview carries the same figure, by the same rule.
	over := overviewRows(t, lead, f.semester)
	if row := over[eins]; row.Person.ID == "" {
		t.Error("the overview does not list Eins")
	}
	var withLoad struct {
		DesiredHoursOverview []struct {
			Person       struct{ ID string }
			AssignedLoad *struct{ Total float64 }
		}
	}
	lead.MustQuery(t, `query($s: String!) {
		desiredHoursOverview(semester: $s) { person { id } assignedLoad { total } }
	}`, map[string]any{"s": f.semester}, &withLoad)
	found := false
	for _, r := range withLoad.DesiredHoursOverview {
		if r.Person.ID == eins {
			found = r.AssignedLoad != nil && r.AssignedLoad.Total == 2
		}
	}
	if !found {
		t.Errorf("the overview does not carry Eins's load for the lead: %+v", withLoad)
	}

	if got := assignedLoads(t, graphqltest.New(f.handler).WithToken(testdata.Vier.Token),
		f.semester); len(got) != 0 {
		t.Errorf("the lead reads %+v through a token", got)
	}

	graphqltest.EachDoor(t, f.handler, testdata.Zwei.Mail, testdata.Zwei.Token,
		func(t *testing.T, c *graphqltest.Client) {
			if got := assignedLoads(t, c, f.semester); len(got) != 0 {
				t.Errorf("a colleague reads %+v before publication", got)
			}
		})

	// The person reads their own, through either door.
	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			if got := assignedLoads(t, c, f.semester)[eins]; got.Total != 2 {
				t.Errorf("Eins reads their own load as %+v", got)
			}
		})

	f.publish(t)
	graphqltest.EachDoor(t, f.handler, testdata.Zwei.Mail, testdata.Zwei.Token,
		func(t *testing.T, c *graphqltest.Client) {
			if got := assignedLoads(t, c, f.semester)[eins]; got.Total != 2 {
				t.Errorf("after publication a colleague reads %+v, want 2", got)
			}
		})
}
