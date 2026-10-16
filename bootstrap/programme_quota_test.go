package bootstrap_test

import (
	"testing"

	"github.com/obcode/tallox.go/internal/auth"
	"github.com/obcode/tallox.go/internal/graphqltest"
	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// The programme quota through the API, through both doors.
//
// What is asserted here is who may enter it — the dean's office and nobody else, whichever door —
// and that the teaching load then reads its balance against half of it. The arithmetic of the
// balance is a domain test; the study year a quota lands in is a store test.

const setQuotaMutation = `mutation($p: String!, $y: Int!, $h: Float) {
	setProgrammeQuota(programme: $p, studyYear: $y, teachingHours: $h) {
		programme { code } studyYear teachingHours
	}
}`

const quotaLoadQuery = `query($s: String!, $y: Int!) {
	programmeQuotas(studyYear: $y) { programme { code } teachingHours }
	teachingLoad(semester: $s) {
		homeProgramme { code } teachingHours yearlyQuota semesterQuota balance
	}
}`

type quotaLoadAnswer struct {
	ProgrammeQuotas []struct {
		Programme     struct{ Code string }
		TeachingHours float64
	}
	TeachingLoad []struct {
		HomeProgramme struct{ Code string }
		TeachingHours float64
		YearlyQuota   *float64
		SemesterQuota *float64
		Balance       *float64
	}
}

// The dean's office enters the quota through either door, and everybody with an account reads
// the balance it produces — the same number at both doors.
func TestTheDeansOfficeEntersAQuotaAndTheLoadReadsAgainstIt(t *testing.T) {
	t.Parallel()

	f := demandHandler(t,
		map[string][]string{testdata.Vier.Mail: {storetest.FixtureProgrammeA}},
		grants{testdata.Vier, []string{"PROGRAMME_LEAD"}},
		grants{testdata.Fuenf, []string{"DEANS_OFFICE"}},
		grants{testdata.Eins, []string{"LECTURER"}})

	lead := graphqltest.New(f.handler).AsUser(testdata.Vier.Mail).On(graphqltest.Browser)
	lead.MustQuery(t, declareMutation, declareInput(f, storetest.FixtureProgrammeA, "2027-SS", ""), nil)

	graphqltest.EachDoor(t, f.handler, testdata.Fuenf.Mail, testdata.Fuenf.Token,
		func(t *testing.T, c *graphqltest.Client) {
			c.MustQuery(t, setQuotaMutation,
				map[string]any{"p": "pa", "y": 2026, "h": 10.0}, nil)
		})

	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var out quotaLoadAnswer
			c.MustQuery(t, quotaLoadQuery, map[string]any{"s": "2027-SS", "y": 2026}, &out)

			if len(out.ProgrammeQuotas) != 1 || out.ProgrammeQuotas[0].TeachingHours != 10 {
				t.Errorf("2026 holds %+v, want PA's quota of 10", out.ProgrammeQuotas)
			}
			for _, l := range out.TeachingLoad {
				switch l.HomeProgramme.Code {
				case storetest.FixtureProgrammeA:
					// One 2+2-hour cohort costs 4 of the 5 the summer may spend.
					if l.YearlyQuota == nil || *l.YearlyQuota != 10 || l.SemesterQuota == nil ||
						*l.SemesterQuota != 5 || l.Balance == nil || *l.Balance != 1 {
						t.Errorf("PA reads quota %v, semester %v, balance %v; want 10, 5 and 1",
							l.YearlyQuota, l.SemesterQuota, l.Balance)
					}
				case storetest.FixtureProgrammeB:
					if l.YearlyQuota != nil || l.Balance != nil {
						t.Errorf("PB has a balance without a quota: %v", l.Balance)
					}
				}
			}
		})

	// And null takes it back.
	graphqltest.New(f.handler).WithToken(testdata.Fuenf.Token).MustQuery(t, setQuotaMutation,
		map[string]any{"p": "PA", "y": 2026, "h": nil}, nil)
	var out quotaLoadAnswer
	graphqltest.New(f.handler).AsUser(testdata.Eins.Mail).MustQuery(t, quotaLoadQuery,
		map[string]any{"s": "2027-SS", "y": 2026}, &out)
	if len(out.ProgrammeQuotas) != 0 {
		t.Errorf("a quota taken back is still listed: %+v", out.ProgrammeQuotas)
	}
}

// The quota is the line a programme is measured against, so the programme lead may not move it,
// nor the administrator, nor the dean's office while narrowed to a programme lead's view — and a
// refused write leaves nothing behind.
func TestOnlyTheDeansOfficeEntersAQuota(t *testing.T) {
	t.Parallel()

	f := demandHandler(t,
		map[string][]string{testdata.Vier.Mail: {storetest.FixtureProgrammeA}},
		grants{testdata.Vier, []string{"PROGRAMME_LEAD"}},
		grants{testdata.Sechs, []string{"ADMIN"}},
		grants{testdata.Fuenf, []string{"DEANS_OFFICE", "PROGRAMME_LEAD"}})

	input := map[string]any{"p": storetest.FixtureProgrammeA, "y": 2026, "h": 99.0}

	for _, who := range []struct {
		name string
		mail string
		tok  string
	}{
		{"the programme lead", testdata.Vier.Mail, testdata.Vier.Token},
		{"the administrator", testdata.Sechs.Mail, testdata.Sechs.Token},
	} {
		t.Run(who.name, func(t *testing.T) {
			graphqltest.EachDoor(t, f.handler, who.mail, who.tok,
				func(t *testing.T, c *graphqltest.Client) {
					assertRefusal(t, c.Do(t, setQuotaMutation, input), "FORBIDDEN")
				})
		})
	}

	t.Run("the dean's office, narrowed to programme lead", func(t *testing.T) {
		resp := graphqltest.New(f.handler).AsUser(testdata.Fuenf.Mail).
			WithHeader(auth.HeaderAssumeRoles, string(policy.RoleProgrammeLead)).
			Do(t, setQuotaMutation, input)
		assertRefusal(t, resp, "FORBIDDEN")
	})

	var out quotaLoadAnswer
	graphqltest.New(f.handler).AsUser(testdata.Fuenf.Mail).MustQuery(t, quotaLoadQuery,
		map[string]any{"s": "2027-SS", "y": 2026}, &out)
	if len(out.ProgrammeQuotas) != 0 {
		t.Errorf("a refused write left %+v", out.ProgrammeQuotas)
	}
}

// What the service refuses, it refuses by name.
func TestAQuotaIsRefusedByName(t *testing.T) {
	t.Parallel()

	f := demandHandler(t, nil, grants{testdata.Fuenf, []string{"DEANS_OFFICE"}})
	c := graphqltest.New(f.handler).WithToken(testdata.Fuenf.Token)

	for _, tc := range []struct {
		name string
		vars map[string]any
		code string
	}{
		{"negative", map[string]any{"p": "PA", "y": 2026, "h": -1.0}, "QUOTA_INVALID"},
		{"a year nobody plans", map[string]any{"p": "PA", "y": 2090, "h": 1.0}, "STUDY_YEAR_OUT_OF_RANGE"},
		{"an unknown programme", map[string]any{"p": "NOPE", "y": 2026, "h": 1.0}, "PROGRAMME_NOT_FOUND"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertRefusal(t, c.Do(t, setQuotaMutation, tc.vars), tc.code)
		})
	}
}

// The quota is planning data, so a token narrowed to another area reaches neither half.
func TestTheQuotaNeedsThePlanningScope(t *testing.T) {
	t.Parallel()

	h := scopedHandler(t, testdata.Fuenf, []string{"PROFILE:READ"})
	c := graphqltest.New(h).WithToken(testdata.Fuenf.Token)

	assertInsufficientScope(t,
		c.Do(t, `query { programmeQuotas(studyYear: 2026) { studyYear } }`, nil),
		policy.Scope{Area: policy.ScopeAreaPlanning, Verb: policy.ScopeVerbRead})
	assertInsufficientScope(t,
		c.Do(t, setQuotaMutation, map[string]any{"p": "PA", "y": 2026, "h": 1.0}),
		policy.Scope{Area: policy.ScopeAreaPlanning, Verb: policy.ScopeVerbWrite})
}

// The import filter through the API: PA's module declared for PB is what PB takes from
// elsewhere, at both doors.
func TestTheForeignHomeFilterThroughBothDoors(t *testing.T) {
	t.Parallel()

	f := demandHandler(t,
		map[string][]string{testdata.Vier.Mail: {storetest.FixtureProgrammeA, storetest.FixtureProgrammeB}},
		grants{testdata.Vier, []string{"PROGRAMME_LEAD"}},
		grants{testdata.Eins, []string{"LECTURER"}})

	lead := graphqltest.New(f.handler).AsUser(testdata.Vier.Mail).On(graphqltest.Browser)
	lead.MustQuery(t, declareMutation, declareInput(f, storetest.FixtureProgrammeA, "2027-SS", ""), nil)
	lead.MustQuery(t, declareMutation, declareInput(f, storetest.FixtureProgrammeB, "2027-SS", ""), nil)

	const q = `query($p: String!) {
		courseInstances(semester: "2027-SS", programme: $p, foreignHome: true) {
			programme { code } module { homeProgramme { code } }
		}
	}`
	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var out struct {
				CourseInstances []struct {
					Programme struct{ Code string }
					Module    struct{ HomeProgramme struct{ Code string } }
				}
			}
			c.MustQuery(t, q, map[string]any{"p": storetest.FixtureProgrammeA}, &out)
			if len(out.CourseInstances) != 0 {
				t.Errorf("PA imports %+v", out.CourseInstances)
			}
			c.MustQuery(t, q, map[string]any{"p": storetest.FixtureProgrammeB}, &out)
			if len(out.CourseInstances) != 1 ||
				out.CourseInstances[0].Module.HomeProgramme.Code != storetest.FixtureProgrammeA {
				t.Errorf("PB imports %+v, want PA's module", out.CourseInstances)
			}
		})
}
