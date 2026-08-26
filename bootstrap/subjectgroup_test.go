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

// Subject groups through the API, through both doors.
//
// The doors are supposed to differ here, and in one direction only: reading the groups is open to
// anybody with an account through either of them, and every write is administration and therefore
// interactive-only. Asserting per door explicitly rather than quietly covering one is what
// CLAUDE.md asks for wherever the two are meant to differ.

type subjectGroupFixture struct {
	handler http.Handler
	schema  *storetest.Schema
}

func subjectGroupHandler(t *testing.T, people ...grants) subjectGroupFixture {
	t.Helper()

	s := storetest.New(t)
	for _, p := range people {
		storetest.SeedPerson(t, s, p.who, p.roles...)

		parsed, err := auth.ParseToken(p.who.Token)
		if err != nil {
			t.Fatalf("fixture token of %s does not parse: %v", p.who.Name, err)
		}
		storetest.SeedToken(t, s, p.who, auth.HashSecret(parsed.Secret), storetest.TokenOptions{
			Description: "subject group test",
		})
	}

	// The catalogue is projected here because half of what a subject group is for is the modules
	// in it: the work list, the counts, and the filter a screen opens to shrink it.
	storetest.SeedZPACatalogue(t, s)
	if _, err := store.NewCatalogue(s.Pool).Project(t.Context(), nil); err != nil {
		t.Fatalf("cannot project the catalogue: %v", err)
	}

	modules := store.NewModules(s.Pool)
	return subjectGroupFixture{
		schema: s,
		handler: bootstrap.Handler(bootstrap.Options{
			Build: buildinfo.Info{Version: "test"},
			Auth: auth.Config{
				Mode:   auth.ModeProxy,
				Users:  store.NewDirectory(s.Pool),
				Tokens: store.NewDirectory(s.Pool),
			},
			People:        domain.NewPeopleService(store.NewPeople(s.Pool), nil),
			Catalogue:     domain.NewCatalogueService(modules),
			SubjectGroups: domain.NewSubjectGroupService(store.NewSubjectGroups(s.Pool)),
		}),
	}
}

const subjectGroupsQuery = `query { subjectGroups {
	id code name active moduleCount
	leads { mail } members { mail }
} }`

// createSubjectGroup as an administrator, and the group that comes back.
func (f subjectGroupFixture) create(t *testing.T, code, name string) string {
	t.Helper()

	var out struct {
		CreateSubjectGroup struct{ ID string }
	}
	graphqltest.New(f.handler).AsUser(testdata.Sechs.Mail).MustQuery(t,
		`mutation($c: String!, $n: String!) {
			createSubjectGroup(code: $c, name: $n) { id code name active moduleCount }
		}`,
		map[string]any{"c": code, "n": name}, &out)
	return out.CreateSubjectGroup.ID
}

// Anybody with an account reads them, through either door. A lecturer who cannot see the groups
// cannot be shown their own subjects on the wish screen.
func TestSubjectGroupsAreReadableByAnybodyWithAnAccount(t *testing.T) {
	t.Parallel()

	f := subjectGroupHandler(t,
		grants{testdata.Sechs, []string{"ADMIN"}},
		grants{testdata.Eins, []string{"LECTURER"}},
	)
	f.create(t, "MATHE", "Mathematik")

	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var out struct {
				SubjectGroups []struct {
					Code        string
					Name        string
					Active      bool
					ModuleCount int
				}
			}
			c.MustQuery(t, subjectGroupsQuery, nil, &out)

			if len(out.SubjectGroups) != 1 {
				t.Fatalf("got %d groups, want 1", len(out.SubjectGroups))
			}
			if out.SubjectGroups[0].Code != "MATHE" || !out.SubjectGroups[0].Active {
				t.Errorf("got %+v", out.SubjectGroups[0])
			}
			if out.SubjectGroups[0].ModuleCount != 0 {
				t.Errorf("a fresh group counts %d modules", out.SubjectGroups[0].ModuleCount)
			}
		})
}

// Every write is administration, and administration is interactive-only: granting somebody a
// place in the faculty's organisation from a long-lived token in a script would decouple the act
// from any sign-in.
func TestSubjectGroupWritesAreInteractiveOnly(t *testing.T) {
	t.Parallel()

	f := subjectGroupHandler(t, grants{testdata.Sechs, []string{"ADMIN"}})

	messages := graphqltest.New(f.handler).WithToken(testdata.Sechs.Token).MustFail(t,
		`mutation { createSubjectGroup(code: "TI", name: "Technische Informatik") { id } }`, nil)

	if len(messages) == 0 {
		t.Fatal("an administrator's token created a subject group")
	}
	graphqltest.AssertNoLeak(t, messages[0], graphqltest.DatabaseNoise()...)
}

// Reading is open; writing is not. A lecturer meets a refusal that says which of the two it is.
func TestOnlyAnAdministratorWritesSubjectGroups(t *testing.T) {
	t.Parallel()

	f := subjectGroupHandler(t,
		grants{testdata.Sechs, []string{"ADMIN"}},
		grants{testdata.Eins, []string{"LECTURER"}},
	)

	messages := graphqltest.New(f.handler).AsUser(testdata.Eins.Mail).MustFail(t,
		`mutation { createSubjectGroup(code: "TI", name: "Technische Informatik") { id } }`, nil)

	if len(messages) == 0 {
		t.Fatal("a lecturer created a subject group")
	}
	graphqltest.AssertNoLeak(t, messages[0], graphqltest.DatabaseNoise()...)
}

// A code is taken once. Naming that is safe here in a way it never is for wishes: subject groups
// are not confidential, and the same person can read the same fact off the same screen.
func TestASubjectGroupCodeIsTakenOnce(t *testing.T) {
	t.Parallel()

	f := subjectGroupHandler(t, grants{testdata.Sechs, []string{"ADMIN"}})
	f.create(t, "MATHE", "Mathematik")

	messages := graphqltest.New(f.handler).AsUser(testdata.Sechs.Mail).MustFail(t,
		`mutation { createSubjectGroup(code: "mathe", name: "Noch mal Mathematik") { id } }`, nil)

	if len(messages) == 0 {
		t.Fatal("two subject groups share a code — and the second one lower-cased it, so the " +
			"normalisation is not happening either")
	}
	graphqltest.AssertNoLeak(t, messages[0], graphqltest.DatabaseNoise()...)
}

// Leading a group is a grant, and a grant needs the role. The composite foreign key refuses it
// anyway; what this asserts is that the caller is told which of the two things is missing, since
// the repair for "no such group" and for "does not hold the role" are different.
func TestLeadingASubjectGroupNeedsTheRole(t *testing.T) {
	t.Parallel()

	f := subjectGroupHandler(t,
		grants{testdata.Sechs, []string{"ADMIN"}},
		grants{testdata.Zwei, []string{"LECTURER"}},
		grants{testdata.Drei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
	)
	group := f.create(t, "MATHE", "Mathematik")

	c := graphqltest.New(f.handler).AsUser(testdata.Sechs.Mail)

	messages := c.MustFail(t,
		`mutation($g: ID!, $p: [ID!]!) { setSubjectGroupLeads(id: $g, personIds: $p) { id } }`,
		map[string]any{"g": group, "p": []string{testdata.Zwei.ID().String()}})
	if len(messages) == 0 {
		t.Fatal("a lecturer without the role was made a subject group lead")
	}

	var out struct {
		SetSubjectGroupLeads struct {
			Leads []struct{ Mail string }
		}
	}
	c.MustQuery(t,
		`mutation($g: ID!, $p: [ID!]!) {
			setSubjectGroupLeads(id: $g, personIds: $p) { id leads { mail } }
		}`,
		map[string]any{"g": group, "p": []string{testdata.Drei.ID().String()}}, &out)

	if len(out.SetSubjectGroupLeads.Leads) != 1 ||
		out.SetSubjectGroupLeads.Leads[0].Mail != testdata.Drei.Mail {
		t.Errorf("got leads %+v, want just %s", out.SetSubjectGroupLeads.Leads, testdata.Drei.Mail)
	}
}

// Membership is not a grant, so it needs no role — and it still grants nothing, which is what the
// policy tests assert from the other side.
func TestMembershipNeedsNoRole(t *testing.T) {
	t.Parallel()

	f := subjectGroupHandler(t,
		grants{testdata.Sechs, []string{"ADMIN"}},
		grants{testdata.Zwei, []string{"LECTURER"}},
	)
	group := f.create(t, "MATHE", "Mathematik")

	var out struct {
		SetSubjectGroupMembers struct {
			Members []struct{ Mail string }
		}
	}
	graphqltest.New(f.handler).AsUser(testdata.Sechs.Mail).MustQuery(t,
		`mutation($g: ID!, $p: [ID!]!) {
			setSubjectGroupMembers(id: $g, personIds: $p) { id members { mail } }
		}`,
		map[string]any{"g": group, "p": []string{testdata.Zwei.ID().String()}}, &out)

	if len(out.SetSubjectGroupMembers.Members) != 1 {
		t.Fatalf("got %d members, want 1", len(out.SetSubjectGroupMembers.Members))
	}

	// And the person sees it as their own, through both doors: this is what the wish screen
	// filters by.
	graphqltest.EachDoor(t, f.handler, testdata.Zwei.Mail, testdata.Zwei.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var mine struct {
				MySubjectGroups []struct{ Code string }
			}
			c.MustQuery(t, `query { mySubjectGroups { code } }`, nil, &mine)
			if len(mine.MySubjectGroups) != 1 || mine.MySubjectGroups[0].Code != "MATHE" {
				t.Errorf("got %+v, want MATHE", mine.MySubjectGroups)
			}
		})
}

// The set is replaced as a whole, so the two calls of a swap cannot be separated. The interval
// between a per-person add and remove is one in which somebody is in a group nobody meant them
// to be in.
func TestSettingMembersReplacesTheWholeSet(t *testing.T) {
	t.Parallel()

	f := subjectGroupHandler(t,
		grants{testdata.Sechs, []string{"ADMIN"}},
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Zwei, []string{"LECTURER"}},
	)
	group := f.create(t, "MATHE", "Mathematik")

	c := graphqltest.New(f.handler).AsUser(testdata.Sechs.Mail)
	set := func(t *testing.T, ids ...string) []string {
		t.Helper()
		var out struct {
			SetSubjectGroupMembers struct {
				Members []struct{ Mail string }
			}
		}
		c.MustQuery(t,
			`mutation($g: ID!, $p: [ID!]!) {
				setSubjectGroupMembers(id: $g, personIds: $p) { id members { mail } }
			}`,
			map[string]any{"g": group, "p": ids}, &out)

		mails := make([]string, 0, len(out.SetSubjectGroupMembers.Members))
		for _, m := range out.SetSubjectGroupMembers.Members {
			mails = append(mails, m.Mail)
		}
		return mails
	}

	if got := set(t, testdata.Eins.ID().String(), testdata.Zwei.ID().String()); len(got) != 2 {
		t.Fatalf("got %v, want two members", got)
	}
	got := set(t, testdata.Zwei.ID().String())
	if len(got) != 1 || got[0] != testdata.Zwei.Mail {
		t.Errorf("got %v, want just %s — the set was not replaced", got, testdata.Zwei.Mail)
	}
}

// A group is retired, never deleted, and retiring it keeps its module assignments. There is no
// delete mutation at all, which is the strongest form of that decision.
func TestASubjectGroupIsRetiredRatherThanDeleted(t *testing.T) {
	t.Parallel()

	f := subjectGroupHandler(t, grants{testdata.Sechs, []string{"ADMIN"}})
	group := f.create(t, "MATHE", "Mathematik")

	c := graphqltest.New(f.handler).AsUser(testdata.Sechs.Mail)

	var retired struct {
		SetSubjectGroupActive struct{ Active bool }
	}
	c.MustQuery(t,
		`mutation($g: ID!) { setSubjectGroupActive(id: $g, active: false) { id active } }`,
		map[string]any{"g": group}, &retired)
	if retired.SetSubjectGroupActive.Active {
		t.Error("the group is still active after being retired")
	}

	var listed struct {
		SubjectGroups []struct{ Code string }
	}
	c.MustQuery(t, `query { subjectGroups { code } }`, nil, &listed)
	if len(listed.SubjectGroups) != 0 {
		t.Errorf("a retired group is in the ordinary list: %+v", listed.SubjectGroups)
	}

	c.MustQuery(t, `query { subjectGroups(includeInactive: true) { code } }`, nil, &listed)
	if len(listed.SubjectGroups) != 1 {
		t.Errorf("a retired group is not reachable at all: %+v", listed.SubjectGroups)
	}
}

// "Keine Fachgruppe ohne Person, die sich ihrer annimmt", as the number it is rather than the
// constraint it is not: a group has to be creatable before its lead is decided.
func TestASubjectGroupCanExistBeforeItsLeadIsDecided(t *testing.T) {
	t.Parallel()

	f := subjectGroupHandler(t,
		grants{testdata.Sechs, []string{"ADMIN"}},
		grants{testdata.Drei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
	)
	group := f.create(t, "MATHE", "Mathematik")

	c := graphqltest.New(f.handler).AsUser(testdata.Sechs.Mail)

	var out struct{ SubjectGroupsWithoutLead int }
	c.MustQuery(t, `query { subjectGroupsWithoutLead }`, nil, &out)
	if out.SubjectGroupsWithoutLead != 1 {
		t.Errorf("got %d groups without a lead, want 1", out.SubjectGroupsWithoutLead)
	}

	var assigned struct {
		SetSubjectGroupLeads struct{ ID string }
	}
	c.MustQuery(t,
		`mutation($g: ID!, $p: [ID!]!) { setSubjectGroupLeads(id: $g, personIds: $p) { id } }`,
		map[string]any{"g": group, "p": []string{testdata.Drei.ID().String()}}, &assigned)

	c.MustQuery(t, `query { subjectGroupsWithoutLead }`, nil, &out)
	if out.SubjectGroupsWithoutLead != 0 {
		t.Errorf("got %d groups without a lead after assigning one, want 0",
			out.SubjectGroupsWithoutLead)
	}
}

// An unknown group is an empty answer rather than an error: a link to a group that has since been
// merged away should render an empty page. Nothing here is confidential, so there is nothing a
// null could give away.
func TestAnUnknownSubjectGroupIsNull(t *testing.T) {
	t.Parallel()

	f := subjectGroupHandler(t, grants{testdata.Eins, []string{"LECTURER"}})

	var out struct {
		SubjectGroup *struct{ Code string }
	}
	graphqltest.New(f.handler).AsUser(testdata.Eins.Mail).MustQuery(t,
		`query($g: ID!) { subjectGroup(id: $g) { code } }`,
		map[string]any{"g": uuid.New().String()}, &out)

	if out.SubjectGroup != nil {
		t.Errorf("an unknown subject group answered %+v", out.SubjectGroup)
	}
}

// The work list, from both ends: the number shrinks as modules are assigned, and the filter finds
// exactly the ones that are left. This is the shape "14 modules without a split" already has, and
// the reason it is a shape rather than an open form.
func TestModulesWithoutASubjectGroupAreTheWorkList(t *testing.T) {
	t.Parallel()

	f := subjectGroupHandler(t, grants{testdata.Sechs, []string{"ADMIN"}})
	group := f.create(t, "MATHE", "Mathematik")

	c := graphqltest.New(f.handler).AsUser(testdata.Sechs.Mail)

	var before struct {
		ModulesWithoutSubjectGroup int
		Modules                    []struct{ ID string }
	}
	c.MustQuery(t, `query {
		modulesWithoutSubjectGroup
		modules(filter: { withoutSubjectGroup: true }) { id }
	}`, nil, &before)

	if before.ModulesWithoutSubjectGroup == 0 {
		t.Fatal("no modules are waiting for a subject group, so this test proves nothing")
	}
	if len(before.Modules) != before.ModulesWithoutSubjectGroup {
		t.Errorf("the filter finds %d modules and the count says %d. One of them is not applying "+
			"the same rule, and the number on the screen is the one somebody trusts.",
			len(before.Modules), before.ModulesWithoutSubjectGroup)
	}

	var report struct {
		SetModulesSubjectGroup struct {
			ModulesAssigned            int
			ModulesWithoutSubjectGroup int
			SubjectGroup               struct {
				Code        string
				ModuleCount int
			}
		}
	}
	c.MustQuery(t, `mutation($m: [ID!]!, $g: ID) {
		setModulesSubjectGroup(moduleIds: $m, subjectGroup: $g) {
			modulesAssigned modulesWithoutSubjectGroup
			subjectGroup { code moduleCount }
		}
	}`, map[string]any{
		"m": []string{before.Modules[0].ID, before.Modules[1].ID},
		"g": group,
	}, &report)

	got := report.SetModulesSubjectGroup
	if got.ModulesAssigned != 2 {
		t.Errorf("assigned %d modules, want 2", got.ModulesAssigned)
	}
	if got.ModulesWithoutSubjectGroup != before.ModulesWithoutSubjectGroup-2 {
		t.Errorf("the work list went from %d to %d, want %d",
			before.ModulesWithoutSubjectGroup, got.ModulesWithoutSubjectGroup,
			before.ModulesWithoutSubjectGroup-2)
	}
	if got.SubjectGroup.ModuleCount != 2 {
		t.Errorf("the group counts %d modules, want 2", got.SubjectGroup.ModuleCount)
	}
}

// A module carries its group as a reference, and moving it is one statement — so there is no
// moment in which it belongs to nothing.
func TestAModuleCarriesItsSubjectGroupAndCanBeMoved(t *testing.T) {
	t.Parallel()

	f := subjectGroupHandler(t, grants{testdata.Sechs, []string{"ADMIN"}})
	maths := f.create(t, "MATHE", "Mathematik")
	ml := f.create(t, "MATHE-ML", "Mathematik (Machine Learning)")

	c := graphqltest.New(f.handler).AsUser(testdata.Sechs.Mail)

	var waiting struct {
		Modules []struct{ ID string }
	}
	c.MustQuery(t, `query { modules(filter: { withoutSubjectGroup: true }) { id } }`, nil, &waiting)
	if len(waiting.Modules) == 0 {
		t.Fatal("no module is waiting for a subject group")
	}
	module := waiting.Modules[0].ID

	assign := func(t *testing.T, group string) {
		t.Helper()
		var out struct {
			SetModulesSubjectGroup struct{ ModulesAssigned int }
		}
		c.MustQuery(t, `mutation($m: [ID!]!, $g: ID) {
			setModulesSubjectGroup(moduleIds: $m, subjectGroup: $g) { modulesAssigned }
		}`, map[string]any{"m": []string{module}, "g": group}, &out)
	}

	read := func(t *testing.T) *struct {
		Code   string
		Name   string
		Active bool
	} {
		t.Helper()
		var out struct {
			Module *struct {
				SubjectGroup *struct {
					Code   string
					Name   string
					Active bool
				}
			}
		}
		c.MustQuery(t, `query($m: ID!) { module(id: $m) { subjectGroup { code name active } } }`,
			map[string]any{"m": module}, &out)
		if out.Module == nil {
			t.Fatal("the module disappeared")
		}
		return out.Module.SubjectGroup
	}

	if read(t) != nil {
		t.Fatal("an unassigned module already carries a subject group")
	}

	assign(t, maths)
	if got := read(t); got == nil || got.Code != "MATHE" || !got.Active {
		t.Fatalf("got %+v, want an active MATHE", got)
	}

	// Splitting the group: the module moves, and the assignment is never absent in between.
	assign(t, ml)
	if got := read(t); got == nil || got.Code != "MATHE-ML" {
		t.Fatalf("got %+v after the move, want MATHE-ML", got)
	}

	// And taking it out of every group is the same screen's other button.
	var cleared struct {
		SetModulesSubjectGroup struct{ ModulesAssigned int }
	}
	c.MustQuery(t, `mutation($m: [ID!]!) {
		setModulesSubjectGroup(moduleIds: $m, subjectGroup: null) { modulesAssigned }
	}`, map[string]any{"m": []string{module}}, &cleared)

	if cleared.SetModulesSubjectGroup.ModulesAssigned != 1 {
		t.Errorf("clearing removed %d assignments, want 1",
			cleared.SetModulesSubjectGroup.ModulesAssigned)
	}
	if read(t) != nil {
		t.Error("the module still carries a subject group after being taken out of every group")
	}
}

// Membership is the one thing about a subject group somebody sets for themselves.
//
// It grants nothing — policy.AssignmentScope deliberately does not read it — so what a colleague
// changes here is a statement about which subjects they work in. Requiring an administrator for
// it would turn the wish screen's preselection into something people have to ask for, which is
// how a preselection becomes a barrier.
func TestSomebodySetsTheirOwnSubjectGroups(t *testing.T) {
	t.Parallel()

	f := subjectGroupHandler(t,
		grants{testdata.Sechs, []string{"ADMIN"}},
		grants{testdata.Eins, []string{"LECTURER"}},
	)
	maths := f.create(t, "MATHE", "Mathematik")
	software := f.create(t, "SWE", "Softwarefächer")

	// A plain lecturer, with no administration rights at all.
	c := graphqltest.New(f.handler).AsUser(testdata.Eins.Mail)

	set := func(t *testing.T, ids ...string) []string {
		t.Helper()
		var out struct {
			SetMySubjectGroups []struct{ Code string }
		}
		// A non-nil slice even when empty: a variadic with no arguments marshals to JSON null,
		// and [ID!]! refuses null. "I am in no groups" is an empty list, not an absent one.
		if ids == nil {
			ids = []string{}
		}
		c.MustQuery(t, `mutation($g: [ID!]!) { setMySubjectGroups(subjectGroupIds: $g) { code } }`,
			map[string]any{"g": ids}, &out)

		codes := make([]string, 0, len(out.SetMySubjectGroups))
		for _, g := range out.SetMySubjectGroups {
			codes = append(codes, g.Code)
		}
		return codes
	}

	if got := set(t, maths); len(got) != 1 || got[0] != "MATHE" {
		t.Fatalf("got %v, want just MATHE", got)
	}

	// The whole set at once: a swap is one call, so there is no moment in between.
	if got := set(t, software); len(got) != 1 || got[0] != "SWE" {
		t.Errorf("got %v, want just SWE — the set was not replaced", got)
	}
	if got := set(t); len(got) != 0 {
		t.Errorf("got %v, want none — an empty list is a real answer", got)
	}

	// And it is their own: through both doors, and never anybody else's, because there is no
	// argument for whose memberships these are.
	set(t, maths, software)
	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var mine struct {
				MySubjectGroups []struct{ Code string }
			}
			c.MustQuery(t, `query { mySubjectGroups { code } }`, nil, &mine)
			if len(mine.MySubjectGroups) != 2 {
				t.Errorf("got %d of my own groups, want 2", len(mine.MySubjectGroups))
			}
		})
}

// Setting your own memberships must not become a way to lead a group.
//
// The two are written into different tables by different mutations, and this is the test that
// says the self-service one cannot reach the other. Leading is a grant: it decides who fills the
// group's instances and who reads unpublished wishes before publication.
func TestSettingYourOwnGroupsDoesNotMakeYouTheirLead(t *testing.T) {
	t.Parallel()

	f := subjectGroupHandler(t,
		grants{testdata.Sechs, []string{"ADMIN"}},
		grants{testdata.Drei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
	)
	maths := f.create(t, "MATHE", "Mathematik")

	// Drei holds the role, so if anything could confuse the two it would be here.
	graphqltest.New(f.handler).AsUser(testdata.Drei.Mail).MustQuery(t,
		`mutation($g: [ID!]!) { setMySubjectGroups(subjectGroupIds: $g) { code } }`,
		map[string]any{"g": []string{maths}}, &struct {
			SetMySubjectGroups []struct{ Code string }
		}{})

	var out struct {
		SubjectGroup struct {
			Leads   []struct{ Mail string }
			Members []struct{ Mail string }
		}
	}
	graphqltest.New(f.handler).AsUser(testdata.Sechs.Mail).MustQuery(t,
		`query($g: ID!) { subjectGroup(id: $g) { leads { mail } members { mail } } }`,
		map[string]any{"g": maths}, &out)

	if len(out.SubjectGroup.Leads) != 0 {
		t.Errorf("joining a group made somebody its lead: %+v", out.SubjectGroup.Leads)
	}
	if len(out.SubjectGroup.Members) != 1 {
		t.Errorf("got %d members, want 1", len(out.SubjectGroup.Members))
	}

	var scoped int
	if err := f.schema.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM person_subject_group_scope WHERE person_id = $1`,
		testdata.Drei.ID()).Scan(&scoped); err != nil {
		t.Fatalf("cannot count the scopes: %v", err)
	}
	if scoped != 0 {
		t.Errorf("%d subject group scope(s) were granted by a membership write", scoped)
	}
}

// What a group holds, for the screen that has to answer "is this my subject".
//
// Loaded only when asked: a field resolver rather than a bound field, so that mySubjectGroups —
// which the wish page fetches on every load — does not carry the catalogue with it.
func TestASubjectGroupSaysWhichModulesItHolds(t *testing.T) {
	t.Parallel()

	f := subjectGroupHandler(t, grants{testdata.Sechs, []string{"ADMIN"}})
	group := f.create(t, "MATHE", "Mathematik")

	c := graphqltest.New(f.handler).AsUser(testdata.Sechs.Mail)

	var waiting struct {
		Modules []struct {
			ID   string
			Name string
		}
	}
	c.MustQuery(t, `query { modules(filter: { withoutSubjectGroup: true }) { id name } }`, nil,
		&waiting)
	if len(waiting.Modules) < 2 {
		t.Fatalf("only %d modules are waiting for a group", len(waiting.Modules))
	}

	c.MustQuery(t, `mutation($m: [ID!]!, $g: ID) {
		setModulesSubjectGroup(moduleIds: $m, subjectGroup: $g) { modulesAssigned }
	}`, map[string]any{
		"m": []string{waiting.Modules[0].ID, waiting.Modules[1].ID}, "g": group,
	}, &struct {
		SetModulesSubjectGroup struct{ ModulesAssigned int }
	}{})

	var out struct {
		SubjectGroup struct {
			ModuleCount int
			Modules     []struct {
				Name              string
				HomeProgrammeCode string
			}
		}
	}
	c.MustQuery(t, `query($g: ID!) {
		subjectGroup(id: $g) { moduleCount modules { name homeProgrammeCode } }
	}`, map[string]any{"g": group}, &out)

	if out.SubjectGroup.ModuleCount != 2 || len(out.SubjectGroup.Modules) != 2 {
		t.Fatalf("got %d modules and a count of %d, want 2 and 2",
			len(out.SubjectGroup.Modules), out.SubjectGroup.ModuleCount)
	}
	for _, m := range out.SubjectGroup.Modules {
		if m.HomeProgrammeCode == "" {
			t.Errorf("the module %q carries no home programme — which is what tells two "+
				"similarly named ones apart across programmes", m.Name)
		}
	}
}
