package bootstrap_test

import (
	"testing"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/graphqltest"
	"github.com/obcode/tallox.go/internal/store"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

const setTrackLimitMutation = `mutation($p: ID!, $n: Int) {
	setWishTrackLimit(courseInstanceId: $p, maxTracks: $n)
}`

const trackLimitsQuery = `query($s: String!) {
	wishes(semester: $s) { person { mail } instance { track } trackLimit }
}`

type trackLimits struct {
	Wishes []struct {
		Person     struct{ Mail string }
		Instance   struct{ Track string }
		TrackLimit *int
	}
}

// secondCohort turns the fixture's instance into cohort A and declares B beside it. Returns B.
func (f wishFixture) secondCohort(t *testing.T) string {
	t.Helper()
	ctx := t.Context()

	if _, err := f.schema.Pool.Exec(ctx,
		`UPDATE course_instance SET track = 'A' WHERE id = $1`, f.instance); err != nil {
		t.Fatalf("cannot name the first cohort: %v", err)
	}
	semester, err := store.NewSemesters(f.schema.Pool).EnsureSemester(ctx, f.semester)
	if err != nil {
		t.Fatalf("cannot read the semester: %v", err)
	}
	modules := store.NewModules(f.schema.Pool)
	b, err := store.NewDemand(f.schema.Pool, modules).CreateCourseInstance(ctx,
		domain.NewCourseInstance{
			SemesterID:  semester.ID,
			ModuleID:    moduleIDOf(t, f.schema, storetest.FixtureModuleOrdinary),
			ProgrammeID: programmeIDOf(t, f.schema, f.programme),
			Track:       "B",
		})
	if err != nil {
		t.Fatalf("cannot declare cohort B: %v", err)
	}
	return b.ID.String()
}

func (f wishFixture) wishFor(t *testing.T, who testdata.Persona, instance, priority string) {
	t.Helper()
	var ignored struct{}
	graphqltest.New(f.handler).AsUser(who.Mail).MustQuery(t, setWishMutation,
		map[string]any{"p": instance, "prio": priority, "note": nil}, &ignored)
}

func limitsOf(t *testing.T, c *graphqltest.Client, semester, mail string) []*int {
	t.Helper()
	var out trackLimits
	c.MustQuery(t, trackLimitsQuery, map[string]any{"s": semester}, &out)
	var limits []*int
	for _, w := range out.Wishes {
		if w.Person.Mail == mail {
			limits = append(limits, w.TrackLimit)
		}
	}
	return limits
}

func allEqual(limits []*int, want int) bool {
	for _, l := range limits {
		if l == nil || *l != want {
			return false
		}
	}
	return len(limits) > 0
}

// The case from the faculty: a module in two cohorts, somebody who takes one of them and does not
// mind which. Two wishes and a bound of one — set and lifted through either door, because it is
// the owner's own data like the wishes.
func TestOneOfTwoCohortsIsTwoWishesAndABound(t *testing.T) {
	t.Parallel()

	f := wishHandler(t, grants{testdata.Eins, []string{"LECTURER"}})
	b := f.secondCohort(t)
	f.wishFor(t, testdata.Eins, f.instance, "HAPPY_TO")
	f.wishFor(t, testdata.Eins, b, "IF_NEEDED")

	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var set struct{ SetWishTrackLimit *int }
			// Named through cohort B; it holds for A as well.
			c.MustQuery(t, setTrackLimitMutation, map[string]any{"p": b, "n": 1}, &set)
			if set.SetWishTrackLimit == nil || *set.SetWishTrackLimit != 1 {
				t.Fatalf("the bound in force is %v, want 1", set.SetWishTrackLimit)
			}
			limits := limitsOf(t, c, f.semester, testdata.Eins.Mail)
			if len(limits) != 2 || !allEqual(limits, 1) {
				t.Errorf("both wishes should carry the bound of 1, got %v", limits)
			}

			var lifted struct{ SetWishTrackLimit *int }
			c.MustQuery(t, setTrackLimitMutation, map[string]any{"p": f.instance, "n": nil}, &lifted)
			for _, l := range limitsOf(t, c, f.semester, testdata.Eins.Mail) {
				if l != nil {
					t.Errorf("the bound survived being lifted: %d", *l)
				}
			}
		})
}

// The bound is part of who wants what, so it is visible exactly where the wishes are: hidden from
// colleagues before publication, shown to them after it, shown to the lead before it in the
// browser. It never reaches anybody through a row they could not see.
func TestTheBoundIsVisibleWhereTheWishIs(t *testing.T) {
	t.Parallel()

	f := wishHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Zwei, []string{"LECTURER"}},
		grants{testdata.Vier, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
	)
	f.leadGroup(t, testdata.Vier, f.group)
	b := f.secondCohort(t)
	f.wishFor(t, testdata.Eins, f.instance, "HAPPY_TO")
	f.wishFor(t, testdata.Eins, b, "HAPPY_TO")
	var ignored struct{}
	graphqltest.New(f.handler).AsUser(testdata.Eins.Mail).
		MustQuery(t, setTrackLimitMutation, map[string]any{"p": f.instance, "n": 1}, &ignored)

	graphqltest.EachDoor(t, f.handler, testdata.Zwei.Mail, testdata.Zwei.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var out trackLimits
			c.MustQuery(t, trackLimitsQuery, map[string]any{"s": f.semester}, &out)
			if len(out.Wishes) != 0 {
				t.Errorf("a colleague reads %+v before publication", out.Wishes)
			}
		})

	lead := limitsOf(t, graphqltest.New(f.handler).AsUser(testdata.Vier.Mail), f.semester,
		testdata.Eins.Mail)
	if len(lead) != 2 || !allEqual(lead, 1) {
		t.Errorf("the lead of the module's group reads the bounds %v before publication", lead)
	}

	f.publish(t)
	graphqltest.EachDoor(t, f.handler, testdata.Zwei.Mail, testdata.Zwei.Token,
		func(t *testing.T, c *graphqltest.Client) {
			limits := limitsOf(t, c, f.semester, testdata.Eins.Mail)
			if len(limits) != 2 || !allEqual(limits, 1) {
				t.Errorf("after publication a colleague reads the bounds %v, want 1 on both", limits)
			}
		})
}

// What is refused, and that each refusal says nothing about anybody else.
func TestABoundIsRefusedWhereAWishWouldBe(t *testing.T) {
	t.Parallel()

	f := wishHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Zwei, []string{"LECTURER"}},
		grants{testdata.Drei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
	)
	f.leadGroup(t, testdata.Drei, f.group)
	b := f.secondCohort(t)
	eins := graphqltest.New(f.handler).AsUser(testdata.Eins.Mail)
	zwei := graphqltest.New(f.handler).AsUser(testdata.Zwei.Mail)

	// Zwei has wished for neither cohort. That Eins has is not what the answer depends on: it
	// reads only the caller's own wishes, so it is the same with and without Eins's entry.
	resp := zwei.Do(t, setTrackLimitMutation, map[string]any{"p": b, "n": 1})
	assertRefusal(t, resp, "WISH_TRACK_LIMIT_WITHOUT_WISHES")
	f.wishFor(t, testdata.Eins, b, "HAPPY_TO")
	again := zwei.Do(t, setTrackLimitMutation, map[string]any{"p": b, "n": 1})
	assertRefusal(t, again, "WISH_TRACK_LIMIT_WITHOUT_WISHES")
	if resp.Messages()[0] != again.Messages()[0] {
		t.Errorf("the refusal changed once somebody else had wished: %q, then %q",
			resp.Messages()[0], again.Messages()[0])
	}
	graphqltest.AssertNoLeak(t, again.Messages()[0],
		append(graphqltest.DatabaseNoise(), testdata.Eins.Name, testdata.Eins.Mail)...)

	for _, n := range []int{0, 10} {
		assertRefusal(t, eins.Do(t, setTrackLimitMutation, map[string]any{"p": b, "n": n}),
			"WISH_TRACK_LIMIT_INVALID")
	}

	// The subject group's round shut: no wish, and no bound on one.
	var ignored struct{}
	graphqltest.New(f.handler).AsUser(testdata.Drei.Mail).MustQuery(t, setWishWindowMutation,
		map[string]any{"s": f.semester, "g": f.group.String(), "open": false}, &ignored)
	assertRefusal(t, eins.Do(t, setTrackLimitMutation, map[string]any{"p": b, "n": 1}),
		"WISH_WINDOW_CLOSED")

	if _, err := f.schema.Pool.Exec(t.Context(),
		`UPDATE semester SET phase = 'FINAL' WHERE code = $1`, f.semester); err != nil {
		t.Fatalf("cannot close the semester: %v", err)
	}
	assertRefusal(t, eins.Do(t, setTrackLimitMutation, map[string]any{"p": b, "n": 1}),
		"WISH_PHASE_CLOSED")
}

// Withdrawing the last wish for the cohorts takes the bound with it. Otherwise it would wait,
// unread, and qualify a wish entered for one of them much later.
func TestWithdrawingTheLastWishDropsTheBound(t *testing.T) {
	t.Parallel()

	f := wishHandler(t, grants{testdata.Eins, []string{"LECTURER"}})
	b := f.secondCohort(t)
	eins := graphqltest.New(f.handler).AsUser(testdata.Eins.Mail)
	f.wishFor(t, testdata.Eins, f.instance, "HAPPY_TO")
	f.wishFor(t, testdata.Eins, b, "HAPPY_TO")
	var ignored struct{}
	eins.MustQuery(t, setTrackLimitMutation, map[string]any{"p": b, "n": 1}, &ignored)

	withdraw := func(instance string) {
		t.Helper()
		var id string
		if err := f.schema.Pool.QueryRow(t.Context(),
			`SELECT id::text FROM wish WHERE course_instance_id = $1`, instance).Scan(&id); err != nil {
			t.Fatalf("cannot find the wish: %v", err)
		}
		eins.MustQuery(t, `mutation($id: ID!) { withdrawWish(id: $id) }`,
			map[string]any{"id": id}, &ignored)
	}

	// One wish left: the bound stays, it still qualifies something.
	withdraw(b)
	if limits := limitsOf(t, eins, f.semester, testdata.Eins.Mail); !allEqual(limits, 1) {
		t.Errorf("the bound went with the first withdrawal: %v", limits)
	}

	withdraw(f.instance)
	f.wishFor(t, testdata.Eins, b, "HAPPY_TO")
	for _, l := range limitsOf(t, eins, f.semester, testdata.Eins.Mail) {
		if l != nil {
			t.Errorf("a new wish came back with the old bound of %d", *l)
		}
	}
}
