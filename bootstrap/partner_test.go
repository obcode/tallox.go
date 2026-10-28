package bootstrap_test

import (
	"testing"

	"github.com/obcode/tallox.go/internal/graphqltest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// partnerOf gives somebody who already holds PARTNER_READER one programme to follow.
func (f wishFixture) partnerOf(t *testing.T, who testdata.Persona, code string) {
	t.Helper()
	if _, err := f.schema.Pool.Exec(t.Context(),
		`INSERT INTO person_programme_scope (person_id, role, programme_id)
		 SELECT $1, 'PARTNER_READER', id FROM programme WHERE code = $2`, who.ID(), code); err != nil {
		t.Fatalf("cannot make %s a partner of %s: %v", who.Name, code, err)
	}
}

const partnerReads = `query($s: String!) {
	courseInstances(semester: $s) { id programme { code } }
	teachingLoad(semester: $s) { programme { code } }
	wishes(semester: $s) { id }
}`

type partnerView struct {
	CourseInstances []struct {
		ID        string
		Programme struct{ Code string }
	}
	TeachingLoad []struct {
		Programme struct{ Code string }
	}
	Wishes []struct{ ID string }
}

// A partner of the instance's programme reads its demand and its row of the controlling; a
// partner of another programme reads neither — and neither reads a published wish. Through both
// doors, because the narrowing is a rule about the account, not about the session.
func TestAPartnerReadsOnlyItsProgrammes(t *testing.T) {
	t.Parallel()

	f := wishHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Zwei, []string{"PARTNER_READER"}},
		grants{testdata.Drei, []string{"PARTNER_READER"}},
	)
	f.partnerOf(t, testdata.Zwei, f.programme)
	f.partnerOf(t, testdata.Drei, f.otherProgramme)
	f.register(t, testdata.Eins)
	f.publish(t)

	graphqltest.EachDoor(t, f.handler, testdata.Zwei.Mail, testdata.Zwei.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var out partnerView
			c.MustQuery(t, partnerReads, map[string]any{"s": f.semester}, &out)
			if len(out.CourseInstances) != 1 || out.CourseInstances[0].Programme.Code != f.programme {
				t.Errorf("the partner of %s reads the demand %+v, want its one instance",
					f.programme, out.CourseInstances)
			}
			for _, l := range out.TeachingLoad {
				if l.Programme.Code != f.programme {
					t.Errorf("the partner of %s reads the controlling row of %s", f.programme, l.Programme.Code)
				}
			}
			if len(out.TeachingLoad) != 1 {
				t.Errorf("the partner of %s reads %d controlling rows, want its own", f.programme, len(out.TeachingLoad))
			}
			if len(out.Wishes) != 0 {
				t.Errorf("a partner reads %d published wishes, want none", len(out.Wishes))
			}
		})

	graphqltest.EachDoor(t, f.handler, testdata.Drei.Mail, testdata.Drei.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var out partnerView
			c.MustQuery(t, partnerReads, map[string]any{"s": f.semester}, &out)
			if len(out.CourseInstances) != 0 {
				t.Errorf("the partner of %s reads %+v of another programme", f.otherProgramme, out.CourseInstances)
			}

			var one struct{ CourseInstance *struct{ ID string } }
			c.MustQuery(t, `query($id: ID!) { courseInstance(id: $id) { id } }`,
				map[string]any{"id": f.instance}, &one)
			if one.CourseInstance != nil {
				t.Error("a partner of another programme reads the instance by its id")
			}
		})

	// The roster of the faculty is not what a partner follows. Browser only, because the field
	// is interactive-only for everybody.
	var roster struct {
		DesiredHoursOverview []struct{ Person struct{ Mail string } }
	}
	graphqltest.New(f.handler).AsUser(testdata.Zwei.Mail).MustQuery(t,
		`query($s: String!) { desiredHoursOverview(semester: $s) { person { mail } } }`,
		map[string]any{"s": f.semester}, &roster)
	if len(roster.DesiredHoursOverview) != 0 {
		t.Errorf("a partner reads the faculty roster: %d people", len(roster.DesiredHoursOverview))
	}

	// An ordinary account still reads everything, which is the point of the narrowing being
	// the partner's alone.
	var everybody partnerView
	graphqltest.New(f.handler).AsUser(testdata.Eins.Mail).
		MustQuery(t, partnerReads, map[string]any{"s": f.semester}, &everybody)
	if len(everybody.TeachingLoad) < 2 {
		t.Errorf("an ordinary account reads %d controlling rows; the partner rule leaked", len(everybody.TeachingLoad))
	}
	graphqltest.New(f.handler).AsUser(testdata.Eins.Mail).MustQuery(t,
		`query($s: String!) { desiredHoursOverview(semester: $s) { person { mail } } }`,
		map[string]any{"s": f.semester}, &roster)
	if len(roster.DesiredHoursOverview) == 0 {
		t.Error("an ordinary account reads an empty roster; the partner rule leaked the other way")
	}
}

// Every write a partner could reach answers with its own code, not with a sentence about a
// finished semester that is not true.
func TestAPartnerWritesNothing(t *testing.T) {
	t.Parallel()

	f := wishHandler(t, grants{testdata.Zwei, []string{"PARTNER_READER"}})
	f.partnerOf(t, testdata.Zwei, f.programme)
	c := graphqltest.New(f.handler).AsUser(testdata.Zwei.Mail)

	mustRefuseWith(t, c, setWishMutation,
		map[string]any{"p": f.instance, "prio": "HAPPY_TO", "note": nil}, "PARTNER_READS_ONLY")
	mustRefuseWith(t, c, setDesired, map[string]any{"s": f.semester, "h": 10}, "PARTNER_READS_ONLY")
	mustRefuseWith(t, c, `mutation($g: [ID!]!) { setMySubjectGroups(subjectGroupIds: $g) { id } }`,
		map[string]any{"g": []string{f.group.String()}}, "PARTNER_READS_ONLY")
}

const rosterReads = `query {
	subjectGroups(includeInactive: true) { code leads { mail } members { mail } }
	programmes { code leads { mail } }
	teachers(includeInactive: true) { mail }
}`

type rosterView struct {
	SubjectGroups []struct {
		Code    string
		Leads   []struct{ Mail string }
		Members []struct{ Mail string }
	}
	Programmes []struct {
		Code  string
		Leads []struct{ Mail string }
	}
	Teachers []struct{ Mail string }
}

// TestAPartnerReadsTheStructureButNotTheRoster: subject groups, programmes and the module
// catalogue are what a partner plans with; who leads and works in them, with mail addresses,
// is the faculty's own — the line desiredHoursOverview draws, now on the side fields too.
func TestAPartnerReadsTheStructureButNotTheRoster(t *testing.T) {
	t.Parallel()

	f := wishHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Zwei, []string{"PARTNER_READER"}},
		grants{testdata.Drei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
		grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}},
	)
	f.partnerOf(t, testdata.Zwei, f.programme)
	f.leadGroup(t, testdata.Drei, f.group)
	if _, err := f.schema.Pool.Exec(t.Context(),
		`INSERT INTO person_programme_scope (person_id, role, programme_id)
		 SELECT $1, 'PROGRAMME_LEAD', id FROM programme WHERE code = $2`,
		testdata.Vier.ID(), f.programme); err != nil {
		t.Fatalf("cannot scope the programme lead: %v", err)
	}
	if _, err := f.schema.Pool.Exec(t.Context(),
		`INSERT INTO person_subject_group (person_id, subject_group_id) VALUES ($1, $2)`,
		testdata.Eins.ID(), f.group); err != nil {
		t.Fatalf("cannot add a member: %v", err)
	}

	graphqltest.EachDoor(t, f.handler, testdata.Zwei.Mail, testdata.Zwei.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var out rosterView
			c.MustQuery(t, rosterReads, nil, &out)
			if len(out.SubjectGroups) == 0 || len(out.Programmes) == 0 {
				t.Fatalf("the partner lost the structure itself: %+v", out)
			}
			for _, g := range out.SubjectGroups {
				if len(g.Leads)+len(g.Members) != 0 {
					t.Errorf("a partner reads the people of subject group %s: %+v", g.Code, g)
				}
			}
			for _, p := range out.Programmes {
				if len(p.Leads) != 0 {
					t.Errorf("a partner reads who leads %s: %+v", p.Code, p.Leads)
				}
			}
			if len(out.Teachers) != 0 {
				t.Errorf("a partner reads the faculty roster: %d teachers", len(out.Teachers))
			}

			var one struct {
				SubjectGroup struct {
					Leads   []struct{ Mail string }
					Members []struct{ Mail string }
				}
			}
			c.MustQuery(t, `query($id: ID!) { subjectGroup(id: $id) { leads { mail } members { mail } } }`,
				map[string]any{"id": f.group.String()}, &one)
			if len(one.SubjectGroup.Leads)+len(one.SubjectGroup.Members) != 0 {
				t.Errorf("a partner reads the people of a subject group by id: %+v", one.SubjectGroup)
			}
		})

	// And the rule did not over-shoot: an ordinary account reads all of it.
	var everybody rosterView
	graphqltest.New(f.handler).AsUser(testdata.Eins.Mail).MustQuery(t, rosterReads, nil, &everybody)
	var leads, members, programmeLeads int
	for _, g := range everybody.SubjectGroups {
		leads += len(g.Leads)
		members += len(g.Members)
	}
	for _, p := range everybody.Programmes {
		programmeLeads += len(p.Leads)
	}
	if leads == 0 || members == 0 || programmeLeads == 0 || len(everybody.Teachers) == 0 {
		t.Errorf("an ordinary account reads %d leads, %d members, %d programme leads, %d teachers; the partner rule leaked the other way",
			leads, members, programmeLeads, len(everybody.Teachers))
	}
}
