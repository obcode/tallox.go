package bootstrap_test

import (
	"testing"

	"github.com/obcode/tallox.go/internal/graphqltest"
	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// The teaching load through the API.
//
// A sum over the demand, which is not confidential, so it answers at both doors to anybody with
// an account — an evaluation script is who it is for. The arithmetic is asserted against the
// database in internal/store; what is asserted here is that the doors agree and that the token's
// scope still narrows it.

const teachingLoadQuery = `query($s: String!) {
	teachingLoad(semester: $s) {
		programme { code planningStatus }
		teachingHours ownHours instanceCount fwpInstanceCount fwpHours partsWithoutHours
		shares { homeProgramme { code } teachingHours instanceCount }
	}
}`

type teachingLoadAnswer struct {
	TeachingLoad []struct {
		Programme struct {
			Code           string
			PlanningStatus string
		}
		TeachingHours     float64
		OwnHours          float64
		InstanceCount     int
		FwpInstanceCount  int
		FwpHours          float64
		PartsWithoutHours int
		Shares            []struct {
			HomeProgramme struct{ Code string }
			TeachingHours float64
			InstanceCount int
		}
	}
}

// PB's cohort joins PA's event, so PA holds the teaching and pays; a lecturer reads the same figure
// through either door.
func TestTheTeachingLoadIsReadableThroughBothDoors(t *testing.T) {
	t.Parallel()

	f := demandHandler(t,
		map[string][]string{testdata.Vier.Mail: {storetest.FixtureProgrammeA, storetest.FixtureProgrammeB}},
		grants{testdata.Vier, []string{"PROGRAMME_LEAD"}},
		grants{testdata.Eins, []string{"LECTURER"}})

	lead := graphqltest.New(f.handler).AsUser(testdata.Vier.Mail).On(graphqltest.Browser)
	lead.MustQuery(t, declareMutation, declareInput(f, storetest.FixtureProgrammeA, "2027-SS", ""), nil)
	lead.MustQuery(t, declareMutation, declareInput(f, storetest.FixtureProgrammeB, "2027-SS", ""), nil)

	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var out teachingLoadAnswer
			c.MustQuery(t, teachingLoadQuery, map[string]any{"s": "2027-SS"}, &out)

			found := false
			for _, l := range out.TeachingLoad {
				switch l.Programme.Code {
				case storetest.FixtureProgrammeA:
					found = true
					// Declared beside PA, PB's cohort is held together with PA's event: PA holds
					// the teaching and pays for it, and PB's covered cohort costs nothing.
					if l.TeachingHours != 4 || l.OwnHours != 4 || l.InstanceCount != 1 {
						t.Errorf("PA pays %v hours, %v of them for its own modules, in %d instances; "+
							"want 4, 4 and 1", l.TeachingHours, l.OwnHours, l.InstanceCount)
					}
					if len(l.Shares) != 1 || l.Shares[0].HomeProgramme.Code != storetest.FixtureProgrammeA {
						t.Errorf("PA's load is split into %+v, want its own modules only", l.Shares)
					}
				case storetest.FixtureProgrammeB:
					if l.TeachingHours != 0 || l.InstanceCount != 1 {
						t.Errorf("PB's covered cohort costs %v hours in %d instances, want 0 and 1",
							l.TeachingHours, l.InstanceCount)
					}
				}
			}
			if !found {
				t.Fatalf("the teaching load names no entry for PA:\n%+v", out)
			}
		})
}

// The figure is planning data, so a token narrowed to another area does not reach it.
func TestTheTeachingLoadNeedsThePlanningScope(t *testing.T) {
	t.Parallel()

	h := scopedHandler(t, testdata.Eins, []string{"PROFILE:READ"})

	resp := graphqltest.New(h).WithToken(testdata.Eins.Token).
		Do(t, teachingLoadQuery, map[string]any{"s": "2027-SS"})
	assertInsufficientScope(t, resp,
		policy.Scope{Area: policy.ScopeAreaPlanning, Verb: policy.ScopeVerbRead})
}

// A semester code that is not one is refused by name, the way every other read refuses it.
func TestTheTeachingLoadRefusesAnInvalidSemester(t *testing.T) {
	t.Parallel()

	f := demandHandler(t, nil, grants{testdata.Eins, []string{"LECTURER"}})

	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			resp := c.Do(t, teachingLoadQuery, map[string]any{"s": "Sommer 27"})
			if code := errorCode(t, resp); code != "SEMESTER_CODE_INVALID" {
				t.Errorf("an invalid semester was answered with %q, want SEMESTER_CODE_INVALID", code)
			}
		})
}
