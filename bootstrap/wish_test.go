package bootstrap_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/bootstrap"
	"github.com/obcode/tallox.go/internal/auth"
	"github.com/obcode/tallox.go/internal/buildinfo"
	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/graphqltest"
	"github.com/obcode/tallox.go/internal/store"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// The wish phase through the API, through both doors.
//
// This is the area the whole project is judged on, so the assertions here are about the leak
// channels and not only about the rows: the list, the absence of any count, the wording of the
// refusals, and what a Personal Access Token reaches that a browser session does not.

type wishFixture struct {
	handler http.Handler
	schema  *storetest.Schema
	// instance is what everybody registers interest in.
	instance string
	// lecture is one of its parts. Nothing points at it any more; it is here so that "removing a
	// part" stays a probe with a real row behind it.
	lecture string
	// semester is the one the instance is in, already switched into the wish phase.
	semester string
	// programme is the study programme whose demand the instance is; other is a second one.
	programme      string
	otherProgramme string
	// group is the subject group the module is in; otherGroup is a second one.
	group      uuid.UUID
	otherGroup uuid.UUID
}

// wishHandler seeds people, a catalogue, an instance with parts, and a semester in the wish
// phase, and returns the handler Serve would build.
func wishHandler(t *testing.T, people ...grants) wishFixture {
	t.Helper()

	s := storetest.New(t)
	ctx := t.Context()

	for _, p := range people {
		storetest.SeedPerson(t, s, p.who, p.roles...)

		parsed, err := auth.ParseToken(p.who.Token)
		if err != nil {
			t.Fatalf("fixture token of %s does not parse: %v", p.who.Name, err)
		}
		storetest.SeedToken(t, s, p.who, auth.HashSecret(parsed.Secret), storetest.TokenOptions{
			Description: "wish test",
		})
	}

	storetest.SeedZPACatalogue(t, s)
	if _, err := store.NewCatalogue(s.Pool).Project(ctx, nil); err != nil {
		t.Fatalf("cannot project the catalogue: %v", err)
	}

	modules := store.NewModules(s.Pool)
	semesters := store.NewSemesters(s.Pool)

	semester, err := semesters.EnsureSemester(ctx, "2027-SS")
	if err != nil {
		t.Fatalf("cannot record the semester: %v", err)
	}

	f := wishFixture{
		schema:         s,
		semester:       semester.Code,
		programme:      storetest.FixtureProgrammeA,
		otherProgramme: storetest.FixtureProgrammeB,
	}

	moduleID := moduleIDOf(t, s, storetest.FixtureModuleOrdinary)
	programmeID := programmeIDOf(t, s, f.programme)

	if _, err := modules.SetModuleComponents(ctx, moduleID, []domain.ModuleComponent{
		{Kind: domain.PartKindLecture, TeachingHours: 2, Position: 0},
		{Kind: domain.PartKindLab, TeachingHours: 2, Position: 1},
	}, uuid.Nil); err != nil {
		t.Fatalf("cannot state the module's split: %v", err)
	}

	f.group = seedGroup(t, s, "MATHE", "Mathematik")
	f.otherGroup = seedGroup(t, s, "SWE", "Softwarefächer")
	if _, err := s.Pool.Exec(ctx,
		`INSERT INTO module_subject_group (module_id, subject_group_id) VALUES ($1, $2)`,
		moduleID, f.group); err != nil {
		t.Fatalf("cannot put the module in a subject group: %v", err)
	}

	instance, err := store.NewDemand(s.Pool, modules).CreateCourseInstance(ctx,
		domain.NewCourseInstance{
			SemesterID: semester.ID, ModuleID: moduleID, ProgrammeID: programmeID,
		})
	if err != nil {
		t.Fatalf("cannot declare the instance: %v", err)
	}
	f.instance = instance.ID.String()
	f.lecture = instance.Parts[0].ID.String()

	// The wish phase is the only one wishes may be written in, so the fixture starts there.
	if _, err := s.Pool.Exec(ctx,
		`UPDATE semester SET phase = 'WISHES' WHERE id = $1`, semester.ID); err != nil {
		t.Fatalf("cannot switch the semester into the wish phase: %v", err)
	}

	planning := domain.NewSemesterService(semesters, nil)
	f.handler = bootstrap.Handler(bootstrap.Options{
		Build: buildinfo.Info{Version: "test"},
		Auth: auth.Config{
			Mode:   auth.ModeProxy,
			Users:  store.NewDirectory(s.Pool),
			Tokens: store.NewDirectory(s.Pool),
		},
		People:        domain.NewPeopleService(store.NewPeople(s.Pool), nil),
		Planning:      planning,
		Catalogue:     domain.NewCatalogueService(modules),
		Demand:        domain.NewDemandService(store.NewDemand(s.Pool, modules), modules, planning),
		SubjectGroups: domain.NewSubjectGroupService(store.NewSubjectGroups(s.Pool)),
		Wishes:        domain.NewWishService(store.NewWishes(s.Pool), planning),
	})
	return f
}

func moduleIDOf(t *testing.T, s *storetest.Schema, zpaID int64) uuid.UUID {
	t.Helper()

	var id uuid.UUID
	if err := s.Pool.QueryRow(t.Context(),
		`SELECT id FROM module WHERE zpa_module_ref = $1`, zpaID).Scan(&id); err != nil {
		t.Fatalf("cannot find module %d: %v", zpaID, err)
	}
	return id
}

func programmeIDOf(t *testing.T, s *storetest.Schema, code string) uuid.UUID {
	t.Helper()

	var id uuid.UUID
	if err := s.Pool.QueryRow(t.Context(),
		`SELECT id FROM programme WHERE code = $1`, code).Scan(&id); err != nil {
		t.Fatalf("cannot find programme %s: %v", code, err)
	}
	return id
}

func seedGroup(t *testing.T, s *storetest.Schema, code, name string) uuid.UUID {
	t.Helper()

	var id uuid.UUID
	if err := s.Pool.QueryRow(t.Context(),
		`INSERT INTO subject_group (code, name) VALUES ($1, $2) RETURNING id`,
		code, name).Scan(&id); err != nil {
		t.Fatalf("cannot seed the subject group %s: %v", code, err)
	}
	return id
}

// leadProgramme and leadGroup assign a scope to somebody who already holds the role.
func (f wishFixture) leadProgramme(t *testing.T, who testdata.Persona, code string) {
	t.Helper()

	if _, err := f.schema.Pool.Exec(t.Context(),
		`INSERT INTO person_programme_scope (person_id, role, programme_id)
		 SELECT $1, 'PROGRAMME_LEAD', id FROM programme WHERE code = $2`,
		who.ID(), code); err != nil {
		t.Fatalf("cannot make %s the lead of %s: %v", who.Name, code, err)
	}
}

func (f wishFixture) leadGroup(t *testing.T, who testdata.Persona, group uuid.UUID) {
	t.Helper()

	if _, err := f.schema.Pool.Exec(t.Context(),
		`INSERT INTO person_subject_group_scope (person_id, role, subject_group_id)
		 VALUES ($1, 'SUBJECT_GROUP_LEAD', $2)`, who.ID(), group); err != nil {
		t.Fatalf("cannot make %s the lead of the group: %v", who.Name, err)
	}
}

func (f wishFixture) publish(t *testing.T) {
	t.Helper()

	if _, err := f.schema.Pool.Exec(t.Context(),
		`UPDATE semester SET wishes_published_at = now() WHERE code = $1`, f.semester); err != nil {
		t.Fatalf("cannot publish the wishes: %v", err)
	}
}

const setWishMutation = `mutation($p: ID!, $prio: WishPriority!, $note: String) {
	setWish(courseInstanceId: $p, priority: $prio, note: $note) {
		id priority note
		person { mail }
		instance { track programme { code } module { name } }
	}
}`

const wishesQuery = `query($s: String!) {
	wishes(semester: $s) { id priority person { mail } }
}`

// register puts one person's interest in through the API, as that person.
func (f wishFixture) register(t *testing.T, who testdata.Persona) string {
	t.Helper()

	var out struct {
		SetWish struct{ ID string }
	}
	graphqltest.New(f.handler).AsUser(who.Mail).MustQuery(t, setWishMutation,
		map[string]any{"p": f.instance, "prio": "HAPPY_TO", "note": nil}, &out)
	return out.SetWish.ID
}

// seen reads the semester's wishes as somebody, through one door, and returns the addresses.
func seen(t *testing.T, c *graphqltest.Client, semester string) []string {
	t.Helper()

	var out struct {
		Wishes []struct {
			Person struct{ Mail string }
		}
	}
	c.MustQuery(t, wishesQuery, map[string]any{"s": semester}, &out)

	mails := make([]string, 0, len(out.Wishes))
	for _, w := range out.Wishes {
		mails = append(mails, w.Person.Mail)
	}
	return mails
}

// The rule the whole project rests on, at the surface colleagues actually use.
func TestAColleagueSeesNoWishBeforePublication(t *testing.T) {
	t.Parallel()

	f := wishHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Zwei, []string{"LECTURER"}},
	)
	f.register(t, testdata.Eins)

	// Through both doors, because the realistic failure is a rule somebody adds for the browser
	// and forgets on the token path.
	graphqltest.EachDoor(t, f.handler, testdata.Zwei.Mail, testdata.Zwei.Token,
		func(t *testing.T, c *graphqltest.Client) {
			if got := seen(t, c, f.semester); len(got) != 0 {
				t.Errorf("a colleague sees %v before publication, want nothing", got)
			}
		})

	// And the owner reads their own, through both doors: it is their data.
	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var out struct {
				MyWishes []struct{ ID string }
			}
			c.MustQuery(t, `query($s: String!) { myWishes(semester: $s) { id } }`,
				map[string]any{"s": f.semester}, &out)
			if len(out.MyWishes) != 1 {
				t.Errorf("the owner reads %d of their own wishes, want 1", len(out.MyWishes))
			}
		})
}

// Asking for somebody else's wishes is an empty answer and not a refusal — because the
// difference between "refused" and "empty" is exactly the fact being protected, so a refusal
// would turn the field into an oracle for it.
func TestAskingForSomebodyElsesWishesIsEmptyRatherThanRefused(t *testing.T) {
	t.Parallel()

	f := wishHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Zwei, []string{"LECTURER"}},
	)
	f.register(t, testdata.Eins)

	var out struct {
		Wishes []struct{ ID string }
	}
	graphqltest.New(f.handler).AsUser(testdata.Zwei.Mail).MustQuery(t,
		`query($s: String!, $p: ID!) { wishes(semester: $s, person: $p) { id } }`,
		map[string]any{"s": f.semester, "p": testdata.Eins.ID().String()}, &out)

	if len(out.Wishes) != 0 {
		t.Errorf("asking for somebody else's wishes answered %d rows", len(out.Wishes))
	}
}

// A lead reads what they are responsible for and no further — the correction this whole step is
// about, asserted at the API rather than only in the policy package.
func TestALeadReadsOnlyTheirOwnSubjectThroughTheAPI(t *testing.T) {
	t.Parallel()

	f := wishHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}},
		grants{testdata.Drei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
	)
	f.register(t, testdata.Eins)

	// Vier leads the *other* programme; Drei leads the *other* subject group. Neither may look.
	f.leadProgramme(t, testdata.Vier, f.otherProgramme)
	f.leadGroup(t, testdata.Drei, f.otherGroup)

	for _, who := range []testdata.Persona{testdata.Vier, testdata.Drei} {
		got := seen(t, graphqltest.New(f.handler).AsUser(who.Mail), f.semester)
		if len(got) != 0 {
			t.Errorf("%s leads something else entirely and sees %v", who.Name, got)
		}
	}

	// Now give each of them the right subject. One reaches it by programme, the other by subject
	// group — the two axes, on the same row.
	f.leadProgramme(t, testdata.Vier, f.programme)
	f.leadGroup(t, testdata.Drei, f.group)

	for _, who := range []testdata.Persona{testdata.Vier, testdata.Drei} {
		got := seen(t, graphqltest.New(f.handler).AsUser(who.Mail), f.semester)
		if len(got) != 1 || got[0] != testdata.Eins.Mail {
			t.Errorf("%s is responsible for this instance and sees %v", who.Name, got)
		}
	}
}

// Through a token, even a lead sees only their own. A long-lived credential in a script makes
// silent bulk export possible and decouples "who saw this" from any login event.
func TestALeadReadsNothingExtraThroughAToken(t *testing.T) {
	t.Parallel()

	f := wishHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}},
	)
	f.register(t, testdata.Eins)
	f.leadProgramme(t, testdata.Vier, f.programme)

	browser := seen(t, graphqltest.New(f.handler).AsUser(testdata.Vier.Mail), f.semester)
	if len(browser) != 1 {
		t.Fatalf("the lead sees %v in the browser, want one wish", browser)
	}

	token := seen(t, graphqltest.New(f.handler).WithToken(testdata.Vier.Token), f.semester)
	if len(token) != 0 {
		t.Errorf("the same lead sees %v through their token — that is the silent bulk export "+
			"path the rule exists to close", token)
	}
}

// Publication is the promise the confidentiality rule is sold on: after the stichtag, everybody.
func TestPublicationOpensTheWishesToEverybody(t *testing.T) {
	t.Parallel()

	f := wishHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Zwei, []string{"LECTURER"}},
	)
	f.register(t, testdata.Eins)
	f.publish(t)

	graphqltest.EachDoor(t, f.handler, testdata.Zwei.Mail, testdata.Zwei.Token,
		func(t *testing.T, c *graphqltest.Client) {
			if got := seen(t, c, f.semester); len(got) != 1 {
				t.Errorf("after publication a colleague sees %v, want the one wish", got)
			}
		})
}

// The schema carries no count over wishes, anywhere, and this is the test that says so.
//
// Not "the count is filtered" — there is no count. A field answering 0 or 1 before publication is
// a field somebody renders as a badge, and "3 Kolleg:innen haben bereits Interesse" is the
// confidential fact with the names taken out.
func TestNoFieldCountsWishes(t *testing.T) {
	t.Parallel()

	f := wishHandler(t, grants{testdata.Eins, []string{"LECTURER"}})

	for _, query := range []string{
		`query { __type(name: "InstancePart") { fields { name } } }`,
		`query { __type(name: "CourseInstance") { fields { name } } }`,
		`query { __type(name: "Wish") { fields { name } } }`,
	} {
		var out struct {
			Type struct {
				Fields []struct{ Name string }
			} `json:"__type"`
		}
		graphqltest.New(f.handler).AsUser(testdata.Eins.Mail).MustQuery(t, query, nil, &out)

		for _, field := range out.Type.Fields {
			switch field.Name {
			case "wishCount", "hasWishes", "interestCount", "wishes":
				t.Errorf("the schema has a %q field. Before publication there are no counts, no "+
					"has-wishes flags and no traversal into wishes from the demand — a badge is "+
					"how this leaks without naming anybody.", field.Name)
			}
		}
	}
}

// A wish may be entered and changed for as long as the semester is not finished.
//
// The faculty asked for this rather than for the tidier rule the table carried first — open in
// the wish phase alone — and the argument is the demand's own: a correction the tool refuses
// happens in a mail instead, and then the list the tool holds is the wrong one. Somebody saying
// in March that they would take the second laboratory group after all is a correction.
//
// FINAL is the one closed cell in the whole write matrix, so this is also the only place either
// phase refusal is reachable at all.
func TestWishesAreWrittenUntilTheSemesterIsFinished(t *testing.T) {
	t.Parallel()

	f := wishHandler(t, grants{testdata.Eins, []string{"LECTURER"}})

	set := func(t *testing.T, phase string) {
		t.Helper()
		if _, err := f.schema.Pool.Exec(t.Context(),
			`UPDATE semester SET phase = $1 WHERE code = $2`, phase, f.semester); err != nil {
			t.Fatalf("cannot switch to %s: %v", phase, err)
		}
	}

	for _, phase := range []string{"DEMAND_PLANNING", "WISHES", "ASSIGNMENT"} {
		t.Run(phase, func(t *testing.T) {
			set(t, phase)

			var out struct {
				SetWish struct{ ID string }
			}
			graphqltest.New(f.handler).AsUser(testdata.Eins.Mail).MustQuery(t, setWishMutation,
				map[string]any{"p": f.instance, "prio": "HAPPY_TO", "note": nil}, &out)

			if out.SetWish.ID == "" {
				t.Errorf("a wish could not be registered in %s", phase)
			}
		})
	}

	t.Run("FINAL", func(t *testing.T) {
		set(t, "FINAL")

		messages := graphqltest.New(f.handler).AsUser(testdata.Eins.Mail).MustFail(t,
			setWishMutation,
			map[string]any{"p": f.instance, "prio": "FIRST_CHOICE", "note": nil})
		if len(messages) == 0 {
			t.Fatal("a wish was changed in a finished semester")
		}
		graphqltest.AssertNoLeak(t, messages[0], graphqltest.DatabaseNoise()...)

		// Withdrawing is bound by the same cell. A list that may be added to but not corrected is
		// worse than a closed one, so both directions are the same decision.
		withdrawals := graphqltest.New(f.handler).AsUser(testdata.Eins.Mail).MustFail(t,
			`mutation($id: ID!) { withdrawWish(id: $id) }`,
			map[string]any{"id": f.wishOf(t, testdata.Eins)})
		if len(withdrawals) == 0 {
			t.Error("a wish was withdrawn from a finished semester")
		}
	})
}

// Registering twice is a correction, and it is sayable in plain words — which is a consequence of
// only-self entry rather than of the table. If proxy entry is ever allowed, this message has to
// become generic in the same commit.
func TestRegisteringTwiceIsACorrectionThroughTheAPI(t *testing.T) {
	t.Parallel()

	f := wishHandler(t, grants{testdata.Eins, []string{"LECTURER"}})

	first := f.register(t, testdata.Eins)

	var out struct {
		SetWish struct {
			ID       string
			Priority string
			Note     string
		}
	}
	graphqltest.New(f.handler).AsUser(testdata.Eins.Mail).MustQuery(t, setWishMutation,
		map[string]any{"p": f.instance, "prio": "FIRST_CHOICE", "note": "doch lieber"}, &out)

	if out.SetWish.ID != first {
		t.Error("registering twice produced a second wish rather than changing the first")
	}
	if out.SetWish.Priority != "FIRST_CHOICE" || out.SetWish.Note != "doch lieber" {
		t.Errorf("got %+v, want the corrected values", out.SetWish)
	}
}

// Withdrawing somebody else's wish answers the same way as withdrawing one that is not there.
// Which of the two it is, is the confidential part — and the message must name nobody.
func TestWithdrawingSomebodyElsesWishSaysNothingAboutIt(t *testing.T) {
	t.Parallel()

	f := wishHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Zwei, []string{"LECTURER"}},
	)
	id := f.register(t, testdata.Eins)

	c := graphqltest.New(f.handler).AsUser(testdata.Zwei.Mail)

	theirs := c.MustFail(t, `mutation($id: ID!) { withdrawWish(id: $id) }`,
		map[string]any{"id": id})
	missing := c.MustFail(t, `mutation($id: ID!) { withdrawWish(id: $id) }`,
		map[string]any{"id": uuid.New().String()})

	if len(theirs) == 0 || len(missing) == 0 {
		t.Fatal("withdrawing a wish that is not the caller's succeeded")
	}
	if theirs[0] != missing[0] {
		t.Errorf("a wish that exists and one that does not answer differently:\n  %q\n  %q\n"+
			"The difference is whose it is, which is the fact this area protects.",
			theirs[0], missing[0])
	}

	graphqltest.AssertNoLeak(t, theirs[0],
		append(graphqltest.DatabaseNoise(), testdata.Mails(testdata.Others(testdata.Zwei))...)...)
}

// A wish carries what a row needs. "Analysis, IF1B" is what somebody recognises; an id is not,
// and a screen that had to ask again for every row would ask five hundred times.
func TestAWishCarriesItsInstance(t *testing.T) {
	t.Parallel()

	f := wishHandler(t, grants{testdata.Eins, []string{"LECTURER"}})

	var out struct {
		SetWish struct {
			Person   struct{ Mail string }
			Instance struct {
				Programme struct{ Code string }
				Module    struct{ Name string }
			}
		}
	}
	graphqltest.New(f.handler).AsUser(testdata.Eins.Mail).MustQuery(t, setWishMutation,
		map[string]any{"p": f.instance, "prio": "HAPPY_TO", "note": nil}, &out)

	if out.SetWish.Person.Mail != testdata.Eins.Mail {
		t.Errorf("the wish names %q", out.SetWish.Person.Mail)
	}
	if out.SetWish.Instance.Module.Name == "" || out.SetWish.Instance.Programme.Code == "" {
		t.Errorf("the wish does not carry enough to render a row: %+v", out.SetWish)
	}
}

// The oracle this migration would have opened, closed and pinned.
//
// `planDemand(dryRun: true)` is free, leaves no trace and reports INSTANCE_IN_USE per cohort. Once
// anything points at a course instance, that report is "which of this programme's instances are
// wished for" — the has-wishes flag the interface may never render, obtained in one call with no
// login event. Interactively it reveals nothing (see the test below); through a token it would
// have, because there the wish rule deliberately collapses to own-only.
//
// Removing a part is in the list although it can no longer fire INSTANCE_IN_USE — a wish points at
// the instance, so re-cutting it is allowed. It stays because the assertion is about the *door*:
// every mutation in the demand area is @interactiveOnly, dateiweise and not "the ones that can
// leak today", because a rule that holds for three of eleven is one somebody forgets on the
// twelfth.
func TestATokenCannotProbeWhichInstancesAreWishedFor(t *testing.T) {
	t.Parallel()

	f := wishHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}},
	)
	f.register(t, testdata.Eins)
	f.leadProgramme(t, testdata.Vier, f.programme)

	c := graphqltest.New(f.handler).WithToken(testdata.Vier.Token)

	for _, probe := range []struct {
		name      string
		query     string
		variables map[string]any
	}{
		{"a dry-run plan", `mutation($s: String!, $p: String!) {
			planDemand(semester: $s, programme: $p, entries: [], dryRun: true) {
				refused { code }
			}
		}`, map[string]any{"s": f.semester, "p": f.programme}},
		{"a withdrawal", `mutation($id: ID!) { withdrawCourseInstance(id: $id) }`,
			map[string]any{"id": f.instance}},
		{"removing a part", `mutation($id: ID!) { removeInstancePart(id: $id) { id } }`,
			map[string]any{"id": f.lecture}},
	} {
		t.Run(probe.name, func(t *testing.T) {
			messages := c.MustFail(t, probe.query, probe.variables)
			if len(messages) == 0 {
				t.Fatalf("%s answered through a token — it is an oracle for which instances are "+
					"wished for", probe.name)
			}
			// And the refusal is about the door rather than about the instance, so it says
			// nothing either way.
			graphqltest.AssertNoLeak(t, messages[0],
				append(graphqltest.DatabaseNoise(),
					testdata.Mails(testdata.Others(testdata.Vier))...)...)
		})
	}

	// The instance is still there: a probe that half-succeeded would be worse than one that
	// answered, because it would leave the demand changed.
	var parts int
	if err := f.schema.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM instance_part WHERE course_instance_id = $1::uuid`, f.instance).
		Scan(&parts); err != nil {
		t.Fatalf("cannot count the parts: %v", err)
	}
	if parts != 2 {
		t.Errorf("the instance has %d parts after the probes, want 2", parts)
	}
}

// The invariant that makes INSTANCE_IN_USE harmless in the browser, asserted rather than argued:
// anybody who can make it fire may already read the wishes on that instance.
//
// It holds because the two sets are the same by construction — MayWriteDemand intersects the
// phase matrix with PlanningScope, and UnpublishedWishScope returns that same PlanningScope. If
// somebody ever breaks them apart, this test goes red instead of the rule going quiet.
func TestInstanceInUseTellsNobodySomethingNew(t *testing.T) {
	t.Parallel()

	f := wishHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Zwei, []string{"LECTURER"}},
		grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}},
		grants{testdata.Fuenf, []string{"LECTURER", "DEANS_OFFICE"}},
	)
	f.register(t, testdata.Eins)
	f.leadProgramme(t, testdata.Vier, f.programme)

	// Counted, because a loop in which nobody can trigger the refusal would pass while proving
	// nothing — the same trap TestGuardAndFilterAgree guards against with its own counter.
	triggers := 0

	for _, who := range []testdata.Persona{
		testdata.Eins, testdata.Zwei, testdata.Vier, testdata.Fuenf,
	} {
		t.Run(who.Name, func(t *testing.T) {
			c := graphqltest.New(f.handler).AsUser(who.Mail)

			// Can this person make the refusal fire at all? A withdrawal that is refused for the
			// programme scope rather than for the instance being in use tells them nothing.
			messages := c.MustFail(t,
				`mutation($id: ID!) { withdrawCourseInstance(id: $id) }`,
				map[string]any{"id": f.instance})
			if len(messages) == 0 {
				t.Fatal("the instance was withdrawn although somebody wants it")
			}
			triggered := strings.Contains(messages[0], "hängt bereits etwas daran")
			if triggered {
				triggers++
			}

			// What can they read of the wishes on it?
			reads := len(seen(t, c, f.semester)) > 0

			if triggered && !reads {
				t.Errorf("%s can make INSTANCE_IN_USE fire and cannot read the wishes on the "+
					"instance. The refusal is then a has-wishes flag for somebody the rule is "+
					"supposed to withhold it from.", who.Name)
			}
		})
	}

	if triggers == 0 {
		t.Fatal("nobody in this cast could make INSTANCE_IN_USE fire, so the invariant was " +
			"never exercised — the refusal has to be reachable for the test to mean anything")
	}
}

// wishOf is the id of somebody's wish on the instance, read straight from the database.
//
// Not through the API: the tests that use it are about a phase in which the API refuses to write,
// and reading the id through a query that the same phase might one day close would make the
// fixture depend on the rule under test.
func (f wishFixture) wishOf(t *testing.T, who testdata.Persona) string {
	t.Helper()

	var id string
	if err := f.schema.Pool.QueryRow(t.Context(),
		`SELECT w.id::text FROM wish w WHERE w.course_instance_id = $1::uuid AND w.person_id = $2`,
		f.instance, who.ID()).Scan(&id); err != nil {
		t.Fatalf("cannot find %s's wish: %v", who.Name, err)
	}
	return id
}

// `myWishes` without a semester is every semester, and it is still own-only.
//
// The asymmetry with `wishes(semester:)` is a rule and not convenience: the confidentiality filter
// is built from *one* semester's publication date, so a query spanning all of them would have to
// pick one date and apply it to the rest. Own entries have no such state to get wrong — which is
// the half this test has to hold on to, through both doors, because "spans everything" and
// "shows everybody" are one careless line apart.
func TestMyWishesWithoutASemesterStaysOwnOnly(t *testing.T) {
	t.Parallel()

	f := wishHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Zwei, []string{"LECTURER"}},
		grants{testdata.Fuenf, []string{"LECTURER", "DEANS_OFFICE"}},
	)
	f.register(t, testdata.Eins)
	f.register(t, testdata.Zwei)

	const everywhere = `query { myWishes { person { mail } instance { semester } } }`

	// Even the dean's office, who may read every wish there is: `myWishes` answers about the
	// caller and not about the faculty, whatever else they are allowed to see.
	for _, who := range []testdata.Persona{testdata.Eins, testdata.Fuenf} {
		graphqltest.EachDoor(t, f.handler, who.Mail, who.Token,
			func(t *testing.T, c *graphqltest.Client) {
				var out struct {
					MyWishes []struct {
						Person   struct{ Mail string }
						Instance struct{ Semester string }
					}
				}
				c.MustQuery(t, everywhere, nil, &out)

				for _, wish := range out.MyWishes {
					if wish.Person.Mail != who.Mail {
						t.Errorf("%s reads %s's wish through myWishes", who.Name, wish.Person.Mail)
					}
					// Each row says which semester it is in, because the screen groups by it.
					if wish.Instance.Semester == "" {
						t.Error("a wish read across semesters does not name its semester")
					}
				}
			})
	}

	// Eins has one and sees it; Fuenf has none and sees none, which is what makes the loop above
	// mean something rather than passing on an empty list twice.
	var out struct {
		MyWishes []struct{ ID string }
	}
	graphqltest.New(f.handler).AsUser(testdata.Eins.Mail).MustQuery(t, everywhere, nil, &out)
	if len(out.MyWishes) != 1 {
		t.Errorf("Eins reads %d of their own wishes, want one", len(out.MyWishes))
	}
	graphqltest.New(f.handler).AsUser(testdata.Fuenf.Mail).MustQuery(t, everywhere, nil, &out)
	if len(out.MyWishes) != 0 {
		t.Errorf("Fuenf has registered nothing and reads %d wishes", len(out.MyWishes))
	}
}

// The other half of the same rule: everybody else's wishes cannot be asked for without a semester.
//
// Enforced by the schema — the argument is not nullable — so this is a validation error rather
// than a refusal, and that is the right place for it: a query the server never has to interpret
// cannot be interpreted wrongly.
func TestWishesStillNeedsASemester(t *testing.T) {
	t.Parallel()

	f := wishHandler(t, grants{testdata.Fuenf, []string{"LECTURER", "DEANS_OFFICE"}})

	messages := graphqltest.New(f.handler).AsUser(testdata.Fuenf.Mail).
		MustFail(t, `query { wishes { id } }`, nil)
	if len(messages) == 0 {
		t.Fatal("`wishes` answered without a semester — the confidentiality filter is built from " +
			"one semester's publication date, so there is no such question to answer")
	}
}
