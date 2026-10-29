package bootstrap_test

import (
	"strings"
	"testing"

	"github.com/obcode/tallox.go/internal/graphqltest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// English only where the module description names it (decided 2026-10-29), through the API: marking
// a cohort as taught in English, listing it as a course in English, and offering it as one with a
// wish. And the lecturers who teach a cohort may list it themselves.
//
// The fixture's module is German with English possible, the commonest real case; germanOnly takes
// the possibility away the way the catalogue would.

func (f assignmentAPIFixture) germanOnly(t *testing.T) {
	t.Helper()

	if _, err := f.schema.Pool.Exec(t.Context(),
		`UPDATE module SET other_language = NULL
		  WHERE id = (SELECT module_id FROM course_instance WHERE id = $1)`, f.instance); err != nil {
		t.Fatalf("cannot make the module German only: %v", err)
	}
}

func (f assignmentAPIFixture) assignPart(t *testing.T, part string, who testdata.Persona) {
	t.Helper()

	if _, err := f.schema.Pool.Exec(t.Context(),
		`INSERT INTO assignment (instance_part_id, person_id) VALUES ($1, $2)`,
		part, who.ID()); err != nil {
		t.Fatalf("cannot assign %s: %v", who.Name, err)
	}
}

const englishPossibleQuery = `query($id: ID!) {
	courseInstance(id: $id) { cie taughtInEnglish module { englishPossible } }
}`

type englishPossibleOut struct {
	CourseInstance struct {
		Cie             bool
		TaughtInEnglish bool
		Module          struct{ EnglishPossible bool }
	}
}

func TestEnglishOnlyWhereTheModuleDescriptionNamesIt(t *testing.T) {
	t.Parallel()

	f := assignmentHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Drei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
	)
	f.leadGroup(t, testdata.Drei, f.group)
	vars := map[string]any{"id": f.instance}

	// Readable through every door, because it is catalogue data.
	possible := func(want bool) func(*testing.T, *graphqltest.Client) {
		return func(t *testing.T, c *graphqltest.Client) {
			t.Helper()
			var out englishPossibleOut
			c.MustQuery(t, englishPossibleQuery, vars, &out)
			if out.CourseInstance.Module.EnglishPossible != want {
				t.Errorf("englishPossible is %v, want %v", out.CourseInstance.Module.EnglishPossible, want)
			}
		}
	}
	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token, possible(true))

	f.germanOnly(t)
	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token, possible(false))

	// Refused for the lead in the browser and through a full-reach token alike.
	doors := map[string]*graphqltest.Client{
		"browser":    graphqltest.New(f.handler).AsUser(testdata.Drei.Mail),
		"full reach": graphqltest.New(f.handler).WithToken(fullReachToken(t, f.schema, testdata.Drei)).On(graphqltest.Token),
	}
	for door, c := range doors {
		resp := c.Do(t, setFormatMutation, map[string]any{"id": f.instance, "d": "WEEKLY", "en": true})
		assertRefusal(t, resp, "ENGLISH_NOT_OFFERED")
		resp = c.Do(t, setCiEMutation, map[string]any{"id": f.instance, "cie": true})
		assertRefusal(t, resp, "ENGLISH_NOT_OFFERED")
		graphqltest.AssertNoLeak(t, door+": "+strings.Join(resp.Messages(), " "),
			graphqltest.DatabaseNoise()...)

		// German, and off the list, are always possible.
		c.MustQuery(t, setFormatMutation, map[string]any{"id": f.instance, "d": "BLOCK", "en": false},
			&struct{ SetCourseInstanceFormat formatOut }{})
		c.MustQuery(t, setCiEMutation, map[string]any{"id": f.instance, "cie": false},
			&struct{ SetCourseInstanceCiE cieOut }{})
	}

	var out englishPossibleOut
	graphqltest.New(f.handler).AsUser(testdata.Eins.Mail).MustQuery(t, englishPossibleQuery, vars, &out)
	if out.CourseInstance.Cie || out.CourseInstance.TaughtInEnglish {
		t.Errorf("a refused write took effect: %+v", out.CourseInstance)
	}

	// A decision taken before the rule existed is not turned into a refusal of an unrelated edit:
	// the cohort that already runs in English and is listed keeps both when its delivery is saved,
	// and may still be taken off the list.
	if _, err := f.schema.Pool.Exec(t.Context(),
		`UPDATE course_instance SET taught_in_english = true, cie = true WHERE id = $1`,
		f.instance); err != nil {
		t.Fatalf("cannot set the earlier decision: %v", err)
	}
	var kept struct{ SetCourseInstanceFormat formatOut }
	doors["browser"].MustQuery(t, setFormatMutation,
		map[string]any{"id": f.instance, "d": "WEEKLY", "en": true}, &kept)
	if !kept.SetCourseInstanceFormat.TaughtInEnglish {
		t.Error("saving the delivery lost the earlier English decision")
	}
	doors["browser"].MustQuery(t, setCiEMutation, map[string]any{"id": f.instance, "cie": true},
		&struct{ SetCourseInstanceCiE cieOut }{})
}

func TestTheLecturerWhoTeachesACohortListsItAsCiE(t *testing.T) {
	t.Parallel()

	f := assignmentHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Zwei, []string{"LECTURER"}},
	)
	f.assignPart(t, f.lab, testdata.Eins)
	listed := map[string]any{"id": f.instance, "cie": true}

	// In the browser and through a full-reach token; a plain token stays outside, as for everybody.
	var out struct{ SetCourseInstanceCiE cieOut }
	graphqltest.New(f.handler).AsUser(testdata.Eins.Mail).MustQuery(t, setCiEMutation, listed, &out)
	if !out.SetCourseInstanceCiE.Cie {
		t.Error("the lecturer could not list the cohort they teach")
	}
	graphqltest.New(f.handler).WithToken(fullReachToken(t, f.schema, testdata.Eins)).
		On(graphqltest.Token).MustQuery(t, setCiEMutation,
		map[string]any{"id": f.instance, "cie": false}, &out)
	if out.SetCourseInstanceCiE.Cie {
		t.Error("a full-reach token of the lecturer could not take the cohort off the list")
	}
	resp := graphqltest.New(f.handler).WithToken(testdata.Eins.Token).Do(t, setCiEMutation, listed)
	assertRefusal(t, resp, "FULL_REACH_REQUIRED")

	// The cohort read through one's own assignment says what it says on its own — listed, and
	// English possible — through both doors. It used to carry only the module's id and name.
	graphqltest.New(f.handler).AsUser(testdata.Eins.Mail).MustQuery(t, setCiEMutation, listed, &out)
	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var mine struct {
				MyAssignments []struct {
					Instance struct {
						Cie      bool
						Delivery string
						Module   struct{ EnglishPossible bool }
					}
				}
			}
			c.MustQuery(t, `query { myAssignments { instance { cie delivery module { englishPossible } } } }`,
				nil, &mine)
			if len(mine.MyAssignments) != 1 {
				t.Fatalf("the lecturer reads %d assignments, want 1", len(mine.MyAssignments))
			}
			if got := mine.MyAssignments[0].Instance; !got.Cie || got.Delivery != "WEEKLY" ||
				!got.Module.EnglishPossible {
				t.Errorf("the cohort through the assignment reads %+v", got)
			}
		})
	graphqltest.New(f.handler).AsUser(testdata.Eins.Mail).MustQuery(t, setCiEMutation,
		map[string]any{"id": f.instance, "cie": false}, &out)

	// Only CiE: the format stays the leads'.
	resp = graphqltest.New(f.handler).AsUser(testdata.Eins.Mail).Do(t, setFormatMutation,
		map[string]any{"id": f.instance, "d": "BLOCK", "en": nil})
	assertRefusal(t, resp, "NOT_YOUR_SUBJECT")

	// A colleague who teaches nothing here is refused, and learns nothing about who does.
	resp = graphqltest.New(f.handler).AsUser(testdata.Zwei.Mail).Do(t, setCiEMutation, listed)
	assertRefusal(t, resp, "NOT_YOUR_SUBJECT")
	graphqltest.AssertNoLeak(t, strings.Join(resp.Messages(), " "),
		append(graphqltest.DatabaseNoise(), testdata.Mails([]testdata.Persona{testdata.Eins})...)...)

	// The module description binds the lecturer like everybody.
	f.germanOnly(t)
	resp = graphqltest.New(f.handler).AsUser(testdata.Eins.Mail).Do(t, setCiEMutation, listed)
	assertRefusal(t, resp, "ENGLISH_NOT_OFFERED")

	// And a finished semester is finished for the lecturer too.
	if _, err := f.schema.Pool.Exec(t.Context(),
		`UPDATE semester SET phase = 'FINAL' WHERE code = $1`, f.semester); err != nil {
		t.Fatalf("cannot finish the semester: %v", err)
	}
	resp = graphqltest.New(f.handler).AsUser(testdata.Eins.Mail).Do(t, setCiEMutation,
		map[string]any{"id": f.instance, "cie": false})
	assertRefusal(t, resp, "ASSIGNMENT_PHASE_CLOSED")
}

const setWishCiEMutation = `mutation($p: ID!, $cie: Boolean!) {
	setWish(courseInstanceId: $p, priority: HAPPY_TO, cie: $cie) { id cie }
}`

func TestACiEOfferRidesOnTheWish(t *testing.T) {
	t.Parallel()

	f := assignmentHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Zwei, []string{"LECTURER"}},
	)

	// The owner's own data, through both doors: offered with one, read back with the other.
	var set struct{ SetWish struct{ Cie bool } }
	graphqltest.New(f.handler).WithToken(testdata.Eins.Token).MustQuery(t, setWishCiEMutation,
		map[string]any{"p": f.instance, "cie": true}, &set)
	if !set.SetWish.Cie {
		t.Fatal("the offer was not recorded")
	}
	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var out struct {
				MyWishes []struct {
					Cie      bool
					Instance struct {
						Delivery string
						Module   struct{ EnglishPossible bool }
					}
				}
			}
			c.MustQuery(t, `query { myWishes { cie instance { delivery module { englishPossible } } } }`,
				nil, &out)
			if len(out.MyWishes) != 1 || !out.MyWishes[0].Cie {
				t.Fatalf("the owner reads %+v, want the one wish with its offer", out.MyWishes)
			}
			// The cohort through the wish says what it says on its own.
			if got := out.MyWishes[0].Instance; got.Delivery != "WEEKLY" || !got.Module.EnglishPossible {
				t.Errorf("the cohort through the wish reads %+v", got)
			}
		})

	// An offer, not a listing: the cohort is untouched.
	var instance englishPossibleOut
	graphqltest.New(f.handler).AsUser(testdata.Eins.Mail).MustQuery(t, englishPossibleQuery,
		map[string]any{"id": f.instance}, &instance)
	if instance.CourseInstance.Cie {
		t.Error("offering CiE with a wish listed the cohort")
	}

	// As confidential as the wish: a colleague reads no row, so no offer, before publication.
	graphqltest.EachDoor(t, f.handler, testdata.Zwei.Mail, testdata.Zwei.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var out struct{ Wishes []struct{ Cie bool } }
			c.MustQuery(t, `query($s: String!) { wishes(semester: $s) { cie } }`,
				map[string]any{"s": f.semester}, &out)
			if len(out.Wishes) != 0 {
				t.Errorf("a colleague reads %d wishes before publication", len(out.Wishes))
			}
		})

	// Leaving it out is false, like the note; and it needs English in the module description.
	graphqltest.New(f.handler).AsUser(testdata.Eins.Mail).MustQuery(t,
		`mutation($p: ID!) { setWish(courseInstanceId: $p, priority: HAPPY_TO) { cie } }`,
		map[string]any{"p": f.instance}, &set)
	if set.SetWish.Cie {
		t.Error("a call without the offer kept it")
	}
	f.germanOnly(t)
	resp := graphqltest.New(f.handler).AsUser(testdata.Eins.Mail).Do(t, setWishCiEMutation,
		map[string]any{"p": f.instance, "cie": true})
	assertRefusal(t, resp, "ENGLISH_NOT_OFFERED")
	graphqltest.AssertNoLeak(t, strings.Join(resp.Messages(), " "),
		append(graphqltest.DatabaseNoise(), testdata.Mails(testdata.Others(testdata.Eins))...)...)
}
