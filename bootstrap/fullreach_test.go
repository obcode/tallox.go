package bootstrap_test

import (
	"slices"
	"testing"

	"github.com/obcode/tallox.go/internal/auth"
	"github.com/obcode/tallox.go/internal/graphqltest"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// Tokens minted with full reach, through the assembled handler.
//
// The claim under test is a pairing, and each test asserts both halves of it against the same
// rows: a full-reach token reads what its owner reads in a browser *and* may make the planning
// moves whose refusals answer questions about those rows — while a plain token of the same person
// gets neither. A test that only showed the writes working would pass with the leak the design
// exists to avoid.

// fullReachToken mints and stores a second token for a persona, with full reach, and returns its
// plaintext. The persona's fixture token stays the plain one.
func fullReachToken(t *testing.T, s *storetest.Schema, p testdata.Persona) string {
	t.Helper()

	minted, err := auth.Mint()
	if err != nil {
		t.Fatalf("cannot mint a token: %v", err)
	}
	storetest.SeedToken(t, s, p, minted.SecretHash, storetest.TokenOptions{
		TokenID:     minted.ID,
		Description: "full reach test",
		FullReach:   true,
	})
	return minted.Plaintext
}

// wishFor registers a wish of somebody on the fixture's instance, in the wish phase, and puts the
// semester back into the assignment phase.
func (f assignmentAPIFixture) wishFor(t *testing.T, who testdata.Persona) {
	t.Helper()

	f.setPhase(t, "WISHES")
	var ignored struct{}
	graphqltest.New(f.handler).AsUser(who.Mail).MustQuery(t, setWishMutation,
		map[string]any{"p": f.instance, "prio": "FIRST_CHOICE", "note": nil}, &ignored)
	f.setPhase(t, "ASSIGNMENT")
}

func (f assignmentAPIFixture) setPhase(t *testing.T, phase string) {
	t.Helper()

	if _, err := f.schema.Pool.Exec(t.Context(),
		`UPDATE semester SET phase = $2 WHERE code = $1`, f.semester, phase); err != nil {
		t.Fatalf("cannot switch the semester into %s: %v", phase, err)
	}
}

func wishers(t *testing.T, c *graphqltest.Client, semester string) []string {
	t.Helper()

	var out struct {
		Wishes []struct{ Person struct{ Mail string } }
	}
	c.MustQuery(t, `query($s: String!) { wishes(semester: $s) { person { mail } } }`,
		map[string]any{"s": semester}, &out)

	mails := make([]string, 0, len(out.Wishes))
	for _, w := range out.Wishes {
		mails = append(mails, w.Person.Mail)
	}
	return mails
}

// A subject group lead plans the group's instance through a full-reach token exactly as in the
// browser: reads the unpublished wish, fills the part, and is told the part is taken — while the
// same lead's plain token reads only their own and may not fill anything.
func TestAFullReachTokenFillsWhatTheBrowserFills(t *testing.T) {
	t.Parallel()

	f := assignmentHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Drei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
	)
	f.leadGroup(t, testdata.Drei, f.group)
	f.wishFor(t, testdata.Eins)

	browser := graphqltest.New(f.handler).AsUser(testdata.Drei.Mail).On(graphqltest.Browser)
	plain := graphqltest.New(f.handler).WithToken(testdata.Drei.Token).On(graphqltest.Token)
	full := graphqltest.New(f.handler).WithToken(fullReachToken(t, f.schema, testdata.Drei)).
		On(graphqltest.Token)

	// Reading: the full-reach token is the browser, the plain token is the owner's own.
	if got := wishers(t, browser, f.semester); !slices.Contains(got, testdata.Eins.Mail) {
		t.Fatalf("the lead does not read the wish in the browser: %v — the fixture is wrong", got)
	}
	if got := wishers(t, full, f.semester); !slices.Contains(got, testdata.Eins.Mail) {
		t.Errorf("a full-reach token of the lead reads %v, want the unpublished wish of Eins", got)
	}
	if got := wishers(t, plain, f.semester); slices.Contains(got, testdata.Eins.Mail) {
		t.Errorf("a plain token of the lead reads somebody else's unpublished wish: %v", got)
	}

	fill := map[string]any{
		"p": f.lecture, "who": testdata.Eins.ID().String(), "note": nil, "replacing": nil,
	}

	// Writing: refused to the plain token, before anything is looked at.
	assertRefusal(t, plain.Do(t, setAssignmentMutation, fill), "FULL_REACH_REQUIRED")

	// Allowed to the full-reach token.
	var out struct {
		SetAssignment struct {
			ID       string
			Assignee struct{ Mail string }
		}
	}
	full.MustQuery(t, setAssignmentMutation, fill, &out)
	if out.SetAssignment.Assignee.Mail != testdata.Eins.Mail {
		t.Fatalf("filled with %q, want Eins", out.SetAssignment.Assignee.Mail)
	}

	// And its refusals are the browser's, because it reads what they are about.
	assertRefusal(t, full.Do(t, setAssignmentMutation, fill), "PART_ALREADY_ASSIGNED")
	if got := held(t, full, f.semester); !slices.Contains(got, testdata.Eins.Mail) {
		t.Errorf("a full-reach token reads the assignments %v, want the one it just made", got)
	}
	if got := held(t, plain, f.semester); slices.Contains(got, testdata.Eins.Mail) {
		t.Errorf("a plain token reads an unpublished assignment of somebody else: %v", got)
	}

	// Clearing is the same pairing.
	clear := `mutation($id: ID!) { clearAssignment(id: $id) }`
	assertRefusal(t, plain.Do(t, clear, map[string]any{"id": out.SetAssignment.ID}), "FULL_REACH_REQUIRED")
	var cleared struct{ ClearAssignment string }
	full.MustQuery(t, clear, map[string]any{"id": out.SetAssignment.ID}, &cleared)
}

// A programme lead withdraws demand through a full-reach token and is told INSTANCE_IN_USE where
// the browser would be — and NOT_YOUR_PROGRAMME for a programme they do not lead. Full reach
// widens the Kind term and nothing else.
func TestAFullReachTokenWritesTheDemandOfTheProgrammeItLeads(t *testing.T) {
	t.Parallel()

	f := assignmentHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Vier, []string{"PROGRAMME_LEAD"}},
		grants{testdata.Zwei, []string{"PROGRAMME_LEAD"}},
	)
	f.leadProgramme(t, testdata.Vier, f.programme)
	f.leadProgramme(t, testdata.Zwei, storetest.FixtureProgrammeB)
	f.wishFor(t, testdata.Eins)

	withdraw := `mutation($id: ID!) { withdrawCourseInstance(id: $id) }`
	variables := map[string]any{"id": f.instance}

	plain := graphqltest.New(f.handler).WithToken(testdata.Vier.Token).On(graphqltest.Token)
	assertRefusal(t, plain.Do(t, withdraw, variables), "FULL_REACH_REQUIRED")

	full := graphqltest.New(f.handler).WithToken(fullReachToken(t, f.schema, testdata.Vier)).
		On(graphqltest.Token)
	assertRefusal(t, full.Do(t, withdraw, variables), "INSTANCE_IN_USE")

	// It reads the wish the refusal is about — which is why the refusal may reach it.
	if got := wishers(t, full, f.semester); !slices.Contains(got, testdata.Eins.Mail) {
		t.Errorf("the refusal reached a token that cannot read its reason: wishes %v", got)
	}

	other := graphqltest.New(f.handler).WithToken(fullReachToken(t, f.schema, testdata.Zwei)).
		On(graphqltest.Token)
	assertRefusal(t, other.Do(t, withdraw, variables), "NOT_YOUR_PROGRAMME")
	if got := wishers(t, other, f.semester); slices.Contains(got, testdata.Eins.Mail) {
		t.Errorf("full reach widened the programme scope: the lead of another programme reads %v", got)
	}
}

// Full reach is the browser's reach for planning, never for access. Token management, person and
// role administration and the access log answer no token at all — also not an administrator's
// full-reach one.
func TestAFullReachTokenNeverReachesAccessAdministration(t *testing.T) {
	t.Parallel()

	f := assignmentHandler(t,
		grants{testdata.Sechs, []string{"ADMIN", "DEANS_OFFICE"}},
	)
	full := graphqltest.New(f.handler).WithToken(fullReachToken(t, f.schema, testdata.Sechs)).
		On(graphqltest.Token)

	for _, op := range []string{
		`mutation { createPersonalAccessToken(description: "Nachfolger", fullReach: true) { secret } }`,
		`mutation { revokePersonalAccessToken(id: "AAAAAAAAAAAAAAAA") { id } }`,
		`mutation { createPerson(mail: "neu@example.org") { id } }`,
		`mutation($id: ID!) { setPersonRoles(id: $id, roles: [ADMIN]) { id } }`,
	} {
		resp := full.Do(t, op, map[string]any{"id": testdata.Sechs.ID().String()})
		assertRefusal(t, resp, "INTERACTIVE_ONLY")
	}

	var out struct {
		MyTokens []any
		People   []any
		Session  struct{ Interactive, FullReach bool }
	}
	full.MustQuery(t, `{ myTokens { id } people { id } session { interactive fullReach } }`, nil, &out)
	if out.MyTokens != nil || out.People != nil {
		t.Errorf("a full-reach token read myTokens %v and people %v, want null for both",
			out.MyTokens, out.People)
	}
	if out.Session.Interactive || !out.Session.FullReach {
		t.Errorf("session says interactive=%v fullReach=%v, want false and true",
			out.Session.Interactive, out.Session.FullReach)
	}
}

// The dean's office publishes through a full-reach token — one of the acts that cannot be undone,
// opened on 2026-10-28 for full reach and only for full reach.
func TestAFullReachTokenPublishesTheAssignments(t *testing.T) {
	t.Parallel()

	f := assignmentHandler(t,
		grants{testdata.Fuenf, []string{"DEANS_OFFICE"}},
	)
	publish := `mutation($s: String!) { publishAssignments(code: $s) { assignmentsPublishedAt } }`
	variables := map[string]any{"s": f.semester}

	plain := graphqltest.New(f.handler).WithToken(testdata.Fuenf.Token).On(graphqltest.Token)
	assertRefusal(t, plain.Do(t, publish, variables), "FULL_REACH_REQUIRED")

	full := graphqltest.New(f.handler).WithToken(fullReachToken(t, f.schema, testdata.Fuenf)).
		On(graphqltest.Token)
	var out struct {
		PublishAssignments struct{ AssignmentsPublishedAt *string }
	}
	full.MustQuery(t, publish, variables, &out)
	if out.PublishAssignments.AssignmentsPublishedAt == nil {
		t.Error("published through a full-reach token, but the semester carries no timestamp")
	}
}
