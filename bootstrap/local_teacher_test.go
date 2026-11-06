package bootstrap_test

import (
	"net/http"
	"testing"

	"github.com/obcode/tallox.go/bootstrap"
	"github.com/obcode/tallox.go/internal/auth"
	"github.com/obcode/tallox.go/internal/buildinfo"
	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/graphqltest"
	"github.com/obcode/tallox.go/internal/store"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// Teachers entered by hand, through the assembled handler.
//
// Both mutations are @fullReach: the refusal of a taken address says whether somebody has an
// account. So the browser and a full-reach token get the same answer, and a plain token none.

const createLocalTeacherMutation = `mutation($in: LocalTeacherInput!) {
	createLocalTeacher(input: $in) { id name sortName mail isLecturerOnContract enteredByHand isUser }
}`

const changeLocalTeacherMutation = `mutation($id: ID!, $in: LocalTeacherInput!) {
	changeLocalTeacher(id: $id, input: $in) { id name active enteredByHand }
}`

type createdLocalTeacher struct {
	CreateLocalTeacher struct {
		ID                   string
		Name                 string
		SortName             string
		Mail                 string
		IsLecturerOnContract bool
		EnteredByHand        bool
		IsUser               bool
	}
}

type localTeacherFixture struct {
	handler http.Handler
	schema  *storetest.Schema
}

func localTeacherHandler(t *testing.T, people ...grants) localTeacherFixture {
	t.Helper()

	s := storetest.New(t)
	for _, p := range people {
		storetest.SeedPerson(t, s, p.who, p.roles...)
		parsed, err := auth.ParseToken(p.who.Token)
		if err != nil {
			t.Fatalf("fixture token of %s does not parse: %v", p.who.Name, err)
		}
		storetest.SeedToken(t, s, p.who, auth.HashSecret(parsed.Secret), storetest.TokenOptions{
			Description: "local teacher test",
		})
	}
	storetest.SeedZPACatalogue(t, s)
	if _, err := store.NewCatalogue(s.Pool).Project(t.Context(), nil); err != nil {
		t.Fatalf("cannot project the catalogue: %v", err)
	}

	return localTeacherFixture{
		schema: s,
		handler: bootstrap.Handler(bootstrap.Options{
			Build: buildinfo.Info{Version: "test"},
			Auth: auth.Config{
				Mode:   auth.ModeProxy,
				Users:  store.NewDirectory(s.Pool),
				Tokens: store.NewDirectory(s.Pool),
			},
			Catalogue: domain.NewCatalogueService(store.NewModules(s.Pool), domain.ModuleHandbook{}),
		}),
	}
}

func localTeacherInput(surname, mail string) map[string]any {
	return map[string]any{"in": map[string]any{
		"surname":   surname,
		"givenName": "Lehrauftrag",
		"mail":      mail,
	}}
}

func TestASubjectGroupLeadEntersATeacherThroughEveryDoorWithFullReach(t *testing.T) {
	t.Parallel()

	f := localTeacherHandler(t, grants{testdata.Vier, []string{"SUBJECT_GROUP_LEAD"}})
	group := seedGroup(t, f.schema, "SWE", "Softwarefächer")
	if _, err := f.schema.Pool.Exec(t.Context(), `INSERT INTO person_subject_group_scope
		(person_id, role, subject_group_id) VALUES ($1, 'SUBJECT_GROUP_LEAD', $2)`,
		testdata.Vier.ID(), group); err != nil {
		t.Fatal(err)
	}

	doors := []struct {
		name string
		c    *graphqltest.Client
	}{
		{"browser", graphqltest.New(f.handler).AsUser(testdata.Vier.Mail).On(graphqltest.Browser)},
		{"full reach", graphqltest.New(f.handler).
			WithToken(fullReachToken(t, f.schema, testdata.Vier)).On(graphqltest.Token)},
	}
	for _, door := range doors {
		t.Run(door.name, func(t *testing.T) {
			// A surname and address per door: the runs share a database.
			mail := "lb." + map[string]string{"browser": "neun", "full reach": "zehn"}[door.name] +
				"@example.org"
			var out createdLocalTeacher
			door.c.MustQuery(t, createLocalTeacherMutation, localTeacherInput("Neu", mail), &out)
			got := out.CreateLocalTeacher
			if !got.EnteredByHand || !got.IsLecturerOnContract || got.IsUser ||
				got.Name != "Lehrauftrag Neu" || got.SortName != "Neu, Lehrauftrag" || got.Mail != mail {
				t.Errorf("created = %+v", got)
			}

			// A second click is the same human twice.
			resp := door.c.Do(t, createLocalTeacherMutation, localTeacherInput("Neu", mail))
			assertRefusal(t, resp, "TEACHER_MAIL_TAKEN")
			for _, m := range resp.Messages() {
				graphqltest.AssertNoLeak(t, m, graphqltest.DatabaseNoise()...)
			}

			vars := localTeacherInput("Neu", mail)
			vars["id"] = got.ID
			vars["in"].(map[string]any)["active"] = false
			var changed struct {
				ChangeLocalTeacher struct {
					Active        bool
					EnteredByHand bool
				}
			}
			door.c.MustQuery(t, changeLocalTeacherMutation, vars, &changed)
			if changed.ChangeLocalTeacher.Active || !changed.ChangeLocalTeacher.EnteredByHand {
				t.Errorf("changed = %+v, want inactive and still entered by hand",
					changed.ChangeLocalTeacher)
			}
		})
	}

	// A plain token gets neither the mutation nor its refusals.
	plain := graphqltest.New(f.handler).WithToken(testdata.Vier.Token).On(graphqltest.Token)
	assertRefusal(t, plain.Do(t, createLocalTeacherMutation,
		localTeacherInput("Neu", "lb.elf@example.org")), "FULL_REACH_REQUIRED")
	assertRefusal(t, plain.Do(t, createLocalTeacherMutation,
		localTeacherInput("Neu", "lb.neun@example.org")), "FULL_REACH_REQUIRED")
}

// The account an address belongs to is said only as "taken", the same as a teacher's.
func TestAnAddressWithAnAccountIsTakenLikeATeachers(t *testing.T) {
	t.Parallel()

	f := localTeacherHandler(t,
		grants{testdata.Zwei, []string{"DEANS_OFFICE"}},
		grants{testdata.Eins, []string{"LECTURER"}})
	dean := graphqltest.New(f.handler).AsUser(testdata.Zwei.Mail).On(graphqltest.Browser)

	for _, mail := range []string{testdata.Eins.Mail, "prof.sieben@example.org"} {
		resp := dean.Do(t, createLocalTeacherMutation, localTeacherInput("Neu", mail))
		assertRefusal(t, resp, "TEACHER_MAIL_TAKEN")
		for _, m := range resp.Messages() {
			graphqltest.AssertNoLeak(t, m, graphqltest.DatabaseNoise()...)
		}
	}

	// An imported teacher is not this mutation's to edit.
	var id string
	if err := f.schema.Pool.QueryRow(t.Context(), `SELECT id FROM teacher WHERE zpa_teacher_ref = $1`,
		storetest.FixtureTeacherNotAdmitted).Scan(&id); err != nil {
		t.Fatal(err)
	}
	vars := localTeacherInput("Sieben", "prof.sieben@example.org")
	vars["id"] = id
	assertRefusal(t, dean.Do(t, changeLocalTeacherMutation, vars), "TEACHER_NOT_FOUND")

	// Nothing plausible, nothing stored.
	assertRefusal(t, dean.Do(t, createLocalTeacherMutation, localTeacherInput("", "x@example.org")),
		"LOCAL_TEACHER_INVALID")
	assertRefusal(t, dean.Do(t, createLocalTeacherMutation, localTeacherInput("Neu", "kein-at")),
		"LOCAL_TEACHER_INVALID")
}

// A programme lead with a programme enters one too: it changes the assignment in the review and
// meets the same missing name. Browser and full-reach token alike.
func TestAProgrammeLeadEntersATeacher(t *testing.T) {
	t.Parallel()

	f := localTeacherHandler(t, grants{testdata.Vier, []string{"PROGRAMME_LEAD"}})
	if _, err := f.schema.Pool.Exec(t.Context(), `INSERT INTO person_programme_scope
		(person_id, role, programme_id) SELECT $1, 'PROGRAMME_LEAD', id FROM programme LIMIT 1`,
		testdata.Vier.ID()); err != nil {
		t.Fatal(err)
	}

	browser := graphqltest.New(f.handler).AsUser(testdata.Vier.Mail).On(graphqltest.Browser)
	fullReach := graphqltest.New(f.handler).
		WithToken(fullReachToken(t, f.schema, testdata.Vier)).On(graphqltest.Token)
	for mail, c := range map[string]*graphqltest.Client{
		"lb.zwoelf@example.org":   browser,
		"lb.dreizehn@example.org": fullReach,
	} {
		var out createdLocalTeacher
		c.MustQuery(t, createLocalTeacherMutation, localTeacherInput("Neu", mail), &out)
		if !out.CreateLocalTeacher.EnteredByHand || out.CreateLocalTeacher.Mail != mail {
			t.Errorf("created = %+v", out.CreateLocalTeacher)
		}
	}

	plain := graphqltest.New(f.handler).WithToken(testdata.Vier.Token).On(graphqltest.Token)
	assertRefusal(t, plain.Do(t, createLocalTeacherMutation,
		localTeacherInput("Neu", "lb.vierzehn@example.org")), "FULL_REACH_REQUIRED")
}

// Who may not: a lecturer, a lead without a group, a programme lead without a programme — in the
// browser, so the refusal is the policy's and not the directive's.
func TestOnlyLeadsAndTheDeansOfficeEnterTeachers(t *testing.T) {
	t.Parallel()

	f := localTeacherHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Drei, []string{"SUBJECT_GROUP_LEAD"}},
		grants{testdata.Vier, []string{"PROGRAMME_LEAD"}})

	for _, who := range []testdata.Persona{testdata.Eins, testdata.Drei, testdata.Vier} {
		c := graphqltest.New(f.handler).AsUser(who.Mail).On(graphqltest.Browser)
		assertRefusal(t, c.Do(t, createLocalTeacherMutation,
			localTeacherInput("Neu", "lb.neun@example.org")), "TEACHER_ENTRY_REFUSED")
	}
}
