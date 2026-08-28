package bootstrap_test

import (
	"net/http"
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

// The demand through the API.
//
// **Reading is asserted through both doors; writing is asserted in the browser only, because the
// writes no longer answer through a token at all.** That is a change and not an omission, and the
// reason is a sentence about wishes rather than about the demand: a withdrawal refused with
// INSTANCE_IN_USE is an answer about who wants an instance, and `planDemand(dryRun: true)` would
// have handed a whole programme's worth of those back for free, with no login event. The argument
// in full is at the top of graph/demand.graphqls; the counterpart of these tests is
// TestEveryDemandMutationRefusesAToken, which asserts the mechanical rule rather than the cases.
//
// EachDoor is still not ceremony on the reads. The catalogue and the demand are neither
// confidential nor personnel data, and a colleague evaluating their own programme's demand from a
// script is a use this API exists for. The realistic failure there is not a wrong answer but a
// rule somebody adds for the browser and forgets on the token path.

// demandFixture is a handler with a projected catalogue and a split module behind it.
type demandFixture struct {
	handler http.Handler
	// schema is the private schema behind it, for the facts the API has no mutation for.
	schema *storetest.Schema
	// module is the ordinary module, split into a lecture and a laboratory.
	module uuid.UUID
	// withoutHours is the module the examination office states no hours for — the one an
	// instance still cannot be declared for.
	withoutHours uuid.UUID
	// foreign is at home in another programme entirely. Declaring it is allowed, and the test
	// below is the only place that says so.
	foreign uuid.UUID
}

// demandHandler seeds people and their programme assignments, projects the catalogue, states a
// split for one module and returns the handler Serve would build.
func demandHandler(t *testing.T, scoped map[string][]string, people ...grants) demandFixture {
	t.Helper()

	s := storetest.New(t)
	for _, p := range people {
		storetest.SeedPerson(t, s, p.who, p.roles...)

		parsed, err := auth.ParseToken(p.who.Token)
		if err != nil {
			t.Fatalf("fixture token of %s does not parse: %v", p.who.Name, err)
		}
		storetest.SeedToken(t, s, p.who, auth.HashSecret(parsed.Secret), storetest.TokenOptions{
			Description: "demand test",
		})
	}

	storetest.SeedZPACatalogue(t, s)
	if _, err := store.NewCatalogue(s.Pool).Project(t.Context(), nil); err != nil {
		t.Fatalf("cannot project the catalogue: %v", err)
	}

	for mail, codes := range scoped {
		for _, code := range codes {
			if _, err := s.Pool.Exec(t.Context(),
				`INSERT INTO person_programme_scope (person_id, role, programme_id)
				 SELECT p.id, 'PROGRAMME_LEAD', pr.id
				   FROM person p, programme pr
				  WHERE p.mail = $1 AND pr.code = $2`, mail, code); err != nil {
				t.Fatalf("cannot assign %s to %s: %v", code, mail, err)
			}
		}
	}

	modules := store.NewModules(s.Pool)
	planning := domain.NewSemesterService(store.NewSemesters(s.Pool), nil)

	read := func(query string, args ...any) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := s.Pool.QueryRow(t.Context(), query, args...).Scan(&id); err != nil {
			t.Fatalf("cannot read a fixture id: %v", err)
		}
		return id
	}
	fixture := demandFixture{
		module: read(`SELECT id FROM module WHERE zpa_module_ref = $1`,
			storetest.FixtureModuleOrdinary),
		withoutHours: read(`SELECT id FROM module WHERE zpa_module_ref = $1`,
			storetest.FixtureModuleWithoutHours),
		foreign: read(`SELECT id FROM module WHERE zpa_module_ref = $1`,
			storetest.FixtureModuleOfProgrammeZ),
	}

	if _, err := modules.SetModuleComponents(t.Context(), fixture.module, []domain.ModuleComponent{
		{Kind: domain.PartKindLecture, TeachingHours: 2, Position: 0},
		{Kind: domain.PartKindLab, TeachingHours: 2, Position: 1},
	}, uuid.Nil); err != nil {
		t.Fatalf("cannot state the module's split: %v", err)
	}

	fixture.schema = s
	fixture.handler = bootstrap.Handler(bootstrap.Options{
		Build: buildinfo.Info{Version: "test"},
		Auth: auth.Config{
			Mode:   auth.ModeProxy,
			Users:  store.NewDirectory(s.Pool),
			Tokens: store.NewDirectory(s.Pool),
		},
		People:    domain.NewPeopleService(store.NewPeople(s.Pool), nil),
		Planning:  planning,
		Catalogue: domain.NewCatalogueService(modules),
		Demand:    domain.NewDemandService(store.NewDemand(s.Pool, modules), modules, planning),
	})
	return fixture
}

const declareMutation = `mutation($in: DeclareCourseInstanceInput!) {
	declareCourseInstance(input: $in) {
		id track programmeSemester teachingHours
		semester
		programme { code }
		module { name }
		parts { id kind teachingHours sharedAcrossTracks }
	}
}`

func declareInput(f demandFixture, programme, semester, track string) map[string]any {
	return map[string]any{"in": map[string]any{
		"semester":  semester,
		"programme": programme,
		"moduleId":  f.module.String(),
		"track":     track,
	}}
}

// errorCode returns the extensions.code of the first error, which is the half of the contract
// the interface branches on.
func errorCode(t *testing.T, resp graphqltest.Response) string {
	t.Helper()

	if !resp.Failed() {
		t.Fatalf("the call succeeded; expected a refusal.\n%s", resp.Body)
	}
	code, _ := resp.Errors[0].Extensions["code"].(string)
	return code
}

// The rule the whole area rests on, through both doors: a programme lead declares the demand of
// their own programme and of no other.
func TestDeclaringDemandIsScopedToTheProgrammeThroughBothDoors(t *testing.T) {
	t.Parallel()

	f := demandHandler(t,
		map[string][]string{testdata.Vier.Mail: {storetest.FixtureProgrammeA}},
		grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}})

	inTheBrowser(t, f.handler, testdata.Vier.Mail,
		func(t *testing.T, c *graphqltest.Client) {
			var out struct {
				DeclareCourseInstance struct {
					ID            string
					Track         string
					TeachingHours float64
					Programme     struct{ Code string }
					Parts         []struct {
						Kind               string
						TeachingHours      *float64
						SharedAcrossTracks bool
					}
				}
			}
			// A different semester per door: the two runs share a database, and declaring the
			// same cohort twice is refused by the identity — which is the point of the identity.
			semester := "2027-SS"
			if c.Door() == graphqltest.Token {
				semester = "2027-WS"
			}
			c.MustQuery(t, declareMutation,
				declareInput(f, storetest.FixtureProgrammeA, semester, "A"), &out)

			got := out.DeclareCourseInstance
			if got.Programme.Code != storetest.FixtureProgrammeA {
				t.Errorf("the instance belongs to %s, want %s",
					got.Programme.Code, storetest.FixtureProgrammeA)
			}
			if len(got.Parts) != 2 {
				t.Fatalf("the instance holds %d parts, want the two the module's split states",
					len(got.Parts))
			}
			if got.TeachingHours != 4 {
				t.Errorf("the instance costs %v hours, want 4 — the sum over its parts",
					got.TeachingHours)
			}
			for _, p := range got.Parts {
				if p.SharedAcrossTracks {
					t.Errorf("part %s is shared across cohorts on declaration; every cohort "+
						"holds its own unless somebody says otherwise", p.Kind)
				}
			}

			// The other programme, same person, same door.
			resp := c.Do(t, declareMutation,
				declareInput(f, storetest.FixtureProgrammeB, semester, "A"))
			if code := errorCode(t, resp); code != "NOT_YOUR_PROGRAMME" {
				t.Errorf("declaring for another programme answered %s, want NOT_YOUR_PROGRAMME",
					code)
			}
		})
}

// The refusal that names its own repair. Somebody who reads "you may not do this" goes and asks
// for a role they already hold.
func TestALeadWithNoProgrammeIsToldWhatIsMissing(t *testing.T) {
	t.Parallel()

	f := demandHandler(t, nil, grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}})

	inTheBrowser(t, f.handler, testdata.Vier.Mail,
		func(t *testing.T, c *graphqltest.Client) {
			resp := c.Do(t, declareMutation,
				declareInput(f, storetest.FixtureProgrammeA, "2027-SS", ""))
			if code := errorCode(t, resp); code != "PROGRAMME_SCOPE_MISSING" {
				t.Errorf("an unassigned lead was told %s, want PROGRAMME_SCOPE_MISSING", code)
			}
		})
}

// Everybody else, through both doors. ADMIN is on this list on purpose: running the system is a
// different job from planning with it, and it is the same decision the wish rule makes.
func TestWhoMayNotDeclareDemand(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		persona testdata.Persona
		roles   []string
	}{
		{"a lecturer", testdata.Eins, []string{"LECTURER"}},
		{"a subject group lead", testdata.Zwei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
		{"an administrator", testdata.Drei, []string{"ADMIN"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := demandHandler(t, nil, grants{tc.persona, tc.roles})

			inTheBrowser(t, f.handler, tc.persona.Mail,
				func(t *testing.T, c *graphqltest.Client) {
					resp := c.Do(t, declareMutation,
						declareInput(f, storetest.FixtureProgrammeA, "2027-SS", ""))
					if code := errorCode(t, resp); code != "NOT_YOUR_PROGRAMME" {
						t.Errorf("%s was told %s, want NOT_YOUR_PROGRAMME", tc.name, code)
					}
				})
		})
	}
}

// The dean's office plans across programmes, including ones created after any list was built.
func TestTheDeansOfficeDeclaresForEveryProgramme(t *testing.T) {
	t.Parallel()

	f := demandHandler(t, nil, grants{testdata.Vier, []string{"DEANS_OFFICE"}})

	inTheBrowser(t, f.handler, testdata.Vier.Mail,
		func(t *testing.T, c *graphqltest.Client) {
			semester := "2027-SS"
			if c.Door() == graphqltest.Token {
				semester = "2027-WS"
			}
			var out struct {
				DeclareCourseInstance struct{ ID string }
			}
			c.MustQuery(t, declareMutation,
				declareInput(f, storetest.FixtureProgrammeA, semester, ""), &out)
			if out.DeclareCourseInstance.ID == "" {
				t.Error("the dean's office declared nothing")
			}
		})
}

// What is left of the precondition: a module the examination office states no hours for. A
// module whose split nobody has stated is declared from the proposal — the estimate is good
// enough to plan with — but zero hours cannot be divided into anything.
func TestAModuleWithoutHoursIsRefusedByName(t *testing.T) {
	t.Parallel()

	f := demandHandler(t,
		map[string][]string{testdata.Vier.Mail: {storetest.FixtureProgrammeA}},
		grants{testdata.Vier, []string{"PROGRAMME_LEAD"}})

	inTheBrowser(t, f.handler, testdata.Vier.Mail,
		func(t *testing.T, c *graphqltest.Client) {
			resp := c.Do(t, declareMutation, map[string]any{"in": map[string]any{
				"semester":  "2027-SS",
				"programme": storetest.FixtureProgrammeA,
				"moduleId":  f.withoutHours.String(),
			}})
			if code := errorCode(t, resp); code != "MODULE_NOT_DECOMPOSED" {
				t.Errorf("a module with no hours was refused with %s, want MODULE_NOT_DECOMPOSED",
					code)
			}
		})
}

// Leak channel 2, on the first write path in the system that has one.
//
// The demand is not confidential, so what must not appear here is not somebody's name — it is
// the database's own vocabulary. A verbatim uniqueness violation would tell the caller the
// constraint, the table and the columns of the identity; the same handler will carry the wish
// table's uniqueness violation later, and that one is the confidential fact itself.
func TestAWriteRefusalSaysNothingAboutTheDatabase(t *testing.T) {
	t.Parallel()

	f := demandHandler(t,
		map[string][]string{testdata.Vier.Mail: {storetest.FixtureProgrammeA}},
		grants{testdata.Vier, []string{"PROGRAMME_LEAD"}})

	inTheBrowser(t, f.handler, testdata.Vier.Mail,
		func(t *testing.T, c *graphqltest.Client) {
			semester := "2028-SS"
			if c.Door() == graphqltest.Token {
				semester = "2028-WS"
			}
			var out struct {
				DeclareCourseInstance struct{ ID string }
			}
			c.MustQuery(t, declareMutation,
				declareInput(f, storetest.FixtureProgrammeA, semester, "A"), &out)

			resp := c.Do(t, declareMutation,
				declareInput(f, storetest.FixtureProgrammeA, semester, "A"))
			if code := errorCode(t, resp); code != "TRACK_TAKEN" {
				t.Errorf("declaring the same cohort twice answered %s, want TRACK_TAKEN", code)
			}
			for _, message := range resp.Messages() {
				graphqltest.AssertNoLeak(t, message,
					append(graphqltest.DatabaseNoise(),
						"course_instance", "instance_part", "constraint")...)
			}
		})
}

// Reading the demand needs an account and no role: it is what the wish phase is about.
func TestTheDemandIsReadableByAnybodyWithAnAccount(t *testing.T) {
	t.Parallel()

	f := demandHandler(t,
		map[string][]string{testdata.Vier.Mail: {storetest.FixtureProgrammeA}},
		grants{testdata.Vier, []string{"PROGRAMME_LEAD"}},
		grants{testdata.Eins, []string{"LECTURER"}})

	lead := graphqltest.New(f.handler).AsUser(testdata.Vier.Mail).On(graphqltest.Browser)
	var declared struct {
		DeclareCourseInstance struct{ ID string }
	}
	lead.MustQuery(t, declareMutation,
		declareInput(f, storetest.FixtureProgrammeA, "2027-SS", "A"), &declared)

	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var out struct {
				CourseInstances []struct {
					Track     string
					Module    struct{ Name string }
					Programme struct{ Code string }
					Parts     []struct{ Kind string }
				}
			}
			c.MustQuery(t, `query($s: String!, $p: String) {
				courseInstances(semester: $s, programme: $p) {
					track module { name } programme { code } parts { kind }
				}
			}`, map[string]any{"s": "2027-SS", "p": storetest.FixtureProgrammeA}, &out)

			if len(out.CourseInstances) != 1 {
				t.Fatalf("a lecturer sees %d instances, want the one that was declared",
					len(out.CourseInstances))
			}
			if len(out.CourseInstances[0].Parts) != 2 {
				t.Errorf("the instance arrived with %d parts, want two — a wish will point at "+
					"one of these", len(out.CourseInstances[0].Parts))
			}
		})

	// And a lecturer may not write it — refused at each door for its own reason, which is worth
	// asserting per door rather than as "some error": in the browser the refusal is about the
	// programme, and through a token the door itself answers first.
	withdraw := `mutation($id: ID!) { withdrawCourseInstance(id: $id) }`
	variables := map[string]any{"id": declared.DeclareCourseInstance.ID}

	inTheBrowser(t, f.handler, testdata.Eins.Mail,
		func(t *testing.T, c *graphqltest.Client) {
			if code := errorCode(t, c.Do(t, withdraw, variables)); code != "NOT_YOUR_PROGRAMME" {
				t.Errorf("a lecturer withdrew an instance, or was told %s", code)
			}
		})

	resp := graphqltest.New(f.handler).WithToken(testdata.Eins.Token).Do(t, withdraw, variables)
	if code := errorCode(t, resp); code != "INTERACTIVE_ONLY" {
		t.Errorf("through a token the withdrawal was answered with %s, want INTERACTIVE_ONLY — "+
			"a refusal that reached the instance would be an answer about who wants it", code)
	}
}

// The whole cohort workflow through the API, on one door each, because what is under test here
// is the sequence rather than the permission: declare, split into two cohorts, add a laboratory
// group, share the lecture, and read what the sibling sees.
func TestTheCohortWorkflow(t *testing.T) {
	t.Parallel()

	f := demandHandler(t,
		map[string][]string{testdata.Vier.Mail: {storetest.FixtureProgrammeA}},
		grants{testdata.Vier, []string{"PROGRAMME_LEAD"}})

	c := graphqltest.New(f.handler).AsUser(testdata.Vier.Mail).On(graphqltest.Browser)

	var declared struct {
		DeclareCourseInstance struct {
			ID    string
			Parts []struct {
				ID   string
				Kind string
			}
		}
	}
	c.MustQuery(t, declareMutation,
		declareInput(f, storetest.FixtureProgrammeA, "2027-SS", ""), &declared)
	first := declared.DeclareCourseInstance

	var lecture string
	for _, p := range first.Parts {
		if p.Kind == "LECTURE" {
			lecture = p.ID
		}
	}
	if lecture == "" {
		t.Fatal("the declared instance holds no lecture")
	}

	var duplicated struct {
		DuplicateCourseInstance struct {
			ID    string
			Track string
			Parts []struct{ Kind string }
		}
	}
	c.MustQuery(t, `mutation($id: ID!, $t: String!, $s: String) {
		duplicateCourseInstance(id: $id, track: $t, sourceTrack: $s) {
			id track parts { kind }
		}
	}`, map[string]any{"id": first.ID, "t": "B", "s": "A"}, &duplicated)

	if duplicated.DuplicateCourseInstance.Track != "B" {
		t.Errorf("the second cohort is %q, want B", duplicated.DuplicateCourseInstance.Track)
	}
	if len(duplicated.DuplicateCourseInstance.Parts) != 2 {
		t.Errorf("the second cohort holds %d parts, want its own copy of both",
			len(duplicated.DuplicateCourseInstance.Parts))
	}

	var withGroup struct {
		AddInstancePart struct {
			TeachingHours float64
			Parts         []struct{ Kind string }
		}
	}
	c.MustQuery(t, `mutation($id: ID!) {
		addInstancePart(instanceId: $id, kind: LAB, teachingHours: 2) {
			teachingHours parts { kind }
		}
	}`, map[string]any{"id": first.ID}, &withGroup)
	if withGroup.AddInstancePart.TeachingHours != 6 {
		t.Errorf("with a second laboratory group the cohort costs %v hours, want 6",
			withGroup.AddInstancePart.TeachingHours)
	}

	var shared struct {
		ShareInstancePartAcrossTracks struct {
			TeachingHours float64
			Parts         []struct {
				Kind               string
				SharedAcrossTracks bool
			}
		}
	}
	c.MustQuery(t, `mutation($id: ID!) {
		shareInstancePartAcrossTracks(id: $id) {
			teachingHours parts { kind sharedAcrossTracks }
		}
	}`, map[string]any{"id": lecture}, &shared)

	var sibling struct {
		CourseInstance struct {
			TeachingHours float64
			Parts         []struct{ Kind string }
			BorrowedParts []struct {
				FromTrack string
				Part      struct{ Kind string }
			}
		}
	}
	c.MustQuery(t, `query($id: ID!) {
		courseInstance(id: $id) {
			teachingHours
			parts { kind }
			borrowedParts { fromTrack part { kind } }
		}
	}`, map[string]any{"id": duplicated.DuplicateCourseInstance.ID}, &sibling)

	if len(sibling.CourseInstance.Parts) != 1 {
		t.Errorf("the sibling holds %d parts, want only its laboratory",
			len(sibling.CourseInstance.Parts))
	}
	if len(sibling.CourseInstance.BorrowedParts) != 1 {
		t.Fatalf("the sibling borrows %d parts, want the shared lecture — a cohort rendered "+
			"with laboratories and no lecture looks like a planning mistake",
			len(sibling.CourseInstance.BorrowedParts))
	}
	if sibling.CourseInstance.BorrowedParts[0].FromTrack != "A" {
		t.Errorf("the borrowed lecture comes from %q, want A",
			sibling.CourseInstance.BorrowedParts[0].FromTrack)
	}
	if sibling.CourseInstance.TeachingHours != 2 {
		t.Errorf("the sibling costs %v hours, want 2 — a lecture held once counts once",
			sibling.CourseInstance.TeachingHours)
	}

	// Withdrawing the cohort that owns the shared lecture takes the lecture with it, and the
	// sibling stops borrowing it. Nothing refuses, because nothing hangs off a part yet.
	var withdrawn struct {
		WithdrawCourseInstance string
	}
	c.MustQuery(t, `mutation($id: ID!) { withdrawCourseInstance(id: $id) }`,
		map[string]any{"id": first.ID}, &withdrawn)
	if withdrawn.WithdrawCourseInstance != first.ID {
		t.Errorf("withdrawing answered %q, want the id that was withdrawn",
			withdrawn.WithdrawCourseInstance)
	}
}

// Copying is what makes the second semester cheap. Through both doors, and it reports what it
// did even when it did nothing.
func TestCopyingASemestersDemandThroughBothDoors(t *testing.T) {
	t.Parallel()

	f := demandHandler(t,
		map[string][]string{testdata.Vier.Mail: {storetest.FixtureProgrammeA}},
		grants{testdata.Vier, []string{"PROGRAMME_LEAD"}})

	c := graphqltest.New(f.handler).AsUser(testdata.Vier.Mail).On(graphqltest.Browser)
	var declared struct {
		DeclareCourseInstance struct{ ID string }
	}
	c.MustQuery(t, declareMutation,
		declareInput(f, storetest.FixtureProgrammeA, "2026-WS", "A"), &declared)

	const copyMutation = `mutation($from: String!, $to: String!, $p: String!) {
		copyDemandFromSemester(from: $from, to: $to, programme: $p) {
			from to created skipped partsCreated
			programme { code }
			instances { track parts { kind } }
		}
	}`

	inTheBrowser(t, f.handler, testdata.Vier.Mail,
		func(t *testing.T, c *graphqltest.Client) {
			target := "2027-SS"
			if c.Door() == graphqltest.Token {
				target = "2027-WS"
			}

			var out struct {
				CopyDemandFromSemester struct {
					From         string
					To           string
					Created      int
					Skipped      int
					PartsCreated int
					Instances    []struct {
						Track string
						Parts []struct{ Kind string }
					}
				}
			}
			c.MustQuery(t, copyMutation, map[string]any{
				"from": "2026-WS", "to": target, "p": storetest.FixtureProgrammeA,
			}, &out)

			got := out.CopyDemandFromSemester
			if got.Created != 1 || got.Skipped != 0 || got.PartsCreated != 2 {
				t.Errorf("the copy reports created=%d skipped=%d parts=%d, want 1, 0 and 2",
					got.Created, got.Skipped, got.PartsCreated)
			}
			if len(got.Instances) != 1 {
				t.Fatalf("the report carries %d instances, want the demand of the target "+
					"semester afterwards", len(got.Instances))
			}

			// Again, and nothing may change. The person pressing the button twice has to be
			// able to tell that from a failure.
			c.MustQuery(t, copyMutation, map[string]any{
				"from": "2026-WS", "to": target, "p": storetest.FixtureProgrammeA,
			}, &out)
			got = out.CopyDemandFromSemester
			if got.Created != 0 || got.Skipped != 1 {
				t.Errorf("the second copy reports created=%d skipped=%d, want 0 and 1",
					got.Created, got.Skipped)
			}
		})
}

// A semester code that is not one, and one too far away to plan — the same codes the semester
// area answers with, so that a client branches on one meaning per code.
func TestTheDemandRefusesASemesterNobodyCanPlan(t *testing.T) {
	t.Parallel()

	f := demandHandler(t,
		map[string][]string{testdata.Vier.Mail: {storetest.FixtureProgrammeA}},
		grants{testdata.Vier, []string{"PROGRAMME_LEAD"}})

	cases := map[string]string{
		"WS 2027": "SEMESTER_CODE_INVALID",
		"9999-WS": "SEMESTER_OUT_OF_RANGE",
	}

	inTheBrowser(t, f.handler, testdata.Vier.Mail,
		func(t *testing.T, c *graphqltest.Client) {
			for code, want := range cases {
				resp := c.Do(t, declareMutation,
					declareInput(f, storetest.FixtureProgrammeA, code, ""))
				if got := errorCode(t, resp); got != want {
					t.Errorf("the semester %q was refused with %s, want %s", code, got, want)
				}
			}
		})
}

const planMutation = `mutation($s: String!, $p: String!, $e: [DemandEntryInput!]!, $d: Boolean!) {
	planDemand(semester: $s, programme: $p, entries: $e, dryRun: $d) {
		dryRun teachingHours
		created { moduleName track }
		withdrawn { moduleName track }
		changed { moduleName track trackBefore groupsBefore groupsAfter }
		refused { moduleName track code message }
		instances { track parts { kind } }
	}
}`

// planReport is the half of the report the tests assert about.
type planReport struct {
	PlanDemand struct {
		DryRun        bool
		TeachingHours float64
		Created       []struct{ ModuleName, Track string }
		Withdrawn     []struct{ ModuleName, Track string }
		Changed       []struct {
			ModuleName   string
			Track        string
			TrackBefore  *string
			GroupsBefore *int
			GroupsAfter  *int
		}
		Refused []struct {
			ModuleName, Track, Code, Message string
		}
		Instances []struct {
			Track string
			Parts []struct{ Kind string }
		}
	}
}

// The screen as one call, through both doors: tick a module, give it two cohorts with different
// numbers of groups, then take the tick away again.
func TestPlanningAWholeScreenThroughBothDoors(t *testing.T) {
	t.Parallel()

	f := demandHandler(t,
		map[string][]string{testdata.Vier.Mail: {storetest.FixtureProgrammeA}},
		grants{testdata.Vier, []string{"PROGRAMME_LEAD"}})

	inTheBrowser(t, f.handler, testdata.Vier.Mail,
		func(t *testing.T, c *graphqltest.Client) {
			// A semester per door: the two runs share a database, and the identity of an
			// instance is what stops the same cohort being declared twice.
			semester := "2027-SS"
			if c.Door() == graphqltest.Token {
				semester = "2027-WS"
			}
			// The empty list is built explicitly: a nil slice travels as JSON null, and the
			// schema refuses that on purpose. "No cohorts" is a statement somebody makes, and
			// it has to look different from a field that was left out.
			entry := func(tracks ...map[string]any) map[string]any {
				list := make([]map[string]any, 0, len(tracks))
				list = append(list, tracks...)
				return map[string]any{"moduleId": f.module.String(), "tracks": list}
			}
			plan := func(dryRun bool, entries ...map[string]any) planReport {
				t.Helper()
				var out planReport
				c.MustQuery(t, planMutation, map[string]any{
					"s": semester, "p": storetest.FixtureProgrammeA, "e": entries, "d": dryRun,
				}, &out)
				return out
			}

			// One cohort, two laboratory groups: a lecture and two groups is six hours of
			// teaching for a four-hour module.
			out := plan(false, entry(map[string]any{"track": "", "groups": 2}))
			if len(out.PlanDemand.Created) != 1 {
				t.Fatalf("the first save reports %+v, want one cohort created", out.PlanDemand)
			}
			if out.PlanDemand.TeachingHours != 6 {
				t.Errorf("the demand costs %v hours, want 6", out.PlanDemand.TeachingHours)
			}

			// A second cohort with a different number of groups — the case a single figure per
			// module cannot express.
			out = plan(false,
				entry(map[string]any{"track": "A", "groups": 3},
					map[string]any{"track": "B", "groups": 2}))
			if len(out.PlanDemand.Created) != 1 {
				t.Errorf("adding a cohort reports %+v created, want one", out.PlanDemand.Created)
			}
			var renamed bool
			for _, ch := range out.PlanDemand.Changed {
				if ch.TrackBefore != nil && *ch.TrackBefore == "" && ch.Track == "A" {
					renamed = true
				}
			}
			if !renamed {
				t.Errorf("the first cohort was not reported as renamed: %+v", out.PlanDemand.Changed)
			}
			if len(out.PlanDemand.Instances) != 2 {
				t.Fatalf("the module runs in %d cohorts, want 2", len(out.PlanDemand.Instances))
			}
			if len(out.PlanDemand.Instances[0].Parts) != 4 ||
				len(out.PlanDemand.Instances[1].Parts) != 3 {
				t.Errorf("the cohorts hold %d and %d parts, want four and three — three groups "+
					"in one and two in the other", len(out.PlanDemand.Instances[0].Parts),
					len(out.PlanDemand.Instances[1].Parts))
			}

			// The tick taken away, previewed first.
			dry := plan(true, entry())
			if !dry.PlanDemand.DryRun || len(dry.PlanDemand.Withdrawn) != 2 {
				t.Fatalf("the dry run reports %+v, want both cohorts withdrawn", dry.PlanDemand)
			}
			if len(dry.PlanDemand.Instances) != 2 {
				t.Errorf("the dry run changed the demand: %d instances left",
					len(dry.PlanDemand.Instances))
			}

			out = plan(false, entry())
			if len(out.PlanDemand.Withdrawn) != 2 || len(out.PlanDemand.Instances) != 0 {
				t.Errorf("the save reports %+v, want both cohorts gone", out.PlanDemand)
			}
			if out.PlanDemand.TeachingHours != 0 {
				t.Errorf("the emptied semester costs %v hours", out.PlanDemand.TeachingHours)
			}
		})
}

// Planning is a write, and it is scoped like every other one — through both doors, and without
// saying anything about the database on the way out.
func TestPlanningIsRefusedForAnotherProgramme(t *testing.T) {
	t.Parallel()

	f := demandHandler(t,
		map[string][]string{testdata.Vier.Mail: {storetest.FixtureProgrammeA}},
		grants{testdata.Vier, []string{"PROGRAMME_LEAD"}})

	inTheBrowser(t, f.handler, testdata.Vier.Mail,
		func(t *testing.T, c *graphqltest.Client) {
			resp := c.Do(t, planMutation, map[string]any{
				"s": "2027-SS",
				"p": storetest.FixtureProgrammeB,
				"e": []map[string]any{{
					"moduleId": f.module.String(),
					"tracks":   []map[string]any{{"track": "", "groups": 1}},
				}},
				"d": false,
			})
			if code := errorCode(t, resp); code != "NOT_YOUR_PROGRAMME" {
				t.Errorf("planning another programme answered %s, want NOT_YOUR_PROGRAMME", code)
			}
			for _, message := range resp.Messages() {
				graphqltest.AssertNoLeak(t, message,
					append(graphqltest.DatabaseNoise(), "course_instance", "instance_part")...)
			}
		})
}

// A module the call does not mention is not planned, not unplanned, not touched. It is the
// property every filter on the demand screen rests on.
func TestPlanningLeavesTheModulesItDoesNotNameAlone(t *testing.T) {
	t.Parallel()

	f := demandHandler(t,
		map[string][]string{testdata.Vier.Mail: {storetest.FixtureProgrammeA}},
		grants{testdata.Vier, []string{"PROGRAMME_LEAD"}})

	c := graphqltest.New(f.handler).AsUser(testdata.Vier.Mail).On(graphqltest.Browser)

	var out planReport
	c.MustQuery(t, planMutation, map[string]any{
		"s": "2028-SS", "p": storetest.FixtureProgrammeA, "d": false,
		"e": []map[string]any{
			{"moduleId": f.module.String(), "tracks": []map[string]any{{"track": "", "groups": 1}}},
			{"moduleId": f.withoutHours.String(), "tracks": []map[string]any{{"track": "", "groups": 1}}},
		},
	}, &out)

	// The module the catalogue states no hours for is refused by name, and the other one is
	// declared anyway — one refusal costs one row.
	if len(out.PlanDemand.Created) != 1 {
		t.Errorf("the save created %+v, want the one module that can be planned",
			out.PlanDemand.Created)
	}
	if len(out.PlanDemand.Refused) != 1 || out.PlanDemand.Refused[0].Code != "MODULE_NOT_DECOMPOSED" {
		t.Fatalf("the report refuses %+v, want the module with no hours", out.PlanDemand.Refused)
	}

	// A second save that names neither of them leaves both exactly as they are.
	c.MustQuery(t, planMutation, map[string]any{
		"s": "2028-SS", "p": storetest.FixtureProgrammeA, "d": false,
		"e": []map[string]any{},
	}, &out)
	if len(out.PlanDemand.Instances) != 1 || len(out.PlanDemand.Withdrawn) != 0 {
		t.Errorf("a plan naming no modules reports %+v — silence has to mean nothing at all",
			out.PlanDemand)
	}
}

// A programme lead may declare a module that is at home in another programme — and still only
// for their own programme.
//
// Two halves of one rule, and they are easy to confuse. The permission is about the *programme
// whose demand this is*, never about where the module comes from: modules are borrowed across
// programmes and faculties, and the difference between where one is at home and who declares it
// is precisely the figure the dean's office's import/export statistics are. A tool that refused
// the case would refuse the thing it exists to measure — and would leave a programme lead with
// no way to offer a module their catalogue does not list, which is the situation this escape
// hatch is for.
//
// Through both doors, because the rule is a permission and permissions are what drift between
// the two.
func TestPlanningAModuleOfAnotherProgrammeThroughBothDoors(t *testing.T) {
	t.Parallel()

	f := demandHandler(t,
		map[string][]string{testdata.Vier.Mail: {storetest.FixtureProgrammeA}},
		grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}})

	inTheBrowser(t, f.handler, testdata.Vier.Mail,
		func(t *testing.T, c *graphqltest.Client) {
			// A semester per door: the two runs share a database, and the identity refuses the
			// same cohort twice — which is the point of the identity.
			semester := "2027-SS"
			if c.Door() == graphqltest.Token {
				semester = "2027-WS"
			}

			var out struct {
				DeclareCourseInstance struct {
					Programme struct{ Code string }
					Module    struct{ Name string }
					Parts     []struct{ Kind string }
				}
			}
			c.MustQuery(t, declareMutation, map[string]any{"in": map[string]any{
				"semester":  semester,
				"programme": storetest.FixtureProgrammeA,
				"moduleId":  f.foreign.String(),
			}}, &out)

			got := out.DeclareCourseInstance
			if got.Programme.Code != storetest.FixtureProgrammeA {
				t.Errorf("the instance belongs to %s, want %s — the demand is the declaring "+
					"programme's, whatever the module's home is",
					got.Programme.Code, storetest.FixtureProgrammeA)
			}
			if len(got.Parts) == 0 {
				t.Error("the instance holds no parts — it was built from neither a split nor a " +
					"proposal")
			}

			// The other half: the module's home programme is not a programme this person may
			// plan, and borrowing the module does not change that.
			refusal := c.Do(t, declareMutation, map[string]any{"in": map[string]any{
				"semester":  semester,
				"programme": storetest.FixtureProgrammeZ,
				"moduleId":  f.foreign.String(),
			}})
			// NOT_YOUR_PROGRAMME rather than a bare FORBIDDEN: the two refusals have
			// different repairs, and this one names the right question — which programme is
			// this demand for.
			if code := errorCode(t, refusal); code != "NOT_YOUR_PROGRAMME" {
				t.Errorf("declaring for somebody else's programme gave %s, want NOT_YOUR_PROGRAMME",
					code)
			}
		})
}

// The point of the whole thing: a placeholder is planned like any other module, and "we need
// three of them" is three cohorts of it.
func TestAPlaceholderIsPlannedLikeAnyOtherModule(t *testing.T) {
	t.Parallel()

	// The demand fixture rather than the catalogue one: this needs both services wired, and
	// that is the whole claim — a placeholder goes through the ordinary planning path.
	f := demandHandler(t,
		map[string][]string{testdata.Vier.Mail: {storetest.FixtureProgrammeA}},
		grants{testdata.Vier, []string{"PROGRAMME_LEAD"}})

	c := graphqltest.New(f.handler).AsUser(testdata.Vier.Mail)

	var created localModuleResult
	c.MustQuery(t, createLocalMutation,
		localInput(storetest.FixtureProgrammeA, "FWP-Platzhalter (technisch)", "FWP_PLACEHOLDER"),
		&created)

	// Three cohorts, which is what three of them *are*: three offerings of one module in one
	// programme and semester have to differ in their track, so the identity says so already.
	for _, track := range []string{"A", "B", "C"} {
		var out struct {
			DeclareCourseInstance struct {
				Track  string
				Module struct{ Kind string }
				Parts  []struct{ Kind string }
			}
		}
		c.MustQuery(t, declareMutation, map[string]any{"in": map[string]any{
			"semester":  "2027-SS",
			"programme": storetest.FixtureProgrammeA,
			"moduleId":  created.CreateLocalModule.ID,
			"track":     track,
		}}, &out)

		if out.DeclareCourseInstance.Track != track {
			t.Errorf("the cohort is %q, want %q", out.DeclareCourseInstance.Track, track)
		}
		if len(out.DeclareCourseInstance.Parts) != 1 {
			t.Errorf("the instance holds %d parts, want the one the split states",
				len(out.DeclareCourseInstance.Parts))
		}
	}
}

// A programme this faculty does not plan takes no demand, whoever holds what.
//
// Not a permission and deliberately not ErrNotYourProgramme: the person may well be a programme
// lead, and the repair is not a grant. It is a statement about the programme — somebody else's,
// or one of ours that has run out — so it gets a code of its own, and the sentence says which.
//
// Reading its demand stays possible, and that is the other half: what was planned in a programme
// that has run out is the record of what the faculty did.
func TestAProgrammeTheFacultyDoesNotPlanTakesNoDemandThroughBothDoors(t *testing.T) {
	t.Parallel()

	f := demandHandler(t,
		map[string][]string{testdata.Vier.Mail: {storetest.FixtureProgrammeA}},
		grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}})

	// Declared while it is still planned, so that there is something to read afterwards.
	graphqltest.New(f.handler).AsUser(testdata.Vier.Mail).MustQuery(t, declareMutation,
		declareInput(f, storetest.FixtureProgrammeA, "2027-SS", ""), nil)

	if _, err := f.schema.Pool.Exec(t.Context(),
		`UPDATE programme SET planning_status = 'DISCONTINUED' WHERE code = $1`,
		storetest.FixtureProgrammeA); err != nil {
		t.Fatalf("cannot retire the programme: %v", err)
	}

	inTheBrowser(t, f.handler, testdata.Vier.Mail,
		func(t *testing.T, c *graphqltest.Client) {
			refusal := c.Do(t, declareMutation,
				declareInput(f, storetest.FixtureProgrammeA, "2027-WS", "A"))
			if code := errorCode(t, refusal); code != "PROGRAMME_NOT_PLANNED" {
				t.Errorf("declaring for a programme that has run out gave %s, want "+
					"PROGRAMME_NOT_PLANNED", code)
			}

			// And its demand is still readable, which is the point of keeping the row at all.
			var out struct {
				CourseInstances []struct{ Semester string }
			}
			c.MustQuery(t, `query($s: String!, $p: String!) {
				courseInstances(semester: $s, programme: $p) { semester }
			}`, map[string]any{"s": "2027-SS", "p": storetest.FixtureProgrammeA}, &out)
			if len(out.CourseInstances) != 1 {
				t.Errorf("the demand of a programme that has run out reads %d instances, want the "+
					"one declared before it did", len(out.CourseInstances))
			}
		})
}

// inTheBrowser runs an assertion once, in a signed-in browser session.
//
// The counterpart of graphqltest.EachDoor for the demand *writes*, which are @interactiveOnly and
// therefore have nothing to assert on the token path beyond the refusal itself — which
// TestEveryDemandMutationRefusesAToken asserts once, for all of them, from the schema.
//
// A named helper rather than an inline client, so that a write test written later reads as
// deliberately one-door rather than as somebody who forgot EachDoor.
func inTheBrowser(t *testing.T, h http.Handler, user string,
	fn func(*testing.T, *graphqltest.Client)) {
	t.Helper()

	t.Run("interactive", func(t *testing.T) {
		fn(t, graphqltest.New(h).AsUser(user).On(graphqltest.Browser))
	})
}

// coverageQuery is what a demand screen asks for once coverage exists.
const coverageQuery = `query($s: String!, $p: String) {
	courseInstances(semester: $s, programme: $p) {
		id track teachingHours
		programme { code }
		parts { kind }
		borrowedParts { fromTrack fromProgramme { code } part { kind teachingHours } }
		coveredBy { requestedAt acceptedAt instance { id programme { code } track } }
		covers { requestedAt acceptedAt instance { id programme { code } track } }
	}
}`

// The handshake, on the way in that still has one.
//
// Declaring a cohort beside another programme's holds the two together on the spot — nothing is
// taken from anybody, because the cohort has nothing yet. Pointing a cohort that *already* holds
// parts at somebody else's event is the larger act, and there the other programme still answers
// for it. This is that way in, so the test begins by planning the two separately.
//
// Read as a sequence rather than as four tests, because the state each step needs is the previous
// step's result and the interesting refusals are the ones in the middle of it.
func TestTheCoverageHandshakeNeedsBothSides(t *testing.T) {
	t.Parallel()

	f := demandHandler(t,
		map[string][]string{
			testdata.Vier.Mail: {storetest.FixtureProgrammeA},
			testdata.Eins.Mail: {storetest.FixtureProgrammeB},
		},
		grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}},
		grants{testdata.Eins, []string{"LECTURER", "PROGRAMME_LEAD"}},
		grants{testdata.Zwei, []string{"LECTURER"}})

	hostLead := graphqltest.New(f.handler).AsUser(testdata.Vier.Mail).On(graphqltest.Browser)
	guestLead := graphqltest.New(f.handler).AsUser(testdata.Eins.Mail).On(graphqltest.Browser)

	var host, guest struct {
		DeclareCourseInstance struct{ ID string }
	}
	hostLead.MustQuery(t, declareMutation,
		declareInput(f, storetest.FixtureProgrammeA, "2027-SS", ""), &host)
	guestLead.MustQuery(t, declareMutation,
		declareInput(f, storetest.FixtureProgrammeB, "2027-SS", ""), &guest)

	hostID := host.DeclareCourseInstance.ID
	guestID := guest.DeclareCourseInstance.ID

	// The second declaration arrived held with the first. Planned apart again, so that what
	// follows is the manual way in rather than a repeat of the automatic one.
	guestLead.MustQuery(t, `mutation($id: ID!) { releaseInstanceCoverage(id: $id) { id } }`,
		map[string]any{"id": guestID}, &struct {
			ReleaseInstanceCoverage struct{ ID string }
		}{})

	const request = `mutation($id: ID!, $by: ID!) {
		requestInstanceCoverage(id: $id, coveredBy: $by) {
			teachingHours parts { kind }
			coveredBy { acceptedAt instance { programme { code } } }
		}
	}`
	const accept = `mutation($id: ID!) {
		acceptInstanceCoverage(id: $id) {
			teachingHours parts { kind }
			borrowedParts { fromProgramme { code } part { kind } }
			coveredBy { acceptedAt }
		}
	}`

	// The holding programme's lead may not ask in the other's name.
	if got := errorCode(t, hostLead.Do(t, request,
		map[string]any{"id": guestID, "by": hostID})); got != "NOT_YOUR_PROGRAMME" {
		t.Errorf("the holding lead asking on the other's behalf answered %s, "+
			"want NOT_YOUR_PROGRAMME", got)
	}

	// The asking lead may — and may name an instance of a programme they cannot write.
	var asked struct {
		RequestInstanceCoverage struct {
			TeachingHours float64
			Parts         []struct{ Kind string }
			CoveredBy     struct {
				AcceptedAt *string
				Instance   struct{ Programme struct{ Code string } }
			}
		}
	}
	guestLead.MustQuery(t, request, map[string]any{"id": guestID, "by": hostID}, &asked)

	if asked.RequestInstanceCoverage.CoveredBy.AcceptedAt != nil {
		t.Error("asking counted as agreeing, which is one programme deciding for two")
	}
	if len(asked.RequestInstanceCoverage.Parts) != 2 {
		t.Errorf("asking removed the asking cohort's parts (%d left, want 2) — nothing may "+
			"change until the other side agrees", len(asked.RequestInstanceCoverage.Parts))
	}
	if got := asked.RequestInstanceCoverage.CoveredBy.Instance.Programme.Code; got != storetest.FixtureProgrammeA {
		t.Errorf("the request names programme %s, want %s", got, storetest.FixtureProgrammeA)
	}

	// The asking lead may not answer their own request.
	if got := errorCode(t, guestLead.Do(t, accept,
		map[string]any{"id": guestID})); got != "NOT_YOUR_PROGRAMME" {
		t.Errorf("the asking lead agreed on the holder's behalf, answering %s, "+
			"want NOT_YOUR_PROGRAMME", got)
	}

	// The holding lead does, and that is when anything changes.
	var agreed struct {
		AcceptInstanceCoverage struct {
			TeachingHours float64
			Parts         []struct{ Kind string }
			BorrowedParts []struct {
				FromProgramme *struct{ Code string }
				Part          struct{ Kind string }
			}
			CoveredBy struct{ AcceptedAt *string }
		}
	}
	hostLead.MustQuery(t, accept, map[string]any{"id": guestID}, &agreed)

	got := agreed.AcceptInstanceCoverage
	if got.CoveredBy.AcceptedAt == nil {
		t.Error("the agreement was not recorded")
	}
	if len(got.Parts) != 0 {
		t.Errorf("the covered cohort still holds %d parts of its own", len(got.Parts))
	}
	if got.TeachingHours != 0 {
		t.Errorf("the covered cohort costs %v hours, want 0 — the event is held once and "+
			"costs once, at the programme that holds it", got.TeachingHours)
	}
	if len(got.BorrowedParts) != 2 {
		t.Fatalf("the covered cohort attends %d parts, want 2 — a cohort with nothing at all "+
			"reads as a planning mistake", len(got.BorrowedParts))
	}
	for _, b := range got.BorrowedParts {
		if b.FromProgramme == nil || b.FromProgramme.Code != storetest.FixtureProgrammeA {
			t.Errorf("a borrowed part does not name the holding programme: %+v", b.FromProgramme)
		}
	}

	// Ending it works from either side. The holding lead does it here, which is the half that
	// would be missing if the mutation had been written as "withdraw your own request".
	const release = `mutation($id: ID!) { releaseInstanceCoverage(id: $id) {
		teachingHours parts { kind } coveredBy { acceptedAt }
	} }`
	var released struct {
		ReleaseInstanceCoverage struct {
			TeachingHours float64
			Parts         []struct{ Kind string }
			CoveredBy     *struct{ AcceptedAt *string }
		}
	}
	hostLead.MustQuery(t, release, map[string]any{"id": guestID}, &released)

	if released.ReleaseInstanceCoverage.CoveredBy != nil {
		t.Error("the link survived being ended")
	}
	if len(released.ReleaseInstanceCoverage.Parts) != 2 {
		t.Errorf("the cohort got %d parts back, want the two of the module's split",
			len(released.ReleaseInstanceCoverage.Parts))
	}

	// A lecturer with no programme at all touches none of it.
	lecturer := graphqltest.New(f.handler).AsUser(testdata.Zwei.Mail).On(graphqltest.Browser)
	if got := errorCode(t, lecturer.Do(t, request,
		map[string]any{"id": guestID, "by": hostID})); got != "NOT_YOUR_PROGRAMME" {
		t.Errorf("a lecturer asking answered %s, want NOT_YOUR_PROGRAMME", got)
	}
}

// The reads answer the same through both doors.
//
// The realistic failure is not a wrong answer but a rule somebody adds for the browser and forgets
// on the token path — and coverage adds three new fields at once, which is exactly the shape of
// thing that gets wired in one place.
func TestCoverageIsReadableThroughBothDoors(t *testing.T) {
	t.Parallel()

	f := demandHandler(t,
		map[string][]string{
			testdata.Vier.Mail: {storetest.FixtureProgrammeA, storetest.FixtureProgrammeB},
		},
		grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}},
		grants{testdata.Eins, []string{"LECTURER"}})

	lead := graphqltest.New(f.handler).AsUser(testdata.Vier.Mail).On(graphqltest.Browser)

	// Two programmes declare the same module, and the second is held with the first as it is
	// made. Nothing is arranged here: this test reads the result of the rule, not of a handshake.
	var declared struct {
		DeclareCourseInstance struct{ ID string }
	}
	lead.MustQuery(t, declareMutation,
		declareInput(f, storetest.FixtureProgrammeA, "2027-SS", ""), &declared)
	lead.MustQuery(t, declareMutation,
		declareInput(f, storetest.FixtureProgrammeB, "2027-SS", ""), &declared)

	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var out struct {
				CourseInstances []struct {
					ID            string
					TeachingHours float64
					Programme     struct{ Code string }
					Parts         []struct{ Kind string }
					BorrowedParts []struct {
						FromProgramme *struct{ Code string }
					}
					CoveredBy *struct {
						AcceptedAt *string
						Instance   struct{ Programme struct{ Code string } }
					}
					Covers []struct {
						AcceptedAt *string
						Instance   struct{ Programme struct{ Code string } }
					}
				}
			}
			c.MustQuery(t, coverageQuery, map[string]any{"s": "2027-SS"}, &out)

			if len(out.CourseInstances) != 2 {
				t.Fatalf("the semester holds %d instances, want 2", len(out.CourseInstances))
			}

			var total float64
			for _, i := range out.CourseInstances {
				total += i.TeachingHours

				switch i.Programme.Code {
				case storetest.FixtureProgrammeB:
					if i.CoveredBy == nil || i.CoveredBy.AcceptedAt == nil {
						t.Error("the covered cohort does not report who holds its teaching")
					} else if got := i.CoveredBy.Instance.Programme.Code; got != storetest.FixtureProgrammeA {
						t.Errorf("it names %s as the holder, want %s",
							got, storetest.FixtureProgrammeA)
					}
					if len(i.Parts) != 0 {
						t.Errorf("the covered cohort reports %d parts of its own", len(i.Parts))
					}
					if len(i.BorrowedParts) != 2 {
						t.Errorf("it attends %d parts, want 2", len(i.BorrowedParts))
					}
				case storetest.FixtureProgrammeA:
					if len(i.Covers) != 1 {
						t.Fatalf("the holding cohort reports %d covered demands, want 1",
							len(i.Covers))
					}
					if i.Covers[0].AcceptedAt == nil {
						t.Error("the agreement is not visible from the side that made it")
					}
				}
			}

			// The number the whole feature is about, asserted through both doors: one event held
			// for two programmes costs the faculty once.
			if total != 4 {
				t.Errorf("the two cohorts cost %v hours together, want 4", total)
			}
		})
}

// declareWithCoverage is the declaration, asked for with what the coupling puts on it.
//
// Its own document rather than a wider declareMutation: every other test in this file reads a
// declaration that holds its own teaching, and adding coverage to the shared one would make them
// all assert against fields they are not about.
const declareWithCoverage = `mutation($in: DeclareCourseInstanceInput!) {
	declareCourseInstance(input: $in) {
		id teachingHours
		parts { kind }
		borrowedParts { fromProgramme { code } }
		coveredBy { acceptedAt instance { programme { code } } }
	}
}`

// The rule, through the API: declaring beside another programme's declaration holds the two
// together, and nobody is asked.
//
// The write is entirely inside the declaring programme — a cohort appears, holding nothing, with a
// reference. Not one field of the other programme's row changes, which is what makes one side
// enough. What it does take from the other side is optionality, and that is given straight back:
// either lead may release, and the release is asserted here beside the coupling for that reason.
func TestDeclaringBesideAnotherProgrammeHoldsTheTwoTogether(t *testing.T) {
	t.Parallel()

	f := demandHandler(t,
		map[string][]string{
			testdata.Vier.Mail: {storetest.FixtureProgrammeA},
			testdata.Eins.Mail: {storetest.FixtureProgrammeB},
		},
		grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}},
		grants{testdata.Eins, []string{"LECTURER", "PROGRAMME_LEAD"}})

	hostLead := graphqltest.New(f.handler).AsUser(testdata.Vier.Mail).On(graphqltest.Browser)
	guestLead := graphqltest.New(f.handler).AsUser(testdata.Eins.Mail).On(graphqltest.Browser)

	var host struct {
		DeclareCourseInstance struct {
			ID            string
			TeachingHours float64
			Parts         []struct{ Kind string }
		}
	}
	hostLead.MustQuery(t, declareWithCoverage,
		declareInput(f, storetest.FixtureProgrammeA, "2027-SS", ""), &host)
	if host.DeclareCourseInstance.TeachingHours == 0 {
		t.Fatal("the first declaration holds nothing; there was nobody to hold it for")
	}

	var guest struct {
		DeclareCourseInstance struct {
			ID            string
			TeachingHours float64
			Parts         []struct{ Kind string }
			BorrowedParts []struct {
				FromProgramme *struct{ Code string }
			}
			CoveredBy *struct {
				AcceptedAt *string
				Instance   struct{ Programme struct{ Code string } }
			}
		}
	}
	guestLead.MustQuery(t, declareWithCoverage,
		declareInput(f, storetest.FixtureProgrammeB, "2027-SS", ""), &guest)

	got := guest.DeclareCourseInstance
	if got.CoveredBy == nil {
		t.Fatal("the second declaration holds its own teaching; the two were not held together")
	}
	if got.CoveredBy.AcceptedAt == nil {
		t.Error("the coupling is waiting for somebody to agree — declaring beside another " +
			"programme is one act by one lead, not two")
	}
	if code := got.CoveredBy.Instance.Programme.Code; code != storetest.FixtureProgrammeA {
		t.Errorf("it is held by %q, want %s", code, storetest.FixtureProgrammeA)
	}
	if len(got.Parts) != 0 {
		t.Errorf("the held cohort holds %d parts of its own", len(got.Parts))
	}
	if got.TeachingHours != 0 {
		t.Errorf("the held cohort costs %v hours, want 0", got.TeachingHours)
	}
	if len(got.BorrowedParts) == 0 {
		t.Error("it attends nothing — a cohort with neither its own teaching nor borrowed " +
			"teaching is the row this mechanism exists to prevent")
	}

	// The veto, exercised after the fact rather than before it. This is what makes one side
	// enough: nobody is held to a state they cannot leave.
	var released struct {
		ReleaseInstanceCoverage struct {
			TeachingHours float64
			CoveredBy     *struct{ AcceptedAt *string }
		}
	}
	hostLead.MustQuery(t, `mutation($id: ID!) {
		releaseInstanceCoverage(id: $id) { teachingHours coveredBy { acceptedAt } }
	}`, map[string]any{"id": got.ID}, &released)

	if released.ReleaseInstanceCoverage.CoveredBy != nil {
		t.Error("the holding programme could not walk away from a coupling it never agreed to")
	}
	if released.ReleaseInstanceCoverage.TeachingHours == 0 {
		t.Error("releasing left the cohort with no teaching at all")
	}
}

// The badge that makes an unnoticed duplicate visible.
//
// Two programmes offering the same module separately is the case this whole area exists to make
// avoidable, and until now nothing said it out loud: the coupling was only ever found by somebody
// looking for it. It is also the net under the race — two leads planning the same module in the
// same moment, neither seeing the other's uncommitted row, both ending up holding their own.
func TestACohortSaysWhoElseOffersTheModuleSeparately(t *testing.T) {
	t.Parallel()

	f := demandHandler(t,
		map[string][]string{
			testdata.Vier.Mail: {storetest.FixtureProgrammeA, storetest.FixtureProgrammeB},
		},
		grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}},
		grants{testdata.Eins, []string{"LECTURER"}})

	lead := graphqltest.New(f.handler).AsUser(testdata.Vier.Mail).On(graphqltest.Browser)

	var first, second struct {
		DeclareCourseInstance struct{ ID string }
	}
	lead.MustQuery(t, declareMutation,
		declareInput(f, storetest.FixtureProgrammeA, "2027-SS", ""), &first)
	lead.MustQuery(t, declareMutation,
		declareInput(f, storetest.FixtureProgrammeB, "2027-SS", ""), &second)

	// The second was held with the first, so neither is "planned separately" — they are one event
	// seen from two programmes, and that is what coveredBy and covers are for.
	lead.MustQuery(t, `mutation($id: ID!) { releaseInstanceCoverage(id: $id) { id } }`,
		map[string]any{"id": second.DeclareCourseInstance.ID}, &struct {
			ReleaseInstanceCoverage struct{ ID string }
		}{})

	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var out struct {
				CourseInstances []struct {
					Programme             struct{ Code string }
					CoveredBy             *struct{ AcceptedAt *string }
					AlsoPlannedSeparately []struct {
						Programme struct{ Code string }
					}
				}
			}
			c.MustQuery(t, `query($s: String!) {
				courseInstances(semester: $s) {
					programme { code }
					coveredBy { acceptedAt }
					alsoPlannedSeparately { programme { code } }
				}
			}`, map[string]any{"s": "2027-SS"}, &out)

			if len(out.CourseInstances) != 2 {
				t.Fatalf("the semester holds %d instances, want 2", len(out.CourseInstances))
			}

			for _, i := range out.CourseInstances {
				if i.CoveredBy != nil {
					t.Fatalf("%s is still held elsewhere; this test is about the separate case",
						i.Programme.Code)
				}
				if len(i.AlsoPlannedSeparately) != 1 {
					t.Fatalf("%s names %d other programmes offering the module separately, want 1",
						i.Programme.Code, len(i.AlsoPlannedSeparately))
				}
				// Each names the other, and neither names itself.
				other := i.AlsoPlannedSeparately[0].Programme.Code
				if other == i.Programme.Code {
					t.Errorf("%s names itself", i.Programme.Code)
				}
			}
		})

	// And once they are held together it stops saying so: a covered cohort is not a second event,
	// it is the same one seen from another programme.
	lead.MustQuery(t, `mutation($id: ID!, $by: ID!) {
		requestInstanceCoverage(id: $id, coveredBy: $by) { id }
	}`, map[string]any{
		"id": second.DeclareCourseInstance.ID, "by": first.DeclareCourseInstance.ID,
	}, &struct {
		RequestInstanceCoverage struct{ ID string }
	}{})
	lead.MustQuery(t, `mutation($id: ID!) { acceptInstanceCoverage(id: $id) { id } }`,
		map[string]any{"id": second.DeclareCourseInstance.ID}, &struct {
			AcceptInstanceCoverage struct{ ID string }
		}{})

	var after struct {
		CourseInstances []struct {
			Programme             struct{ Code string }
			AlsoPlannedSeparately []struct{ ID string }
		}
	}
	lead.MustQuery(t, `query($s: String!) {
		courseInstances(semester: $s) {
			programme { code }
			alsoPlannedSeparately { id }
		}
	}`, map[string]any{"s": "2027-SS"}, &after)

	for _, i := range after.CourseInstances {
		if len(i.AlsoPlannedSeparately) != 0 {
			t.Errorf("%s still reports %d separate offerings after the two were held together",
				i.Programme.Code, len(i.AlsoPlannedSeparately))
		}
	}
}
