package bootstrap_test

import (
	"testing"

	"github.com/obcode/tallox.go/internal/graphqltest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// Confirming one's assignments through the API, through all three doors: one's own through every
// one of them, other people's only with full reach and only as far as their assignments reach.

const confirmMutation = `mutation($s: String!) {
	confirmMyAssignments(semester: $s) { semester personId state confirmedAt }
}`

const myConfirmationQuery = `query($s: String!) {
	myAssignmentConfirmation(semester: $s) { personId state confirmedAt }
}`

const confirmationsQuery = `query($s: String!) {
	assignmentConfirmations(semester: $s) { personId state }
}`

const confirmationDueMutation = `mutation($s: String!, $due: Date) {
	setAssignmentConfirmationDue(code: $s, due: $due) { code assignmentConfirmationDue }
}`

type confirmationRow struct {
	PersonID string
	State    string
}

// TestOwnConfirmationThroughEveryDoor: confirming and reading one's own is one's own timetable.
func TestOwnConfirmationThroughEveryDoor(t *testing.T) {
	t.Parallel()

	f := assignmentHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Drei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
	)
	f.leadGroup(t, testdata.Drei, f.group)
	f.fill(t, testdata.Drei, f.lecture, testdata.Eins)

	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var out struct {
				ConfirmMyAssignments struct {
					PersonID, State string
					ConfirmedAt     *string
				}
			}
			c.MustQuery(t, confirmMutation, map[string]any{"s": f.semester}, &out)
			got := out.ConfirmMyAssignments
			if got.State != "CONFIRMED" || got.ConfirmedAt == nil ||
				got.PersonID != testdata.Eins.ID().String() {
				t.Errorf("confirming answered %+v, want own CONFIRMED with a time", got)
			}

			var mine struct{ MyAssignmentConfirmation confirmationRow }
			c.MustQuery(t, myConfirmationQuery, map[string]any{"s": f.semester}, &mine)
			if mine.MyAssignmentConfirmation.State != "CONFIRMED" {
				t.Errorf("reading it back: %+v", mine.MyAssignmentConfirmation)
			}
		})

	// A colleague holding nothing is refused with a code that is about them alone.
	graphqltest.EachDoor(t, f.handler, testdata.Drei.Mail, testdata.Drei.Token,
		func(t *testing.T, c *graphqltest.Client) {
			resp := c.Do(t, confirmMutation, map[string]any{"s": f.semester})
			if !hasCode(resp, "NOTHING_TO_CONFIRM") {
				t.Errorf("confirming nothing answered %v, want NOTHING_TO_CONFIRM", resp.Messages())
			}
			for _, msg := range resp.Messages() {
				graphqltest.AssertNoLeak(t, msg, append(graphqltest.DatabaseNoise(),
					testdata.Mails(testdata.Others(testdata.Drei))...)...)
			}
		})
}

// TestOthersConfirmationsNeedFullReachAndFollowTheAssignments is the pairing: the browser and a
// full-reach token read the same rows, a plain token none, and a colleague who may not read the
// assignment does not learn the confirmation either.
func TestOthersConfirmationsNeedFullReachAndFollowTheAssignments(t *testing.T) {
	t.Parallel()

	f := assignmentHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Zwei, []string{"LECTURER"}},
		grants{testdata.Drei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
	)
	f.leadGroup(t, testdata.Drei, f.group)
	f.fill(t, testdata.Drei, f.lecture, testdata.Eins)
	graphqltest.New(f.handler).AsUser(testdata.Eins.Mail).MustQuery(t, confirmMutation,
		map[string]any{"s": f.semester}, &struct{}{})

	read := func(t *testing.T, c *graphqltest.Client) []confirmationRow {
		t.Helper()
		var out struct{ AssignmentConfirmations []confirmationRow }
		c.MustQuery(t, confirmationsQuery, map[string]any{"s": f.semester}, &out)
		return out.AssignmentConfirmations
	}
	want := []confirmationRow{{PersonID: testdata.Eins.ID().String(), State: "CONFIRMED"}}

	browser := read(t, graphqltest.New(f.handler).AsUser(testdata.Drei.Mail))
	if len(browser) != 1 || browser[0] != want[0] {
		t.Errorf("the lead reads %+v in the browser, want %+v", browser, want)
	}
	full := read(t, graphqltest.New(f.handler).WithToken(fullReachToken(t, f.schema, testdata.Drei)))
	if len(full) != len(browser) || (len(full) == 1 && full[0] != browser[0]) {
		t.Errorf("a full-reach token reads %+v, want what the browser reads: %+v", full, browser)
	}

	resp := graphqltest.New(f.handler).WithToken(testdata.Drei.Token).
		Do(t, confirmationsQuery, map[string]any{"s": f.semester})
	if !hasCode(resp, "FULL_REACH_REQUIRED") {
		t.Errorf("a plain token answered %v, want FULL_REACH_REQUIRED", resp.Messages())
	}

	// Before publication a colleague who may not read the assignment learns nothing about it.
	if got := read(t, graphqltest.New(f.handler).AsUser(testdata.Zwei.Mail)); len(got) != 0 {
		t.Errorf("an uninvolved colleague reads %+v before publication, want nothing", got)
	}
}

// TestTheDueDateIsTheDeansOffices: set in the browser and with full reach, refused to a plain
// token and to everybody else.
func TestTheDueDateIsTheDeansOffices(t *testing.T) {
	t.Parallel()

	f := assignmentHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Vier, []string{"DEANS_OFFICE"}},
	)
	vars := map[string]any{"s": f.semester, "due": "2029-01-15"}

	var out struct {
		SetAssignmentConfirmationDue struct{ AssignmentConfirmationDue *string }
	}
	graphqltest.New(f.handler).AsUser(testdata.Vier.Mail).MustQuery(t, confirmationDueMutation,
		vars, &out)
	if got := out.SetAssignmentConfirmationDue.AssignmentConfirmationDue; got == nil ||
		*got != "2029-01-15" {
		t.Errorf("the browser set the date to %v, want 2029-01-15", got)
	}

	full := graphqltest.New(f.handler).WithToken(fullReachToken(t, f.schema, testdata.Vier))
	full.MustQuery(t, confirmationDueMutation, map[string]any{"s": f.semester, "due": nil}, &out)
	if got := out.SetAssignmentConfirmationDue.AssignmentConfirmationDue; got != nil {
		t.Errorf("clearing with a full-reach token left %v", *got)
	}

	plain := graphqltest.New(f.handler).WithToken(testdata.Vier.Token).
		Do(t, confirmationDueMutation, vars)
	if !hasCode(plain, "FULL_REACH_REQUIRED") {
		t.Errorf("a plain token answered %v, want FULL_REACH_REQUIRED", plain.Messages())
	}

	lecturer := graphqltest.New(f.handler).AsUser(testdata.Eins.Mail).
		Do(t, confirmationDueMutation, vars)
	if !hasCode(lecturer, "FORBIDDEN") {
		t.Errorf("a lecturer answered %v, want FORBIDDEN", lecturer.Messages())
	}
}
