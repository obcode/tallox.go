package bootstrap_test

import (
	"slices"
	"testing"

	"github.com/obcode/tallox.go/internal/graphqltest"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

const setDegreeMutation = `mutation($code: String!, $degree: Degree) {
	setProgrammeDegree(code: $code, degree: $degree) { code degree }
}`

// orNull spells a nullable answer for a failure message.
func orNull(s *string) string {
	if s == nil {
		return "null"
	}
	return *s
}

type degreeAnswer struct {
	SetProgrammeDegree struct {
		Code   string
		Degree *string
	}
}

// Recording what a programme leads to is the dean's office's, through both doors, and `null`
// takes it back.
func TestSettingTheDegreeIsTheDeansOfficeThroughBothDoors(t *testing.T) {
	t.Parallel()

	f := catalogueHandler(t, nil, grants{testdata.Fuenf, []string{"DEANS_OFFICE"}})

	graphqltest.EachDoor(t, f.handler, testdata.Fuenf.Mail, testdata.Fuenf.Token,
		func(t *testing.T, c *graphqltest.Client) {
			// A programme per door: the two runs share a database.
			code := storetest.FixtureProgrammeB
			if c.Door() == graphqltest.Token {
				code = storetest.FixtureProgrammeR
			}

			var out degreeAnswer
			c.MustQuery(t, setDegreeMutation, map[string]any{"code": code, "degree": "MASTER"}, &out)
			if d := out.SetProgrammeDegree.Degree; d == nil || *d != "MASTER" {
				t.Errorf("the degree reads %s, want MASTER", orNull(d))
			}

			// And every reader sees it on the programme list.
			var list struct {
				Programmes []struct {
					Code   string
					Degree *string
				}
			}
			c.MustQuery(t, `{ programmes(includeUnplanned: true) { code degree } }`, nil, &list)
			for _, p := range list.Programmes {
				if p.Code == code && (p.Degree == nil || *p.Degree != "MASTER") {
					t.Errorf("the programme list reads %s for %s, want MASTER", orNull(p.Degree), code)
				}
			}

			c.MustQuery(t, setDegreeMutation, map[string]any{"code": code, "degree": nil}, &out)
			if out.SetProgrammeDegree.Degree != nil {
				t.Errorf("taking it back left %q", *out.SetProgrammeDegree.Degree)
			}
		})
}

// Two writes in one operation: the second answer is not the first one's cached lookup.
func TestTheDegreeIsReadAfterTheWriteInOneOperation(t *testing.T) {
	t.Parallel()

	f := catalogueHandler(t, nil, grants{testdata.Fuenf, []string{"DEANS_OFFICE"}})
	c := graphqltest.New(f.handler).AsUser(testdata.Fuenf.Mail)

	var out struct {
		A struct{ Degree *string }
		B struct{ Degree *string }
	}
	c.MustQuery(t, `mutation($code: String!) {
		a: setProgrammeDegree(code: $code, degree: MASTER) { degree }
		b: setProgrammeDegree(code: $code, degree: BACHELOR) { degree }
	}`, map[string]any{"code": storetest.FixtureProgrammeA}, &out)

	if out.A.Degree == nil || *out.A.Degree != "MASTER" {
		t.Errorf("the first answer reads %s, want MASTER", orNull(out.A.Degree))
	}
	if out.B.Degree == nil || *out.B.Degree != "BACHELOR" {
		t.Errorf("the second answer reads %s, want BACHELOR — a stale lookup",
			orNull(out.B.Degree))
	}
}

// A programme lead knows their programme's degree best and still does not record it: it decides
// what other people's filtered screens show. Neither does an administrator.
func TestWhoMayNotSetTheDegree(t *testing.T) {
	t.Parallel()

	f := catalogueHandler(t, nil,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Vier, []string{"PROGRAMME_LEAD"}},
		grants{testdata.Sechs, []string{"ADMIN"}})

	for _, who := range []testdata.Persona{testdata.Eins, testdata.Vier, testdata.Sechs} {
		refusal := graphqltest.New(f.handler).AsUser(who.Mail).
			Do(t, setDegreeMutation,
				map[string]any{"code": storetest.FixtureProgrammeB, "degree": "MASTER"})
		if code := errorCode(t, refusal); code != "FORBIDDEN" {
			t.Errorf("%s setting the degree gave %s, want FORBIDDEN", who.Name, code)
		}
	}
}

func TestTheDegreeRefusesAnUnknownProgramme(t *testing.T) {
	t.Parallel()

	f := catalogueHandler(t, nil, grants{testdata.Fuenf, []string{"DEANS_OFFICE"}})
	c := graphqltest.New(f.handler).AsUser(testdata.Fuenf.Mail)

	refusal := c.Do(t, setDegreeMutation, map[string]any{"code": "GIBTESNICHT", "degree": "MASTER"})
	if code := errorCode(t, refusal); code != "PROGRAMME_NOT_FOUND" {
		t.Errorf("an unknown programme gave %s, want PROGRAMME_NOT_FOUND", code)
	}
}

// The catalogue's degree filter, through both doors: PB is a master's, and the one module that
// counts in it is all a master's filter finds.
func TestTheCatalogueFiltersByDegreeThroughBothDoors(t *testing.T) {
	t.Parallel()

	f := catalogueHandler(t, nil,
		grants{testdata.Fuenf, []string{"DEANS_OFFICE"}},
		grants{testdata.Eins, []string{"LECTURER"}})
	dean := graphqltest.New(f.handler).AsUser(testdata.Fuenf.Mail)
	var out degreeAnswer
	dean.MustQuery(t, setDegreeMutation,
		map[string]any{"code": storetest.FixtureProgrammeB, "degree": "MASTER"}, &out)

	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var masters, bachelors struct {
				Modules []struct{ Name string }
			}
			c.MustQuery(t, modulesQuery, map[string]any{"f": map[string]any{"degree": "MASTER"}},
				&masters)
			if len(masters.Modules) != 1 {
				t.Errorf("MASTER found %d modules, want the one that counts in PB",
					len(masters.Modules))
			}

			// Nobody said what PA leads to, so a bachelor's filter finds nothing yet — not
			// everything.
			c.MustQuery(t, modulesQuery, map[string]any{"f": map[string]any{"degree": "BACHELOR"}},
				&bachelors)
			if len(bachelors.Modules) != 0 {
				t.Errorf("BACHELOR found %d modules with no bachelor's recorded, want none",
					len(bachelors.Modules))
			}
		})
}

// The demand narrows by degree through both doors, and the degree is the same wherever a
// programme turns up in the answer — on the cohort and as a module's home.
func TestTheDemandFiltersByDegreeThroughBothDoors(t *testing.T) {
	t.Parallel()

	f := demandHandler(t, nil,
		grants{testdata.Vier, []string{"DEANS_OFFICE"}},
		grants{testdata.Eins, []string{"LECTURER"}})

	dean := graphqltest.New(f.handler).AsUser(testdata.Vier.Mail).On(graphqltest.Browser)
	for _, code := range []string{storetest.FixtureProgrammeA, storetest.FixtureProgrammeB} {
		var declared struct {
			DeclareCourseInstance struct{ ID string }
		}
		dean.MustQuery(t, declareMutation, declareInput(f, code, "2027-SS", ""), &declared)
	}
	var out degreeAnswer
	dean.MustQuery(t, setDegreeMutation,
		map[string]any{"code": storetest.FixtureProgrammeA, "degree": "BACHELOR"}, &out)
	dean.MustQuery(t, setDegreeMutation,
		map[string]any{"code": storetest.FixtureProgrammeB, "degree": "MASTER"}, &out)

	const query = `query($s: String!, $d: Degree) {
		courseInstances(semester: $s, degree: $d) {
			programme { code degree }
			module { homeProgramme { code degree } }
		}
	}`
	type answer struct {
		CourseInstances []struct {
			Programme struct {
				Code   string
				Degree *string
			}
			Module struct {
				HomeProgramme struct {
					Code   string
					Degree *string
				}
			}
		}
	}

	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var all answer
			c.MustQuery(t, query, map[string]any{"s": "2027-SS"}, &all)
			var codes []string
			for _, i := range all.CourseInstances {
				codes = append(codes, i.Programme.Code)
				want := map[string]string{
					storetest.FixtureProgrammeA: "BACHELOR",
					storetest.FixtureProgrammeB: "MASTER",
				}[i.Programme.Code]
				if i.Programme.Degree == nil || *i.Programme.Degree != want {
					t.Errorf("the cohort of %s reads degree %s, want %s",
						i.Programme.Code, orNull(i.Programme.Degree), want)
				}
				// Module 101 is at home in PA.
				if d := i.Module.HomeProgramme.Degree; d == nil || *d != "BACHELOR" {
					t.Errorf("the module's home %s reads degree %s, want BACHELOR",
						i.Module.HomeProgramme.Code, orNull(d))
				}
			}
			slices.Sort(codes)
			if !slices.Equal(codes, []string{storetest.FixtureProgrammeA, storetest.FixtureProgrammeB}) {
				t.Fatalf("unfiltered the demand holds %v, want both cohorts", codes)
			}

			var masters answer
			c.MustQuery(t, query, map[string]any{"s": "2027-SS", "d": "MASTER"}, &masters)
			if len(masters.CourseInstances) != 1 ||
				masters.CourseInstances[0].Programme.Code != storetest.FixtureProgrammeB {
				t.Errorf("MASTER found %+v, want only PB's cohort", masters.CourseInstances)
			}
		})
}
