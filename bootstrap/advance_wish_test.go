package bootstrap_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/graphqltest"
	"github.com/obcode/tallox.go/internal/store"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// Advance wishes ("Vormerkungen") through the real handler. The wish fixture's module, for a
// semester the fixture has not touched: no row, no cohort, no release.

const advanceSemester = "2028-SS"

const setAdvanceWishMutation = `mutation($s: String!, $m: ID!, $p: String!, $prio: WishPriority!, $note: String, $n: Int) {
	setAdvanceWish(semester: $s, module: $m, programme: $p, priority: $prio, note: $note, maxTracks: $n) {
		id semester priority note maxTracks status
		person { mail } module { name } programme { code }
	}
}`

const myAdvanceWishesQuery = `query { myAdvanceWishes { id semester status maxTracks } }`

const advanceWishesQuery = `query($s: String!) {
	advanceWishes(semester: $s) { id note createdAt person { mail } }
}`

const myWishesQuery = `query($s: String) {
	myWishes(semester: $s) { priority note trackLimit instance { track } }
}`

type advanceWishRow struct {
	ID, Semester, Status string
	MaxTracks            *int
}

type advanceWishesOut struct {
	AdvanceWishes []struct {
		ID, Note  string
		CreatedAt *string
		Person    struct{ Mail string }
	}
}

func (f wishFixture) noteAdvance(t *testing.T, c *graphqltest.Client, prio, note string,
	maxTracks any) string {
	t.Helper()
	var out struct{ SetAdvanceWish struct{ ID, Status string } }
	c.MustQuery(t, setAdvanceWishMutation, map[string]any{
		"s": advanceSemester, "m": moduleIDOfFixture(t, f).String(), "p": f.programme,
		"prio": prio, "note": note, "n": maxTracks,
	}, &out)
	if out.SetAdvanceWish.Status != "PENDING" {
		t.Errorf("a fresh advance wish reads %s, want PENDING", out.SetAdvanceWish.Status)
	}
	return out.SetAdvanceWish.ID
}

// releaseAdvance has the lead release the programme for the advance semester, through the API.
func (f wishFixture) releaseAdvance(t *testing.T, lead testdata.Persona) graphqltest.Response {
	t.Helper()
	resp := graphqltest.New(f.handler).AsUser(lead.Mail).Do(t, setDemandCompleteMutation,
		map[string]any{"s": advanceSemester, "p": f.programme, "c": true})
	if resp.Failed() {
		t.Fatalf("the release failed: %s", resp.Body)
	}
	return resp
}

// The case from the faculty, end to end: noted down years ahead through either door, nothing
// happens while the demand is open, and the lead's release turns it into a wish on each cohort.
func TestAnAdvanceWishBecomesAWishWhenTheLeadReleases(t *testing.T) {
	t.Parallel()

	f := wishHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}},
	)
	f.leadProgramme(t, testdata.Vier, f.programme)

	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			// The same offering through both doors is the same row, changed.
			f.noteAdvance(t, c, "FIRST_CHOICE", "nur die Vorlesung", 1)
			var mine struct{ MyAdvanceWishes []advanceWishRow }
			c.MustQuery(t, myAdvanceWishesQuery, nil, &mine)
			if len(mine.MyAdvanceWishes) != 1 || mine.MyAdvanceWishes[0].Semester != advanceSemester {
				t.Errorf("myAdvanceWishes reads %+v, want the one for %s", mine.MyAdvanceWishes,
					advanceSemester)
			}
		})

	// The lead plans two cohorts — through planDemand, the path the screen takes — and nothing
	// converts while the demand is not released.
	var plan planReport
	graphqltest.New(f.handler).AsUser(testdata.Vier.Mail).MustQuery(t, planMutation, map[string]any{
		"s": advanceSemester, "p": f.programme, "d": false,
		"e": []map[string]any{{
			"moduleId": moduleIDOfFixture(t, f).String(),
			"tracks":   []map[string]any{{"track": "A", "groups": 1}, {"track": "B", "groups": 1}},
		}},
	}, &plan)
	if len(plan.PlanDemand.Created) != 2 {
		t.Fatalf("the plan created %d cohorts, want two", len(plan.PlanDemand.Created))
	}

	eins := graphqltest.New(f.handler).AsUser(testdata.Eins.Mail)
	var before struct{ MyWishes []struct{ Priority string } }
	eins.MustQuery(t, myWishesQuery, map[string]any{"s": advanceSemester}, &before)
	if len(before.MyWishes) != 0 {
		t.Fatalf("before the release there are %d wishes, want none", len(before.MyWishes))
	}

	f.releaseAdvance(t, testdata.Vier)

	var after struct {
		MyWishes []struct {
			Priority, Note string
			TrackLimit     *int
			Instance       struct{ Track string }
		}
	}
	eins.MustQuery(t, myWishesQuery, map[string]any{"s": advanceSemester}, &after)
	if len(after.MyWishes) != 2 {
		t.Fatalf("after the release there are %d wishes, want one per cohort", len(after.MyWishes))
	}
	for _, w := range after.MyWishes {
		if w.Priority != "FIRST_CHOICE" || w.Note != "nur die Vorlesung" ||
			w.TrackLimit == nil || *w.TrackLimit != 1 {
			t.Errorf("the converted wish on %s reads %+v", w.Instance.Track, w)
		}
	}
	var mine struct{ MyAdvanceWishes []advanceWishRow }
	eins.MustQuery(t, myAdvanceWishesQuery, nil, &mine)
	if len(mine.MyAdvanceWishes) != 1 || mine.MyAdvanceWishes[0].Status != "CONVERTED" {
		t.Errorf("after the release the advance wish reads %+v, want CONVERTED", mine.MyAdvanceWishes)
	}

	// Released: from now on the wish is what one enters.
	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			resp := c.Do(t, setAdvanceWishMutation, map[string]any{
				"s": advanceSemester, "m": moduleIDOfFixture(t, f).String(), "p": f.programme,
				"prio": "HAPPY_TO", "note": nil, "n": nil,
			})
			assertRefusal(t, resp, "ADVANCE_WISH_RELEASED")
		})
}

// Not needed after all: the release without a cohort of the module is the notification.
func TestAReleaseWithoutTheModuleSaysNotOffered(t *testing.T) {
	t.Parallel()

	f := wishHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}},
	)
	f.leadProgramme(t, testdata.Vier, f.programme)
	f.noteAdvance(t, graphqltest.New(f.handler).AsUser(testdata.Eins.Mail), "HAPPY_TO", "", nil)
	f.releaseAdvance(t, testdata.Vier)

	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var mine struct{ MyAdvanceWishes []advanceWishRow }
			c.MustQuery(t, myAdvanceWishesQuery, nil, &mine)
			if len(mine.MyAdvanceWishes) != 1 || mine.MyAdvanceWishes[0].Status != "NOT_OFFERED" {
				t.Errorf("reads %+v, want NOT_OFFERED", mine.MyAdvanceWishes)
			}
		})
}

// The confidentiality rule, unchanged: an advance wish is read where the wish would be — and
// never through a plain token of somebody else, never by a colleague before publication.
func TestAdvanceWishesAreSeenWhereWishesAre(t *testing.T) {
	t.Parallel()

	f := wishHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Zwei, []string{"LECTURER"}},
		grants{testdata.Vier, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
	)
	f.leadGroup(t, testdata.Vier, f.group)
	f.noteAdvance(t, graphqltest.New(f.handler).AsUser(testdata.Eins.Mail), "HAPPY_TO",
		"nur wenn es sonst niemand macht", nil)

	seen := func(t *testing.T, c *graphqltest.Client) advanceWishesOut {
		t.Helper()
		var out advanceWishesOut
		c.MustQuery(t, advanceWishesQuery, map[string]any{"s": advanceSemester}, &out)
		return out
	}

	graphqltest.EachDoor(t, f.handler, testdata.Zwei.Mail, testdata.Zwei.Token,
		func(t *testing.T, c *graphqltest.Client) {
			if got := seen(t, c); len(got.AdvanceWishes) != 0 {
				t.Errorf("a colleague sees %d advance wishes before publication", len(got.AdvanceWishes))
			}
		})

	t.Run("the subject group's lead", func(t *testing.T) {
		browser := seen(t, graphqltest.New(f.handler).AsUser(testdata.Vier.Mail))
		if len(browser.AdvanceWishes) != 1 || browser.AdvanceWishes[0].Note == "" {
			t.Errorf("in the browser the lead reads %+v, want the advance wish with its note", browser)
		}
		plain := seen(t, graphqltest.New(f.handler).WithToken(testdata.Vier.Token))
		if len(plain.AdvanceWishes) != 0 {
			t.Errorf("through a plain token the lead reads %d advance wishes, want none",
				len(plain.AdvanceWishes))
		}
		full := seen(t, graphqltest.New(f.handler).WithToken(fullReachToken(t, f.schema, testdata.Vier)).
			On(graphqltest.Token))
		if len(full.AdvanceWishes) != 1 {
			t.Errorf("with full reach the lead reads %d advance wishes, want what the browser reads",
				len(full.AdvanceWishes))
		}
	})

	// Published: who wants what is everybody's, the note and the order stay the owner's.
	semester, err := store.NewSemesters(f.schema.Pool).EnsureSemester(t.Context(), advanceSemester)
	if err != nil {
		t.Fatalf("cannot record the semester: %v", err)
	}
	if _, err := f.schema.Pool.Exec(t.Context(),
		`UPDATE semester SET wishes_published_at = now() WHERE id = $1`, semester.ID); err != nil {
		t.Fatalf("cannot publish: %v", err)
	}
	graphqltest.EachDoor(t, f.handler, testdata.Zwei.Mail, testdata.Zwei.Token,
		func(t *testing.T, c *graphqltest.Client) {
			got := seen(t, c)
			if len(got.AdvanceWishes) != 1 {
				t.Fatalf("after publication a colleague sees %d advance wishes, want one",
					len(got.AdvanceWishes))
			}
			if got.AdvanceWishes[0].Note != "" || got.AdvanceWishes[0].CreatedAt != nil {
				t.Errorf("after publication a colleague reads the note %q and the time %v, want neither",
					got.AdvanceWishes[0].Note, got.AdvanceWishes[0].CreatedAt)
			}
		})
}

// The conversion runs inside the release and the demand planning, and reports nothing: the lead's
// answer is the same whether or not anybody noted the module down — that would be the count.
func TestTheReleaseSaysNothingAboutAdvanceWishes(t *testing.T) {
	t.Parallel()

	answer := func(t *testing.T, withAdvance bool) []byte {
		t.Helper()
		f := wishHandler(t,
			grants{testdata.Eins, []string{"LECTURER"}},
			grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}},
		)
		f.leadProgramme(t, testdata.Vier, f.programme)
		if withAdvance {
			f.noteAdvance(t, graphqltest.New(f.handler).AsUser(testdata.Eins.Mail), "HAPPY_TO",
				"", nil)
		}

		var plan planReport
		resp := graphqltest.New(f.handler).AsUser(testdata.Vier.Mail).Do(t, planMutation,
			map[string]any{
				"s": advanceSemester, "p": f.programme, "d": true,
				"e": []map[string]any{{
					"moduleId": moduleIDOfFixture(t, f).String(),
					"tracks":   []map[string]any{{"track": "", "groups": 1}},
				}},
			})
		resp.Decode(t, &plan)
		release := f.releaseAdvance(t, testdata.Vier)
		graphqltest.AssertNoLeak(t, resp.Body+release.Body, testdata.Mails(testdata.Others(testdata.Vier))...)
		return append(append([]byte{}, resp.Data...), release.Data...)
	}

	with, without := answer(t, true), answer(t, false)
	if !bytes.Equal(with, without) {
		t.Errorf("the lead's answers differ with an advance wish noted down:\n%s\n%s",
			with, without)
	}
}

func TestAnAdvanceWishIsWithdrawnOnlyByItsOwner(t *testing.T) {
	t.Parallel()

	f := wishHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Zwei, []string{"LECTURER"}},
	)
	id := f.noteAdvance(t, graphqltest.New(f.handler).AsUser(testdata.Eins.Mail), "HAPPY_TO", "", nil)

	const withdraw = `mutation($id: ID!) { withdrawAdvanceWish(id: $id) }`
	graphqltest.EachDoor(t, f.handler, testdata.Zwei.Mail, testdata.Zwei.Token,
		func(t *testing.T, c *graphqltest.Client) {
			resp := c.Do(t, withdraw, map[string]any{"id": id})
			assertRefusal(t, resp, "ADVANCE_WISH_NOT_FOUND")
			graphqltest.AssertNoLeak(t, strings.Join(resp.Messages(), " "),
				append(graphqltest.DatabaseNoise(), testdata.Eins.Mail)...)
		})

	var out struct{ WithdrawAdvanceWish string }
	graphqltest.New(f.handler).WithToken(testdata.Eins.Token).MustQuery(t, withdraw,
		map[string]any{"id": id}, &out)
	if out.WithdrawAdvanceWish != id {
		t.Errorf("withdrawing answered %q, want the id", out.WithdrawAdvanceWish)
	}
}

// Since 2026-11-12: after the release, a module the demand lacks can still be noted down — the
// demand may grow, and the lead wants to see who would teach it. Through either door; it reads
// NOT_OFFERED, and becomes wishes when the lead adds the module. A module the demand has stays
// refused, with the same answer whether or not anybody else wants it.
func TestAnAdvanceWishCanBeNotedAfterTheReleaseForAModuleNotInTheDemand(t *testing.T) {
	t.Parallel()

	f := wishHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}},
	)
	f.leadProgramme(t, testdata.Vier, f.programme)
	f.releaseAdvance(t, testdata.Vier)

	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var out struct{ SetAdvanceWish struct{ Status, Note string } }
			c.MustQuery(t, setAdvanceWishMutation, map[string]any{
				"s": advanceSemester, "m": moduleIDOfFixture(t, f).String(), "p": f.programme,
				"prio": "HAPPY_TO", "note": "Wahlfach", "n": nil,
			}, &out)
			if out.SetAdvanceWish.Status != "NOT_OFFERED" || out.SetAdvanceWish.Note != "Wahlfach" {
				t.Errorf("noted down after the release it reads %+v, want NOT_OFFERED with the note",
					out.SetAdvanceWish)
			}
		})

	// The lead adds the module to the released demand: the advance wish becomes the wish.
	var plan planReport
	graphqltest.New(f.handler).AsUser(testdata.Vier.Mail).MustQuery(t, planMutation, map[string]any{
		"s": advanceSemester, "p": f.programme, "d": false,
		"e": []map[string]any{{
			"moduleId": moduleIDOfFixture(t, f).String(),
			"tracks":   []map[string]any{{"track": "", "groups": 1}},
		}},
	}, &plan)
	var mine struct{ MyWishes []struct{ Note string } }
	graphqltest.New(f.handler).AsUser(testdata.Eins.Mail).MustQuery(t, myWishesQuery,
		map[string]any{"s": advanceSemester}, &mine)
	if len(mine.MyWishes) != 1 || mine.MyWishes[0].Note != "Wahlfach" {
		t.Fatalf("after the lead added the module the wishes are %+v, want the one noted down",
			mine.MyWishes)
	}
}

func TestTheRefusalAfterTheReleaseSaysNothingAboutOthers(t *testing.T) {
	t.Parallel()

	refusal := func(t *testing.T, withOthers bool) string {
		t.Helper()
		f := wishHandler(t,
			grants{testdata.Eins, []string{"LECTURER"}},
			grants{testdata.Zwei, []string{"LECTURER"}},
			grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}},
		)
		f.leadProgramme(t, testdata.Vier, f.programme)
		if withOthers {
			f.noteAdvance(t, graphqltest.New(f.handler).AsUser(testdata.Zwei.Mail), "FIRST_CHOICE",
				"", nil)
		}
		var plan planReport
		graphqltest.New(f.handler).AsUser(testdata.Vier.Mail).MustQuery(t, planMutation,
			map[string]any{
				"s": advanceSemester, "p": f.programme, "d": false,
				"e": []map[string]any{{
					"moduleId": moduleIDOfFixture(t, f).String(),
					"tracks":   []map[string]any{{"track": "", "groups": 1}},
				}},
			}, &plan)
		f.releaseAdvance(t, testdata.Vier)

		var messages []string
		graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
			func(t *testing.T, c *graphqltest.Client) {
				resp := c.Do(t, setAdvanceWishMutation, map[string]any{
					"s": advanceSemester, "m": moduleIDOfFixture(t, f).String(), "p": f.programme,
					"prio": "HAPPY_TO", "note": nil, "n": nil,
				})
				assertRefusal(t, resp, "ADVANCE_WISH_RELEASED")
				graphqltest.AssertNoLeak(t, strings.Join(resp.Messages(), " "),
					append(graphqltest.DatabaseNoise(), testdata.Zwei.Mail)...)
				messages = append(messages, resp.Messages()...)
			})
		return strings.Join(messages, "|")
	}

	if with, without := refusal(t, true), refusal(t, false); with != without {
		t.Errorf("the refusal differs with somebody else's interest:\n%s\n%s", with, without)
	}
}

func moduleIDOfFixture(t *testing.T, f wishFixture) uuid.UUID {
	t.Helper()
	return moduleIDOf(t, f.schema, storetest.FixtureModuleOrdinary)
}
