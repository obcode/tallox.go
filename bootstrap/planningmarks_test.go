package bootstrap_test

import (
	"strings"
	"testing"

	"github.com/obcode/tallox.go/internal/graphqltest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// The two planning marks through the API, through both doors.
//
// What is asserted here is mostly who may switch them, because that is where the correction of
// 2026-08-28 lives: the planning opens and closes where the responsibility already is, and nobody
// needed a new role for it. The reading half is asserted as *public*, which is unusual in this
// codebase and deliberate — a colleague who meets a shut door has to be able to see that it is
// shut.

const setWishWindowMutation = `mutation($s: String!, $g: ID!, $open: Boolean!) {
	setWishWindow(semester: $s, subjectGroupId: $g, open: $open) {
		open
		subjectGroup { code }
	}
}`

const setDemandCompleteMutation = `mutation($s: String!, $p: String!, $c: Boolean!) {
	setDemandComplete(semester: $s, programme: $p, complete: $c) {
		semester
		programme { code }
	}
}`

const marksQuery = `query($s: String!) {
	wishWindows(semester: $s) { open subjectGroup { code } }
	demandCompletions(semester: $s) { programme { code } }
}`

// TestTheSubjectGroupLeadSwitchesTheirOwnWishRound is the decision in one sentence.
func TestTheSubjectGroupLeadSwitchesTheirOwnWishRound(t *testing.T) {
	t.Parallel()

	f := wishHandler(t,
		grants{testdata.Drei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
		grants{testdata.Zwei, []string{"LECTURER"}},
	)
	f.leadGroup(t, testdata.Drei, f.group)

	var out struct {
		SetWishWindow struct {
			Open         bool
			SubjectGroup struct{ Code string }
		}
	}
	graphqltest.New(f.handler).AsUser(testdata.Drei.Mail).MustQuery(t, setWishWindowMutation,
		map[string]any{"s": f.semester, "g": f.group.String(), "open": false}, &out)

	if out.SetWishWindow.Open {
		t.Error("shutting the round answered open")
	}

	// And the group next door is not theirs to shut.
	resp := graphqltest.New(f.handler).AsUser(testdata.Drei.Mail).Do(t, setWishWindowMutation,
		map[string]any{"s": f.semester, "g": f.otherGroup.String(), "open": false})
	assertRefusal(t, resp, "NOT_YOUR_SUBJECT_GROUP")

	// Nor is it a lecturer's.
	resp = graphqltest.New(f.handler).AsUser(testdata.Zwei.Mail).Do(t, setWishWindowMutation,
		map[string]any{"s": f.semester, "g": f.group.String(), "open": true})
	assertRefusal(t, resp, "NOT_YOUR_SUBJECT_GROUP")
}

// TestAShutRoundRefusesAWishAndSaysWhoCanOpenIt is what a lecturer meets.
//
// Its own code and its own sentence rather than the phase's, because the repair is a different
// person: a shut window is one switch held by this subject group's lead.
func TestAShutRoundRefusesAWishAndSaysWhoCanOpenIt(t *testing.T) {
	t.Parallel()

	f := wishHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Drei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
	)
	f.leadGroup(t, testdata.Drei, f.group)

	// Open: the ordinary case, and it works without anybody having switched anything.
	f.register(t, testdata.Eins)

	graphqltest.New(f.handler).AsUser(testdata.Drei.Mail).MustQuery(t, setWishWindowMutation,
		map[string]any{"s": f.semester, "g": f.group.String(), "open": false}, &struct {
			SetWishWindow struct{ Open bool }
		}{})

	resp := graphqltest.New(f.handler).AsUser(testdata.Eins.Mail).Do(t, setWishMutation,
		map[string]any{"p": f.instance, "prio": "FIRST_CHOICE", "note": nil})
	assertRefusal(t, resp, "WISH_WINDOW_CLOSED")

	// The sentence names who can change it, because that is the repair.
	messages := resp.Messages()
	if len(messages) == 0 || !strings.Contains(messages[0], "Fachgruppenleitung") {
		t.Errorf("the refusal is %v — it should name who can open the round again", messages)
	}

	// Reopened, and it takes entries again. A door and not a phase.
	graphqltest.New(f.handler).AsUser(testdata.Drei.Mail).MustQuery(t, setWishWindowMutation,
		map[string]any{"s": f.semester, "g": f.group.String(), "open": true}, &struct {
			SetWishWindow struct{ Open bool }
		}{})
	f.register(t, testdata.Eins)
}

// TestTheProgrammeLeadAnnouncesTheirOwnDemand is the other mark: who may release a programme.
func TestTheProgrammeLeadAnnouncesTheirOwnDemand(t *testing.T) {
	t.Parallel()

	f := wishHandler(t,
		grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}},
		grants{testdata.Zwei, []string{"LECTURER"}},
	)
	f.leadProgramme(t, testdata.Vier, f.programme)

	var out struct {
		SetDemandComplete struct {
			Semester  string
			Programme struct{ Code string }
		}
	}
	graphqltest.New(f.handler).AsUser(testdata.Vier.Mail).MustQuery(t, setDemandCompleteMutation,
		map[string]any{"s": f.semester, "p": f.programme, "c": true}, &out)

	if out.SetDemandComplete.Programme.Code != f.programme {
		t.Errorf("announced %q, want %q", out.SetDemandComplete.Programme.Code, f.programme)
	}

	// Another programme is not theirs to speak for.
	resp := graphqltest.New(f.handler).AsUser(testdata.Vier.Mail).Do(t, setDemandCompleteMutation,
		map[string]any{"s": f.semester, "p": f.otherProgramme, "c": true})
	assertRefusal(t, resp, "NOT_YOUR_PROGRAMME")

	// And a lecturer speaks for none.
	resp = graphqltest.New(f.handler).AsUser(testdata.Zwei.Mail).Do(t, setDemandCompleteMutation,
		map[string]any{"s": f.semester, "p": f.programme, "c": true})
	assertRefusal(t, resp, "NOT_YOUR_PROGRAMME")
}

// TestAnnouncingTheDemandBlocksNothing is about the demand: the release opens the wishes, and
// shuts nothing.
//
// The faculty was explicit about this: "es ist nie absolut". Declaring an instance after the
// announcement stays possible, and the announcement goes out of date rather than becoming a
// promise somebody broke.
func TestAnnouncingTheDemandBlocksNothing(t *testing.T) {
	t.Parallel()

	f := wishHandler(t,
		grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}},
	)
	f.leadProgramme(t, testdata.Vier, f.programme)

	graphqltest.New(f.handler).AsUser(testdata.Vier.Mail).MustQuery(t, setDemandCompleteMutation,
		map[string]any{"s": f.semester, "p": f.programme, "c": true}, &struct {
			SetDemandComplete struct{ Semester string }
		}{})

	// Still possible afterwards: a part added to the instance that was already there.
	graphqltest.New(f.handler).AsUser(testdata.Vier.Mail).MustQuery(t,
		`mutation($i: ID!) { addInstancePart(instanceId: $i, kind: EXERCISE) { id } }`,
		map[string]any{"i": f.instance}, &struct {
			AddInstancePart struct{ ID string }
		}{})
}

// TestBothMarksAreReadableThroughBothDoors is the public half.
//
// Unusual in this codebase and deliberate. Both marks are facts about the process rather than
// about people, and a colleague who cannot see them gets a tool that refuses writes without
// saying why. What is confidential is what the marks are about, and that has its own rules.
func TestBothMarksAreReadableThroughBothDoors(t *testing.T) {
	t.Parallel()

	f := wishHandler(t,
		grants{testdata.Drei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
		grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}},
		grants{testdata.Zwei, []string{"LECTURER"}},
	)
	f.leadGroup(t, testdata.Drei, f.group)
	f.leadProgramme(t, testdata.Vier, f.programme)

	graphqltest.New(f.handler).AsUser(testdata.Drei.Mail).MustQuery(t, setWishWindowMutation,
		map[string]any{"s": f.semester, "g": f.group.String(), "open": false}, &struct {
			SetWishWindow struct{ Open bool }
		}{})
	graphqltest.New(f.handler).AsUser(testdata.Vier.Mail).MustQuery(t, setDemandCompleteMutation,
		map[string]any{"s": f.semester, "p": f.programme, "c": true}, &struct {
			SetDemandComplete struct{ Semester string }
		}{})

	graphqltest.EachDoor(t, f.handler, testdata.Zwei.Mail, testdata.Zwei.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var out struct {
				WishWindows []struct {
					Open         bool
					SubjectGroup struct{ Code string }
				}
				DemandCompletions []struct {
					Programme struct{ Code string }
				}
			}
			c.MustQuery(t, marksQuery, map[string]any{"s": f.semester}, &out)

			if len(out.WishWindows) != 1 || out.WishWindows[0].Open {
				t.Errorf("an uninvolved colleague reads %+v, want the one shut window",
					out.WishWindows)
			}
			if len(out.DemandCompletions) != 1 {
				t.Errorf("an uninvolved colleague reads %d announcements, want 1",
					len(out.DemandCompletions))
			}
		})
}

// TestWishWindowsListsOnlyWhatSomebodySwitched is the shape a caller has to read correctly.
//
// The list is the exceptions, not the state of every subject group. Reading it as "these are the
// open ones" is the mistake the field's shape invites.
func TestWishWindowsListsOnlyWhatSomebodySwitched(t *testing.T) {
	t.Parallel()

	f := wishHandler(t, grants{testdata.Eins, []string{"LECTURER"}})

	var out struct {
		WishWindows       []struct{ Open bool }
		DemandCompletions []struct{}
	}
	graphqltest.New(f.handler).AsUser(testdata.Eins.Mail).MustQuery(t, marksQuery,
		map[string]any{"s": f.semester}, &out)

	if len(out.WishWindows) != 0 {
		t.Errorf("a semester nobody switched anything in lists %d windows, want none",
			len(out.WishWindows))
	}
	// The one release the fixture made, and only that: an announcement is a row somebody wrote.
	if len(out.DemandCompletions) != 1 {
		t.Errorf("…and %d announcements, want the fixture's one", len(out.DemandCompletions))
	}

	// And the round is open, which is what an absent window row means.
	f.register(t, testdata.Eins)
}

// TestARefusedAnnouncementLeavesNoSemesterBehind: recording a semester is a write, and until
// 2026-10-28 it happened before the policy had its say — so anybody with an account could leave
// a semester row for every code in the ±10-year window by asking and being refused.
func TestARefusedAnnouncementLeavesNoSemesterBehind(t *testing.T) {
	t.Parallel()

	f := wishHandler(t, grants{testdata.Eins, []string{"LECTURER"}})
	const far = "2035-WS"

	rows := func(t *testing.T) int {
		t.Helper()
		var n int
		if err := f.schema.Pool.QueryRow(t.Context(),
			`SELECT count(*) FROM semester WHERE code = $1`, far).Scan(&n); err != nil {
			t.Fatalf("cannot count semesters: %v", err)
		}
		return n
	}

	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			for _, mutation := range []string{setDemandCompleteMutation, setReviewCompleteMutation} {
				resp := c.Do(t, mutation, map[string]any{"s": far, "p": f.programme, "c": true})
				assertRefusal(t, resp, "NOT_YOUR_PROGRAMME")
			}
			if got := rows(t); got != 0 {
				t.Errorf("two refused announcements left %d semester row(s) behind", got)
			}
		})
}

// TestAnUnreleasedProgrammeTakesNoWishThroughAnyDoor is what a lecturer meets before the
// programme lead has released the demand, and again after the release is withdrawn.
//
// Its own code and a sentence that names the programme lead, because that is the repair — not the
// subject group's lead, whose window is open the whole time here. Through all three doors: a wish
// is the one write a plain token may make, so the rule must not be a browser rule.
func TestAnUnreleasedProgrammeTakesNoWishThroughAnyDoor(t *testing.T) {
	t.Parallel()

	f := wishHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Zwei, []string{"LECTURER"}},
		grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}},
	)
	f.leadProgramme(t, testdata.Vier, f.programme)

	// Somebody else registered while the programme was released: the refusal must not tell Eins.
	f.register(t, testdata.Zwei)

	// The lead withdraws the release.
	var withdrawn struct{ SetDemandComplete *struct{ Semester string } }
	graphqltest.New(f.handler).AsUser(testdata.Vier.Mail).MustQuery(t, setDemandCompleteMutation,
		map[string]any{"s": f.semester, "p": f.programme, "c": false}, &withdrawn)

	refused := func(t *testing.T, c *graphqltest.Client) {
		t.Helper()
		resp := c.Do(t, setWishMutation,
			map[string]any{"p": f.instance, "prio": "FIRST_CHOICE", "note": nil})
		assertRefusal(t, resp, "WISHES_NOT_RELEASED")
		messages := resp.Messages()
		if len(messages) == 0 || !strings.Contains(messages[0], "Studiengangsleitung") {
			t.Errorf("the refusal is %v — it should name who can release the programme", messages)
		}
		graphqltest.AssertNoLeak(t, strings.Join(messages, " "),
			append(graphqltest.DatabaseNoise(),
				testdata.Mails(testdata.Others(testdata.Eins))...)...)
	}

	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token, refused)
	t.Run("full reach", func(t *testing.T) {
		refused(t, graphqltest.New(f.handler).WithToken(fullReachToken(t, f.schema, testdata.Eins)).
			On(graphqltest.Token))
	})

	// Released again, and it takes the entry. The demand did not change in between.
	graphqltest.New(f.handler).AsUser(testdata.Vier.Mail).MustQuery(t, setDemandCompleteMutation,
		map[string]any{"s": f.semester, "p": f.programme, "c": true}, &struct {
			SetDemandComplete struct{ Semester string }
		}{})
	f.register(t, testdata.Eins)
}
