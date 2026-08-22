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
	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/store"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

const (
	teacherAccountsQuery = `{ teacherAccounts {
		teacher { id name mail isUser }
		account { id mail active roles programmes { code } }
	} }`
	admitMutation = `mutation ($id: ID!, $admitted: Boolean!) {
		setTeacherAdmitted(teacherId: $id, admitted: $admitted) {
			teacher { id mail isUser }
			account { id mail active roles programmes { code } }
		}
	}`
)

type teacherAccountsResponse struct {
	TeacherAccounts *[]struct {
		Teacher struct {
			ID     string  `json:"id"`
			Name   string  `json:"name"`
			Mail   *string `json:"mail"`
			IsUser bool    `json:"isUser"`
		} `json:"teacher"`
		Account *struct {
			ID         string   `json:"id"`
			Mail       string   `json:"mail"`
			Active     bool     `json:"active"`
			Roles      []string `json:"roles"`
			Programmes []struct {
				Code string `json:"code"`
			} `json:"programmes"`
		} `json:"account"`
	} `json:"teacherAccounts"`
}

type admitResponse struct {
	SetTeacherAdmitted struct {
		Teacher struct {
			ID     string  `json:"id"`
			Mail   *string `json:"mail"`
			IsUser bool    `json:"isUser"`
		} `json:"teacher"`
		Account *struct {
			ID         string   `json:"id"`
			Mail       string   `json:"mail"`
			Active     bool     `json:"active"`
			Roles      []string `json:"roles"`
			Programmes []struct {
				Code string `json:"code"`
			} `json:"programmes"`
		} `json:"account"`
	} `json:"setTeacherAdmitted"`
}

// admissionFixture is a handler with a projected catalogue behind it, so that there are people
// who teach — and, unlike adminHandler, ids to admit.
type admissionFixture struct {
	handler http.Handler
	schema  *storetest.Schema
	// notAdmitted teaches, has an address, and has no account. The ordinary row.
	notAdmitted uuid.UUID
	// withoutMail can never be admitted, because the address is the link.
	withoutMail uuid.UUID
}

// admissionHandler wires what the admission screen talks to.
//
// Same shape as adminHandler, with the catalogue projected on top — a handler with no teacher
// rows would answer every assertion below with an empty list, and they would all pass for the
// wrong reason.
func admissionHandler(t *testing.T) admissionFixture {
	t.Helper()

	s := storetest.New(t)
	storetest.SeedPerson(t, s, testdata.Sechs, string(policy.RoleAdmin), string(policy.RoleLecturer))
	storetest.SeedPerson(t, s, testdata.Zwei, string(policy.RoleLecturer))

	// A working token for the administrator, so that "not through a token" cannot pass because
	// of a 401 for an unknown credential.
	parsed, err := auth.ParseToken(testdata.Sechs.Token)
	if err != nil {
		t.Fatalf("the fixture token does not parse: %v", err)
	}
	storetest.SeedToken(t, s, testdata.Sechs, auth.HashSecret(parsed.Secret), storetest.TokenOptions{})

	storetest.SeedZPACatalogue(t, s)
	if _, err := store.NewCatalogue(s.Pool).Project(t.Context(), nil); err != nil {
		t.Fatalf("cannot project the catalogue: %v", err)
	}

	directory := store.NewDirectory(s.Pool)
	fixture := admissionFixture{
		schema: s,
		handler: bootstrap.Handler(bootstrap.Options{
			Build:  buildinfo.Info{Version: "test"},
			Auth:   auth.Config{Mode: auth.ModeProxy, Users: directory, Tokens: directory},
			People: domain.NewPeopleService(store.NewPeople(s.Pool), nil),
		}),
	}

	read := func(ref int64) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := s.Pool.QueryRow(t.Context(),
			`SELECT id FROM teacher WHERE zpa_teacher_ref = $1`, ref).Scan(&id); err != nil {
			t.Fatalf("cannot read the fixture teacher %d: %v", ref, err)
		}
		return id
	}
	fixture.notAdmitted = read(storetest.FixtureTeacherNotAdmitted)
	fixture.withoutMail = read(storetest.FixtureTeacherWithoutMail)

	return fixture
}

// countPeople answers how many accounts exist, for the assertions about what a refusal must not
// have written.
func (f admissionFixture) countPeople(t *testing.T) int {
	t.Helper()

	var n int
	if err := f.schema.Pool.QueryRow(t.Context(), `SELECT count(*) FROM person`).Scan(&n); err != nil {
		t.Fatalf("cannot count the accounts: %v", err)
	}
	return n
}

// The admission surface is administration, so it is not reachable through a token — the same
// rule as the rest of it, asserted per door because this is a place the doors differ.
//
// A script that could admit people would decouple the granting of access from any sign-in, and
// this is the surface where the thing being granted is access itself.
func TestTheAdmissionSurfaceIsNeverReachableThroughAToken(t *testing.T) {
	t.Parallel()

	f := admissionHandler(t)
	c := graphqltest.New(f.handler)

	t.Run("browser", func(t *testing.T) {
		var got teacherAccountsResponse
		c.AsUser(testdata.Sechs.Mail).MustQuery(t, teacherAccountsQuery, nil, &got)
		if got.TeacherAccounts == nil || len(*got.TeacherAccounts) == 0 {
			t.Fatal("the administrator got no teachers at all")
		}
	})

	t.Run("token: the list answers null", func(t *testing.T) {
		var got teacherAccountsResponse
		c.WithToken(testdata.Sechs.Token).On(graphqltest.Token).
			MustQuery(t, teacherAccountsQuery, nil, &got)
		if got.TeacherAccounts != nil {
			t.Errorf("a token read the admission list: %v", *got.TeacherAccounts)
		}
	})

	t.Run("token: admitting is refused", func(t *testing.T) {
		resp := c.WithToken(testdata.Sechs.Token).On(graphqltest.Token).
			Do(t, admitMutation, map[string]any{
				"id": f.notAdmitted.String(), "admitted": true,
			})
		assertRefusal(t, resp, "INTERACTIVE_ONLY")
		if n := f.countPeople(t); n != 2 {
			t.Errorf("%d accounts exist after a refused admission, want the two seeded ones", n)
		}
	})
}

// The directive answers "not through a token". This answers the other half: not for everybody
// else either. Who teaches is not confidential, but who may sign in and as what is
// administration, and a field readable by anybody signed in would hand the whole faculty's
// roles to the whole faculty.
func TestOnlyAnAdministratorSeesTheAdmissionList(t *testing.T) {
	t.Parallel()

	f := admissionHandler(t)

	resp := graphqltest.New(f.handler).AsUser(testdata.Zwei.Mail).On(graphqltest.Browser).
		Do(t, teacherAccountsQuery, nil)
	assertRefusal(t, resp, "FORBIDDEN")
}

// Admitting grants exactly LECTURER — the decision, asserted, so that a later tidying of it to
// "no roles" has to argue with a test rather than with a comment.
//
// It also asserts what the answer carries. The screen's next click is a role switch on the row
// that was just admitted, and it needs the account's id; a mutation that returned only the
// teacher would make the screen ask again in the middle of somebody's click path.
func TestAdmittingATeacherGrantsExactlyLecturer(t *testing.T) {
	t.Parallel()

	f := admissionHandler(t)
	c := graphqltest.New(f.handler).AsUser(testdata.Sechs.Mail).On(graphqltest.Browser)

	var got admitResponse
	c.MustQuery(t, admitMutation, map[string]any{
		"id": f.notAdmitted.String(), "admitted": true,
	}, &got)

	account := got.SetTeacherAdmitted.Account
	if account == nil {
		t.Fatal("admitting answered with no account")
	}
	if len(account.Roles) != 1 || account.Roles[0] != string(policy.RoleLecturer) {
		t.Errorf("the new account holds %v, want exactly LECTURER", account.Roles)
	}
	if !account.Active {
		t.Error("the new account is not active")
	}
	if account.ID == "" {
		t.Error("the answer carries no account id, so the screen cannot set a role next")
	}
	if !got.SetTeacherAdmitted.Teacher.IsUser {
		t.Error("the teacher does not read as somebody who may sign in right after admission")
	}

	// And the list says the same thing, so that a reload shows what the click did.
	var list teacherAccountsResponse
	c.MustQuery(t, teacherAccountsQuery, nil, &list)
	var found bool
	for _, row := range *list.TeacherAccounts {
		if row.Teacher.ID == f.notAdmitted.String() {
			found = true
			if row.Account == nil || row.Account.ID != account.ID {
				t.Error("the list does not show the account that was just created")
			}
		}
	}
	if !found {
		t.Error("the admitted teacher is not in the list any more")
	}
}

// Withdrawing is the deactivation that already exists, so it is the same guard: the last
// administrator cannot be locked out, and it makes no difference which screen asks.
func TestWithdrawingAdmissionCannotRemoveTheLastAdministratorThroughTheAPI(t *testing.T) {
	t.Parallel()

	f := admissionHandler(t)
	c := graphqltest.New(f.handler).AsUser(testdata.Sechs.Mail).On(graphqltest.Browser)

	// Admit somebody and hand the administration over to them, so that they are the only one
	// left — the state this guard exists for, arrived at the way it would really be arrived at.
	var admitted admitResponse
	c.MustQuery(t, admitMutation, map[string]any{
		"id": f.notAdmitted.String(), "admitted": true,
	}, &admitted)

	var roles struct {
		SetPersonRoles struct {
			Roles []string `json:"roles"`
		} `json:"setPersonRoles"`
	}
	c.MustQuery(t, setRolesMutation, map[string]any{
		"id":    admitted.SetTeacherAdmitted.Account.ID,
		"roles": []string{"LECTURER", "ADMIN"},
	}, &roles)
	c.MustQuery(t, setRolesMutation, map[string]any{
		"id":    testdata.Sechs.ID().String(),
		"roles": []string{"LECTURER"},
	}, &roles)

	// Now the last administrator withdraws their own admission — the one click that would leave
	// this installation with nobody who can administer it, on a screen that is behind a VPN and
	// whose other repair is psql on the host.
	successor := admitted.SetTeacherAdmitted.Teacher.Mail
	if successor == nil {
		t.Fatal("the admitted teacher has no address")
	}
	resp := graphqltest.New(f.handler).AsUser(*successor).On(graphqltest.Browser).
		Do(t, admitMutation, map[string]any{
			"id": f.notAdmitted.String(), "admitted": false,
		})
	assertRefusal(t, resp, "LAST_ADMIN")

	// And the refusal left them able to sign in, rather than half-applying.
	var list teacherAccountsResponse
	graphqltest.New(f.handler).AsUser(*successor).On(graphqltest.Browser).
		MustQuery(t, teacherAccountsQuery, nil, &list)
	for _, row := range *list.TeacherAccounts {
		if row.Teacher.ID == f.notAdmitted.String() && (row.Account == nil || !row.Account.Active) {
			t.Error("the refusal still locked the last administrator out")
		}
	}
}

// Somebody the examination office gives no address for cannot be admitted: the address is the
// whole link between the two lists, and there is nothing here to invent one from.
func TestATeacherWithoutAnAddressCannotBeAdmittedThroughTheAPI(t *testing.T) {
	t.Parallel()

	f := admissionHandler(t)
	before := f.countPeople(t)

	resp := graphqltest.New(f.handler).AsUser(testdata.Sechs.Mail).On(graphqltest.Browser).
		Do(t, admitMutation, map[string]any{
			"id": f.withoutMail.String(), "admitted": true,
		})
	assertRefusal(t, resp, "TEACHER_HAS_NO_MAIL")

	if after := f.countPeople(t); after != before {
		t.Errorf("a refused admission created %d account(s)", after-before)
	}
}

// A teacher id nobody has. Its own code rather than the generic refusal, because the realistic
// way to get here is a screen whose list is older than the import that withdrew the row.
func TestAdmittingSomebodyTheImportDoesNotKnow(t *testing.T) {
	t.Parallel()

	f := admissionHandler(t)

	resp := graphqltest.New(f.handler).AsUser(testdata.Sechs.Mail).On(graphqltest.Browser).
		Do(t, admitMutation, map[string]any{
			"id": uuid.New().String(), "admitted": true,
		})
	assertRefusal(t, resp, "TEACHER_NOT_FOUND")
}
