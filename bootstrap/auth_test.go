package bootstrap_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/obcode/tallox.go/bootstrap"
	"github.com/obcode/tallox.go/internal/auth"
	"github.com/obcode/tallox.go/internal/buildinfo"
	"github.com/obcode/tallox.go/internal/graphqltest"
	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/store"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// The tests in this file drive the real handler, with the real middleware, against a real
// migrated PostgreSQL schema. Nothing between the HTTP request and the row is faked, which is
// the only way the assertions mean anything: the wish filter will be a WHERE clause, the
// role list comes back from an aggregate, and "the same principal through both doors" is a
// claim about the query as much as about the Go code.

const meQuery = `{ me { id mail name roles } }`

type meResponse struct {
	Me *struct {
		ID    string   `json:"id"`
		Mail  string   `json:"mail"`
		Name  string   `json:"name"`
		Roles []string `json:"roles"`
	} `json:"me"`
}

// seeded returns a handler backed by a fresh schema, with the given personas inserted.
//
// Each persona gets their fixture token, hashed the way the production authenticator will
// hash what arrives in the header — so a test that passes here could not pass against a token
// the real parser rejects.
func seeded(t *testing.T, mode auth.Mode, people map[testdata.Persona][]policy.Role) http.Handler {
	t.Helper()

	s := storetest.New(t)

	for persona, roles := range people {
		grants := make([]string, 0, len(roles))
		for _, r := range roles {
			grants = append(grants, string(r))
		}
		storetest.SeedPerson(t, s, persona, grants...)

		parsed, err := auth.ParseToken(persona.Token)
		if err != nil {
			t.Fatalf("fixture token of %s does not parse: %v", persona.Name, err)
		}
		storetest.SeedToken(t, s, persona, auth.HashSecret(parsed.Secret), storetest.TokenOptions{
			Description: "integration test",
		})
	}

	directory := store.NewDirectory(s.Pool)

	return bootstrap.Handler(bootstrap.Options{
		Build: buildinfo.Info{Version: "test"},
		Auth: auth.Config{
			Mode:   mode,
			Users:  directory,
			Tokens: directory,
		},
	})
}

// TestTheSamePersonThroughBothDoors is the invariant of the whole design, made checkable:
//
//	effective permission = (what the Role allows) ∩ (what the Scopes grant) ∩ (what the Kind allows)
//
// A Personal Access Token is its owner. The header is its owner. If those two ever produce
// different principals, every rule downstream has to be written twice — and the realistic
// failure is not a wrong answer but a rule somebody adds for the browser and forgets on the
// token path six months from now.
func TestTheSamePersonThroughBothDoors(t *testing.T) {
	t.Parallel()

	h := seeded(t, auth.ModeProxy, map[testdata.Persona][]policy.Role{
		testdata.Vier: {policy.RoleLecturer, policy.RoleProgrammeLead},
	})

	answers := map[string]meResponse{}

	graphqltest.EachDoor(t, h, testdata.Vier.Mail, testdata.Vier.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var got meResponse
			c.MustQuery(t, meQuery, nil, &got)

			if got.Me == nil {
				t.Fatal("a valid credential resolved to nobody")
			}
			if got.Me.ID != testdata.Vier.ID().String() {
				t.Errorf("id is %s, want %s", got.Me.ID, testdata.Vier.ID())
			}
			if got.Me.Mail != testdata.Vier.Mail {
				t.Errorf("mail is %s", got.Me.Mail)
			}
			if len(got.Me.Roles) != 2 {
				t.Errorf("roles are %v, want both grants", got.Me.Roles)
			}

			answers[c.Door().Name] = got
		})

	browser, token := answers[graphqltest.Browser.Name], answers[graphqltest.Token.Name]
	if browser.Me == nil || token.Me == nil {
		t.Fatal("one of the doors produced no answer")
	}
	if browser.Me.ID != token.Me.ID || browser.Me.Mail != token.Me.Mail {
		t.Errorf("the doors disagree about who is calling: %+v vs %+v", browser.Me, token.Me)
	}
	if len(browser.Me.Roles) != len(token.Me.Roles) {
		t.Errorf("the doors disagree about the roles: %v vs %v",
			browser.Me.Roles, token.Me.Roles)
	}
}

// TestATokenIsNeverMoreThanItsOwner covers the half of that invariant that has teeth.
//
// Roles are resolved from the person on every request rather than copied onto the token when
// it is minted, so revoking a role demotes every token that person holds — immediately,
// without anybody having to find those tokens. This test revokes and asks again.
func TestATokenIsNeverMoreThanItsOwner(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	storetest.SeedPerson(t, s, testdata.Vier,
		string(policy.RoleLecturer), string(policy.RoleProgrammeLead))

	parsed, err := auth.ParseToken(testdata.Vier.Token)
	if err != nil {
		t.Fatalf("fixture token does not parse: %v", err)
	}
	storetest.SeedToken(t, s, testdata.Vier, auth.HashSecret(parsed.Secret), storetest.TokenOptions{})

	directory := store.NewDirectory(s.Pool)
	h := bootstrap.Handler(bootstrap.Options{
		Build: buildinfo.Info{Version: "test"},
		Auth:  auth.Config{Mode: auth.ModeProxy, Users: directory, Tokens: directory},
	})

	c := graphqltest.New(h).WithToken(testdata.Vier.Token)

	var before meResponse
	c.MustQuery(t, meQuery, nil, &before)
	if before.Me == nil || len(before.Me.Roles) != 2 {
		t.Fatalf("token started with %+v", before.Me)
	}

	err = s.Queries().RevokeRole(t.Context(), store.RevokeRoleParams{
		PersonID: testdata.Vier.ID(),
		Role:     string(policy.RoleProgrammeLead),
	})
	if err != nil {
		t.Fatalf("cannot revoke the role: %v", err)
	}

	var after meResponse
	c.MustQuery(t, meQuery, nil, &after)
	if after.Me == nil {
		t.Fatal("the token stopped working entirely")
	}
	if len(after.Me.Roles) != 1 || after.Me.Roles[0] != string(policy.RoleLecturer) {
		t.Errorf("after revoking a role the token still carries %v — permissions are being "+
			"copied onto the token instead of resolved from its owner", after.Me.Roles)
	}
}

// TestCredentialsThatDoNotWorkAreRefused walks the refusals through the real router, because
// the status code is what the GUI and every script actually react to.
//
// The unknown-identity case is the one worth writing down: a colleague the identity provider
// knows and this installation does not gets a 401 that says so, not an empty page. The fix is
// an import, and a caller who is told "no account" can say that to whoever runs the import.
func TestCredentialsThatDoNotWorkAreRefused(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	storetest.SeedPerson(t, s, testdata.Eins, string(policy.RoleLecturer))

	parsed, err := auth.ParseToken(testdata.Eins.Token)
	if err != nil {
		t.Fatalf("fixture token does not parse: %v", err)
	}
	storetest.SeedToken(t, s, testdata.Eins, auth.HashSecret(parsed.Secret), storetest.TokenOptions{})

	// Zwei is seeded with an expired token, and Drei with a revoked one, so that all three
	// token failures are reachable from one schema — which is only possible because the
	// personas have distinct token ids.
	storetest.SeedPerson(t, s, testdata.Zwei, string(policy.RoleLecturer))
	zwei, err := auth.ParseToken(testdata.Zwei.Token)
	if err != nil {
		t.Fatalf("fixture token does not parse: %v", err)
	}
	storetest.SeedToken(t, s, testdata.Zwei, auth.HashSecret(zwei.Secret), storetest.TokenOptions{
		ExpiresAt: time.Now().Add(-time.Hour),
	})

	storetest.SeedPerson(t, s, testdata.Drei, string(policy.RoleLecturer))
	drei, err := auth.ParseToken(testdata.Drei.Token)
	if err != nil {
		t.Fatalf("fixture token does not parse: %v", err)
	}
	storetest.SeedToken(t, s, testdata.Drei, auth.HashSecret(drei.Secret), storetest.TokenOptions{
		Revoked: true,
	})

	directory := store.NewDirectory(s.Pool)
	h := bootstrap.Handler(bootstrap.Options{
		Build: buildinfo.Info{Version: "test"},
		Auth:  auth.Config{Mode: auth.ModeProxy, Users: directory, Tokens: directory},
	})

	for _, tc := range []struct {
		name   string
		client *graphqltest.Client
	}{
		{
			name:   "an identity with no account here",
			client: graphqltest.New(h).AsUser("neu@example.org"),
		},
		{
			name:   "a token that does not exist",
			client: graphqltest.New(h).WithToken(testdata.Fuenf.Token),
		},
		{
			name: "the right token id with the wrong secret",
			client: graphqltest.New(h).WithToken(
				"tallox_" + testdata.Eins.TokenID + "_" +
					"exampleXforgedXsecretX00000000000000000000000"[:43]),
		},
		{
			name:   "an expired token",
			client: graphqltest.New(h).WithToken(testdata.Zwei.Token),
		},
		{
			name:   "a revoked token",
			client: graphqltest.New(h).WithToken(testdata.Drei.Token),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := tc.client.Do(t, meQuery, nil)

			if resp.HTTPStatus != http.StatusUnauthorized {
				t.Errorf("answered %d, want 401:\n%s", resp.HTTPStatus, resp.Body)
			}
			if !resp.Failed() {
				t.Errorf("the refusal carries no GraphQL error: %s", resp.Body)
			}

			// A refusal is a leak channel of its own. Naming the owner of a token, or
			// confirming who does have an account, hands out exactly what the confidentiality
			// rule withholds.
			for _, message := range resp.Messages() {
				graphqltest.AssertNoLeak(t, message,
					append(graphqltest.DatabaseNoise(), testdata.Mails(testdata.All())...)...)
			}
		})
	}
}

// TestADeactivatedPersonLosesBothDoors covers what deactivation is for: a leaver loses
// everything at once, including tokens nobody remembered to revoke one by one.
func TestADeactivatedPersonLosesBothDoors(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	storetest.SeedPerson(t, s, testdata.Eins, string(policy.RoleLecturer))
	parsed, err := auth.ParseToken(testdata.Eins.Token)
	if err != nil {
		t.Fatalf("fixture token does not parse: %v", err)
	}
	storetest.SeedToken(t, s, testdata.Eins, auth.HashSecret(parsed.Secret), storetest.TokenOptions{})

	directory := store.NewDirectory(s.Pool)
	h := bootstrap.Handler(bootstrap.Options{
		Build: buildinfo.Info{Version: "test"},
		Auth:  auth.Config{Mode: auth.ModeProxy, Users: directory, Tokens: directory},
	})

	// Works before.
	graphqltest.EachDoor(t, h, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var got meResponse
			c.MustQuery(t, meQuery, nil, &got)
			if got.Me == nil {
				t.Fatal("an active person could not authenticate")
			}
		})

	err = s.Queries().SetPersonActive(t.Context(), store.SetPersonActiveParams{
		ID:     testdata.Eins.ID(),
		Active: false,
	})
	if err != nil {
		t.Fatalf("cannot deactivate: %v", err)
	}

	// And not after — on either door.
	graphqltest.EachDoor(t, h, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			resp := c.Do(t, meQuery, nil)
			if resp.HTTPStatus != http.StatusUnauthorized {
				t.Errorf("a deactivated person still gets %d on the %s door:\n%s",
					resp.HTTPStatus, c.Door().Name, resp.Body)
			}
		})
}

// TestMeSaysWhetherTheAccountIsActive pins down the one answer Person.active can have here.
//
// The field is filled in from the actor rather than read back, because authentication has
// already decided it: TestADeactivatedPersonLosesBothDoors above is the other half, where the
// same request gets a 401 instead. Without this assertion the field would answer false for
// everybody — the actor carries no such flag, and a struct literal that forgets it compiles.
func TestMeSaysWhetherTheAccountIsActive(t *testing.T) {
	t.Parallel()

	h := seeded(t, auth.ModeProxy, map[testdata.Persona][]policy.Role{
		testdata.Eins: {policy.RoleLecturer},
	})

	graphqltest.EachDoor(t, h, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var got struct {
				Me *struct {
					Active bool `json:"active"`
				} `json:"me"`
			}
			c.MustQuery(t, `{ me { active } }`, nil, &got)
			if got.Me == nil {
				t.Fatal("an active person could not authenticate")
			}
			if !got.Me.Active {
				t.Errorf("somebody who just authenticated on the %s door reads as inactive",
					c.Door().Name)
			}
		})
}

// TestDevModeLeavesTheTokenDoorReal is the asymmetry that makes auth.mode=dev safe to use
// every day.
//
// The browser door hands out a development user, so the GUI works without an auth proxy in
// front of it. The token door does not budge: it still parses, looks up and compares. That is
// what keeps the production credential path exercised daily instead of discovered in October
// — and it is the property somebody will be tempted to remove the first time a local script
// is inconvenient.
func TestDevModeLeavesTheTokenDoorReal(t *testing.T) {
	t.Parallel()

	// No people seeded at all: the development user does not come from the database.
	h := seeded(t, auth.ModeDev, nil)

	var dev meResponse
	graphqltest.New(h).Anonymous().MustQuery(t, meQuery, nil, &dev)
	if dev.Me == nil {
		t.Fatal("dev mode did not inject a user on the browser door")
	}
	if len(dev.Me.Roles) != len(policy.AllRoles()) {
		t.Errorf("the development user holds %v, want every role", dev.Me.Roles)
	}

	// The same server, the same moment: a token that is not in the database is refused.
	resp := graphqltest.New(h).WithToken(testdata.Eins.Token).Do(t, meQuery, nil)
	if resp.HTTPStatus != http.StatusUnauthorized {
		t.Errorf("dev mode answered %d on the token door — the machine door has stopped being "+
			"real, and the production path is no longer exercised:\n%s",
			resp.HTTPStatus, resp.Body)
	}
}

// TestTheBrowserDoorIgnoresBearerTokens and its mirror image: each door reads only its own
// credential.
//
// Accepting a bearer token on /query would mean that everything the token path is not
// allowed to reach — personnel data, other people's unpublished wishes, token management —
// becomes reachable by aiming the script at the other URL. Accepting X-Remote-User on
// /api/graphql would be worse: that route has no proxy in front of it stripping headers, so
// the header would be whatever the caller wrote.
func TestEachDoorReadsOnlyItsOwnCredential(t *testing.T) {
	t.Parallel()

	h := seeded(t, auth.ModeProxy, map[testdata.Persona][]policy.Role{
		testdata.Eins: {policy.RoleLecturer},
	})

	t.Run("bearer token on the browser door", func(t *testing.T) {
		var got meResponse
		graphqltest.New(h).On(graphqltest.Browser).
			WithHeader("Authorization", "Bearer "+testdata.Eins.Token).
			MustQuery(t, meQuery, nil, &got)

		if got.Me != nil {
			t.Errorf("the browser door authenticated a bearer token as %s", got.Me.Mail)
		}
	})

	t.Run("proxy header on the token door", func(t *testing.T) {
		var got meResponse
		graphqltest.New(h).On(graphqltest.Token).
			WithHeader(auth.HeaderRemoteUser, testdata.Eins.Mail).
			MustQuery(t, meQuery, nil, &got)

		if got.Me != nil {
			t.Errorf("the token door authenticated a header as %s — on the one route with no "+
				"proxy in front of it", got.Me.Mail)
		}
	})
}

// TestRolesTheDatabaseHoldsButThePolicyDoesNotKnowGrantNothing follows an unrecognised grant
// all the way from the row to the API answer.
//
// The three lists — the CHECK constraint, internal/policy and the GraphQL enum — are compared
// pairwise by other tests. This one covers the behaviour those comparisons exist to protect:
// if a grant ever does slip through, it has to mean nothing rather than mean something
// unpredictable, and it must not reach the client as an enum value the schema cannot express.
func TestUnknownGrantsNeverReachTheClient(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	storetest.SeedPerson(t, s, testdata.Eins, string(policy.RoleLecturer))

	// Straight past the CHECK constraint, the way a future migration widening the list could.
	_, err := s.Pool.Exec(t.Context(),
		`ALTER TABLE person_role DROP CONSTRAINT person_role_role_known`)
	if err != nil {
		t.Fatalf("cannot drop the constraint: %v", err)
	}
	_, err = s.Pool.Exec(t.Context(),
		`INSERT INTO person_role (person_id, role) VALUES ($1, 'SUPERUSER')`, testdata.Eins.ID())
	if err != nil {
		t.Fatalf("cannot insert the rogue grant: %v", err)
	}

	directory := store.NewDirectory(s.Pool)
	h := bootstrap.Handler(bootstrap.Options{
		Build: buildinfo.Info{Version: "test"},
		Auth:  auth.Config{Mode: auth.ModeProxy, Users: directory, Tokens: directory},
	})

	var got meResponse
	graphqltest.New(h).AsUser(testdata.Eins.Mail).MustQuery(t, meQuery, nil, &got)

	if got.Me == nil {
		t.Fatal("an unknown grant made the person unreadable")
	}
	if len(got.Me.Roles) != 1 || got.Me.Roles[0] != string(policy.RoleLecturer) {
		t.Errorf("roles are %v — an unrecognised grant reached the client", got.Me.Roles)
	}
}

// TestAnExpiredGrantLapsesOnBothDoors.
//
// A grant with an expiry is how an administrator who genuinely has to look at something
// grants themselves DEANS_OFFICE — visibly, and for an hour. The threshold only works if
// stepping back over it costs nobody anything, which means the expiry has to end the grant on
// every route in and not only in the browser.
//
// It did not, for a while: the two doors resolve roles through different queries, and the
// filter that arrived with person_role.expires_at reached only one of them. EachDoor is what
// makes that a red test rather than something noticed in a token's answer in February.
func TestAnExpiredGrantLapsesOnBothDoors(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	storetest.SeedPerson(t, s, testdata.Eins, string(policy.RoleLecturer))

	parsed, err := auth.ParseToken(testdata.Eins.Token)
	if err != nil {
		t.Fatalf("fixture token does not parse: %v", err)
	}
	storetest.SeedToken(t, s, testdata.Eins, auth.HashSecret(parsed.Secret), storetest.TokenOptions{})

	// Granted two hours ago, ran out an hour ago — a grant that was real and is over, which is
	// the state the column exists to represent.
	if _, err := s.Pool.Exec(t.Context(),
		`INSERT INTO person_role (person_id, role, granted_at, expires_at)
		 VALUES ($1, 'DEANS_OFFICE', now() - interval '2 hours', now() - interval '1 hour')`,
		testdata.Eins.ID()); err != nil {
		t.Fatalf("cannot seed the expired grant: %v", err)
	}

	directory := store.NewDirectory(s.Pool)
	h := bootstrap.Handler(bootstrap.Options{
		Build: buildinfo.Info{Version: "test"},
		Auth:  auth.Config{Mode: auth.ModeProxy, Users: directory, Tokens: directory},
	})

	graphqltest.EachDoor(t, h, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var got meResponse
			c.MustQuery(t, meQuery, nil, &got)

			if got.Me == nil {
				t.Fatal("a valid credential resolved to nobody")
			}
			for _, r := range got.Me.Roles {
				if r == string(policy.RoleDeansOffice) {
					t.Errorf("roles are %v — an expired DEANS_OFFICE grant is still in "+
						"force on the %s door", got.Me.Roles, c.Door().Name)
				}
			}
			// The live grant has to survive: an expiry filter that also drops unexpiring
			// grants would lock everybody out instead of letting one grant lapse.
			if len(got.Me.Roles) != 1 || got.Me.Roles[0] != string(policy.RoleLecturer) {
				t.Errorf("roles are %v, want only the unexpiring LECTURER grant", got.Me.Roles)
			}
		})
}
