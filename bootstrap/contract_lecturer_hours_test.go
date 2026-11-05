package bootstrap_test

import (
	"testing"

	"github.com/obcode/tallox.go/internal/graphqltest"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// ProgrammeTeachingLoad.contractLecturerHours through every door.
//
// The rest of the teaching load is a sum over the demand and answers at every door. This figure is
// a sum over assignments, so it follows the assignment rule: before publication only the programme's
// lead and the dean's office see it, in the browser or with a full-reach token — never through a
// plain token, and not as a partial figure for a subject group lead who reaches some of the
// programme's modules. After publication it is there for everybody who reads the row. The
// arithmetic is asserted against the database in internal/store.

const contractLecturerHoursQuery = `query($s: String!) {
	teachingLoad(semester: $s) { programme { code } teachingHours contractLecturerHours }
}`

// contractLecturerHoursOf reads the figure of the fixture's programme through one client, and
// asserts on the way that the rest of the row is unaffected.
func contractLecturerHoursOf(t *testing.T, c *graphqltest.Client, f assignmentAPIFixture) *float64 {
	t.Helper()

	var out struct {
		TeachingLoad []struct {
			Programme             struct{ Code string }
			TeachingHours         float64
			ContractLecturerHours *float64
		}
	}
	c.MustQuery(t, contractLecturerHoursQuery, map[string]any{"s": f.semester}, &out)
	for _, l := range out.TeachingLoad {
		if l.Programme.Code != f.programme {
			continue
		}
		if l.TeachingHours != 4 {
			t.Errorf("the programme's teaching is %v hours, want 4 at every door", l.TeachingHours)
		}
		return l.ContractLecturerHours
	}
	t.Fatalf("the teaching load names no entry for %s", f.programme)
	return nil
}

// contractFixture is the assignment fixture with the laboratory held by a lecturer on contract
// who has no account. Vier leads the programme, Drei the module's subject group, Eins lectures.
func contractFixture(t *testing.T) assignmentAPIFixture {
	t.Helper()

	f := assignmentHandler(t,
		grants{testdata.Vier, []string{"PROGRAMME_LEAD"}},
		grants{testdata.Drei, []string{"SUBJECT_GROUP_LEAD"}},
		grants{testdata.Eins, []string{"LECTURER"}})
	f.leadProgramme(t, testdata.Vier, f.programme)
	f.leadGroup(t, testdata.Drei, f.group)

	lba := teacherIDOf(t, f.schema, storetest.FixtureTeacherNotAdmitted)
	if _, err := f.schema.Pool.Exec(t.Context(),
		`UPDATE teacher SET is_lecturer_on_contract = true WHERE id = $1`, lba); err != nil {
		t.Fatalf("cannot flag the teacher: %v", err)
	}
	graphqltest.New(f.handler).AsUser(testdata.Drei.Mail).MustQuery(t,
		`mutation($p: ID!, $t: ID) { setAssignment(instancePartId: $p, teacherId: $t) { id } }`,
		map[string]any{"p": f.lab, "t": lba.String()}, nil)
	return f
}

func TestContractLecturerHoursFollowTheAssignmentRuleBeforePublication(t *testing.T) {
	t.Parallel()

	f := contractFixture(t)

	doors := func(p testdata.Persona) map[string]*graphqltest.Client {
		base := graphqltest.New(f.handler)
		return map[string]*graphqltest.Client{
			"browser":     base.AsUser(p.Mail),
			"plain token": base.WithToken(p.Token),
			"full reach":  base.WithToken(fullReachToken(t, f.schema, p)),
		}
	}

	for _, tc := range []struct {
		who  testdata.Persona
		role string
		want map[string]bool
	}{
		// The programme's lead: the browser and the full-reach token, never the plain token.
		{testdata.Vier, "programme lead",
			map[string]bool{"browser": true, "plain token": false, "full reach": true}},
		// Reaches the laboratory row, not the whole programme: no partial sum.
		{testdata.Drei, "subject group lead",
			map[string]bool{"browser": false, "plain token": false, "full reach": false}},
		{testdata.Eins, "lecturer",
			map[string]bool{"browser": false, "plain token": false, "full reach": false}},
	} {
		for door, c := range doors(tc.who) {
			t.Run(tc.role+" via "+door, func(t *testing.T) {
				got := contractLecturerHoursOf(t, c, f)
				switch {
				case tc.want[door] && (got == nil || *got != 2):
					t.Errorf("the figure is %v, want 2", deref(got))
				case !tc.want[door] && got != nil:
					t.Errorf("the figure is %v before publication, want null", *got)
				}
			})
		}
	}
}

// After publication the assignments are everybody's to read, and so is their sum — through both
// doors, and through a full-reach token as well.
func TestContractLecturerHoursAreEverybodysAfterPublication(t *testing.T) {
	t.Parallel()

	f := contractFixture(t)
	f.publish(t)

	check := func(t *testing.T, c *graphqltest.Client) {
		if got := contractLecturerHoursOf(t, c, f); got == nil || *got != 2 {
			t.Errorf("the published figure is %v, want 2", deref(got))
		}
	}
	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token, check)
	t.Run("full reach", func(t *testing.T) {
		check(t, graphqltest.New(f.handler).WithToken(fullReachToken(t, f.schema, testdata.Eins)))
	})
}

func deref(p *float64) any {
	if p == nil {
		return "null"
	}
	return *p
}
