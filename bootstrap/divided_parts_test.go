package bootstrap_test

import (
	"strings"
	"testing"

	"github.com/obcode/tallox.go/internal/graphqltest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// Dividing a part and handing it to another faculty, through the API.
//
// Bound by the assignment's rule, interactive only, and every refusal through both doors — the
// wish refusal included, because a wish is the one write here a token may make.

const dividePartMutation = `mutation($p: ID!, $h: Float!) {
	divideInstancePart(id: $p, teachingHours: $h) {
		teachingHours
		parts { id kind teachingHours dividedFrom taughtBy }
	}
}`

const taughtByMutation = `mutation($p: ID!, $f: String) {
	setInstancePartTaughtBy(id: $p, faculty: $f) {
		parts { id kind teachingHours dividedFrom taughtBy }
	}
}`

const rejoinMutation = `mutation($p: ID!) {
	rejoinInstancePart(id: $p) { parts { id dividedFrom } }
}`

type arrangedParts struct {
	TeachingHours float64
	Parts         []struct {
		ID            string
		Kind          string
		TeachingHours *float64
		DividedFrom   *string
		TaughtBy      *string
	}
}

// The case from the request, at the surface the subject group lead uses: half of the laboratory
// cut off and handed to another faculty.
func TestTheSubjectGroupLeadDividesAndHandsAPartAway(t *testing.T) {
	t.Parallel()

	f := assignmentHandler(t, grants{testdata.Drei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}})
	f.leadGroup(t, testdata.Drei, f.group)
	lead := graphqltest.New(f.handler).AsUser(testdata.Drei.Mail)

	var divided struct{ DivideInstancePart arrangedParts }
	lead.MustQuery(t, dividePartMutation, map[string]any{"p": f.lab, "h": 1.0}, &divided)
	if got := divided.DivideInstancePart.TeachingHours; got != 4 {
		t.Errorf("the divided instance costs %v hours, want the 4 it cost before", got)
	}

	var piece string
	for _, p := range divided.DivideInstancePart.Parts {
		if p.DividedFrom != nil && *p.DividedFrom == f.lab {
			piece = p.ID
		}
	}
	if piece == "" {
		t.Fatalf("no part names the laboratory as the one it was cut from: %+v", divided.DivideInstancePart.Parts)
	}

	var handed struct{ SetInstancePartTaughtBy arrangedParts }
	lead.MustQuery(t, taughtByMutation, map[string]any{"p": piece, "f": "  FK 04 "}, &handed)
	for _, p := range handed.SetInstancePartTaughtBy.Parts {
		if p.ID == piece && (p.TaughtBy == nil || *p.TaughtBy != "FK 04") {
			t.Errorf("the piece reads as taught by %v, want \"FK 04\" trimmed", p.TaughtBy)
		}
	}

	// Nobody of ours can be put on it now.
	resp := lead.Do(t, setAssignmentMutation, map[string]any{
		"p": piece, "who": testdata.Drei.ID().String(), "note": nil, "replacing": nil,
	})
	assertRefusal(t, resp, "PART_TAUGHT_ELSEWHERE")

	// And the piece goes back once it is ours again.
	lead.MustQuery(t, taughtByMutation, map[string]any{"p": piece, "f": nil}, &handed)
	var rejoined struct {
		RejoinInstancePart struct{ Parts []struct{ ID string } }
	}
	lead.MustQuery(t, rejoinMutation, map[string]any{"p": piece}, &rejoined)
	if got := len(rejoined.RejoinInstancePart.Parts); got != 2 {
		t.Errorf("after rejoining the instance holds %d parts, want the two it started with", got)
	}
}

func TestOnlyTheResponsibleArrangeAPart(t *testing.T) {
	t.Parallel()

	f := assignmentHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Drei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
	)
	// Drei leads the other group, which is not this module's.
	f.leadGroup(t, testdata.Drei, f.other)

	for _, who := range []testdata.Persona{testdata.Eins, testdata.Drei} {
		c := graphqltest.New(f.handler).AsUser(who.Mail)
		for name, call := range map[string]graphqltest.Response{
			"divide":   c.Do(t, dividePartMutation, map[string]any{"p": f.lab, "h": 1.0}),
			"taughtBy": c.Do(t, taughtByMutation, map[string]any{"p": f.lab, "f": "FK 04"}),
			"rejoin":   c.Do(t, rejoinMutation, map[string]any{"p": f.lab}),
		} {
			t.Run(who.Name+"/"+name, func(t *testing.T) {
				assertRefusal(t, call, "NOT_YOUR_SUBJECT")
				graphqltest.AssertNoLeak(t, strings.Join(call.Messages(), " "),
					append(graphqltest.DatabaseNoise(), testdata.Mails(testdata.Others(who))...)...)
			})
		}
	}
}

// Interactive only, like setAssignment: both writes can answer PART_ASSIGNED, and through a token
// that is an is-staffed flag the read rule withholds.
func TestArrangingAPartRefusesAToken(t *testing.T) {
	t.Parallel()

	f := assignmentHandler(t, grants{testdata.Drei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}})
	f.leadGroup(t, testdata.Drei, f.group)
	token := graphqltest.New(f.handler).WithToken(testdata.Drei.Token)

	assertRefusal(t, token.Do(t, dividePartMutation, map[string]any{"p": f.lab, "h": 1.0}), "INTERACTIVE_ONLY")
	assertRefusal(t, token.Do(t, taughtByMutation, map[string]any{"p": f.lab, "f": "FK 04"}), "INTERACTIVE_ONLY")
	assertRefusal(t, token.Do(t, rejoinMutation, map[string]any{"p": f.lab}), "INTERACTIVE_ONLY")

	// Nothing was written.
	var out struct {
		CourseInstance struct{ Parts []struct{ TaughtBy *string } }
	}
	graphqltest.New(f.handler).AsUser(testdata.Drei.Mail).MustQuery(t,
		`query($id: ID!) { courseInstance(id: $id) { parts { taughtBy } } }`,
		map[string]any{"id": f.instance}, &out)
	if got := len(out.CourseInstance.Parts); got != 2 {
		t.Errorf("the refused token writes left %d parts, want 2", got)
	}
	for _, p := range out.CourseInstance.Parts {
		if p.TaughtBy != nil {
			t.Errorf("a refused token write handed a part to %q", *p.TaughtBy)
		}
	}
}

// Handing a staffed part away is refused with PART_ASSIGNED, and that may only be said to
// somebody who may read the assignment behind it — the invariant
// TestPartAssignedTellsNobodySomethingNew holds for removing a part, here for the new write.
func TestHandingAStaffedPartAwayTellsNobodySomethingNew(t *testing.T) {
	t.Parallel()

	triggers := 0
	for _, who := range []struct {
		persona testdata.Persona
		roles   []string
	}{
		{testdata.Zwei, []string{"LECTURER"}},
		{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}},
		{testdata.Fuenf, []string{"LECTURER", "DEANS_OFFICE"}},
		{testdata.Sechs, []string{"LECTURER", "ADMIN"}},
	} {
		t.Run(who.persona.Name, func(t *testing.T) {
			f := assignmentHandler(t,
				grants{testdata.Eins, []string{"LECTURER"}},
				grants{testdata.Drei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
				grants{who.persona, who.roles},
			)
			f.leadGroup(t, testdata.Drei, f.group)
			if who.persona.Mail == testdata.Vier.Mail {
				f.leadProgramme(t, testdata.Vier, f.programme)
			}
			f.fill(t, testdata.Drei, f.lab, testdata.Eins)

			c := graphqltest.New(f.handler).AsUser(who.persona.Mail)
			fires := hasCode(c.Do(t, taughtByMutation, map[string]any{"p": f.lab, "f": "FK 04"}),
				"PART_ASSIGNED")
			if !fires {
				return
			}
			triggers++
			if len(held(t, c, f.semester)) == 0 {
				t.Errorf("%s can make PART_ASSIGNED fire and cannot read the assignment on the part",
					who.persona.Name)
			}
		})
	}
	if triggers == 0 {
		t.Fatal("nobody in this cast could make PART_ASSIGNED fire — this test checked nothing")
	}
}

// A wish is the one write a token may make here, so its refusal goes through both doors.
func TestAnInstanceTaughtElsewhereCannotBeWishedFor(t *testing.T) {
	t.Parallel()

	f := assignmentHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Drei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
	)
	f.leadGroup(t, testdata.Drei, f.group)
	lead := graphqltest.New(f.handler).AsUser(testdata.Drei.Mail)

	// Half of it elsewhere: still ours to want.
	var handed struct{ SetInstancePartTaughtBy arrangedParts }
	lead.MustQuery(t, taughtByMutation, map[string]any{"p": f.lab, "f": "FK 04"}, &handed)
	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			resp := c.Do(t, setWishMutation, map[string]any{"p": f.instance, "prio": "HAPPY_TO", "note": nil})
			if resp.Failed() {
				t.Errorf("a wish for an instance taught partly elsewhere was refused: %v", resp.Messages())
			}
		})

	// All of it elsewhere: nothing to want.
	lead.MustQuery(t, taughtByMutation, map[string]any{"p": f.lecture, "f": "FK 04"}, &handed)
	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			resp := c.Do(t, setWishMutation, map[string]any{"p": f.instance, "prio": "FIRST_CHOICE", "note": nil})
			assertRefusal(t, resp, "INSTANCE_TAUGHT_ELSEWHERE")
			graphqltest.AssertNoLeak(t, strings.Join(resp.Messages(), " "),
				append(graphqltest.DatabaseNoise(), testdata.Mails(testdata.Others(testdata.Eins))...)...)
		})
}

// The controlling figure through both doors: another faculty's hours beside the load, never in it.
func TestTheTeachingLoadReportsWhatOtherFacultiesTeach(t *testing.T) {
	t.Parallel()

	f := assignmentHandler(t,
		grants{testdata.Drei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
		grants{testdata.Fuenf, []string{"LECTURER", "DEANS_OFFICE"}},
	)
	f.leadGroup(t, testdata.Drei, f.group)
	var handed struct{ SetInstancePartTaughtBy arrangedParts }
	graphqltest.New(f.handler).AsUser(testdata.Drei.Mail).MustQuery(t, taughtByMutation,
		map[string]any{"p": f.lab, "f": "FK 04"}, &handed)

	graphqltest.EachDoor(t, f.handler, testdata.Fuenf.Mail, testdata.Fuenf.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var out struct {
				TeachingLoad []struct {
					Programme     struct{ Code string }
					TeachingHours float64
					ExternalHours float64
				}
			}
			c.MustQuery(t, `query($s: String!) {
				teachingLoad(semester: $s) { programme { code } teachingHours externalHours }
			}`, map[string]any{"s": f.semester}, &out)
			for _, l := range out.TeachingLoad {
				if l.Programme.Code != f.programme {
					continue
				}
				if l.TeachingHours != 2 || l.ExternalHours != 2 {
					t.Errorf("%s pays %v hours with %v taught elsewhere, want 2 and 2",
						f.programme, l.TeachingHours, l.ExternalHours)
				}
				return
			}
			t.Errorf("the teaching load has no entry for %s", f.programme)
		})
}
