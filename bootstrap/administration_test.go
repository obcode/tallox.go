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
	peopleQuery       = `{ people { id mail name roles } }`
	sessionQuery      = `{ session { effectiveRoles grantedRoles narrowed interactive person { mail } } }`
	createPersonMutat = `mutation ($mail: String!, $name: String) {
		createPerson(mail: $mail, name: $name) { id mail name roles }
	}`
	setRolesMutation = `mutation ($id: ID!, $roles: [Role!]!) {
		setPersonRoles(id: $id, roles: $roles) { id roles }
	}`
	setActiveMutation = `mutation ($id: ID!, $active: Boolean!) {
		setPersonActive(id: $id, active: $active) { id }
	}`
)

type peopleResponse struct {
	People *[]struct {
		ID    string   `json:"id"`
		Mail  string   `json:"mail"`
		Name  string   `json:"name"`
		Roles []string `json:"roles"`
	} `json:"people"`
}

type sessionResponse struct {
	Session struct {
		EffectiveRoles []string `json:"effectiveRoles"`
		GrantedRoles   []string `json:"grantedRoles"`
		Narrowed       bool     `json:"narrowed"`
		Interactive    bool     `json:"interactive"`
		Person         *struct {
			Mail string `json:"mail"`
		} `json:"person"`
	} `json:"session"`
}

// adminHandler returns a handler on a fresh schema with an administrator and an ordinary
// colleague seeded, wired the way Serve wires it.
func adminHandler(t *testing.T) http.Handler {
	t.Helper()

	s := storetest.New(t)
	// Sechs administers. Zwei does not — she is the persona every "may she?" question is about.
	storetest.SeedPerson(t, s, testdata.Sechs, string(policy.RoleAdmin), string(policy.RoleLecturer))
	storetest.SeedPerson(t, s, testdata.Zwei, string(policy.RoleLecturer))

	// Sechs also holds a working token. Without it the "not through a token" assertions would
	// pass for the wrong reason — a 401 for an unknown credential looks exactly like the
	// directive doing its job, and would keep looking like it after somebody removed the
	// directive.
	parsed, err := auth.ParseToken(testdata.Sechs.Token)
	if err != nil {
		t.Fatalf("the fixture token does not parse: %v", err)
	}
	storetest.SeedToken(t, s, testdata.Sechs, auth.HashSecret(parsed.Secret), storetest.TokenOptions{})

	directory := store.NewDirectory(s.Pool)

	return bootstrap.Handler(bootstrap.Options{
		Build:  buildinfo.Info{Version: "test"},
		Auth:   auth.Config{Mode: auth.ModeProxy, Users: directory, Tokens: directory},
		Tokens: domain.NewTokenService(store.NewTokens(s.Pool), nil),
		People: domain.NewPeopleService(store.NewPeople(s.Pool), nil),
	})
}

// TestUserAdministrationIsNeverReachableThroughAToken.
//
// The rule that has to hold on both doors, asserted per door rather than through EachDoor,
// because this is one of the places the doors are *supposed* to differ — and a helper that
// asserted the same thing twice would quietly cover only one of them.
//
// The browser gets the list. A token gets null on the query and a refusal on every mutation,
// which is what the @interactiveOnly directive does on a nullable field and on a mutation
// respectively. Granting somebody a role from a script would decouple "who did this" from any
// sign-in, and the act it would decouple is the granting of access itself.
func TestUserAdministrationIsNeverReachableThroughAToken(t *testing.T) {
	t.Parallel()

	h := adminHandler(t)
	c := graphqltest.New(h)

	t.Run("browser", func(t *testing.T) {
		var got peopleResponse
		c.AsUser(testdata.Sechs.Mail).MustQuery(t, peopleQuery, nil, &got)
		if got.People == nil || len(*got.People) != 2 {
			t.Fatalf("the administrator got %v, want both seeded people", got.People)
		}
	})

	t.Run("token: the query answers null", func(t *testing.T) {
		var got peopleResponse
		c.WithToken(testdata.Sechs.Token).On(graphqltest.Token).
			MustQuery(t, peopleQuery, nil, &got)
		if got.People != nil {
			t.Errorf("a token read the user list: %v", *got.People)
		}
	})

	t.Run("token: every mutation is refused", func(t *testing.T) {
		token := c.WithToken(testdata.Sechs.Token).On(graphqltest.Token)
		for name, call := range map[string]struct {
			query string
			vars  map[string]any
		}{
			"createPerson": {createPersonMutat, map[string]any{"mail": "neu@example.org"}},
			"setPersonRoles": {setRolesMutation,
				map[string]any{"id": testdata.Zwei.ID().String(), "roles": []string{"LECTURER"}}},
			"setPersonActive": {setActiveMutation,
				map[string]any{"id": testdata.Zwei.ID().String(), "active": false}},
			// The id names nobody here — this handler has no catalogue behind it. The directive
			// refuses before anything looks it up, which is the point: the checklist is every
			// administration mutation, and admission is one. What it does when it is *allowed*
			// to run is in admission_test.go.
			"setTeacherAdmitted": {admitMutation,
				map[string]any{"id": uuid.Nil.String(), "admitted": true}},
		} {
			t.Run(name, func(t *testing.T) {
				token.MustFail(t, call.query, call.vars)
			})
		}
	})
}

// TestOnlyAnAdministratorSeesTheUserList.
//
// The directive answers "not through a token". This answers the other half — "not for anybody
// else" — and it has to be a separate assertion, because a field that is interactive-only and
// readable by everybody signed in is a field that leaks to the whole faculty.
func TestOnlyAnAdministratorSeesTheUserList(t *testing.T) {
	t.Parallel()

	c := graphqltest.New(adminHandler(t))

	messages := c.AsUser(testdata.Zwei.Mail).MustFail(t, peopleQuery, nil)
	if len(messages) == 0 {
		t.Fatal("an ordinary colleague read the user list")
	}
	// The refusal must not double as a directory: it says why, not who else exists.
	graphqltest.AssertNoLeak(t, messages[0],
		append(graphqltest.DatabaseNoise(), testdata.Mails(testdata.Others(testdata.Zwei))...)...)
}

// TestTheLastAdministratorCannotBeRemovedThroughTheAPI.
//
// The guard lives in a transaction in internal/store and is tested there against the database.
// This is the other half of the same claim: that it is actually reachable from the surface
// somebody would use to cause the problem, and that it arrives as a refusal a client can
// branch on rather than as a 500.
func TestTheLastAdministratorCannotBeRemovedThroughTheAPI(t *testing.T) {
	t.Parallel()

	c := graphqltest.New(adminHandler(t)).AsUser(testdata.Sechs.Mail)
	admin := testdata.Sechs.ID().String()

	t.Run("by taking the role away", func(t *testing.T) {
		c.MustFail(t, setRolesMutation, map[string]any{"id": admin, "roles": []string{"LECTURER"}})
	})

	t.Run("by deactivating the account", func(t *testing.T) {
		c.MustFail(t, setActiveMutation, map[string]any{"id": admin, "active": false})
	})

	// Still there, and still able to administer — which is the only assertion that matters.
	var got peopleResponse
	c.MustQuery(t, peopleQuery, nil, &got)
	if got.People == nil {
		t.Fatal("the administrator lost access despite both refusals")
	}
}

// TestSessionAnswersBeforeAnybodyIsSignedIn: the interface renders its signed-out state from
// this field, so a query that failed would leave it nothing to render from.
func TestSessionAnswersBeforeAnybodyIsSignedIn(t *testing.T) {
	t.Parallel()

	var got sessionResponse
	graphqltest.New(adminHandler(t)).Anonymous().MustQuery(t, sessionQuery, nil, &got)

	if got.Session.Person != nil {
		t.Errorf("an anonymous session has a person: %+v", got.Session.Person)
	}
	if len(got.Session.EffectiveRoles) != 0 {
		t.Errorf("an anonymous session holds %v", got.Session.EffectiveRoles)
	}
}

// TestNarrowingRemovesPermissionsAndNeverAddsThem is the role-preview feature seen from the
// outside, through the door it is served on.
//
// The header is not set by the proxy and is not trusted. It does not have to be: the
// intersection in policy.Narrow means the worst it can do is take privileges away from whoever
// sent it. The second subtest is the one that would matter if that were ever untrue.
func TestNarrowingRemovesPermissionsAndNeverAddsThem(t *testing.T) {
	t.Parallel()

	h := adminHandler(t)
	c := graphqltest.New(h)

	t.Run("an administrator can look as a lecturer", func(t *testing.T) {
		narrowed := c.AsUser(testdata.Sechs.Mail).
			WithHeader(auth.HeaderAssumeRoles, string(policy.RoleLecturer))

		var got sessionResponse
		narrowed.MustQuery(t, sessionQuery, nil, &got)

		if !got.Session.Narrowed {
			t.Error("the session does not report being narrowed — the banner would never show")
		}
		if len(got.Session.EffectiveRoles) != 1 ||
			got.Session.EffectiveRoles[0] != string(policy.RoleLecturer) {
			t.Errorf("effective roles are %v, want only LECTURER", got.Session.EffectiveRoles)
		}
		if len(got.Session.GrantedRoles) != 2 {
			t.Errorf("granted roles are %v, want both — this is the way back out",
				got.Session.GrantedRoles)
		}

		// And the narrowing reaches the rules, not just the display.
		narrowed.MustFail(t, peopleQuery, nil)
	})

	t.Run("a lecturer cannot claim to be an administrator", func(t *testing.T) {
		claiming := c.AsUser(testdata.Zwei.Mail).
			WithHeader(auth.HeaderAssumeRoles, string(policy.RoleAdmin))

		var got sessionResponse
		claiming.MustQuery(t, sessionQuery, nil, &got)
		for _, r := range got.Session.EffectiveRoles {
			if r == string(policy.RoleAdmin) {
				t.Fatal("a hand-written header granted ADMIN — the narrowing is an " +
					"escalation rather than a preview")
			}
		}

		claiming.MustFail(t, peopleQuery, nil)
	})
}

// TestCreatingAPersonNeedsNothingButAMailAddress.
//
// The requirement as it was asked for: adding a new colleague must not wait on finding out how
// they spell their first name. The name is filled in later, by them or by an import.
func TestCreatingAPersonNeedsNothingButAMailAddress(t *testing.T) {
	t.Parallel()

	c := graphqltest.New(adminHandler(t)).AsUser(testdata.Sechs.Mail)

	var created struct {
		CreatePerson struct {
			Mail  string   `json:"mail"`
			Name  string   `json:"name"`
			Roles []string `json:"roles"`
		} `json:"createPerson"`
	}
	c.MustQuery(t, createPersonMutat, map[string]any{"mail": "neue.kollegin@example.org"}, &created)

	if created.CreatePerson.Mail != "neue.kollegin@example.org" {
		t.Errorf("mail is %q", created.CreatePerson.Mail)
	}
	if len(created.CreatePerson.Roles) != 0 {
		t.Errorf("a new person holds %v — even LECTURER is granted explicitly, so that who "+
			"may do what is a list somebody wrote", created.CreatePerson.Roles)
	}
}
