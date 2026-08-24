package bootstrap_test

import (
	"net/http"
	"testing"
	"time"

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
	semestersQuery = `{ semesters { code phase reachablePhases wishesPublishedAt decidedAt } }`
	semesterQuery  = `query ($code: String!) {
		semester(code: $code) { code phase decidedAt }
	}`
	advancePhase = `mutation ($code: String!, $to: Phase!) {
		advanceSemesterPhase(code: $code, to: $to) { code phase reachablePhases }
	}`
	publishWishes = `mutation ($code: String!) {
		publishWishes(code: $code) { code wishesPublishedAt }
	}`
)

type semesterEntry struct {
	Code              string   `json:"code"`
	Phase             string   `json:"phase"`
	ReachablePhases   []string `json:"reachablePhases"`
	WishesPublishedAt *string  `json:"wishesPublishedAt"`
	DecidedAt         *string  `json:"decidedAt"`
}

type semesterList struct {
	Semesters []semesterEntry `json:"semesters"`
}

// find picks one semester out of the list, which now holds the window from the calendar as
// well as everything anybody has decided something about.
func find(t *testing.T, list semesterList, code string) semesterEntry {
	t.Helper()

	for _, s := range list.Semesters {
		if s.Code == code {
			return s
		}
	}

	got := make([]string, 0, len(list.Semesters))
	for _, s := range list.Semesters {
		got = append(got, s.Code)
	}
	t.Fatalf("%s is not in the list: %v", code, got)
	return semesterEntry{}
}

// untouchedAhead is a semester nobody has decided anything about, far enough forward to be in
// the list whatever "now" means.
//
// Not currentSemester() any more, and that is the change the planning mark made: the list
// starts at the semester the faculty is planning, and the semester we are sitting in is behind
// it. What the assertion below is about — a semester is there without anybody setting it up —
// is unchanged, and forward is where the untouched ones now are.
func untouchedAhead() string {
	// Newest first, so [0] is three semesters out: past the seeded planning semester and well
	// inside the window the calendar offers.
	return domain.SemestersAround(time.Now(), 0, 3)[0]
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
		Planning: domain.NewSemesterService(store.NewSemesters(s.Pool), nil),
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

// decideAbout puts a semester into a phase through the API, as the dean's office.
//
// Nothing "creates" the semester here — it is there either way. What this does is record a
// decision about it, which is also what puts a row behind it, and several tests below need a
// semester that has one.
func decideAbout(t *testing.T, h http.Handler, code string, phase policy.Phase) {
	t.Helper()

	graphqltest.New(h).AsUser(testdata.Fuenf.Mail).
		MustQuery(t, advancePhase, map[string]any{"code": code, "to": string(phase)}, nil)
}

func deansOffice() grants {
	return grants{who: testdata.Fuenf, roles: []string{string(policy.RoleDeansOffice)}}
}

func lecturer() grants {
	return grants{who: testdata.Eins, roles: []string{string(policy.RoleLecturer)}}
}

// TestSemestersAreThereWithoutAnybodySettingThemUp is the change stated as an assertion.
//
// A database in which nobody has ever done anything still answers with semesters, in the phase
// every untouched semester is in, through both doors. The alternative — an empty list until
// somebody with the right role creates a row — is the shape this replaced, and it made the
// first step of the whole process an administrative act.
func TestSemestersAreThereWithoutAnybodySettingThemUp(t *testing.T) {
	t.Parallel()

	h := planningHandler(t, lecturer())

	graphqltest.EachDoor(t, h, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var out semesterList
			c.MustQuery(t, semestersQuery, nil, &out)

			now := find(t, out, untouchedAhead())
			if now.Phase != string(policy.PhaseDemandPlanning) {
				t.Errorf("phase = %s, want %s — an untouched semester is at the start of the "+
					"process", now.Phase, policy.PhaseDemandPlanning)
			}
			if now.WishesPublishedAt != nil {
				t.Errorf("wishesPublishedAt = %v, want null", *now.WishesPublishedAt)
			}
			if now.DecidedAt != nil {
				t.Errorf("decidedAt = %v, want null — nothing has been decided about it",
					*now.DecidedAt)
			}

			// The list reaches forward as well, which is what lets a programme lead plan for a
			// semester years out without asking anybody to open it first.
			if len(out.Semesters) < 4 {
				t.Errorf("the list holds %d semesters, want the window around now",
					len(out.Semesters))
			}
		})
}

// TestALecturerSeesTheProcessThroughBothDoors is the read rule, and the reason it is not
// narrower: "may I enter my wishes yet" is the phase.
func TestALecturerSeesTheProcessThroughBothDoors(t *testing.T) {
	t.Parallel()

	h := planningHandler(t, deansOffice(), lecturer())
	decideAbout(t, h, "2027-SS", policy.PhaseWishes)

	graphqltest.EachDoor(t, h, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var out semesterList
			c.MustQuery(t, semestersQuery, nil, &out)

			got := find(t, out, "2027-SS")
			if got.Phase != string(policy.PhaseWishes) {
				t.Errorf("phase = %s, want %s", got.Phase, policy.PhaseWishes)
			}
			if got.WishesPublishedAt != nil {
				t.Errorf("wishesPublishedAt = %v, want null", *got.WishesPublishedAt)
			}
			if got.DecidedAt == nil {
				t.Error("decidedAt is null although the phase was switched")
			}
		})
}

// TestAPlanFarAheadStaysOnTheList is the second half of what the list is made of.
//
// The window from the calendar reaches three years out; a decision recorded for a semester
// beyond it took a deliberate act, and it is exactly the one that must not quietly fall off
// the page. Ten years is inside what may be planned at all — see SEMESTER_OUT_OF_RANGE.
func TestAPlanFarAheadStaysOnTheList(t *testing.T) {
	t.Parallel()

	h := planningHandler(t, deansOffice())

	far := domain.SemestersAround(time.Now(), 0, 18)[0] // nine years out, past the window
	decideAbout(t, h, far, policy.PhaseWishes)

	var out semesterList
	graphqltest.New(h).AsUser(testdata.Fuenf.Mail).MustQuery(t, semestersQuery, nil, &out)

	if got := find(t, out, far); got.Phase != string(policy.PhaseWishes) {
		t.Errorf("phase of %s = %s, want %s", far, got.Phase, policy.PhaseWishes)
	}
}

// TestASemesterIsAlwaysAnAnswer covers the single lookup: there is no "no such semester" any
// more, because a code within reach names a stretch of time that will happen.
func TestASemesterIsAlwaysAnAnswer(t *testing.T) {
	t.Parallel()

	h := planningHandler(t, lecturer())
	c := graphqltest.New(h).AsUser(testdata.Eins.Mail)

	var out struct {
		Semester struct {
			Code      string  `json:"code"`
			Phase     string  `json:"phase"`
			DecidedAt *string `json:"decidedAt"`
		} `json:"semester"`
	}

	untouched := domain.SemestersAround(time.Now(), 0, 12)[0]
	c.MustQuery(t, semesterQuery, map[string]any{"code": untouched}, &out)

	if out.Semester.Code != untouched {
		t.Errorf("code = %q, want %q", out.Semester.Code, untouched)
	}
	if out.Semester.Phase != string(policy.PhaseDemandPlanning) {
		t.Errorf("phase = %s, want %s", out.Semester.Phase, policy.PhaseDemandPlanning)
	}
	if out.Semester.DecidedAt != nil {
		t.Errorf("decidedAt = %v, want null", *out.Semester.DecidedAt)
	}
}

func TestAnAnonymousCallerSeesNoSemesters(t *testing.T) {
	t.Parallel()

	h := planningHandler(t, deansOffice())

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
//
// What is *not* on trial here any more is bringing a semester into existence. Nobody does
// that, so there is no permission for it and a programme lead can plan for any semester within
// reach; what the dean's office alone decides is which phase it is in.
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
						// A different semester per door: the browser subtest may already have
						// moved 2027-SS on, and the second call would then fail on the
						// compare-and-set rather than on the rule under test.
						code = "2027-WS"
					}

					resp := c.Do(t, advancePhase, map[string]any{
						"code": code, "to": string(policy.PhaseWishes),
					})
					if tt.allowed {
						if resp.Failed() {
							t.Errorf("expected to switch the phase: %v", resp.Messages())
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
			AdvanceSemesterPhase struct {
				Code string `json:"code"`
			} `json:"advanceSemesterPhase"`
		}
		c.MustQuery(t, advancePhase, map[string]any{
			"code": " 2027-ss ", "to": string(policy.PhaseWishes),
		}, &out)

		if out.AdvanceSemesterPhase.Code != "2027-SS" {
			t.Errorf("code = %q, want 2027-SS", out.AdvanceSemesterPhase.Code)
		}
	})

	t.Run("a plausible-looking wrong shape is refused rather than guessed at", func(t *testing.T) {
		// "WS 2026" is the examination office's own spelling, and it is refused on purpose: it
		// is only unambiguous once one knows that the year names the term's start, and a guess
		// there produces a semester one year out that looks like one somebody chose.
		for _, code := range []string{"SS2027", "WS 2026", "2027S"} {
			resp := c.Do(t, advancePhase, map[string]any{
				"code": code, "to": string(policy.PhaseWishes),
			})
			assertRefusal(t, resp, "SEMESTER_CODE_INVALID")
		}
	})

	t.Run("a semester too far away to plan is refused, and told why", func(t *testing.T) {
		// The bound exists because nothing here can be taken back: there is no delete and no
		// un-publishing, so a mistyped year in somebody's script would otherwise leave a
		// decision about the year 9999 in the faculty's planning for good.
		for _, code := range []string{"9999-WS", "1999-SS"} {
			resp := c.Do(t, advancePhase, map[string]any{
				"code": code, "to": string(policy.PhaseWishes),
			})
			assertRefusal(t, resp, "SEMESTER_OUT_OF_RANGE")

			// Leak channel 2, practised where it is harmless: which semesters exist is not
			// confidential, and the same SQLSTATE on the wish write path reveals that a
			// colleague has already registered interest. The habit is what has to be in place
			// by then.
			for _, message := range resp.Messages() {
				graphqltest.AssertNoLeak(t, message, graphqltest.DatabaseNoise()...)
			}
		}
	})
}

// TestPhaseMovesOneStepAtATime is the adjacency rule through the API, in both directions.
func TestPhaseMovesOneStepAtATime(t *testing.T) {
	t.Parallel()

	h := planningHandler(t, deansOffice())
	c := graphqltest.New(h).AsUser(testdata.Fuenf.Mail)

	var out struct {
		AdvanceSemesterPhase struct {
			Phase           string   `json:"phase"`
			ReachablePhases []string `json:"reachablePhases"`
		} `json:"advanceSemesterPhase"`
	}

	t.Run("forward", func(t *testing.T) {
		c.MustQuery(t, advancePhase, map[string]any{
			"code": "2027-SS", "to": string(policy.PhaseWishes),
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
			"code": "2027-SS", "to": string(policy.PhaseFinal),
		})
		assertRefusal(t, resp, "PHASE_NOT_ADJACENT")
	})

	t.Run("backward, because reopening a plan is a normal thing to do", func(t *testing.T) {
		c.MustQuery(t, advancePhase, map[string]any{
			"code": "2027-SS", "to": string(policy.PhaseDemandPlanning),
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

	t.Run("token", func(t *testing.T) {
		t.Parallel()

		resp := graphqltest.New(h).WithToken(testdata.Fuenf.Token).
			Do(t, publishWishes, map[string]any{"code": "2027-SS"})
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
		c.MustQuery(t, publishWishes, map[string]any{"code": "2027-SS"}, &out)

		if out.PublishWishes.WishesPublishedAt == nil {
			t.Fatal("wishesPublishedAt is still null after publishing")
		}
		first := *out.PublishWishes.WishesPublishedAt

		// Twice is not an error, and the moment does not move.
		c.MustQuery(t, publishWishes, map[string]any{"code": "2027-SS"}, &out)
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

	resp := graphqltest.New(h).AsUser(testdata.Eins.Mail).
		Do(t, publishWishes, map[string]any{"code": "2027-SS"})
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
		Planning: domain.NewSemesterService(store.NewSemesters(s.Pool), nil),
	})

	c := graphqltest.New(h).WithToken(testdata.Fuenf.Token)

	t.Run("reads the planning", func(t *testing.T) {
		var out semesterList
		c.MustQuery(t, semestersQuery, nil, &out)
	})

	t.Run("and cannot write it, although the role would allow it", func(t *testing.T) {
		resp := c.Do(t, advancePhase, map[string]any{
			"code": "2027-SS", "to": string(policy.PhaseWishes),
		})
		assertRefusal(t, resp, "INSUFFICIENT_SCOPE")
	})

	t.Run("and cannot reach another area", func(t *testing.T) {
		resp := c.Do(t, `{ me { mail } }`, nil)
		assertRefusal(t, resp, "INSUFFICIENT_SCOPE")
	})
}
