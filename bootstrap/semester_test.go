package bootstrap_test

import (
	"net/http"
	"testing"

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
	semestersQuery = `{ semesters { id code phase reachablePhases wishesPublishedAt } }`
	createSemester = `mutation ($code: String!) { createSemester(code: $code) { id code phase } }`
	advancePhase   = `mutation ($id: ID!, $to: Phase!) {
		advanceSemesterPhase(id: $id, to: $to) { id phase reachablePhases }
	}`
	publishWishes = `mutation ($id: ID!) { publishWishes(id: $id) { id wishesPublishedAt } }`
)

type semesterList struct {
	Semesters []struct {
		ID                string   `json:"id"`
		Code              string   `json:"code"`
		Phase             string   `json:"phase"`
		ReachablePhases   []string `json:"reachablePhases"`
		WishesPublishedAt *string  `json:"wishesPublishedAt"`
	} `json:"semesters"`
}

// grants is a persona plus the roles the test depends on. Spelled out at every call site
// rather than attached to the persona, because which grant an assertion rests on is the thing
// a reader has to know and the thing that would otherwise be three files away.
type grants struct {
	who   testdata.Persona
	roles []string
}

// planningHandler seeds people with their roles and their tokens, and returns the handler
// Serve would build with the semester workflow wired.
func planningHandler(t *testing.T, people ...grants) http.Handler {
	t.Helper()

	s := storetest.New(t)
	for _, p := range people {
		storetest.SeedPerson(t, s, p.who, p.roles...)

		parsed, err := auth.ParseToken(p.who.Token)
		if err != nil {
			t.Fatalf("fixture token of %s does not parse: %v", p.who.Name, err)
		}
		storetest.SeedToken(t, s, p.who, auth.HashSecret(parsed.Secret), storetest.TokenOptions{
			Description: "semester test",
		})
	}

	directory := store.NewDirectory(s.Pool)

	return bootstrap.Handler(bootstrap.Options{
		Build:    buildinfo.Info{Version: "test"},
		Auth:     auth.Config{Mode: auth.ModeProxy, Users: directory, Tokens: directory},
		Planning: domain.NewSemesterService(store.NewSemesters(s.Pool)),
	})
}

// assertRefusal checks a refusal by its extensions code, never by its German sentence.
func assertRefusal(t *testing.T, resp graphqltest.Response, wantCode string) {
	t.Helper()

	if !resp.Failed() {
		t.Fatalf("expected %s, but the call succeeded:\n%s", wantCode, resp.Body)
	}
	for _, e := range resp.Errors {
		if code, _ := e.Extensions["code"].(string); code == wantCode {
			return
		}
	}
	t.Errorf("expected the code %s, got %v:\n%s", wantCode, resp.Messages(), resp.Body)
}

// seedSemester creates one through the API as the dean's office and returns its id.
func seedSemester(t *testing.T, h http.Handler, code string) string {
	t.Helper()

	var out struct {
		CreateSemester struct {
			ID string `json:"id"`
		} `json:"createSemester"`
	}
	graphqltest.New(h).AsUser(testdata.Fuenf.Mail).
		MustQuery(t, createSemester, map[string]any{"code": code}, &out)

	return out.CreateSemester.ID
}

func deansOffice() grants {
	return grants{who: testdata.Fuenf, roles: []string{string(policy.RoleDeansOffice)}}
}

func lecturer() grants {
	return grants{who: testdata.Eins, roles: []string{string(policy.RoleLecturer)}}
}

// TestALecturerSeesTheProcessThroughBothDoors is the read rule, and the reason it is not
// narrower: "may I enter my wishes yet" is the phase.
func TestALecturerSeesTheProcessThroughBothDoors(t *testing.T) {
	t.Parallel()

	h := planningHandler(t, deansOffice(), lecturer())
	seedSemester(t, h, "2027-SS")

	graphqltest.EachDoor(t, h, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var out semesterList
			c.MustQuery(t, semestersQuery, nil, &out)

			if len(out.Semesters) != 1 {
				t.Fatalf("got %d semesters, want 1", len(out.Semesters))
			}
			got := out.Semesters[0]
			if got.Code != "2027-SS" || got.Phase != string(policy.PhaseDemandPlanning) {
				t.Errorf("got %s in %s, want 2027-SS in %s",
					got.Code, got.Phase, policy.PhaseDemandPlanning)
			}
			if got.WishesPublishedAt != nil {
				t.Errorf("wishesPublishedAt = %v, want null", *got.WishesPublishedAt)
			}
		})
}

func TestAnAnonymousCallerSeesNoSemesters(t *testing.T) {
	t.Parallel()

	h := planningHandler(t, deansOffice())
	seedSemester(t, h, "2027-SS")

	for _, door := range []graphqltest.Door{graphqltest.Browser, graphqltest.Token} {
		t.Run(door.Name, func(t *testing.T) {
			t.Parallel()

			resp := graphqltest.New(h).On(door).Anonymous().Do(t, semestersQuery, nil)
			assertRefusal(t, resp, "FORBIDDEN")
		})
	}
}

// TestOnlyTheDeansOfficeAdministersSemesters covers the two absences that are decisions: an
// administrator runs the system and does not plan with it, and a programme lead declares
// demand within a phase rather than ending one.
func TestOnlyTheDeansOfficeAdministersSemesters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		who     grants
		allowed bool
	}{
		{name: "the dean's office", who: deansOffice(), allowed: true},
		{name: "a lecturer", who: lecturer()},
		{
			name: "an administrator",
			who:  grants{who: testdata.Sechs, roles: []string{string(policy.RoleAdmin)}},
		},
		{
			name: "a programme lead",
			who:  grants{who: testdata.Vier, roles: []string{string(policy.RoleProgrammeLead)}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := planningHandler(t, tt.who)

			// Both doors: the realistic failure is a rule someone adds for the browser and
			// forgets on the token path.
			graphqltest.EachDoor(t, h, tt.who.who.Mail, tt.who.who.Token,
				func(t *testing.T, c *graphqltest.Client) {
					code := "2027-SS"
					if c.Door() == graphqltest.Token {
						code = "2027-WS" // the browser subtest may already have taken 2027-SS
					}

					resp := c.Do(t, createSemester, map[string]any{"code": code})
					if tt.allowed {
						if resp.Failed() {
							t.Errorf("expected to create a semester: %v", resp.Messages())
						}
						return
					}
					assertRefusal(t, resp, "FORBIDDEN")
				})
		})
	}
}

func TestSemesterCodeIsValidatedAtTheAPI(t *testing.T) {
	t.Parallel()

	h := planningHandler(t, deansOffice())
	c := graphqltest.New(h).AsUser(testdata.Fuenf.Mail)

	t.Run("lower case is accepted, because a form does not shout", func(t *testing.T) {
		var out struct {
			CreateSemester struct {
				Code string `json:"code"`
			} `json:"createSemester"`
		}
		c.MustQuery(t, createSemester, map[string]any{"code": " 2027-ss "}, &out)

		if out.CreateSemester.Code != "2027-SS" {
			t.Errorf("code = %q, want 2027-SS", out.CreateSemester.Code)
		}
	})

	t.Run("a plausible-looking wrong shape is refused rather than guessed at", func(t *testing.T) {
		// "WS 2026" is the examination office's own spelling, and it is refused on purpose: it
		// is only unambiguous once one knows that the year names the term's start, and a guess
		// there produces a semester one year out that looks like one somebody chose.
		for _, code := range []string{"SS2027", "WS 2026", "2027S"} {
			resp := c.Do(t, createSemester, map[string]any{"code": code})
			assertRefusal(t, resp, "SEMESTER_CODE_INVALID")
		}
	})

	t.Run("a duplicate says so without the driver's vocabulary", func(t *testing.T) {
		c.MustQuery(t, createSemester, map[string]any{"code": "2028-SS"}, nil)

		resp := c.Do(t, createSemester, map[string]any{"code": "2028-SS"})
		assertRefusal(t, resp, "SEMESTER_EXISTS")

		// Leak channel 2. Harmless here — which semesters exist is not confidential — and the
		// same SQLSTATE on the wish write path reveals that a colleague has already registered
		// interest. The habit is what has to be in place by then.
		for _, message := range resp.Messages() {
			graphqltest.AssertNoLeak(t, message, graphqltest.DatabaseNoise()...)
		}
	})
}

// TestPhaseMovesOneStepAtATime is the adjacency rule through the API, in both directions.
func TestPhaseMovesOneStepAtATime(t *testing.T) {
	t.Parallel()

	h := planningHandler(t, deansOffice())
	id := seedSemester(t, h, "2027-SS")
	c := graphqltest.New(h).AsUser(testdata.Fuenf.Mail)

	var out struct {
		AdvanceSemesterPhase struct {
			Phase           string   `json:"phase"`
			ReachablePhases []string `json:"reachablePhases"`
		} `json:"advanceSemesterPhase"`
	}

	t.Run("forward", func(t *testing.T) {
		c.MustQuery(t, advancePhase, map[string]any{
			"id": id, "to": string(policy.PhaseWishes),
		}, &out)

		if out.AdvanceSemesterPhase.Phase != string(policy.PhaseWishes) {
			t.Fatalf("phase = %s, want %s", out.AdvanceSemesterPhase.Phase, policy.PhaseWishes)
		}

		// The buttons an interface would render. Computed from the same rule the mutation
		// enforces, so this list and the next subtest cannot disagree.
		want := map[string]bool{
			string(policy.PhaseDemandPlanning): true,
			string(policy.PhaseAssignment):     true,
		}
		if len(out.AdvanceSemesterPhase.ReachablePhases) != len(want) {
			t.Errorf("reachablePhases = %v, want %v",
				out.AdvanceSemesterPhase.ReachablePhases, want)
		}
		for _, p := range out.AdvanceSemesterPhase.ReachablePhases {
			if !want[p] {
				t.Errorf("reachablePhases offers %s, which is not one step away", p)
			}
		}
	})

	t.Run("skipping is refused", func(t *testing.T) {
		resp := c.Do(t, advancePhase, map[string]any{
			"id": id, "to": string(policy.PhaseFinal),
		})
		assertRefusal(t, resp, "PHASE_NOT_ADJACENT")
	})

	t.Run("backward, because reopening a plan is a normal thing to do", func(t *testing.T) {
		c.MustQuery(t, advancePhase, map[string]any{
			"id": id, "to": string(policy.PhaseDemandPlanning),
		}, &out)

		if out.AdvanceSemesterPhase.Phase != string(policy.PhaseDemandPlanning) {
			t.Errorf("phase = %s, want %s",
				out.AdvanceSemesterPhase.Phase, policy.PhaseDemandPlanning)
		}
	})
}

// TestPublishingIsBrowserOnly asserts a place where the doors are *supposed* to differ, so it
// says so per door rather than quietly covering one.
//
// The same person, the same role, the same semester — and the token is refused, because
// publishing cannot be undone and is the moment the confidentiality rule stops applying.
func TestPublishingIsBrowserOnly(t *testing.T) {
	t.Parallel()

	h := planningHandler(t, deansOffice())
	id := seedSemester(t, h, "2027-SS")

	t.Run("token", func(t *testing.T) {
		t.Parallel()

		resp := graphqltest.New(h).WithToken(testdata.Fuenf.Token).
			Do(t, publishWishes, map[string]any{"id": id})
		assertRefusal(t, resp, "INTERACTIVE_ONLY")
	})

	t.Run("browser", func(t *testing.T) {
		t.Parallel()

		var out struct {
			PublishWishes struct {
				WishesPublishedAt *string `json:"wishesPublishedAt"`
			} `json:"publishWishes"`
		}
		c := graphqltest.New(h).AsUser(testdata.Fuenf.Mail)
		c.MustQuery(t, publishWishes, map[string]any{"id": id}, &out)

		if out.PublishWishes.WishesPublishedAt == nil {
			t.Fatal("wishesPublishedAt is still null after publishing")
		}
		first := *out.PublishWishes.WishesPublishedAt

		// Twice is not an error, and the moment does not move.
		c.MustQuery(t, publishWishes, map[string]any{"id": id}, &out)
		if out.PublishWishes.WishesPublishedAt == nil ||
			*out.PublishWishes.WishesPublishedAt != first {
			t.Errorf("the second call moved the timestamp: %v then %v",
				first, out.PublishWishes.WishesPublishedAt)
		}
	})
}

func TestALecturerCannotPublish(t *testing.T) {
	t.Parallel()

	h := planningHandler(t, deansOffice(), lecturer())
	id := seedSemester(t, h, "2027-SS")

	resp := graphqltest.New(h).AsUser(testdata.Eins.Mail).
		Do(t, publishWishes, map[string]any{"id": id})
	assertRefusal(t, resp, "FORBIDDEN")
}

// TestAScopedTokenIsHeldToItsArea ties the two steps together: PLANNING is the first area for
// which narrowing a token expresses something somebody would want to say.
func TestAScopedTokenIsHeldToItsArea(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	storetest.SeedPerson(t, s, testdata.Fuenf, string(policy.RoleDeansOffice))

	parsed, err := auth.ParseToken(testdata.Fuenf.Token)
	if err != nil {
		t.Fatalf("fixture token does not parse: %v", err)
	}
	storetest.SeedToken(t, s, testdata.Fuenf, auth.HashSecret(parsed.Secret),
		storetest.TokenOptions{Scopes: []string{"PLANNING:READ"}})

	directory := store.NewDirectory(s.Pool)
	h := bootstrap.Handler(bootstrap.Options{
		Build:    buildinfo.Info{Version: "test"},
		Auth:     auth.Config{Mode: auth.ModeProxy, Users: directory, Tokens: directory},
		Planning: domain.NewSemesterService(store.NewSemesters(s.Pool)),
	})

	c := graphqltest.New(h).WithToken(testdata.Fuenf.Token)

	t.Run("reads the planning", func(t *testing.T) {
		var out semesterList
		c.MustQuery(t, semestersQuery, nil, &out)
	})

	t.Run("and cannot write it, although the role would allow it", func(t *testing.T) {
		resp := c.Do(t, createSemester, map[string]any{"code": "2027-SS"})
		assertRefusal(t, resp, "INSUFFICIENT_SCOPE")
	})

	t.Run("and cannot reach another area", func(t *testing.T) {
		resp := c.Do(t, `{ me { mail } }`, nil)
		assertRefusal(t, resp, "INSUFFICIENT_SCOPE")
	})
}
