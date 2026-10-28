package bootstrap_test

import (
	"strings"
	"testing"

	"github.com/obcode/tallox.go/internal/graphqltest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// Whether a cohort is listed as a course in English (CiE), through the API. An explicit listing,
// independent of the language the cohort runs in, and written under the same rule as the format:
// both leads and the dean's office, nobody else, and not once the semester is over. Read by
// everybody through every door, because it is planning rather than anything about a person.

const setCiEMutation = `mutation($id: ID!, $cie: Boolean!) {
	setCourseInstanceCiE(id: $id, cie: $cie) { id cie taughtInEnglish }
}`

type cieOut struct {
	ID              string
	Cie             bool
	TaughtInEnglish bool
}

func TestBothLeadsAndTheDeansOfficeListACohortAsCiE(t *testing.T) {
	t.Parallel()

	f := assignmentHandler(t,
		grants{testdata.Drei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
		grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}},
		grants{testdata.Fuenf, []string{"DEANS_OFFICE"}},
	)
	f.leadGroup(t, testdata.Drei, f.group)
	f.leadProgramme(t, testdata.Vier, f.programme)

	for _, step := range []struct {
		who testdata.Persona
		cie bool
	}{
		{testdata.Drei, true},
		{testdata.Vier, false},
		{testdata.Fuenf, true},
	} {
		var out struct{ SetCourseInstanceCiE cieOut }
		graphqltest.New(f.handler).AsUser(step.who.Mail).MustQuery(t, setCiEMutation,
			map[string]any{"id": f.instance, "cie": step.cie}, &out)
		// The listing leaves the language alone: the fixture's module is German, and stays so.
		if got := out.SetCourseInstanceCiE; got.Cie != step.cie || got.TaughtInEnglish {
			t.Errorf("%s set CiE %v and got %+v", step.who.Name, step.cie, got)
		}
	}
}

func TestOnlyTheResponsibleListACohortAsCiE(t *testing.T) {
	t.Parallel()

	f := assignmentHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Zwei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
		grants{testdata.Drei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
	)
	// Zwei leads a subject group, just not this module's.
	f.leadGroup(t, testdata.Zwei, f.other)
	f.leadGroup(t, testdata.Drei, f.group)

	vars := map[string]any{"id": f.instance, "cie": true}
	for _, who := range []testdata.Persona{testdata.Eins, testdata.Zwei} {
		resp := graphqltest.New(f.handler).AsUser(who.Mail).Do(t, setCiEMutation, vars)
		assertRefusal(t, resp, "NOT_YOUR_SUBJECT")
		graphqltest.AssertNoLeak(t, strings.Join(resp.Messages(), " "),
			graphqltest.DatabaseNoise()...)
	}

	// A plain token is refused even for somebody who may do this in the browser; a full-reach
	// token of the same person may.
	resp := graphqltest.New(f.handler).WithToken(testdata.Drei.Token).Do(t, setCiEMutation, vars)
	assertRefusal(t, resp, "FULL_REACH_REQUIRED")
	var unlisted struct{ SetCourseInstanceCiE cieOut }
	graphqltest.New(f.handler).WithToken(fullReachToken(t, f.schema, testdata.Drei)).
		On(graphqltest.Token).MustQuery(t, setCiEMutation,
		map[string]any{"id": f.instance, "cie": false}, &unlisted)
	if unlisted.SetCourseInstanceCiE.Cie {
		t.Errorf("a full-reach token could not take the cohort off the list")
	}

	// A finished semester refuses the lead too.
	if _, err := f.schema.Pool.Exec(t.Context(),
		`UPDATE semester SET phase = 'FINAL' WHERE code = $1`, f.semester); err != nil {
		t.Fatalf("cannot finish the semester: %v", err)
	}
	resp = graphqltest.New(f.handler).AsUser(testdata.Drei.Mail).Do(t, setCiEMutation, vars)
	assertRefusal(t, resp, "ASSIGNMENT_PHASE_CLOSED")

	// None of the refusals listed it.
	var read struct{ CourseInstance struct{ Cie bool } }
	graphqltest.New(f.handler).AsUser(testdata.Eins.Mail).MustQuery(t,
		`query($id: ID!) { courseInstance(id: $id) { cie } }`,
		map[string]any{"id": f.instance}, &read)
	if read.CourseInstance.Cie {
		t.Errorf("a refused write listed the cohort")
	}
}

func TestTheCiEListingIsReadableThroughEveryDoor(t *testing.T) {
	t.Parallel()

	f := assignmentHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Drei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
	)
	f.leadGroup(t, testdata.Drei, f.group)

	graphqltest.New(f.handler).AsUser(testdata.Drei.Mail).MustQuery(t, setCiEMutation,
		map[string]any{"id": f.instance, "cie": true}, &struct{ SetCourseInstanceCiE cieOut }{})

	read := func(t *testing.T, c *graphqltest.Client) {
		t.Helper()
		var out struct{ CourseInstances []cieOut }
		c.MustQuery(t, `query($s: String!, $p: String) {
			courseInstances(semester: $s, programme: $p) { id cie taughtInEnglish }
		}`, map[string]any{"s": f.semester, "p": f.programme}, &out)
		if len(out.CourseInstances) != 1 || !out.CourseInstances[0].Cie {
			t.Errorf("a lecturer reads %+v, want one cohort listed as CiE", out.CourseInstances)
		}
	}
	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token, read)
	t.Run("full reach", func(t *testing.T) {
		read(t, graphqltest.New(f.handler).WithToken(fullReachToken(t, f.schema, testdata.Eins)).
			On(graphqltest.Token))
	})
}
