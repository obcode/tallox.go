package bootstrap_test

import (
	"strings"
	"testing"

	"github.com/obcode/tallox.go/internal/graphqltest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// The topic of a cohort — which seminar a cohort of an umbrella module is — through the API.
// Written by whoever may write the cohort's demand or its assignment, decided 2026-10-20; read by
// everybody through both doors, because it is planning and the thing colleagues wish for.

const setTopicMutation = `mutation($id: ID!, $topic: String!) {
	setCourseInstanceTopic(id: $id, topic: $topic) { id topic }
}`

type topicOut struct {
	SetCourseInstanceTopic struct {
		ID    string
		Topic *string
	}
}

func TestBothLeadsAndTheDeansOfficeNameASeminar(t *testing.T) {
	t.Parallel()

	f := assignmentHandler(t,
		grants{testdata.Drei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
		grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}},
		grants{testdata.Fuenf, []string{"DEANS_OFFICE"}},
	)
	f.leadGroup(t, testdata.Drei, f.group)
	f.leadProgramme(t, testdata.Vier, f.programme)

	for _, step := range []struct {
		who   testdata.Persona
		topic string
		want  string
	}{
		// The programme lead names it in advance, the subject group lead corrects it.
		{testdata.Vier, "  Design Thinking ", "Design Thinking"},
		{testdata.Drei, "Design Thinking for Engineers", "Design Thinking for Engineers"},
		{testdata.Fuenf, "AI Fundamentals", "AI Fundamentals"},
		// Empty takes it away, and "none" is null on the wire rather than "".
		{testdata.Vier, "", ""},
	} {
		var out topicOut
		graphqltest.New(f.handler).AsUser(step.who.Mail).MustQuery(t, setTopicMutation,
			map[string]any{"id": f.instance, "topic": step.topic}, &out)
		got := out.SetCourseInstanceTopic.Topic
		switch {
		case step.want == "" && got != nil:
			t.Errorf("%s cleared the topic and got %q, want null", step.who.Name, *got)
		case step.want != "" && (got == nil || *got != step.want):
			t.Errorf("%s set %q and got %v, want %q", step.who.Name, step.topic, got, step.want)
		}
	}
}

func TestOnlyTheResponsibleNameASeminar(t *testing.T) {
	t.Parallel()

	f := assignmentHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Zwei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
		grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}},
	)
	// Zwei leads a subject group, just not this module's.
	f.leadGroup(t, testdata.Zwei, f.other)
	f.leadProgramme(t, testdata.Vier, f.programme)

	vars := map[string]any{"id": f.instance, "topic": "Green Software Engineering"}
	for _, who := range []testdata.Persona{testdata.Eins, testdata.Zwei} {
		resp := graphqltest.New(f.handler).AsUser(who.Mail).Do(t, setTopicMutation, vars)
		assertRefusal(t, resp, "NOT_YOUR_SUBJECT")
		graphqltest.AssertNoLeak(t, strings.Join(resp.Messages(), " "),
			graphqltest.DatabaseNoise()...)
	}

	// The token door is refused even for somebody who may do this in the browser.
	resp := graphqltest.New(f.handler).WithToken(testdata.Vier.Token).Do(t, setTopicMutation, vars)
	assertRefusal(t, resp, "FULL_REACH_REQUIRED")

	// A heading, not a paragraph.
	resp = graphqltest.New(f.handler).AsUser(testdata.Vier.Mail).Do(t, setTopicMutation,
		map[string]any{"id": f.instance, "topic": strings.Repeat("x", 121)})
	assertRefusal(t, resp, "TOPIC_TOO_LONG")

	// A finished semester refuses the lead too.
	if _, err := f.schema.Pool.Exec(t.Context(),
		`UPDATE semester SET phase = 'FINAL' WHERE code = $1`, f.semester); err != nil {
		t.Fatalf("cannot finish the semester: %v", err)
	}
	resp = graphqltest.New(f.handler).AsUser(testdata.Vier.Mail).Do(t, setTopicMutation, vars)
	assertRefusal(t, resp, "ASSIGNMENT_PHASE_CLOSED")

	// None of the refusals wrote anything.
	var read struct{ CourseInstance struct{ Topic *string } }
	graphqltest.New(f.handler).AsUser(testdata.Eins.Mail).MustQuery(t,
		`query($id: ID!) { courseInstance(id: $id) { topic } }`,
		map[string]any{"id": f.instance}, &read)
	if read.CourseInstance.Topic != nil {
		t.Errorf("a refused write left the topic %q behind", *read.CourseInstance.Topic)
	}
}

func TestTheTopicOfACohortIsReadableThroughBothDoors(t *testing.T) {
	t.Parallel()

	f := assignmentHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}},
	)
	f.leadProgramme(t, testdata.Vier, f.programme)

	graphqltest.New(f.handler).AsUser(testdata.Vier.Mail).MustQuery(t, setTopicMutation,
		map[string]any{"id": f.instance, "topic": "AI Fundamentals"}, &topicOut{})

	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var read struct {
				CourseInstances []struct{ Topic *string }
			}
			c.MustQuery(t, `query($s: String!, $p: String) {
				courseInstances(semester: $s, programme: $p) { topic }
			}`, map[string]any{"s": f.semester, "p": f.programme}, &read)

			if len(read.CourseInstances) != 1 {
				t.Fatalf("a lecturer sees %d instances, want one", len(read.CourseInstances))
			}
			if got := read.CourseInstances[0].Topic; got == nil || *got != "AI Fundamentals" {
				t.Errorf("a lecturer reads the topic %v, want AI Fundamentals", got)
			}
		})
}
