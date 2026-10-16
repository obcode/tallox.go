package bootstrap_test

import (
	"strings"
	"testing"

	"github.com/obcode/tallox.go/internal/graphqltest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// How a cohort is taught — in a block or across the semester, in English or not — through the
// API. Set where the cohort is staffed, so it follows the assignment's rule: both leads and the
// dean's office may write it, nobody else, and not once the semester is over. Read by everybody
// through both doors, because it is planning rather than anything about a person.

const setFormatMutation = `mutation($id: ID!, $d: DeliveryFormat!, $en: Boolean) {
	setCourseInstanceFormat(id: $id, delivery: $d, taughtInEnglish: $en) {
		id delivery taughtInEnglish taughtInEnglishOverridden
	}
}`

type formatOut struct {
	ID                        string
	Delivery                  string
	TaughtInEnglish           bool
	TaughtInEnglishOverridden bool
}

func TestBothLeadsAndTheDeansOfficeSayHowACohortIsTaught(t *testing.T) {
	t.Parallel()

	f := assignmentHandler(t,
		grants{testdata.Drei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
		grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}},
		grants{testdata.Fuenf, []string{"DEANS_OFFICE"}},
	)
	f.leadGroup(t, testdata.Drei, f.group)
	f.leadProgramme(t, testdata.Vier, f.programme)

	for _, step := range []struct {
		who      testdata.Persona
		delivery string
		english  any
		want     formatOut
	}{
		{testdata.Drei, "BLOCK", true, formatOut{Delivery: "BLOCK", TaughtInEnglish: true, TaughtInEnglishOverridden: true}},
		{testdata.Vier, "WEEKLY", true, formatOut{Delivery: "WEEKLY", TaughtInEnglish: true, TaughtInEnglishOverridden: true}},
		// null hands the language back to the catalogue, which says German for this module.
		{testdata.Fuenf, "BLOCK", nil, formatOut{Delivery: "BLOCK", TaughtInEnglish: false, TaughtInEnglishOverridden: false}},
	} {
		var out struct{ SetCourseInstanceFormat formatOut }
		graphqltest.New(f.handler).AsUser(step.who.Mail).MustQuery(t, setFormatMutation,
			map[string]any{"id": f.instance, "d": step.delivery, "en": step.english}, &out)
		got := out.SetCourseInstanceFormat
		got.ID = ""
		if got != step.want {
			t.Errorf("%s set %s/%v and got %+v, want %+v", step.who.Name, step.delivery,
				step.english, got, step.want)
		}
	}
}

func TestOnlyTheResponsibleSayHowACohortIsTaught(t *testing.T) {
	t.Parallel()

	f := assignmentHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Zwei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
		grants{testdata.Drei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
	)
	// Zwei leads a subject group, just not this module's.
	f.leadGroup(t, testdata.Zwei, f.other)
	f.leadGroup(t, testdata.Drei, f.group)

	vars := map[string]any{"id": f.instance, "d": "BLOCK", "en": true}
	for _, who := range []testdata.Persona{testdata.Eins, testdata.Zwei} {
		resp := graphqltest.New(f.handler).AsUser(who.Mail).Do(t, setFormatMutation, vars)
		assertRefusal(t, resp, "NOT_YOUR_SUBJECT")
		graphqltest.AssertNoLeak(t, strings.Join(resp.Messages(), " "),
			graphqltest.DatabaseNoise()...)
	}

	// The token door is refused even for somebody who may do this in the browser.
	resp := graphqltest.New(f.handler).WithToken(testdata.Drei.Token).Do(t, setFormatMutation, vars)
	assertRefusal(t, resp, "INTERACTIVE_ONLY")

	// A finished semester refuses the lead too.
	if _, err := f.schema.Pool.Exec(t.Context(),
		`UPDATE semester SET phase = 'FINAL' WHERE code = $1`, f.semester); err != nil {
		t.Fatalf("cannot finish the semester: %v", err)
	}
	resp = graphqltest.New(f.handler).AsUser(testdata.Drei.Mail).Do(t, setFormatMutation, vars)
	assertRefusal(t, resp, "ASSIGNMENT_PHASE_CLOSED")

	// None of the refusals wrote anything.
	var read struct {
		CourseInstance struct {
			Delivery        string
			TaughtInEnglish bool
		}
	}
	graphqltest.New(f.handler).AsUser(testdata.Eins.Mail).MustQuery(t,
		`query($id: ID!) { courseInstance(id: $id) { delivery taughtInEnglish } }`,
		map[string]any{"id": f.instance}, &read)
	if read.CourseInstance.Delivery != "WEEKLY" || read.CourseInstance.TaughtInEnglish {
		t.Errorf("a refused write left %+v behind", read.CourseInstance)
	}
}

func TestHowACohortIsTaughtIsReadableThroughBothDoors(t *testing.T) {
	t.Parallel()

	f := assignmentHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Drei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
	)
	f.leadGroup(t, testdata.Drei, f.group)

	graphqltest.New(f.handler).AsUser(testdata.Drei.Mail).MustQuery(t, setFormatMutation,
		map[string]any{"id": f.instance, "d": "BLOCK", "en": true},
		&struct{ SetCourseInstanceFormat formatOut }{})

	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var read struct {
				CourseInstances []formatOut
			}
			c.MustQuery(t, `query($s: String!, $p: String) {
				courseInstances(semester: $s, programme: $p) {
					id delivery taughtInEnglish taughtInEnglishOverridden
				}
			}`, map[string]any{"s": f.semester, "p": f.programme}, &read)

			if len(read.CourseInstances) != 1 {
				t.Fatalf("a lecturer sees %d instances, want one", len(read.CourseInstances))
			}
			got := read.CourseInstances[0]
			if got.Delivery != "BLOCK" || !got.TaughtInEnglish || !got.TaughtInEnglishOverridden {
				t.Errorf("a lecturer reads %+v, want a block in English", got)
			}
		})
}
