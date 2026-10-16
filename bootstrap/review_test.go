package bootstrap_test

import (
	"testing"

	"github.com/obcode/tallox.go/internal/graphqltest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// The programme review through the API (2026-10-16).
//
// After the assignment the programme leads go over the whole once more, and everything stays
// writable while they do: the cut of the demand changes with who was found to teach it, and a
// colleague falling ill before the first lecture is handled here. Only a semester that is over —
// FINAL — refuses. The announcement that a review is done blocks nothing, like the demand's.

const setReviewCompleteMutation = `mutation($s: String!, $p: String!, $c: Boolean!) {
	setReviewComplete(semester: $s, programme: $p, complete: $c) {
		semester
		programme { code }
	}
}`

const addLabMutation = `mutation($i: ID!) { addInstancePart(instanceId: $i, kind: LAB) { id } }`

// In REVIEW the programme lead still cuts the demand and the subject group lead still fills it;
// in FINAL neither does.
func TestTheReviewKeepsEverythingWritableUntilTheSemesterIsOver(t *testing.T) {
	t.Parallel()

	f := assignmentHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Drei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
		grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}},
	)
	f.leadGroup(t, testdata.Drei, f.group)
	f.leadProgramme(t, testdata.Vier, f.programme)
	f.fill(t, testdata.Drei, f.lecture, testdata.Eins)

	if _, err := f.schema.Pool.Exec(t.Context(),
		`UPDATE semester SET phase = 'REVIEW' WHERE code = $1`, f.semester); err != nil {
		t.Fatalf("cannot move the semester into the review: %v", err)
	}

	lead := graphqltest.New(f.handler).AsUser(testdata.Vier.Mail)
	lead.MustQuery(t, addLabMutation, map[string]any{"i": f.instance}, nil)
	f.fill(t, testdata.Drei, f.lab, testdata.Eins)

	if _, err := f.schema.Pool.Exec(t.Context(),
		`UPDATE semester SET phase = 'FINAL' WHERE code = $1`, f.semester); err != nil {
		t.Fatalf("cannot finish the semester: %v", err)
	}
	assertRefusal(t, lead.Do(t, addLabMutation, map[string]any{"i": f.instance}),
		"DEMAND_PHASE_CLOSED")
	assertRefusal(t, graphqltest.New(f.handler).AsUser(testdata.Drei.Mail).Do(t,
		setAssignmentMutation, map[string]any{
			"p": f.lab, "who": testdata.Drei.ID().String(), "note": nil, "replacing": nil,
		}), "ASSIGNMENT_PHASE_CLOSED")
}

// The lead of a programme announces its review, nobody else's; everybody reads it, at both doors.
func TestTheReviewAnnouncement(t *testing.T) {
	t.Parallel()

	f := wishHandler(t,
		grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}},
		grants{testdata.Zwei, []string{"LECTURER"}},
	)
	f.leadProgramme(t, testdata.Vier, f.programme)

	lead := graphqltest.New(f.handler).AsUser(testdata.Vier.Mail)
	lead.MustQuery(t, setReviewCompleteMutation,
		map[string]any{"s": f.semester, "p": f.programme, "c": true}, nil)

	assertRefusal(t, lead.Do(t, setReviewCompleteMutation,
		map[string]any{"s": f.semester, "p": f.otherProgramme, "c": true}), "NOT_YOUR_PROGRAMME")
	assertRefusal(t, graphqltest.New(f.handler).AsUser(testdata.Zwei.Mail).Do(t,
		setReviewCompleteMutation, map[string]any{"s": f.semester, "p": f.programme, "c": true}),
		"NOT_YOUR_PROGRAMME")

	// Announcing blocks nothing: the lead still changes the demand afterwards.
	lead.MustQuery(t, addLabMutation, map[string]any{"i": f.instance}, nil)

	const q = `query($s: String!) { reviewCompletions(semester: $s) { programme { code } } }`
	graphqltest.EachDoor(t, f.handler, testdata.Zwei.Mail, testdata.Zwei.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var out struct {
				ReviewCompletions []struct{ Programme struct{ Code string } }
			}
			c.MustQuery(t, q, map[string]any{"s": f.semester}, &out)
			if len(out.ReviewCompletions) != 1 ||
				out.ReviewCompletions[0].Programme.Code != f.programme {
				t.Errorf("a colleague reads %+v, want the one announcement", out.ReviewCompletions)
			}
		})

	// And withdrawing answers null and leaves nothing.
	var out struct{ SetReviewComplete *struct{ Semester string } }
	lead.MustQuery(t, setReviewCompleteMutation,
		map[string]any{"s": f.semester, "p": f.programme, "c": false}, &out)
	if out.SetReviewComplete != nil {
		t.Errorf("withdrawing answered %+v, want null", out.SetReviewComplete)
	}
}
