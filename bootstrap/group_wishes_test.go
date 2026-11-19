package bootstrap_test

import (
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/graphqltest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// A subject group's lead shows the group its wishes (2026-11-19): once the round is shut, the
// members of the group read who wants what on the group's modules — through the browser and a
// full-reach token, never through a plain one — and nobody else reads anything new.

const publishGroupWishesMutation = `mutation($s: String!, $g: ID!) {
	publishSubjectGroupWishes(semester: $s, subjectGroupId: $g) {
		open wishesPublishedAt subjectGroup { code }
	}
}`

const groupWishDetailsQuery = `query($s: String!) {
	wishes(semester: $s) { person { mail } note createdAt }
}`

// joinGroup makes somebody a member of a subject group, as setMySubjectGroups would.
func (f wishFixture) joinGroup(t *testing.T, who testdata.Persona, group uuid.UUID) {
	t.Helper()

	if _, err := f.schema.Pool.Exec(t.Context(),
		`INSERT INTO person_subject_group (person_id, subject_group_id) VALUES ($1, $2)`,
		who.ID(), group); err != nil {
		t.Fatalf("cannot put %s in the subject group: %v", who.Mail, err)
	}
}

// groupWishesFixture is the cast: Drei leads the fixture group, Eins has wished for its module
// with a note, Zwei is a member of the group, Fuenf a member of another one.
func groupWishesFixture(t *testing.T) wishFixture {
	t.Helper()

	f := wishHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Zwei, []string{"LECTURER"}},
		grants{testdata.Drei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
		grants{testdata.Fuenf, []string{"LECTURER"}},
	)
	f.leadGroup(t, testdata.Drei, f.group)
	f.joinGroup(t, testdata.Zwei, f.group)
	f.joinGroup(t, testdata.Fuenf, f.otherGroup)

	graphqltest.New(f.handler).AsUser(testdata.Eins.Mail).MustQuery(t, setWishMutation,
		map[string]any{"p": f.instance, "prio": "HAPPY_TO", "note": "only the lecture"}, nil)
	return f
}

func (f wishFixture) shutRound(t *testing.T, lead testdata.Persona) {
	t.Helper()

	graphqltest.New(f.handler).AsUser(lead.Mail).MustQuery(t, setWishWindowMutation,
		map[string]any{"s": f.semester, "g": f.group.String(), "open": false}, nil)
}

// TestALeadShowsTheGroupItsWishesThroughEveryDoor is the feature: the member reads the wish
// through the browser and a full-reach token, without its note or timestamps; a plain token and a
// member of another group read nothing new.
func TestALeadShowsTheGroupItsWishesThroughEveryDoor(t *testing.T) {
	t.Parallel()

	f := groupWishesFixture(t)

	// Before: nothing for the member, through any door.
	for door, c := range f.threeDoors(t, testdata.Zwei) {
		if got := seen(t, c, f.semester); len(got) != 0 {
			t.Errorf("%s: a member reads %v before the lead showed the group anything", door, got)
		}
	}

	f.shutRound(t, testdata.Drei)
	var out struct {
		PublishSubjectGroupWishes struct {
			Open              bool
			WishesPublishedAt *string
		}
	}
	graphqltest.New(f.handler).AsUser(testdata.Drei.Mail).MustQuery(t, publishGroupWishesMutation,
		map[string]any{"s": f.semester, "g": f.group.String()}, &out)
	if out.PublishSubjectGroupWishes.WishesPublishedAt == nil {
		t.Fatal("the window does not say when the wishes were shown to the group")
	}

	want := map[string]bool{"browser": true, "token": false, "full reach": true}
	for door, c := range f.threeDoors(t, testdata.Zwei) {
		got := seen(t, c, f.semester)
		if visible := slices.Contains(got, testdata.Eins.Mail); visible != want[door] {
			t.Errorf("%s: member reads the colleague's wish = %v, want %v (%v)",
				door, visible, want[door], got)
		}
	}

	// The note and the timestamps stay with those who read them before.
	var details struct {
		Wishes []struct {
			Person    struct{ Mail string }
			Note      string
			CreatedAt *string
		}
	}
	graphqltest.New(f.handler).AsUser(testdata.Zwei.Mail).MustQuery(t, groupWishDetailsQuery,
		map[string]any{"s": f.semester}, &details)
	for _, w := range details.Wishes {
		if w.Note != "" || w.CreatedAt != nil {
			t.Errorf("a member reads the note %q / time %v of a wish shown only within the group",
				w.Note, w.CreatedAt)
		}
	}

	// A member of another group, and a lecturer in no group, read nothing new — through any door.
	for _, who := range []testdata.Persona{testdata.Fuenf} {
		for door, c := range f.threeDoors(t, who) {
			if got := seen(t, c, f.semester); len(got) != 0 {
				t.Errorf("%s, %s: reads %v from a group they are not in", who.Mail, door, got)
			}
		}
	}

	// Repeating it is not an error and does not move the timestamp, also after reopening.
	first := *out.PublishSubjectGroupWishes.WishesPublishedAt
	graphqltest.New(f.handler).AsUser(testdata.Drei.Mail).MustQuery(t, setWishWindowMutation,
		map[string]any{"s": f.semester, "g": f.group.String(), "open": true}, nil)
	graphqltest.New(f.handler).AsUser(testdata.Drei.Mail).MustQuery(t, publishGroupWishesMutation,
		map[string]any{"s": f.semester, "g": f.group.String()}, &out)
	if got := *out.PublishSubjectGroupWishes.WishesPublishedAt; got != first {
		t.Errorf("a repeated call moved the timestamp from %s to %s", first, got)
	}
}

// TestShowingTheGroupItsWishesIsRefusedWhereItMustBe: an open round, somebody who does not lead the
// group, and a plain token. None of the refusals may say anything about the wishes.
func TestShowingTheGroupItsWishesIsRefusedWhereItMustBe(t *testing.T) {
	t.Parallel()

	f := groupWishesFixture(t)
	vars := map[string]any{"s": f.semester, "g": f.group.String()}
	noise := append(graphqltest.DatabaseNoise(),
		testdata.Mails(testdata.Others(testdata.Drei))...)

	check := func(t *testing.T, c *graphqltest.Client, want string) {
		t.Helper()
		resp := c.Do(t, publishGroupWishesMutation, vars)
		if code := errorCode(t, resp); code != want {
			t.Errorf("refused with %q, want %q", code, want)
		}
		for _, message := range resp.Messages() {
			graphqltest.AssertNoLeak(t, message, noise...)
		}
	}

	t.Run("open round", func(t *testing.T) {
		for door, c := range f.threeDoors(t, testdata.Drei) {
			want := "WISH_WINDOW_OPEN"
			if door == "token" {
				want = "FULL_REACH_REQUIRED"
			}
			t.Run(door, func(t *testing.T) { check(t, c, want) })
		}
	})

	f.shutRound(t, testdata.Drei)

	t.Run("member, not lead", func(t *testing.T) {
		check(t, graphqltest.New(f.handler).AsUser(testdata.Zwei.Mail).On(graphqltest.Browser),
			"NOT_YOUR_SUBJECT_GROUP")
	})
	t.Run("lead through a plain token", func(t *testing.T) {
		check(t, graphqltest.New(f.handler).WithToken(testdata.Drei.Token).On(graphqltest.Token),
			"FULL_REACH_REQUIRED")
	})

	// None of the refusals published anything.
	if got := seen(t, graphqltest.New(f.handler).AsUser(testdata.Zwei.Mail), f.semester); len(got) != 0 {
		t.Errorf("after refusals only, a member reads %v", got)
	}

	t.Run("lead through a full-reach token", func(t *testing.T) {
		c := graphqltest.New(f.handler).WithToken(fullReachToken(t, f.schema, testdata.Drei)).
			On(graphqltest.Token)
		if resp := c.Do(t, publishGroupWishesMutation, vars); resp.Failed() {
			t.Errorf("the lead's full-reach token was refused: %v", resp.Messages())
		}
	})
}

const wishGroupQuery = `query($s: String!) {
	wishes(semester: $s) { person { mail } instance { module { subjectGroup { id code } } } }
}`

// TestAWishNamesTheGroupOfItsModuleThroughEveryDoor: a wish answers
// instance.module.subjectGroup like any module does. The wish screen keeps another person's wish
// only on a module of a group that has shown its wishes, and decides that by this field; with the
// field always null, the member who was shown the wishes saw none of them. Every door that reads
// the row reads the group — the owner through all three, the member and the lead through those
// that read other people's wishes before the semester publication.
func TestAWishNamesTheGroupOfItsModuleThroughEveryDoor(t *testing.T) {
	t.Parallel()

	f := groupWishesFixture(t)
	f.shutRound(t, testdata.Drei)
	graphqltest.New(f.handler).AsUser(testdata.Drei.Mail).MustQuery(t, publishGroupWishesMutation,
		map[string]any{"s": f.semester, "g": f.group.String()}, nil)

	check := func(who testdata.Persona, doors ...string) {
		t.Helper()

		all := f.threeDoors(t, who)
		for _, door := range doors {
			var out struct {
				Wishes []struct {
					Person   struct{ Mail string }
					Instance struct {
						Module struct {
							SubjectGroup *struct{ ID, Code string }
						}
					}
				}
			}
			all[door].MustQuery(t, wishGroupQuery, map[string]any{"s": f.semester}, &out)
			if len(out.Wishes) == 0 {
				t.Errorf("%s, %s: reads no wish at all", who.Mail, door)
			}
			for _, w := range out.Wishes {
				g := w.Instance.Module.SubjectGroup
				if g == nil || g.ID != f.group.String() || g.Code == "" {
					t.Errorf("%s, %s: the wish of %s names the module's group as %+v, want %s",
						who.Mail, door, w.Person.Mail, g, f.group)
				}
			}
		}
	}
	check(testdata.Eins, "browser", "token", "full reach")
	check(testdata.Zwei, "browser", "full reach")
	check(testdata.Drei, "browser", "full reach")
}
