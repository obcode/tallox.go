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

// The demand through the API, and through both doors.
//
// EachDoor is not ceremony here. Planning is neither confidential nor personnel data, so a
// Personal Access Token is supposed to reach exactly as far as a browser session — a colleague
// declaring their own programme's demand from a script is a use this API exists for. The
// realistic failure is not a wrong answer but a rule somebody adds for the browser and forgets
// on the token path, and that is what running the same assertion twice catches.

// demandFixture is a handler with a projected catalogue and a split module behind it.
type demandFixture struct {
	handler http.Handler
	// module is the ordinary module, split into a lecture and a laboratory.
	module uuid.UUID
	// undivided is a module nobody has stated a split for.
	undivided uuid.UUID
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
		undivided: read(`SELECT id FROM module WHERE zpa_module_ref = $1`,
			storetest.FixtureModuleDutyDiffers),
	}

	if _, err := modules.SetModuleComponents(t.Context(), fixture.module, []domain.ModuleComponent{
		{Kind: domain.PartKindLecture, TeachingHours: 2, Position: 0},
		{Kind: domain.PartKindLab, TeachingHours: 2, Position: 1},
	}, uuid.Nil); err != nil {
		t.Fatalf("cannot state the module's split: %v", err)
	}

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

	graphqltest.EachDoor(t, f.handler, testdata.Vier.Mail, testdata.Vier.Token,
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

	graphqltest.EachDoor(t, f.handler, testdata.Vier.Mail, testdata.Vier.Token,
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

			graphqltest.EachDoor(t, f.handler, tc.persona.Mail, tc.persona.Token,
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

	graphqltest.EachDoor(t, f.handler, testdata.Vier.Mail, testdata.Vier.Token,
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

// The precondition, as the caller meets it: a module whose hours nobody has split cannot be
// offered, and the code says which repair is needed.
func TestAModuleWithoutASplitIsRefusedByName(t *testing.T) {
	t.Parallel()

	f := demandHandler(t,
		map[string][]string{testdata.Vier.Mail: {storetest.FixtureProgrammeA}},
		grants{testdata.Vier, []string{"PROGRAMME_LEAD"}})

	graphqltest.EachDoor(t, f.handler, testdata.Vier.Mail, testdata.Vier.Token,
		func(t *testing.T, c *graphqltest.Client) {
			resp := c.Do(t, declareMutation, map[string]any{"in": map[string]any{
				"semester":  "2027-SS",
				"programme": storetest.FixtureProgrammeA,
				"moduleId":  f.undivided.String(),
			}})
			if code := errorCode(t, resp); code != "MODULE_NOT_DECOMPOSED" {
				t.Errorf("a module with no split was refused with %s, want MODULE_NOT_DECOMPOSED",
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

	graphqltest.EachDoor(t, f.handler, testdata.Vier.Mail, testdata.Vier.Token,
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

	// And a lecturer may not write it.
	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			resp := c.Do(t, `mutation($id: ID!) { withdrawCourseInstance(id: $id) }`,
				map[string]any{"id": declared.DeclareCourseInstance.ID})
			if code := errorCode(t, resp); code != "NOT_YOUR_PROGRAMME" {
				t.Errorf("a lecturer withdrew an instance, or was told %s", code)
			}
		})
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

	graphqltest.EachDoor(t, f.handler, testdata.Vier.Mail, testdata.Vier.Token,
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

	graphqltest.EachDoor(t, f.handler, testdata.Vier.Mail, testdata.Vier.Token,
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
