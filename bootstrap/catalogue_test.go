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

// catalogueFixture is a handler with a projected catalogue behind it, plus the ids a test needs
// to talk about it.
type catalogueFixture struct {
	handler http.Handler
	// programmeA is the ordinary programme, the one the leads below are scoped to.
	programmeA uuid.UUID
	// moduleOrdinary is compulsory in programme A and also counts in programme B.
	moduleOrdinary uuid.UUID
	// moduleElsewhere is at home in another programme, so a lead of A may not touch its split.
	moduleElsewhere uuid.UUID
}

// catalogueHandler seeds people, projects the synthetic catalogue and returns the handler Serve
// would build.
//
// The catalogue arrives through the projection rather than through hand-written INSERTs on
// purpose: what the API serves is what the import produces, and a fixture assembled by hand
// would let the two drift while every test stayed green.
func catalogueHandler(t *testing.T, scoped map[string][]string, people ...grants) catalogueFixture {
	t.Helper()

	s := storetest.New(t)
	for _, p := range people {
		storetest.SeedPerson(t, s, p.who, p.roles...)

		parsed, err := auth.ParseToken(p.who.Token)
		if err != nil {
			t.Fatalf("fixture token of %s does not parse: %v", p.who.Name, err)
		}
		storetest.SeedToken(t, s, p.who, auth.HashSecret(parsed.Secret), storetest.TokenOptions{
			Description: "catalogue test",
		})
	}

	storetest.SeedZPACatalogue(t, s)
	if _, err := store.NewCatalogue(s.Pool).Project(t.Context(), nil); err != nil {
		t.Fatalf("cannot project the catalogue: %v", err)
	}

	// The programme assignments, by persona mail.
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

	fixture := catalogueFixture{
		handler: bootstrap.Handler(bootstrap.Options{
			Build: buildinfo.Info{Version: "test"},
			Auth: auth.Config{
				Mode:   auth.ModeProxy,
				Users:  store.NewDirectory(s.Pool),
				Tokens: store.NewDirectory(s.Pool),
			},
			People: domain.NewPeopleService(store.NewPeople(s.Pool), nil),
			Import: domain.NewZPASyncService(
				store.NewZPA(s.Pool), nil, nil, store.NewCatalogue(s.Pool)),
			Catalogue: domain.NewCatalogueService(store.NewModules(s.Pool)),
		}),
	}

	read := func(query string, args ...any) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := s.Pool.QueryRow(t.Context(), query, args...).Scan(&id); err != nil {
			t.Fatalf("cannot read a fixture id: %v", err)
		}
		return id
	}
	fixture.programmeA = read(`SELECT id FROM programme WHERE code = $1`, storetest.FixtureProgrammeA)
	fixture.moduleOrdinary = read(`SELECT id FROM module WHERE zpa_module_ref = $1`,
		storetest.FixtureModuleOrdinary)
	fixture.moduleElsewhere = read(`SELECT id FROM module WHERE zpa_module_ref = $1`,
		storetest.FixtureModuleOfProgrammeZ)

	return fixture
}

const modulesQuery = `query($f: ModuleFilter) { modules(filter: $f) { name homeProgramme { code } } }`

// The catalogue is not confidential. A lecturer has to see a module before they can say they
// would like to teach it, and it is published by the examination office anyway.
func TestTheCatalogueIsReadableByAnybodyWithAnAccount(t *testing.T) {
	t.Parallel()

	f := catalogueHandler(t, nil, grants{testdata.Eins, []string{"LECTURER"}})

	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var out struct {
				Modules []struct {
					Name          string
					HomeProgramme struct{ Code string }
				}
			}
			c.MustQuery(t, modulesQuery, nil, &out)

			if len(out.Modules) == 0 {
				t.Fatal("a lecturer sees no modules at all")
			}
		})
}

// Without an account there is nothing, through either door — the same 401-shaped answer every
// other field gives, because the person table is the access control.
func TestTheCatalogueRefusesAnAnonymousCaller(t *testing.T) {
	t.Parallel()

	f := catalogueHandler(t, nil, grants{testdata.Eins, []string{"LECTURER"}})

	c := graphqltest.New(f.handler).Anonymous().On(graphqltest.Browser)
	resp := c.Do(t, modulesQuery, nil)
	if len(resp.Errors) == 0 {
		t.Fatal("an anonymous caller read the catalogue")
	}
}

// The union that a programme's list is, asserted as a difference rather than as a number: the
// module at home in the programme but in none of its regulations has to be in it.
func TestAProgrammesListIncludesItsOwnModulesThatAreInNoRegulations(t *testing.T) {
	t.Parallel()

	f := catalogueHandler(t, nil, grants{testdata.Eins, []string{"LECTURER"}})

	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var out struct {
				Modules []struct {
					Name        string
					InCatalogue bool
				}
			}
			c.MustQuery(t, `query($p: String!) {
				modules(filter: {programme: $p}) { name inCatalogue(programme: $p) }
			}`, map[string]any{"p": storetest.FixtureProgrammeA}, &out)

			var onlyAtHome int
			for _, m := range out.Modules {
				if !m.InCatalogue {
					onlyAtHome++
				}
			}
			if onlyAtHome == 0 {
				t.Error("every module in the programme's list is in its catalogue. The module " +
					"that is at home there and in none of its regulations is missing — and 26 " +
					"active modules of the real catalogue are in exactly that state.")
			}
		})
}

// The field takes an argument, and the argument has to matter. gqlgen binds a field with an
// argument to a struct field of the same name if it finds one, silently ignoring the argument;
// this is the assertion that says it did not.
func TestDutyStatusAnswersAboutTheProgrammeItWasAsked(t *testing.T) {
	t.Parallel()

	f := catalogueHandler(t, nil, grants{testdata.Eins, []string{"LECTURER"}})

	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var out struct {
				Modules []struct {
					Name   string
					InA    *string
					InB    *string
					Absent *string
				}
			}
			c.MustQuery(t, `query($a: String!, $b: String!, $z: String!) {
				modules(filter: {search: "Modul mit wechselnder"}) {
					name
					inA: dutyStatus(programme: $a)
					inB: dutyStatus(programme: $b)
					absent: dutyStatus(programme: $z)
				}
			}`, map[string]any{
				"a": storetest.FixtureProgrammeA,
				"b": storetest.FixtureProgrammeB,
				"z": storetest.FixtureProgrammeZ,
			}, &out)

			if len(out.Modules) != 1 {
				t.Fatalf("found %d modules, want the one that is compulsory in one version and "+
					"elective in another", len(out.Modules))
			}
			m := out.Modules[0]

			// Compulsory under one version of A's regulations and elective under another. Two
			// values would have to choose; three can say so.
			if m.InA == nil || *m.InA != "MIXED" {
				t.Errorf("in programme A the status is %v, want MIXED", m.InA)
			}
			// It does not appear in B at all, which is different from being elective there.
			if m.InB != nil {
				t.Errorf("in programme B the status is %v, want null — the module does not "+
					"appear in that catalogue", *m.InB)
			}
			if m.Absent != nil {
				t.Errorf("in a programme with no regulations the status is %v, want null", *m.Absent)
			}
		})
}

// The demand deadline is a term, and 89 modules of the real catalogue run only in the other one.
func TestTheFrequencyFilterKeepsWhatItSays(t *testing.T) {
	t.Parallel()

	f := catalogueHandler(t, nil, grants{testdata.Eins, []string{"LECTURER"}})

	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var out struct {
				Modules []struct {
					Name      string
					Frequency string
				}
			}
			c.MustQuery(t, `{
				modules(filter: {frequency: [EVERY_WINTER_SEMESTER]}) { name frequency }
			}`, nil, &out)

			if len(out.Modules) == 0 {
				t.Fatal("no winter-only modules at all")
			}
			for _, m := range out.Modules {
				if m.Frequency != "EVERY_WINTER_SEMESTER" {
					t.Errorf("%s has frequency %s and passed a filter for winter-only", m.Name, m.Frequency)
				}
			}

			// An absent filter is every frequency, not none. `= ANY('{}')` is false for every
			// row, so the natural reading of an empty list would return an empty catalogue.
			var all struct{ Modules []struct{ Name string } }
			c.MustQuery(t, `{ modules { name } }`, nil, &all)
			if len(all.Modules) <= len(out.Modules) {
				t.Errorf("the unfiltered list has %d modules and the winter-only one %d",
					len(all.Modules), len(out.Modules))
			}
		})
}

// The work list. A programme lead getting ready for a semester needs to know which of their
// modules cannot be declared yet.
func TestTheWorkListFindsModulesWithNoSplit(t *testing.T) {
	t.Parallel()

	f := catalogueHandler(t,
		map[string][]string{testdata.Vier.Mail: {storetest.FixtureProgrammeA}},
		grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}})

	c := graphqltest.New(f.handler).AsUser(testdata.Vier.Mail).On(graphqltest.Browser)

	var before struct{ Modules []struct{ Name string } }
	c.MustQuery(t, `{ modules(filter: {withoutComponents: true}) { name } }`, nil, &before)
	if len(before.Modules) == 0 {
		t.Fatal("nothing is missing a split, so the work list proves nothing")
	}

	var set struct {
		SetModuleComponents struct {
			ComponentHours *float64
			Components     []struct {
				Kind          string
				TeachingHours float64
				Position      int
			}
		}
	}
	c.MustQuery(t, `mutation($m: ID!) {
		setModuleComponents(moduleId: $m, components: [
			{kind: LECTURE, teachingHours: 2},
			{kind: LAB, teachingHours: 2}
		]) { componentHours components { kind teachingHours position } }
	}`, map[string]any{"m": f.moduleOrdinary.String()}, &set)

	if got := set.SetModuleComponents.ComponentHours; got == nil || *got != 4 {
		t.Errorf("the split adds up to %v, want 4", got)
	}
	if len(set.SetModuleComponents.Components) != 2 {
		t.Fatalf("the split has %d parts, want 2", len(set.SetModuleComponents.Components))
	}
	// Positions come from the order the caller sent, so a client cannot produce a gap.
	for i, c := range set.SetModuleComponents.Components {
		if c.Position != i {
			t.Errorf("part %d is at position %d", i, c.Position)
		}
	}

	var after struct{ Modules []struct{ Name string } }
	c.MustQuery(t, `{ modules(filter: {withoutComponents: true}) { name } }`, nil, &after)
	if len(after.Modules) != len(before.Modules)-1 {
		t.Errorf("the work list went from %d to %d; stating one split should shorten it by one",
			len(before.Modules), len(after.Modules))
	}
}

// The rule this release exists to make enforceable, through both doors.
func TestOnlyTheHomeProgrammesLeadStatesASplit(t *testing.T) {
	t.Parallel()

	f := catalogueHandler(t,
		map[string][]string{testdata.Vier.Mail: {storetest.FixtureProgrammeA}},
		grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}},
		grants{testdata.Drei, []string{"LECTURER", "PROGRAMME_LEAD"}},
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Fuenf, []string{"LECTURER", "DEANS_OFFICE"}})

	const mutation = `mutation($m: ID!) {
		setModuleComponents(moduleId: $m, components: [{kind: LECTURE, teachingHours: 2}]) { id }
	}`

	t.Run("the lead of the home programme may", func(t *testing.T) {
		graphqltest.EachDoor(t, f.handler, testdata.Vier.Mail, testdata.Vier.Token,
			func(t *testing.T, c *graphqltest.Client) {
				var out struct{ SetModuleComponents struct{ ID string } }
				c.MustQuery(t, mutation, map[string]any{"m": f.moduleOrdinary.String()}, &out)
			})
	})

	t.Run("the dean's office may", func(t *testing.T) {
		graphqltest.EachDoor(t, f.handler, testdata.Fuenf.Mail, testdata.Fuenf.Token,
			func(t *testing.T, c *graphqltest.Client) {
				var out struct{ SetModuleComponents struct{ ID string } }
				c.MustQuery(t, mutation, map[string]any{"m": f.moduleOrdinary.String()}, &out)
			})
	})

	t.Run("a lead of another programme may not", func(t *testing.T) {
		graphqltest.EachDoor(t, f.handler, testdata.Vier.Mail, testdata.Vier.Token,
			func(t *testing.T, c *graphqltest.Client) {
				resp := c.Do(t, mutation, map[string]any{"m": f.moduleElsewhere.String()})
				assertRefusal(t, resp, "NOT_YOUR_PROGRAMME")
			})
	})

	t.Run("a lead with no programme is told what is missing, not that they may not", func(t *testing.T) {
		graphqltest.EachDoor(t, f.handler, testdata.Drei.Mail, testdata.Drei.Token,
			func(t *testing.T, c *graphqltest.Client) {
				resp := c.Do(t, mutation, map[string]any{"m": f.moduleOrdinary.String()})
				// A different code from the refusal above, because the two need different
				// actions: this person should not go and ask for a role they already hold.
				assertRefusal(t, resp, "PROGRAMME_SCOPE_MISSING")
			})
	})

	t.Run("a lecturer may not", func(t *testing.T) {
		graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
			func(t *testing.T, c *graphqltest.Client) {
				resp := c.Do(t, mutation, map[string]any{"m": f.moduleOrdinary.String()})
				assertRefusal(t, resp, "NOT_YOUR_PROGRAMME")
			})
	})
}

// A refusal must not become a source of information about the database or about other people.
func TestARefusedSplitLeaksNothing(t *testing.T) {
	t.Parallel()

	f := catalogueHandler(t, nil,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Zwei, []string{"LECTURER"}})

	c := graphqltest.New(f.handler).AsUser(testdata.Eins.Mail).On(graphqltest.Browser)
	resp := c.Do(t, `mutation($m: ID!) {
		setModuleComponents(moduleId: $m, components: [{kind: LECTURE, teachingHours: 2}]) { id }
	}`, map[string]any{"m": f.moduleOrdinary.String()})

	if len(resp.Errors) == 0 {
		t.Fatal("the mutation was allowed")
	}
	graphqltest.AssertNoLeak(t, resp.Errors[0].Message,
		append(graphqltest.DatabaseNoise(),
			testdata.Mails(testdata.Others(testdata.Eins))...)...)
}

// An empty split clears the module and makes it undeclarable again — allowed on purpose, so
// that a split entered wrongly can be removed by the person who entered it.
func TestAnEmptySplitClearsTheModule(t *testing.T) {
	t.Parallel()

	f := catalogueHandler(t,
		map[string][]string{testdata.Vier.Mail: {storetest.FixtureProgrammeA}},
		grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}})

	c := graphqltest.New(f.handler).AsUser(testdata.Vier.Mail).On(graphqltest.Browser)

	var out struct {
		SetModuleComponents struct {
			ComponentHours *float64
			Components     []struct{ Kind string }
		}
	}
	c.MustQuery(t, `mutation($m: ID!) {
		setModuleComponents(moduleId: $m, components: [{kind: LECTURE, teachingHours: 2}]) {
			componentHours components { kind }
		}
	}`, map[string]any{"m": f.moduleOrdinary.String()}, &out)
	if len(out.SetModuleComponents.Components) != 1 {
		t.Fatalf("the split was not stated")
	}

	c.MustQuery(t, `mutation($m: ID!) {
		setModuleComponents(moduleId: $m, components: []) { componentHours components { kind } }
	}`, map[string]any{"m": f.moduleOrdinary.String()}, &out)

	if len(out.SetModuleComponents.Components) != 0 {
		t.Errorf("the split survived being cleared")
	}
	if out.SetModuleComponents.ComponentHours != nil {
		t.Errorf("a module with no split reports %v hours, want null",
			*out.SetModuleComponents.ComponentHours)
	}
}

// Hours that describe nothing are refused before they reach the constraint, so the caller gets
// a sentence rather than a SQLSTATE.
func TestASplitWithImpossibleHoursIsRefused(t *testing.T) {
	t.Parallel()

	f := catalogueHandler(t,
		map[string][]string{testdata.Vier.Mail: {storetest.FixtureProgrammeA}},
		grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}})

	c := graphqltest.New(f.handler).AsUser(testdata.Vier.Mail).On(graphqltest.Browser)

	for _, hours := range []float64{0, -2, 40} {
		resp := c.Do(t, `mutation($m: ID!, $h: Float!) {
			setModuleComponents(moduleId: $m, components: [{kind: LECTURE, teachingHours: $h}]) { id }
		}`, map[string]any{"m": f.moduleOrdinary.String(), "h": hours})

		assertRefusal(t, resp, "COMPONENTS_INVALID")
		graphqltest.AssertNoLeak(t, resp.Errors[0].Message, graphqltest.DatabaseNoise()...)
	}
}

// Assigning programmes is the deploy step this release creates, and the screen that has to ship
// with it — without it, PROGRAMME_LEAD is a role nobody can use.
func TestAssigningProgrammesToALead(t *testing.T) {
	t.Parallel()

	f := catalogueHandler(t, nil,
		grants{testdata.Sechs, []string{"LECTURER", "ADMIN"}},
		grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}},
		grants{testdata.Eins, []string{"LECTURER"}})

	admin := graphqltest.New(f.handler).AsUser(testdata.Sechs.Mail).On(graphqltest.Browser)

	var people struct {
		People []struct {
			ID         string
			Mail       string
			Programmes []struct{ Code string }
		}
	}
	admin.MustQuery(t, `{ people { id mail programmes { code } } }`, nil, &people)

	id := func(mail string) string {
		t.Helper()
		for _, p := range people.People {
			if p.Mail == mail {
				return p.ID
			}
		}
		t.Fatalf("%s is not in the list", mail)
		return ""
	}

	// A fresh lead leads nothing, and that is a state with consequences rather than a gap.
	for _, p := range people.People {
		if p.Mail == testdata.Vier.Mail && len(p.Programmes) != 0 {
			t.Errorf("a lead nobody has assigned anything to already leads %d programme(s)",
				len(p.Programmes))
		}
	}

	const assign = `mutation($id: ID!, $p: [String!]!) {
		setPersonProgrammes(id: $id, programmes: $p) { programmes { code } }
	}`

	var out struct {
		SetPersonProgrammes struct{ Programmes []struct{ Code string } }
	}
	// Lower case and a duplicate, both of which a person types.
	admin.MustQuery(t, assign, map[string]any{
		"id": id(testdata.Vier.Mail),
		"p":  []string{"pa", "PA", storetest.FixtureProgrammeB},
	}, &out)

	if len(out.SetPersonProgrammes.Programmes) != 2 {
		t.Fatalf("the lead now leads %d programme(s), want 2 — the duplicate should have been "+
			"folded and the lower-case code accepted", len(out.SetPersonProgrammes.Programmes))
	}

	// Replacing, not adding: the whole set at once is what stops the two halves of a swap being
	// separated.
	admin.MustQuery(t, assign, map[string]any{
		"id": id(testdata.Vier.Mail),
		"p":  []string{storetest.FixtureProgrammeB},
	}, &out)
	if len(out.SetPersonProgrammes.Programmes) != 1 ||
		out.SetPersonProgrammes.Programmes[0].Code != storetest.FixtureProgrammeB {
		t.Errorf("setting the list to one programme left %v", out.SetPersonProgrammes.Programmes)
	}

	t.Run("an unknown code is named rather than dropped", func(t *testing.T) {
		resp := admin.Do(t, assign, map[string]any{
			"id": id(testdata.Vier.Mail), "p": []string{"NOPE"},
		})
		assertRefusal(t, resp, "UNKNOWN_PROGRAMME")
	})

	t.Run("somebody who does not lead is refused with the repair", func(t *testing.T) {
		resp := admin.Do(t, assign, map[string]any{
			"id": id(testdata.Eins.Mail), "p": []string{storetest.FixtureProgrammeA},
		})
		// Its own code: the next step is to grant the role, and a generic refusal would not say
		// so.
		assertRefusal(t, resp, "NOT_A_PROGRAMME_LEAD")
	})

	t.Run("only an administrator may", func(t *testing.T) {
		lead := graphqltest.New(f.handler).AsUser(testdata.Vier.Mail).On(graphqltest.Browser)
		resp := lead.Do(t, assign, map[string]any{
			"id": id(testdata.Vier.Mail), "p": []string{storetest.FixtureProgrammeA},
		})
		assertRefusal(t, resp, "FORBIDDEN")
	})
}

// Granting access from a long-lived token in a script would decouple the granting of access
// from any sign-in. Asserted per door rather than through EachDoor, because this is one of the
// places the two are supposed to differ.
func TestAssigningProgrammesIsInteractiveOnly(t *testing.T) {
	t.Parallel()

	f := catalogueHandler(t, nil,
		grants{testdata.Sechs, []string{"LECTURER", "ADMIN"}},
		grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}})

	var people struct{ People []struct{ ID, Mail string } }
	graphqltest.New(f.handler).AsUser(testdata.Sechs.Mail).On(graphqltest.Browser).
		MustQuery(t, `{ people { id mail } }`, nil, &people)

	var leadID string
	for _, p := range people.People {
		if p.Mail == testdata.Vier.Mail {
			leadID = p.ID
		}
	}

	resp := graphqltest.New(f.handler).WithToken(testdata.Sechs.Token).On(graphqltest.Token).
		Do(t, `mutation($id: ID!) {
			setPersonProgrammes(id: $id, programmes: []) { id }
		}`, map[string]any{"id": leadID})

	assertRefusal(t, resp, "INTERACTIVE_ONLY")
}

// Which programmes you may plan is the first thing a script needs to know, and on `me` it is
// your own data — so it answers through both doors, like `roles`.
func TestYourOwnProgrammesAreReadableThroughBothDoors(t *testing.T) {
	t.Parallel()

	f := catalogueHandler(t,
		map[string][]string{testdata.Vier.Mail: {storetest.FixtureProgrammeA}},
		grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}})

	graphqltest.EachDoor(t, f.handler, testdata.Vier.Mail, testdata.Vier.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var out struct {
				Me struct {
					Programmes []struct{ Code string }
				}
			}
			c.MustQuery(t, `{ me { programmes { code } } }`, nil, &out)

			if len(out.Me.Programmes) != 1 ||
				out.Me.Programmes[0].Code != storetest.FixtureProgrammeA {
				t.Errorf("me.programmes is %v, want the one programme this lead was assigned",
					out.Me.Programmes)
			}
		})
}

// The diagnosis exists to answer "why can my colleague not do this", and this release creates a
// new way for the answer to be "because nobody assigned them a programme" — which looks, from
// every other line, exactly like being set up correctly.
func TestTheDiagnosisNamesAMissingProgrammeAssignment(t *testing.T) {
	t.Parallel()

	f := catalogueHandler(t,
		map[string][]string{testdata.Drei.Mail: {storetest.FixtureProgrammeA}},
		grants{testdata.Sechs, []string{"LECTURER", "ADMIN"}},
		grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}},
		grants{testdata.Drei, []string{"LECTURER", "PROGRAMME_LEAD"}})

	admin := graphqltest.New(f.handler).AsUser(testdata.Sechs.Mail).On(graphqltest.Browser)

	diagnosisOf := func(mail string) (bool, string) {
		t.Helper()
		var out struct {
			DiagnoseAccess struct {
				Decisions []struct {
					Rule    string
					Allowed bool
					Reason  string
				}
			}
		}
		admin.MustQuery(t, `query($m: String!) {
			diagnoseAccess(mail: $m) { decisions { rule allowed reason } }
		}`, map[string]any{"m": mail}, &out)

		for _, d := range out.DiagnoseAccess.Decisions {
			if d.Rule == "policy.PlanningScope" {
				return d.Allowed, d.Reason
			}
		}
		t.Fatalf("the diagnosis of %s says nothing about planning", mail)
		return false, ""
	}

	allowed, reason := diagnosisOf(testdata.Vier.Mail)
	if allowed {
		t.Error("a lead with no programme is diagnosed as able to plan")
	}
	if !strings.Contains(reason, "keinem Studiengang zugeordnet") {
		t.Errorf("the reason is %q; it has to name the thing that is missing, or an "+
			"administrator reads it as 'the role is wrong'", reason)
	}

	allowed, reason = diagnosisOf(testdata.Drei.Mail)
	if !allowed {
		t.Error("a lead who was assigned a programme is diagnosed as unable to plan")
	}
	if !strings.Contains(reason, storetest.FixtureProgrammeA) {
		t.Errorf("the reason is %q; it should name the programmes they lead", reason)
	}
}

// The projection is a second thing that can be stale, and it has to be visible as one: a
// successful import with a failed projection is fresh payloads behind a week-old catalogue.
func TestTheProjectionCanBeRunOnItsOwnAndReportsWhatItFound(t *testing.T) {
	t.Parallel()

	f := catalogueHandler(t, nil, grants{testdata.Sechs, []string{"LECTURER", "ADMIN"}})

	admin := graphqltest.New(f.handler).AsUser(testdata.Sechs.Mail).On(graphqltest.Browser)

	var out struct {
		ProjectZpaCatalogue struct {
			RunID             *string
			Status            string
			ProgrammesWritten int
			ModulesWritten    int
			OfferingsWritten  int
			Notes             []struct {
				Finding string
				Count   int
				Sample  []string
			}
		}
	}
	admin.MustQuery(t, `mutation {
		projectZpaCatalogue {
			runId status programmesWritten modulesWritten offeringsWritten
			notes { finding count sample }
		}
	}`, nil, &out)

	got := out.ProjectZpaCatalogue
	if got.Status != "SUCCEEDED" {
		t.Fatalf("the projection ended as %s", got.Status)
	}
	// Asked for on its own, so it belongs to no import run.
	if got.RunID != nil {
		t.Errorf("a projection nobody's import triggered claims run %s", *got.RunID)
	}
	if got.ModulesWritten == 0 || got.OfferingsWritten == 0 {
		t.Errorf("the projection wrote %d modules and %d offerings",
			got.ModulesWritten, got.OfferingsWritten)
	}

	findings := make(map[string]int, len(got.Notes))
	for _, n := range got.Notes {
		findings[n.Finding] = n.Count
		if n.Count == 0 {
			t.Errorf("%s is reported with a count of zero; a line that means nothing is noise "+
				"in a report whose value is that every line means something", n.Finding)
		}
		if len(n.Sample) == 0 {
			t.Errorf("%s has no examples, so nobody can go and look", n.Finding)
		}
	}

	// The decisions the synthetic catalogue was built to force. Named individually rather than
	// counted, because what matters is that each is *visible* — a projection that silently
	// dropped rows would be indistinguishable from a catalogue that never had them.
	for _, want := range []string{
		"MODULE_WITHOUT_HOME_PROGRAMME",
		"PROGRAMME_WITHOUT_REGULATIONS",
		"MODULE_WITHOUT_NAME",
		"ASSOCIATION_WITH_UNKNOWN_REGULATIONS",
		"FREQUENCY_UNMAPPED",
		"COURSE_TYPE_UNMAPPED",
	} {
		if _, ok := findings[want]; !ok {
			t.Errorf("the report says nothing about %s", want)
		}
	}

	// The one that is an alarm rather than a note.
	if count, ok := findings["DUTY_CONFLICT"]; ok {
		t.Errorf("%d set(s) of regulations call a module both compulsory and elective. The "+
			"grain of a module's offerings rests on that never happening.", count)
	}

	// And it is readable afterwards, beside the import runs.
	var list struct {
		ZpaCatalogueProjections []struct {
			Status string
			Notes  []struct{ Finding string }
		}
	}
	admin.MustQuery(t, `{ zpaCatalogueProjections(limit: 5) { status notes { finding } } }`,
		nil, &list)
	if len(list.ZpaCatalogueProjections) == 0 {
		t.Fatal("the projection that just ran is not in the list")
	}
	if len(list.ZpaCatalogueProjections[0].Notes) != len(got.Notes) {
		t.Errorf("the stored report has %d lines and the one just returned had %d",
			len(list.ZpaCatalogueProjections[0].Notes), len(got.Notes))
	}
}

// It rewrites the catalogue everybody plans with, so it stays attributable to a sign-in. Per
// door rather than through EachDoor, because this is one of the places the two differ.
func TestProjectingIsInteractiveOnlyAndAdministrative(t *testing.T) {
	t.Parallel()

	f := catalogueHandler(t, nil,
		grants{testdata.Sechs, []string{"LECTURER", "ADMIN"}},
		grants{testdata.Fuenf, []string{"LECTURER", "DEANS_OFFICE"}},
		grants{testdata.Eins, []string{"LECTURER"}})

	const mutation = `mutation { projectZpaCatalogue { status } }`

	t.Run("an administrator in a browser may", func(t *testing.T) {
		var out struct{ ProjectZpaCatalogue struct{ Status string } }
		graphqltest.New(f.handler).AsUser(testdata.Sechs.Mail).On(graphqltest.Browser).
			MustQuery(t, mutation, nil, &out)
	})

	t.Run("the dean's office may — it is who notices a stale catalogue", func(t *testing.T) {
		var out struct{ ProjectZpaCatalogue struct{ Status string } }
		graphqltest.New(f.handler).AsUser(testdata.Fuenf.Mail).On(graphqltest.Browser).
			MustQuery(t, mutation, nil, &out)
	})

	t.Run("the same administrator through a token may not", func(t *testing.T) {
		resp := graphqltest.New(f.handler).WithToken(testdata.Sechs.Token).On(graphqltest.Token).
			Do(t, mutation, nil)
		assertRefusal(t, resp, "INTERACTIVE_ONLY")
	})

	t.Run("a lecturer may not", func(t *testing.T) {
		resp := graphqltest.New(f.handler).AsUser(testdata.Eins.Mail).On(graphqltest.Browser).
			Do(t, mutation, nil)
		assertRefusal(t, resp, "FORBIDDEN")
	})

	t.Run("reading the reports is interactive-only too", func(t *testing.T) {
		resp := graphqltest.New(f.handler).WithToken(testdata.Sechs.Token).On(graphqltest.Token).
			Do(t, `{ zpaCatalogueProjections { status } }`, nil)
		assertRefusal(t, resp, "INTERACTIVE_ONLY")
	})
}
