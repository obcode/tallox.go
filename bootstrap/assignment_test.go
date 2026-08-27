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

// The assignment phase through the API, through both doors.
//
// The assertions here are about the leak channels and not only about the rows: the list, the
// absence of any count, the wording of the refusals, and what a Personal Access Token reaches
// that a browser session does not.

type assignmentAPIFixture struct {
	handler http.Handler
	schema  *storetest.Schema
	// lecture and lab are the two parts of the instance, which is what gets filled.
	instance string
	lecture  string
	lab      string
	semester string

	programme string
	group     uuid.UUID
	other     uuid.UUID
}

// assignmentHandler seeds people, a catalogue, an instance with parts, and a semester already in
// the assignment phase, and returns the handler Serve would build.
func assignmentHandler(t *testing.T, people ...grants) assignmentAPIFixture {
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
			Description: "assignment test",
		})
	}

	storetest.SeedZPACatalogue(t, s)
	if _, err := store.NewCatalogue(s.Pool).Project(ctx, nil); err != nil {
		t.Fatalf("cannot project the catalogue: %v", err)
	}

	modules := store.NewModules(s.Pool)
	semesters := store.NewSemesters(s.Pool)

	semester, err := semesters.EnsureSemester(ctx, "2028-WS")
	if err != nil {
		t.Fatalf("cannot record the semester: %v", err)
	}

	f := assignmentAPIFixture{
		schema:    s,
		semester:  semester.Code,
		programme: storetest.FixtureProgrammeA,
	}

	moduleID := moduleIDOf(t, s, storetest.FixtureModuleOrdinary)
	programmeID := programmeIDOf(t, s, f.programme)

	if _, err := modules.SetModuleComponents(ctx, moduleID, []domain.ModuleComponent{
		{Kind: domain.PartKindLecture, TeachingHours: 2, Position: 0},
		{Kind: domain.PartKindLab, TeachingHours: 2, Position: 1},
	}, uuid.Nil); err != nil {
		t.Fatalf("cannot state the module's split: %v", err)
	}

	f.group = seedGroup(t, s, "MATHE2", "Mathematik")
	f.other = seedGroup(t, s, "SWE2", "Softwarefächer")
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
	f.lab = instance.Parts[1].ID.String()

	// The phase this area is open in, so that the write tests fail for the reason they are about.
	if _, err := s.Pool.Exec(ctx,
		`UPDATE semester SET phase = 'ASSIGNMENT' WHERE id = $1`, semester.ID); err != nil {
		t.Fatalf("cannot switch the semester into the assignment phase: %v", err)
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
		Staffing:      domain.NewAssignmentService(store.NewAssignments(s.Pool), planning),
	})
	return f
}

func (f assignmentAPIFixture) leadGroup(t *testing.T, who testdata.Persona, group uuid.UUID) {
	t.Helper()

	if _, err := f.schema.Pool.Exec(t.Context(),
		`INSERT INTO person_subject_group_scope (person_id, role, subject_group_id)
		 VALUES ($1, 'SUBJECT_GROUP_LEAD', $2)`, who.ID(), group); err != nil {
		t.Fatalf("cannot make %s the lead of the group: %v", who.Name, err)
	}
}

func (f assignmentAPIFixture) leadProgramme(t *testing.T, who testdata.Persona, code string) {
	t.Helper()

	if _, err := f.schema.Pool.Exec(t.Context(),
		`INSERT INTO person_programme_scope (person_id, role, programme_id)
		 SELECT $1, 'PROGRAMME_LEAD', id FROM programme WHERE code = $2`,
		who.ID(), code); err != nil {
		t.Fatalf("cannot make %s the lead of %s: %v", who.Name, code, err)
	}
}

func (f assignmentAPIFixture) publish(t *testing.T) {
	t.Helper()

	if _, err := f.schema.Pool.Exec(t.Context(),
		`UPDATE semester SET assignments_published_at = now() WHERE code = $1`,
		f.semester); err != nil {
		t.Fatalf("cannot publish the assignments: %v", err)
	}
}

const setAssignmentMutation = `mutation($p: ID!, $who: ID, $note: String, $replacing: ID) {
	setAssignment(instancePartId: $p, personId: $who, note: $note, replacing: $replacing) {
		id note
		assignee { personId teacherId name mail }
		part { kind teachingHours }
		instance { track programme { code } module { name } }
	}
}`

const assignmentsQuery = `query($s: String!) {
	assignments(semester: $s) { id assignee { name mail personId } }
}`

// fill puts somebody on a part as somebody, through the browser door.
func (f assignmentAPIFixture) fill(t *testing.T, as testdata.Persona, part string,
	who testdata.Persona) string {
	t.Helper()

	var out struct {
		SetAssignment struct{ ID string }
	}
	graphqltest.New(f.handler).AsUser(as.Mail).MustQuery(t, setAssignmentMutation,
		map[string]any{"p": part, "who": who.ID().String(), "note": nil, "replacing": nil}, &out)
	return out.SetAssignment.ID
}

// held reads the semester's assignments as somebody, through one door, and returns the addresses.
func held(t *testing.T, c *graphqltest.Client, semester string) []string {
	t.Helper()

	var out struct {
		Assignments []struct {
			Assignee struct{ Mail string }
		}
	}
	c.MustQuery(t, assignmentsQuery, map[string]any{"s": semester}, &out)

	mails := make([]string, 0, len(out.Assignments))
	for _, a := range out.Assignments {
		mails = append(mails, a.Assignee.Mail)
	}
	return mails
}

// hasCode reports whether a refused response carries this extensions.code.
//
// The code rather than the German sentence: the sentence is the half that gets reworded after a
// support question, and a test that matched on it would go green the day somebody improves it.
func hasCode(resp graphqltest.Response, want string) bool {
	for _, e := range resp.Errors {
		if code, _ := e.Extensions["code"].(string); code == want {
			return true
		}
	}
	return false
}

// TestAColleagueSeesNoAssignmentBeforePublicationThroughTheAPI is the rule at the surface people
// actually use, through both doors.
func TestAColleagueSeesNoAssignmentBeforePublicationThroughTheAPI(t *testing.T) {
	t.Parallel()

	f := assignmentHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Zwei, []string{"LECTURER"}},
		grants{testdata.Drei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
	)
	f.leadGroup(t, testdata.Drei, f.group)
	f.fill(t, testdata.Drei, f.lecture, testdata.Eins)

	graphqltest.EachDoor(t, f.handler, testdata.Zwei.Mail, testdata.Zwei.Token,
		func(t *testing.T, c *graphqltest.Client) {
			if got := held(t, c, f.semester); len(got) != 0 {
				t.Errorf("an uninvolved colleague sees %v, want nothing before publication", got)
			}
		})

	// The holder reads their own through both doors — that is their own timetable.
	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			if got := held(t, c, f.semester); len(got) != 1 {
				t.Errorf("the holder sees %v of their own assignments, want one", got)
			}
		})
}

// TestALeadReadsNothingExtraThroughATokenAboutAssignments is the decision a token cannot buy back.
func TestALeadReadsNothingExtraThroughATokenAboutAssignments(t *testing.T) {
	t.Parallel()

	f := assignmentHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Drei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
	)
	f.leadGroup(t, testdata.Drei, f.group)
	f.fill(t, testdata.Drei, f.lecture, testdata.Eins)

	browser := graphqltest.New(f.handler).AsUser(testdata.Drei.Mail)
	if got := held(t, browser, f.semester); len(got) != 1 {
		t.Errorf("the lead sees %v in the browser, want the one assignment in their subject", got)
	}

	token := graphqltest.New(f.handler).WithToken(testdata.Drei.Token)
	if got := held(t, token, f.semester); len(got) != 0 {
		t.Errorf("the lead sees %v through a token, want nothing: a token never reads somebody "+
			"else's unpublished assignment", got)
	}
}

// TestPublicationOpensTheAssignmentsToEverybody is the other side of the same rule.
func TestPublicationOpensTheAssignmentsToEverybody(t *testing.T) {
	t.Parallel()

	f := assignmentHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Zwei, []string{"LECTURER"}},
		grants{testdata.Drei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
	)
	f.leadGroup(t, testdata.Drei, f.group)
	f.fill(t, testdata.Drei, f.lecture, testdata.Eins)
	f.publish(t)

	graphqltest.EachDoor(t, f.handler, testdata.Zwei.Mail, testdata.Zwei.Token,
		func(t *testing.T, c *graphqltest.Client) {
			if got := held(t, c, f.semester); len(got) != 1 {
				t.Errorf("after publication a colleague sees %v, want the one assignment", got)
			}
		})
}

// TestNoFieldCountsAssignments is the aggregate leak, closed by introspection rather than by
// review.
//
// The interface *will* want a badge — "2 von 3 Praktika besetzt" is the obvious thing to put on a
// planning screen — and before publication that is the confidential fact with the names taken out.
// So there is no field to render it from, and no traversal from a part or an instance into what
// is assigned to it.
func TestNoFieldCountsAssignments(t *testing.T) {
	t.Parallel()

	f := assignmentHandler(t, grants{testdata.Eins, []string{"LECTURER"}})

	var out struct {
		Schema struct {
			Types []struct {
				Name   string
				Fields []struct{ Name string }
			}
		} `json:"__schema"`
	}
	graphqltest.New(f.handler).AsUser(testdata.Eins.Mail).MustQuery(t,
		`query { __schema { types { name fields { name } } } }`, nil, &out)

	watched := map[string]bool{"InstancePart": true, "CourseInstance": true, "Assignment": true}
	checked := 0
	for _, typ := range out.Schema.Types {
		if !watched[typ.Name] {
			continue
		}
		checked++
		for _, field := range typ.Fields {
			switch field.Name {
			case "assignmentCount", "isAssigned", "assigned", "staffed", "assignments":
				t.Errorf("%s has a %q field. Before publication there are no counts, no "+
					"is-assigned flags and no traversal from the demand into what is assigned "+
					"— a badge is how this leaks without naming anybody.", typ.Name, field.Name)
			}
		}
	}
	if checked != len(watched) {
		t.Errorf("checked %d of %d types — introspection did not return what this test reads",
			checked, len(watched))
	}
}

// TestEveryAssignmentMutationRefusesAToken is the oracle, closed before it exists.
//
// PART_ALREADY_ASSIGNED tells a caller that a part is taken. Through a token the read rule
// collapses to "your own", so a script naming one part after another would learn the staffing of a
// whole semester from the refusals, without a login event and without reading a row. Same shape as
// planDemand(dryRun:) had, and closed the same way: the whole file, not the one field that can
// produce the refusal today.
//
// Reads the origin file from the parsed field position rather than matching on names, so that a
// mutation added to assignment.graphqls tomorrow is covered without anybody remembering this test.
func TestEveryAssignmentMutationRefusesAToken(t *testing.T) {
	t.Parallel()

	f := assignmentHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Drei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
	)
	f.leadGroup(t, testdata.Drei, f.group)

	// The lead, who may do this in the browser, is refused through the token door.
	resp := graphqltest.New(f.handler).WithToken(testdata.Drei.Token).Do(t,
		setAssignmentMutation,
		map[string]any{
			"p": f.lecture, "who": testdata.Eins.ID().String(), "note": nil, "replacing": nil,
		})
	assertRefusal(t, resp, "INTERACTIVE_ONLY")

	// And nothing was written, which matters more than the refusal: a half-successful probe
	// would be worse than an answered one.
	browser := graphqltest.New(f.handler).AsUser(testdata.Drei.Mail)
	if got := held(t, browser, f.semester); len(got) != 0 {
		t.Errorf("the refused token write left %v behind", got)
	}
}

// TestPartAssignedTellsNobodySomethingNew is the invariant behind naming what hangs off a part.
//
// Removing a staffed part is refused with PART_ASSIGNED, which says a part is filled. That is only
// safe while everybody who can make it fire may also read the assignment that made it fire. Both
// halves are properties of two separate rules — the demand write rule and the assignment read rule
// — and nothing but this test holds them together.
func TestPartAssignedTellsNobodySomethingNew(t *testing.T) {
	t.Parallel()

	triggers := 0
	for _, who := range []struct {
		persona testdata.Persona
		roles   []string
	}{
		{testdata.Eins, []string{"LECTURER"}},
		{testdata.Zwei, []string{"LECTURER"}},
		{testdata.Drei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
		{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}},
		{testdata.Fuenf, []string{"LECTURER", "DEANS_OFFICE"}},
		{testdata.Sechs, []string{"LECTURER", "ADMIN"}},
	} {
		t.Run(who.persona.Name, func(t *testing.T) {
			// Eins holds the part and Drei fills it, so both are in every run. Adding the
			// persona under test again would seed them twice; where it *is* one of them, the
			// roles below are already the ones this case is about.
			seeds := []grants{
				{testdata.Eins, []string{"LECTURER"}},
				{testdata.Drei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
			}
			if who.persona.Mail != testdata.Eins.Mail && who.persona.Mail != testdata.Drei.Mail {
				seeds = append(seeds, grants{who.persona, who.roles})
			}
			f := assignmentHandler(t, seeds...)
			f.leadGroup(t, testdata.Drei, f.group)
			if who.persona.Mail == testdata.Vier.Mail {
				f.leadProgramme(t, testdata.Vier, f.programme)
			}
			f.fill(t, testdata.Drei, f.lab, testdata.Eins)

			c := graphqltest.New(f.handler).AsUser(who.persona.Mail)

			// Can this person make the refusal fire?
			removal := c.Do(t, `mutation($p: ID!) { removeInstancePart(id: $p) { id } }`,
				map[string]any{"p": f.lab})
			fires := hasCode(removal, "PART_ASSIGNED")

			// Can they read the assignment that made it fire?
			reads := len(held(t, c, f.semester)) > 0

			if fires {
				triggers++
				if !reads {
					t.Errorf("%s can make PART_ASSIGNED fire and cannot read the assignment on "+
						"the part. The refusal is then an is-assigned flag for somebody the "+
						"rule is supposed to withhold it from.", who.persona.Name)
				}
			}
		})
	}
	if triggers == 0 {
		t.Fatal("nobody in this cast could make PART_ASSIGNED fire — this test checked nothing")
	}
}

// TestAssignmentWritesDoNotLeak keeps driver noise and other people's addresses out of the
// refusals.
func TestAssignmentWritesDoNotLeak(t *testing.T) {
	t.Parallel()

	f := assignmentHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Zwei, []string{"LECTURER"}},
		grants{testdata.Drei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
	)
	f.leadGroup(t, testdata.Drei, f.group)
	f.fill(t, testdata.Drei, f.lecture, testdata.Eins)

	// Somebody who may not fill this part at all.
	stranger := graphqltest.New(f.handler).AsUser(testdata.Zwei.Mail).MustFail(t,
		setAssignmentMutation,
		map[string]any{
			"p": f.lecture, "who": testdata.Zwei.ID().String(), "note": nil, "replacing": nil,
		})
	graphqltest.AssertNoLeak(t, strings.Join(stranger, " "), append(graphqltest.DatabaseNoise(),
		testdata.Mails(testdata.Others(testdata.Zwei))...)...)

	// And the lead colliding with an existing assignment: the refusal names the fact, never the
	// person holding it.
	collision := graphqltest.New(f.handler).AsUser(testdata.Drei.Mail).Do(t,
		setAssignmentMutation,
		map[string]any{
			"p": f.lecture, "who": testdata.Zwei.ID().String(), "note": nil, "replacing": nil,
		})
	assertRefusal(t, collision, "PART_ALREADY_ASSIGNED")
	graphqltest.AssertNoLeak(t, strings.Join(collision.Messages(), " "),
		append(graphqltest.DatabaseNoise(), testdata.Eins.Mail, testdata.Eins.Name)...)
}

// TestFillingIsRefusedWhileTheWishPhaseRuns is the cell that closes early, at the surface.
func TestFillingIsRefusedWhileTheWishPhaseRuns(t *testing.T) {
	t.Parallel()

	f := assignmentHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Drei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
	)
	f.leadGroup(t, testdata.Drei, f.group)

	if _, err := f.schema.Pool.Exec(t.Context(),
		`UPDATE semester SET phase = 'WISHES' WHERE code = $1`, f.semester); err != nil {
		t.Fatalf("cannot go back to the wish phase: %v", err)
	}

	resp := graphqltest.New(f.handler).AsUser(testdata.Drei.Mail).Do(t,
		setAssignmentMutation,
		map[string]any{
			"p": f.lecture, "who": testdata.Eins.ID().String(), "note": nil, "replacing": nil,
		})
	assertRefusal(t, resp, "ASSIGNMENT_PHASE_CLOSED")
}

// TestBothLeadsMayFillTheSameInstance is the decision of 2026-08-27, at the surface.
//
// The union of the two axes: the lead of the module's subject group reaches it, and so does the
// lead of the instance's study programme. The second one is what makes a module in no subject
// group fillable by somebody other than the dean's office.
func TestBothLeadsMayFillTheSameInstance(t *testing.T) {
	t.Parallel()

	f := assignmentHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Zwei, []string{"LECTURER"}},
		grants{testdata.Drei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
		grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}},
	)
	f.leadGroup(t, testdata.Drei, f.group)
	f.leadProgramme(t, testdata.Vier, f.programme)

	f.fill(t, testdata.Drei, f.lecture, testdata.Eins)
	f.fill(t, testdata.Vier, f.lab, testdata.Zwei)

	deans := graphqltest.New(f.handler).AsUser(testdata.Drei.Mail)
	if got := held(t, deans, f.semester); len(got) != 2 {
		t.Errorf("the subject group lead sees %v, want both parts filled", got)
	}
}

// TestReplacingWhatSomebodyElseChangedIsRefusedThroughTheAPI is the compare-and-set at the
// surface, and the reason the union above needs one.
func TestReplacingWhatSomebodyElseChangedIsRefusedThroughTheAPI(t *testing.T) {
	t.Parallel()

	f := assignmentHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Zwei, []string{"LECTURER"}},
		grants{testdata.Drei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
		grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}},
	)
	f.leadGroup(t, testdata.Drei, f.group)
	f.leadProgramme(t, testdata.Vier, f.programme)

	// What the first lead is looking at.
	stale := f.fill(t, testdata.Drei, f.lecture, testdata.Eins)

	// The second lead clears it and fills it afresh, so what the first one saw is gone.
	graphqltest.New(f.handler).AsUser(testdata.Vier.Mail).MustQuery(t,
		`mutation($id: ID!) { clearAssignment(id: $id) }`, map[string]any{"id": stale}, &struct {
			ClearAssignment string
		}{})
	f.fill(t, testdata.Vier, f.lecture, testdata.Zwei)

	// The first lead now writes against a state that has moved on.
	resp := graphqltest.New(f.handler).AsUser(testdata.Drei.Mail).Do(t,
		setAssignmentMutation,
		map[string]any{
			"p": f.lecture, "who": testdata.Eins.ID().String(), "note": nil, "replacing": stale,
		})
	assertRefusal(t, resp, "ASSIGNMENT_MOVED_ON")

	// And the decision that was actually taken still stands.
	browser := graphqltest.New(f.handler).AsUser(testdata.Vier.Mail)
	got := held(t, browser, f.semester)
	if len(got) != 1 || got[0] != testdata.Zwei.Mail {
		t.Errorf("after the refused write the part is held by %v, want %s", got, testdata.Zwei.Mail)
	}
}
