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
