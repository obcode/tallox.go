package bootstrap_test

import (
	"net/http"
	"slices"
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
	// schema is the private schema behind it, for the two or three tests that have to state a
	// fact the API has no mutation for.
	schema *storetest.Schema
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
	// After the people, so that the derived "is this teacher a user here" has something to find:
	// the fixture's ordinary teacher carries the same address as the persona Eins.
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
		schema: s,
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

// An empty split clears the module and hands it back to the proposal — allowed on purpose, so
// that a split entered wrongly can be removed by the person who entered it without making the
// module unplannable.
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
			TeachersWritten   int
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
			runId status programmesWritten teachersWritten modulesWritten offeringsWritten
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
	// Every counter, and not only the interesting one. A field that is added to the schema and
	// to the row type but never filled in the mapping between them reads as zero for ever — the
	// projection does the work, the page says nothing happened, and nothing is red.
	if got.ProgrammesWritten == 0 || got.TeachersWritten == 0 ||
		got.ModulesWritten == 0 || got.OfferingsWritten == 0 {
		t.Errorf("the projection reports %d programmes, %d teachers, %d modules and %d "+
			"offerings; a zero here is usually a counter that is never mapped rather than work "+
			"that did not happen", got.ProgrammesWritten, got.TeachersWritten,
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

// The link the fifth endpoint exists for, seen from the API. Through both doors, because a
// colleague evaluating "which modules am I responsible for" from a script is a use this API
// exists for.
func TestAModuleNamesItsResponsibleTeacher(t *testing.T) {
	t.Parallel()

	f := catalogueHandler(t, nil, grants{testdata.Eins, []string{"LECTURER"}})

	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var out struct {
				Modules []struct {
					Name        string
					Responsible *struct {
						Name        string
						SortName    string
						Mail        *string
						IsProfessor bool
						IsUser      bool
					}
				}
			}
			c.MustQuery(t, `{
				modules { name responsible { name sortName mail isProfessor isUser } }
			}`, nil, &out)

			var named, unnamed int
			for _, m := range out.Modules {
				if m.Responsible == nil {
					unnamed++
					continue
				}
				named++
				if m.Responsible.SortName == "" {
					t.Errorf("%s names somebody with no sort name", m.Name)
				}
			}
			if named == 0 {
				t.Error("no module names anybody as responsible")
			}
			// The fixture has one module whose responsible person is a placeholder. Null and not
			// an empty teacher: about one real module in thirty is in that state.
			if unnamed == 0 {
				t.Error("every module names somebody, so the unresolvable case is not covered")
			}
		})
}

// Importing 257 teachers must not admit anybody. The rule lives in the schema — there is no
// person row and no foreign key — and this is the same assertion from the outside: somebody
// who teaches and has never been admitted is not a user.
func TestATeacherIsNotAUserUntilSomebodyAdmitsThem(t *testing.T) {
	t.Parallel()

	f := catalogueHandler(t, nil, grants{testdata.Eins, []string{"LECTURER"}})

	c := graphqltest.New(f.handler).AsUser(testdata.Eins.Mail).On(graphqltest.Browser)

	var out struct {
		Teachers []struct {
			Name     string
			SortName string
			Mail     *string
			IsUser   bool
			Active   bool
		}
	}
	c.MustQuery(t, `{ teachers(includeInactive: true) { name sortName mail isUser active } }`,
		nil, &out)

	if len(out.Teachers) == 0 {
		t.Fatal("no teachers at all")
	}

	var users, withoutMail int
	for _, teacher := range out.Teachers {
		if teacher.IsUser {
			users++
		}
		if teacher.Mail == nil {
			withoutMail++
		}
	}

	// Exactly the one persona seeded above, who is in the fixture as a teacher and has a person
	// row. Everybody else teaches and cannot sign in.
	if users != 1 {
		t.Errorf("%d of %d teachers read as users of this installation, want the one who was "+
			"deliberately given a person row", users, len(out.Teachers))
	}
	// The address is the link, so a teacher without one can never become a user. Three of the
	// 257 real ones are like this.
	if withoutMail == 0 {
		t.Error("no teacher without an address, so the case that can never be linked is not covered")
	}

	// The default hides the ones the source marks as no longer teaching.
	var active struct{ Teachers []struct{ Active bool } }
	c.MustQuery(t, `{ teachers { active } }`, nil, &active)
	for _, teacher := range active.Teachers {
		if !teacher.Active {
			t.Error("the default list contains somebody the source marks as no longer teaching")
		}
	}
	if len(active.Teachers) >= len(out.Teachers) {
		t.Error("including the inactive ones changed nothing, so the filter is not exercised")
	}
}

// The list of people who teach is not confidential — it is who teaches at the faculty — but it
// still needs an account, like everything else here.
func TestTheTeacherListNeedsAnAccount(t *testing.T) {
	t.Parallel()

	f := catalogueHandler(t, nil, grants{testdata.Eins, []string{"LECTURER"}})

	resp := graphqltest.New(f.handler).Anonymous().On(graphqltest.Browser).
		Do(t, `{ teachers { name } }`, nil)
	if len(resp.Errors) == 0 {
		t.Fatal("an anonymous caller read the list of people who teach")
	}
}

// The proposal, through both doors.
//
// It is not a convenience of the interface: an instance may be declared from it, so a script has
// to be able to see the same guess the browser shows — and to see that it is one.
func TestAModuleWithoutAStatedSplitCarriesAProposal(t *testing.T) {
	t.Parallel()

	f := catalogueHandler(t, nil, grants{testdata.Eins, []string{"LECTURER"}})

	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var out struct {
				Modules []struct {
					Name               string
					SplitIsEstimated   bool
					Plannable          bool
					ProgrammeSemester  *int
					Components         []struct{ Kind string }
					ProposedComponents []struct {
						Kind          string
						TeachingHours float64
					}
				}
			}
			c.MustQuery(t, `query($p: String!) {
				modules(filter: {programme: $p}) {
					name splitIsEstimated plannable
					programmeSemester(programme: $p)
					components { kind }
					proposedComponents { kind teachingHours }
				}
			}`, map[string]any{"p": storetest.FixtureProgrammeA}, &out)

			byName := map[string]int{}
			for i, m := range out.Modules {
				byName[m.Name] = i
			}

			ordinary, ok := byName["Ordentliches Modul"]
			if !ok {
				t.Fatalf("the ordinary module is not in the list: %+v", out.Modules)
			}
			m := out.Modules[ordinary]

			if len(m.Components) != 0 {
				t.Fatalf("the fixture module has a stated split; this test is about the one that "+
					"has none: %+v", m.Components)
			}
			if !m.SplitIsEstimated || !m.Plannable {
				t.Errorf("estimated=%v plannable=%v, want both true — a four-hour module with a "+
					"laboratory has a proposal, and an instance may be declared from it",
					m.SplitIsEstimated, m.Plannable)
			}
			if len(m.ProposedComponents) != 2 ||
				m.ProposedComponents[0].Kind != "LECTURE" || m.ProposedComponents[0].TeachingHours != 2 ||
				m.ProposedComponents[1].Kind != "LAB" || m.ProposedComponents[1].TeachingHours != 2 {
				t.Errorf("the proposal is %+v, want a two-hour lecture and a two-hour laboratory",
					m.ProposedComponents)
			}
			if m.ProgrammeSemester == nil || *m.ProgrammeSemester != 1 {
				t.Errorf("the earliest semester is %v, want 1", m.ProgrammeSemester)
			}

			// A module whose course type names one unit gets one part, not a lecture and a rest.
			if seminar, ok := byName["Modul in zwei Körben"]; ok {
				p := out.Modules[seminar].ProposedComponents
				if len(p) != 1 || p[0].Kind != "SEMINAR" || p[0].TeachingHours != 2 {
					t.Errorf("the seminar's proposal is %+v, want one two-hour seminar", p)
				}
			}

			// The fold that matters: this module's two versions of the regulations disagree — the
			// old one says the fourth semester, the new one the third — and the earliest wins.
			if differs, ok := byName["Modul mit wechselnder Pflicht"]; ok {
				got := out.Modules[differs].ProgrammeSemester
				if got == nil || *got != 3 {
					t.Errorf("the earliest semester of the module whose versions disagree is %v, "+
						"want 3 — the earliest of the two", got)
				}
			}
		})
}

// Confirming is one click, and it is what turns a guess into the faculty's own statement.
func TestConfirmingAProposalMakesItAStatedSplit(t *testing.T) {
	t.Parallel()

	f := catalogueHandler(t,
		map[string][]string{testdata.Vier.Mail: {storetest.FixtureProgrammeA}},
		grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}})

	c := graphqltest.New(f.handler).AsUser(testdata.Vier.Mail).On(graphqltest.Browser)

	var before struct {
		Module struct {
			SplitIsEstimated   bool
			ProposedComponents []struct {
				Kind          string
				TeachingHours float64
			}
		}
	}
	c.MustQuery(t, `query($m: ID!) {
		module(id: $m) { splitIsEstimated proposedComponents { kind teachingHours } }
	}`, map[string]any{"m": f.moduleOrdinary.String()}, &before)

	if !before.Module.SplitIsEstimated {
		t.Fatal("the module already carries a stated split")
	}

	components := make([]map[string]any, 0, len(before.Module.ProposedComponents))
	for _, p := range before.Module.ProposedComponents {
		components = append(components, map[string]any{
			"kind": p.Kind, "teachingHours": p.TeachingHours,
		})
	}

	var after struct {
		SetModuleComponents struct {
			SplitIsEstimated bool
			ComponentHours   *float64
			Components       []struct {
				Kind          string
				TeachingHours float64
			}
		}
	}
	c.MustQuery(t, `mutation($m: ID!, $c: [ModuleComponentInput!]!) {
		setModuleComponents(moduleId: $m, components: $c) {
			splitIsEstimated componentHours components { kind teachingHours }
		}
	}`, map[string]any{"m": f.moduleOrdinary.String(), "c": components}, &after)

	got := after.SetModuleComponents
	if got.SplitIsEstimated {
		t.Error("the split is still reported as a guess after somebody confirmed it")
	}
	if len(got.Components) != 2 || got.Components[0].Kind != "LECTURE" {
		t.Errorf("the stated split is %+v, want what the proposal said", got.Components)
	}
	if got.ComponentHours == nil || *got.ComponentHours != 4 {
		t.Errorf("the confirmed split adds up to %v, want the 4 the catalogue states",
			got.ComponentHours)
	}
}

const createLocalMutation = `mutation($in: LocalModuleInput!) {
	createLocalModule(input: $in) {
		id name source kind plannable splitIsEstimated
		homeProgramme { code }
		components { kind teachingHours }
	}
}`

type localModuleResult struct {
	CreateLocalModule struct {
		ID               string
		Name             string
		Source           string
		Kind             string
		Plannable        bool
		SplitIsEstimated bool
		HomeProgramme    struct{ Code string }
		Components       []struct {
			Kind          string
			TeachingHours float64
		}
	}
}

func localInput(programme, name, kind string) map[string]any {
	return map[string]any{"in": map[string]any{
		"programme":           programme,
		"name":                name,
		"kind":                kind,
		"courseType":          "SU",
		"frequency":           "ON_ANNOUNCEMENT",
		"contactHoursPerWeek": 4,
		"components":          []map[string]any{{"kind": "LECTURE", "teachingHours": 4}},
	}}
}

// A course the faculty enters itself, through both doors, and scoped to the programme it is at
// home in.
//
// The same rule setModuleComponents uses and the same sentence behind it: a module is planned
// where it is at home. Not @interactiveOnly — a course is neither confidential nor personnel
// data, and a programme lead who keeps their placeholders in a script is doing nothing this API
// should refuse.
func TestCreatingALocalModuleIsScopedToTheProgrammeThroughBothDoors(t *testing.T) {
	t.Parallel()

	f := catalogueHandler(t,
		map[string][]string{testdata.Vier.Mail: {storetest.FixtureProgrammeA}},
		grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}})

	graphqltest.EachDoor(t, f.handler, testdata.Vier.Mail, testdata.Vier.Token,
		func(t *testing.T, c *graphqltest.Client) {
			// A name per door: the two runs share a database, and the name is the identity of a
			// local row — which is the point of the unique index.
			name := "FWP-Platzhalter (technisch)"
			if c.Door() == graphqltest.Token {
				name = "FWP-Platzhalter (allgemein)"
			}

			var out localModuleResult
			c.MustQuery(t, createLocalMutation,
				localInput(storetest.FixtureProgrammeA, name, "FWP_PLACEHOLDER"), &out)

			got := out.CreateLocalModule
			switch {
			case got.Source != "LOCAL":
				t.Errorf("the source is %q, want LOCAL", got.Source)
			case got.Kind != "FWP_PLACEHOLDER":
				t.Errorf("the kind is %q, want FWP_PLACEHOLDER", got.Kind)
			case got.HomeProgramme.Code != storetest.FixtureProgrammeA:
				t.Errorf("it is at home in %s, want %s",
					got.HomeProgramme.Code, storetest.FixtureProgrammeA)
			case !got.Plannable:
				t.Error("the placeholder is not plannable, so no instance could be declared of it")
			case got.SplitIsEstimated:
				t.Error("the split it was given is being reported as an estimate")
			case len(got.Components) != 1:
				t.Errorf("the split holds %d parts, want the one it was given", len(got.Components))
			}

			// And not for a programme this person does not lead. The refusal names the right
			// question — which programme — rather than the role they already hold.
			refusal := c.Do(t, createLocalMutation,
				localInput(storetest.FixtureProgrammeZ, "Fremdes Fach", "MODULE"))
			if code := errorCode(t, refusal); code != "NOT_YOUR_PROGRAMME" {
				t.Errorf("entering a course for somebody else's programme gave %s, want "+
					"NOT_YOUR_PROGRAMME", code)
			}
		})
}

// A lecturer may read the catalogue and may not add to it.
func TestALecturerCannotEnterALocalModule(t *testing.T) {
	t.Parallel()

	f := catalogueHandler(t, nil, grants{testdata.Eins, []string{"LECTURER"}})

	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			refusal := c.Do(t, createLocalMutation,
				localInput(storetest.FixtureProgrammeA, "Eigene LV", "MODULE"))
			if code := errorCode(t, refusal); code != "NOT_YOUR_PROGRAMME" {
				t.Errorf("a lecturer entering a course gave %s, want NOT_YOUR_PROGRAMME", code)
			}
		})
}

// Two clicks on the button are one row, and the second says which repair it needs.
func TestASecondLocalModuleOfTheSameNameSaysWhy(t *testing.T) {
	t.Parallel()

	f := catalogueHandler(t,
		map[string][]string{testdata.Vier.Mail: {storetest.FixtureProgrammeA}},
		grants{testdata.Vier, []string{"PROGRAMME_LEAD"}})

	c := graphqltest.New(f.handler).AsUser(testdata.Vier.Mail)
	input := localInput(storetest.FixtureProgrammeA, "Eigene Lehrveranstaltung", "MODULE")

	c.MustQuery(t, createLocalMutation, input, nil)
	if code := errorCode(t, c.Do(t, createLocalMutation, input)); code != "MODULE_NAME_TAKEN" {
		t.Errorf("the second course of the same name gave %s, want MODULE_NAME_TAKEN", code)
	}
}

const programmesQuery = `query($all: Boolean!) {
	programmes(includeUnplanned: $all) { code planningStatus }
}`

const setStatusMutation = `mutation($code: String!, $status: ProgrammeStatus!) {
	setProgrammePlanningStatus(code: $code, status: $status) { code planningStatus }
}`

type programmeList struct {
	Programmes []struct {
		Code           string
		PlanningStatus string
	}
}

func codesOf(list programmeList) []string {
	out := make([]string, 0, len(list.Programmes))
	for _, p := range list.Programmes {
		out = append(out, p.Code)
	}
	return out
}

// markUnplanned states what the API has no mutation for at this point in a test: that this
// faculty does not plan a programme.
func markUnplanned(t *testing.T, f catalogueFixture, code string, status string) {
	t.Helper()

	if _, err := f.schema.Pool.Exec(t.Context(),
		`UPDATE programme SET planning_status = $2 WHERE code = $1`, code, status); err != nil {
		t.Fatalf("cannot mark %s as %s: %v", code, status, err)
	}
}

// The catalogue holds every programme the examination office's regulations mention. A picker
// built from that list would ask somebody to plan a programme nobody here runs, so the list
// leaves them out — and says so when asked for all of them.
func TestProgrammesLeaveOutTheOnesTheFacultyDoesNotPlan(t *testing.T) {
	t.Parallel()

	f := catalogueHandler(t, nil, grants{testdata.Eins, []string{"LECTURER"}})
	markUnplanned(t, f, storetest.FixtureProgrammeZ, "NOT_OURS")

	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var planned programmeList
			c.MustQuery(t, programmesQuery, map[string]any{"all": false}, &planned)
			if slices.Contains(codesOf(planned), storetest.FixtureProgrammeZ) {
				t.Errorf("a programme the faculty does not plan is offered: %v", codesOf(planned))
			}
			if !slices.Contains(codesOf(planned), storetest.FixtureProgrammeA) {
				t.Errorf("the planned programmes are missing from %v", codesOf(planned))
			}

			var all programmeList
			c.MustQuery(t, programmesQuery, map[string]any{"all": true}, &all)
			if !slices.Contains(codesOf(all), storetest.FixtureProgrammeZ) {
				t.Errorf("includeUnplanned left it out anyway: %v", codesOf(all))
			}
			for _, p := range all.Programmes {
				if p.Code == storetest.FixtureProgrammeZ && p.PlanningStatus != "NOT_OURS" {
					t.Errorf("its status reads %q, want NOT_OURS", p.PlanningStatus)
				}
			}
		})
}

// Saying which programmes the faculty plans is the dean's office's, and it reaches through both
// doors: a programme running out is reversible, ordinary process work.
func TestSettingThePlanningStatusIsTheDeansOfficeThroughBothDoors(t *testing.T) {
	t.Parallel()

	f := catalogueHandler(t, nil, grants{testdata.Fuenf, []string{"DEANS_OFFICE"}})

	graphqltest.EachDoor(t, f.handler, testdata.Fuenf.Mail, testdata.Fuenf.Token,
		func(t *testing.T, c *graphqltest.Client) {
			// A programme per door: the two runs share a database, and the second would otherwise
			// assert about what the first one left behind.
			code := storetest.FixtureProgrammeB
			if c.Door() == graphqltest.Token {
				code = storetest.FixtureProgrammeR
			}

			var out struct {
				SetProgrammePlanningStatus struct {
					Code           string
					PlanningStatus string
				}
			}
			c.MustQuery(t, setStatusMutation,
				map[string]any{"code": code, "status": "DISCONTINUED"}, &out)

			if out.SetProgrammePlanningStatus.PlanningStatus != "DISCONTINUED" {
				t.Errorf("the status reads %q, want DISCONTINUED",
					out.SetProgrammePlanningStatus.PlanningStatus)
			}

			// And it is gone from the list every picker asks for.
			var planned programmeList
			c.MustQuery(t, programmesQuery, map[string]any{"all": false}, &planned)
			if slices.Contains(codesOf(planned), code) {
				t.Errorf("%s still stands in the picker's list: %v", code, codesOf(planned))
			}

			// Both directions: a programme that starts is marked planned again.
			c.MustQuery(t, setStatusMutation,
				map[string]any{"code": code, "status": "PLANNED"}, &out)
			if out.SetProgrammePlanningStatus.PlanningStatus != "PLANNED" {
				t.Errorf("it did not come back: %q", out.SetProgrammePlanningStatus.PlanningStatus)
			}
		})
}

// A programme lead runs one programme; deciding which ones the faculty plans is not theirs, and
// neither is it an administrator's — running the installation is a different job from planning.
func TestWhoMayNotSetThePlanningStatus(t *testing.T) {
	t.Parallel()

	f := catalogueHandler(t, nil,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Vier, []string{"PROGRAMME_LEAD"}},
		grants{testdata.Sechs, []string{"ADMIN"}})

	for _, who := range []testdata.Persona{testdata.Eins, testdata.Vier, testdata.Sechs} {
		refusal := graphqltest.New(f.handler).AsUser(who.Mail).
			Do(t, setStatusMutation,
				map[string]any{"code": storetest.FixtureProgrammeB, "status": "NOT_OURS"})
		if code := errorCode(t, refusal); code != "FORBIDDEN" {
			t.Errorf("%s setting the planning status gave %s, want FORBIDDEN", who.Name, code)
		}
	}
}

// A code that names no programme, and a status this build does not know.
func TestThePlanningStatusRefusesWhatItCannotRecord(t *testing.T) {
	t.Parallel()

	f := catalogueHandler(t, nil, grants{testdata.Fuenf, []string{"DEANS_OFFICE"}})
	c := graphqltest.New(f.handler).AsUser(testdata.Fuenf.Mail)

	refusal := c.Do(t, setStatusMutation, map[string]any{"code": "GIBTESNICHT", "status": "PLANNED"})
	if code := errorCode(t, refusal); code != "PROGRAMME_NOT_FOUND" {
		t.Errorf("an unknown programme gave %s, want PROGRAMME_NOT_FOUND", code)
	}
}

// Leading a programme this faculty does not plan is a grant that could never be used, so it is
// refused where it would be given rather than where it would be exercised.
func TestAProgrammeTheFacultyDoesNotPlanCannotBeAssigned(t *testing.T) {
	t.Parallel()

	f := catalogueHandler(t, nil,
		grants{testdata.Sechs, []string{"ADMIN"}},
		grants{testdata.Vier, []string{"PROGRAMME_LEAD"}})
	markUnplanned(t, f, storetest.FixtureProgrammeB, "DISCONTINUED")

	admin := graphqltest.New(f.handler).AsUser(testdata.Sechs.Mail)

	var people struct {
		People []struct {
			ID   string
			Mail string
		}
	}
	admin.MustQuery(t, `{ people { id mail } }`, nil, &people)
	var vier string
	for _, p := range people.People {
		if p.Mail == testdata.Vier.Mail {
			vier = p.ID
		}
	}

	refusal := admin.Do(t, `mutation($id: ID!, $p: [String!]!) {
		setPersonProgrammes(id: $id, programmes: $p) { id }
	}`, map[string]any{"id": vier, "p": []string{storetest.FixtureProgrammeB}})

	if code := errorCode(t, refusal); code != "PROGRAMME_NOT_PLANNED" {
		t.Errorf("assigning a programme that has run out gave %s, want PROGRAMME_NOT_PLANNED",
			code)
	}
}
