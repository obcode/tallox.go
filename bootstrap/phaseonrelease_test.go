package bootstrap_test

import (
	"testing"

	"github.com/obcode/tallox.go/internal/graphqltest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// The first release moves the semester on to the wishes (2026-11-10), through the API.
//
// Three doors, because setDemandComplete is a write that a plain token reaches as well as a
// browser and a full-reach token: the step must follow the release through every one of them, and
// a refused release must move nothing through any of them.

const releaseStateQuery = `query($s: String!) {
	semester(code: $s) { phase demandRelease { released planned } }
	demandCompletions(semester: $s) { programme { code } }
}`

type releaseState struct {
	Semester struct {
		Phase         string
		DemandRelease struct{ Released, Planned int }
	}
	DemandCompletions []struct{ Programme struct{ Code string } }
}

// backToDemandPlanning undoes the fixture's release and phase, so each door starts from the
// state the rule is about.
func (f wishFixture) backToDemandPlanning(t *testing.T) {
	t.Helper()

	if _, err := f.schema.Pool.Exec(t.Context(),
		`DELETE FROM demand_completion WHERE semester_id = (SELECT id FROM semester WHERE code = $1)`,
		f.semester); err != nil {
		t.Fatalf("cannot clear the releases: %v", err)
	}
	if _, err := f.schema.Pool.Exec(t.Context(),
		`UPDATE semester SET phase = 'DEMAND_PLANNING' WHERE code = $1`, f.semester); err != nil {
		t.Fatalf("cannot switch the semester back: %v", err)
	}
}

func (f wishFixture) releaseState(t *testing.T, c *graphqltest.Client) releaseState {
	t.Helper()

	var out releaseState
	c.MustQuery(t, releaseStateQuery, map[string]any{"s": f.semester}, &out)
	return out
}

// threeDoors are the same persona through the browser, a plain token and a full-reach token.
func (f wishFixture) threeDoors(t *testing.T, who testdata.Persona) map[string]*graphqltest.Client {
	t.Helper()

	base := graphqltest.New(f.handler)
	return map[string]*graphqltest.Client{
		"browser":    base.AsUser(who.Mail).On(graphqltest.Browser),
		"token":      base.WithToken(who.Token).On(graphqltest.Token),
		"full reach": base.WithToken(fullReachToken(t, f.schema, who)).On(graphqltest.Token),
	}
}

// TestReleasingTheDemandStartsTheWishesThroughEveryDoor is the step itself.
func TestReleasingTheDemandStartsTheWishesThroughEveryDoor(t *testing.T) {
	t.Parallel()

	f := wishHandler(t,
		grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}},
	)
	f.leadProgramme(t, testdata.Vier, f.programme)

	for door, c := range f.threeDoors(t, testdata.Vier) {
		t.Run(door, func(t *testing.T) {
			f.backToDemandPlanning(t)

			c.MustQuery(t, setDemandCompleteMutation,
				map[string]any{"s": f.semester, "p": f.programme, "c": true}, &struct {
					SetDemandComplete struct{ Semester string }
				}{})

			state := f.releaseState(t, c)
			if state.Semester.Phase != "WISHES" {
				t.Errorf("after the first release the semester is in %s, want WISHES",
					state.Semester.Phase)
			}
			if state.Semester.DemandRelease.Released != 1 {
				t.Errorf("demandRelease counts %d releases, want 1",
					state.Semester.DemandRelease.Released)
			}

			// Withdrawing does not move it back.
			c.MustQuery(t, setDemandCompleteMutation,
				map[string]any{"s": f.semester, "p": f.programme, "c": false}, &struct {
					SetDemandComplete *struct{ Semester string }
				}{})
			state = f.releaseState(t, c)
			if state.Semester.Phase != "WISHES" {
				t.Errorf("withdrawing the release moved the semester to %s, want WISHES",
					state.Semester.Phase)
			}
			if state.Semester.DemandRelease.Released != 0 {
				t.Errorf("after withdrawing demandRelease counts %d, want 0",
					state.Semester.DemandRelease.Released)
			}
		})
	}
}

// TestARefusedReleaseMovesNothing: neither the marks nor the phase, through any door, and the
// refusal says nothing it should not.
func TestARefusedReleaseMovesNothing(t *testing.T) {
	t.Parallel()

	f := wishHandler(t,
		grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}},
		grants{testdata.Zwei, []string{"LECTURER"}},
	)
	f.leadProgramme(t, testdata.Vier, f.programme)
	f.backToDemandPlanning(t)

	for _, tc := range []struct {
		who       testdata.Persona
		programme string
	}{
		// A lecturer speaks for no programme.
		{testdata.Zwei, f.programme},
		// A lead speaks for their own and not the one next door.
		{testdata.Vier, f.otherProgramme},
	} {
		for door, c := range f.threeDoors(t, tc.who) {
			t.Run(tc.who.Name+"/"+tc.programme+"/"+door, func(t *testing.T) {
				resp := c.Do(t, setDemandCompleteMutation,
					map[string]any{"s": f.semester, "p": tc.programme, "c": true})
				assertRefusal(t, resp, "NOT_YOUR_PROGRAMME")
				for _, message := range resp.Messages() {
					graphqltest.AssertNoLeak(t, message, append(graphqltest.DatabaseNoise(),
						testdata.Mails(testdata.Others(tc.who))...)...)
				}

				state := f.releaseState(t, c)
				if state.Semester.Phase != "DEMAND_PLANNING" {
					t.Errorf("a refused release moved the semester to %s", state.Semester.Phase)
				}
				if len(state.DemandCompletions) != 0 || state.Semester.DemandRelease.Released != 0 {
					t.Errorf("a refused release left marks behind: %+v", state)
				}
			})
		}
	}
}

// TestALecturerReadsTheReleaseProgress: public like demandCompletions, through every door.
func TestALecturerReadsTheReleaseProgress(t *testing.T) {
	t.Parallel()

	f := wishHandler(t,
		grants{testdata.Zwei, []string{"LECTURER"}},
	)

	var planned int
	if err := f.schema.Pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM programme WHERE planning_status = 'PLANNED'`).Scan(&planned); err != nil {
		t.Fatalf("cannot count the planned programmes: %v", err)
	}

	for door, c := range f.threeDoors(t, testdata.Zwei) {
		t.Run(door, func(t *testing.T) {
			state := f.releaseState(t, c)
			got := state.Semester.DemandRelease
			if got.Released != 1 || got.Planned != planned {
				t.Errorf("demandRelease is %d of %d, want 1 of %d", got.Released, got.Planned, planned)
			}
		})
	}
}
