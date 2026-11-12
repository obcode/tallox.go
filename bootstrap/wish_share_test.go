package bootstrap_test

import (
	"testing"

	"github.com/obcode/tallox.go/internal/graphqltest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// The fixture's cohort is a lecture of 2 SWS and a laboratory of 2 SWS: 4 SWS.

const setWishShareMutation = `mutation($p: ID!, $share: Int) {
	setWish(courseInstanceId: $p, priority: HAPPY_TO, shareHours: $share) { id shareHours }
}`

const wishSharesQuery = `query($s: String!) {
	wishes(semester: $s) { person { mail } shareHours }
}`

type wishShares struct {
	Wishes []struct {
		Person     struct{ Mail string }
		ShareHours *int
	}
}

func sharesOf(t *testing.T, c *graphqltest.Client, semester, mail string) []*int {
	t.Helper()
	var out wishShares
	c.MustQuery(t, wishSharesQuery, map[string]any{"s": semester}, &out)
	var shares []*int
	for _, w := range out.Wishes {
		if w.Person.Mail == mail {
			shares = append(shares, w.ShareHours)
		}
	}
	return shares
}

// The case from the faculty: two people divide a cohort's lecture between them, and each wishes
// for their part of its hours. Set, read back and taken back through either door, because it is
// the owner's own data like the wish.
func TestAWishStatesAShareOfTheCohort(t *testing.T) {
	t.Parallel()

	f := wishHandler(t, grants{testdata.Eins, []string{"LECTURER"}})

	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var set struct {
				SetWish struct{ ShareHours *int }
			}
			c.MustQuery(t, setWishShareMutation, map[string]any{"p": f.instance, "share": 2}, &set)
			if set.SetWish.ShareHours == nil || *set.SetWish.ShareHours != 2 {
				t.Fatalf("the written share is %v, want 2", set.SetWish.ShareHours)
			}
			var mine struct {
				MyWishes []struct{ ShareHours *int }
			}
			c.MustQuery(t, `query { myWishes { shareHours } }`, nil, &mine)
			if len(mine.MyWishes) != 1 || mine.MyWishes[0].ShareHours == nil ||
				*mine.MyWishes[0].ShareHours != 2 {
				t.Errorf("my wishes read %+v, want one with a share of 2", mine.MyWishes)
			}

			// Like the note, written with every call: leaving it out is the whole cohort.
			var whole struct {
				SetWish struct{ ShareHours *int }
			}
			c.MustQuery(t, setWishShareMutation, map[string]any{"p": f.instance, "share": nil}, &whole)
			if whole.SetWish.ShareHours != nil {
				t.Errorf("a wish without a share reads a share of %d", *whole.SetWish.ShareHours)
			}
		})
}

// A share as large as the cohort is the whole cohort, and is stored as such: "4 of 4" and "all of
// it" are one wish, not two.
func TestAShareOfTheWholeCohortIsTheWholeCohort(t *testing.T) {
	t.Parallel()

	f := wishHandler(t, grants{testdata.Eins, []string{"LECTURER"}})
	var set struct {
		SetWish struct{ ShareHours *int }
	}
	graphqltest.New(f.handler).AsUser(testdata.Eins.Mail).MustQuery(t, setWishShareMutation,
		map[string]any{"p": f.instance, "share": 4}, &set)
	if set.SetWish.ShareHours != nil {
		t.Errorf("a share of all 4 SWS reads %d, want null for the whole cohort",
			*set.SetWish.ShareHours)
	}
}

// What is refused: nothing, more than the cohort, and a share of a cohort whose hours nobody has
// stated. The refusal is about the cohort, which everybody may read, and says nothing about
// anybody else's wish.
func TestAShareOutsideTheCohortIsRefused(t *testing.T) {
	t.Parallel()

	f := wishHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Zwei, []string{"LECTURER"}},
	)
	zwei := graphqltest.New(f.handler).AsUser(testdata.Zwei.Mail)

	for _, share := range []int{0, -1, 5} {
		assertRefusal(t, zwei.Do(t, setWishShareMutation,
			map[string]any{"p": f.instance, "share": share}), "WISH_SHARE_OUT_OF_RANGE")
	}

	// The same answer with somebody else's share on the cohort.
	before := zwei.Do(t, setWishShareMutation, map[string]any{"p": f.instance, "share": 5})
	var ignored struct{}
	graphqltest.New(f.handler).AsUser(testdata.Eins.Mail).MustQuery(t, setWishShareMutation,
		map[string]any{"p": f.instance, "share": 2}, &ignored)
	after := zwei.Do(t, setWishShareMutation, map[string]any{"p": f.instance, "share": 5})
	assertRefusal(t, after, "WISH_SHARE_OUT_OF_RANGE")
	if before.Messages()[0] != after.Messages()[0] {
		t.Errorf("the refusal changed once somebody else had wished: %q, then %q",
			before.Messages()[0], after.Messages()[0])
	}
	graphqltest.AssertNoLeak(t, after.Messages()[0],
		append(graphqltest.DatabaseNoise(), testdata.Eins.Name, testdata.Eins.Mail)...)

	// A cohort whose parts state no hours has nothing to take a share of.
	if _, err := f.schema.Pool.Exec(t.Context(),
		`UPDATE instance_part SET teaching_hours = NULL WHERE course_instance_id = $1`,
		f.instance); err != nil {
		t.Fatalf("cannot unstate the hours: %v", err)
	}
	assertRefusal(t, zwei.Do(t, setWishShareMutation, map[string]any{"p": f.instance, "share": 1}),
		"WISH_SHARE_OUT_OF_RANGE")
}

// The share is part of who wants what, so it is visible exactly where the wish is: hidden from
// colleagues before publication through every door, shown to the responsible lead before it in
// the browser and through a full-reach token but not through a plain one, and shown to colleagues
// after publication.
func TestTheShareIsVisibleWhereTheWishIs(t *testing.T) {
	t.Parallel()

	f := wishHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Zwei, []string{"LECTURER"}},
		grants{testdata.Vier, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
	)
	f.leadGroup(t, testdata.Vier, f.group)
	var ignored struct{}
	graphqltest.New(f.handler).AsUser(testdata.Eins.Mail).MustQuery(t, setWishShareMutation,
		map[string]any{"p": f.instance, "share": 2}, &ignored)

	graphqltest.EachDoor(t, f.handler, testdata.Zwei.Mail, testdata.Zwei.Token,
		func(t *testing.T, c *graphqltest.Client) {
			if got := sharesOf(t, c, f.semester, testdata.Eins.Mail); len(got) != 0 {
				t.Errorf("a colleague reads a share before publication: %v", got)
			}
		})

	browser := graphqltest.New(f.handler).AsUser(testdata.Vier.Mail).On(graphqltest.Browser)
	full := graphqltest.New(f.handler).WithToken(fullReachToken(t, f.schema, testdata.Vier)).
		On(graphqltest.Token)
	plain := graphqltest.New(f.handler).WithToken(testdata.Vier.Token).On(graphqltest.Token)
	for name, c := range map[string]*graphqltest.Client{"browser": browser, "full reach": full} {
		got := sharesOf(t, c, f.semester, testdata.Eins.Mail)
		if len(got) != 1 || got[0] == nil || *got[0] != 2 {
			t.Errorf("%s: the lead reads the shares %v before publication, want 2", name, got)
		}
	}
	if got := sharesOf(t, plain, f.semester, testdata.Eins.Mail); len(got) != 0 {
		t.Errorf("a plain token of the lead reads somebody else's share: %v", got)
	}

	f.publish(t)
	graphqltest.EachDoor(t, f.handler, testdata.Zwei.Mail, testdata.Zwei.Token,
		func(t *testing.T, c *graphqltest.Client) {
			got := sharesOf(t, c, f.semester, testdata.Eins.Mail)
			if len(got) != 1 || got[0] == nil || *got[0] != 2 {
				t.Errorf("after publication a colleague reads the shares %v, want 2", got)
			}
		})
}
