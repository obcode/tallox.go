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
	"github.com/obcode/tallox.go/internal/store"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// Competences through the API, through both doors. "Würde ich gern halten" is a wish without a
// semester, so the assertions are the wish tests' assertions: the list, what a token reaches, and
// what a refusal says.

type competenceFixture struct {
	handler http.Handler
	schema  *storetest.Schema
	module  string
	group   uuid.UUID
	other   uuid.UUID
}

func competenceHandler(t *testing.T, people ...grants) competenceFixture {
	t.Helper()

	s := storetest.New(t)
	for _, p := range people {
		storetest.SeedPerson(t, s, p.who, p.roles...)
		parsed, err := auth.ParseToken(p.who.Token)
		if err != nil {
			t.Fatalf("fixture token of %s does not parse: %v", p.who.Name, err)
		}
		storetest.SeedToken(t, s, p.who, auth.HashSecret(parsed.Secret), storetest.TokenOptions{
			Description: "competence test",
		})
	}

	storetest.SeedZPACatalogue(t, s)
	if _, err := store.NewCatalogue(s.Pool).Project(t.Context(), nil); err != nil {
		t.Fatalf("cannot project the catalogue: %v", err)
	}

	module := moduleIDOf(t, s, storetest.FixtureModuleOrdinary)
	f := competenceFixture{
		schema: s,
		module: module.String(),
		group:  seedGroup(t, s, "MATHE", "Mathematik"),
		other:  seedGroup(t, s, "SWE", "Softwarefächer"),
	}
	f.exec(t, `INSERT INTO module_subject_group (module_id, subject_group_id) VALUES ($1, $2)`,
		module, f.group)

	f.handler = bootstrap.Handler(bootstrap.Options{
		Build: buildinfo.Info{Version: "test"},
		Auth: auth.Config{
			Mode:   auth.ModeProxy,
			Users:  store.NewDirectory(s.Pool),
			Tokens: store.NewDirectory(s.Pool),
		},
		People:        domain.NewPeopleService(store.NewPeople(s.Pool), nil),
		SubjectGroups: domain.NewSubjectGroupService(store.NewSubjectGroups(s.Pool)),
		Expertise:     domain.NewCompetenceService(store.NewCompetences(s.Pool)),
	})
	return f
}

func (f competenceFixture) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := f.schema.Pool.Exec(t.Context(), sql, args...); err != nil {
		t.Fatalf("cannot run %q: %v", sql, err)
	}
}

func (f competenceFixture) join(t *testing.T, who testdata.Persona) {
	t.Helper()
	f.exec(t, `INSERT INTO person_subject_group (person_id, subject_group_id) VALUES ($1, $2)`,
		who.ID(), f.group)
}

func (f competenceFixture) leadGroup(t *testing.T, who testdata.Persona, group uuid.UUID) {
	t.Helper()
	f.exec(t, `INSERT INTO person_subject_group_scope (person_id, role, subject_group_id)
		VALUES ($1, 'SUBJECT_GROUP_LEAD', $2)`, who.ID(), group)
}

const setMyCompetenceMutation = `mutation($m: ID!, $l: CompetenceLevel!) {
	setMyCompetence(moduleId: $m, level: $l) {
		id level outsideSubjectGroups
		holder { personId mail }
		module { name compulsory subjectGroup { code } }
	}
}`

// state puts one person's own statement in through the browser door.
func (f competenceFixture) state(t *testing.T, who testdata.Persona, level string) string {
	t.Helper()
	var out struct {
		SetMyCompetence struct{ ID string }
	}
	graphqltest.New(f.handler).AsUser(who.Mail).MustQuery(t, setMyCompetenceMutation,
		map[string]any{"m": f.module, "l": level}, &out)
	return out.SetMyCompetence.ID
}

func competencesSeen(t *testing.T, c *graphqltest.Client) []string {
	t.Helper()
	var out struct {
		Competences []struct {
			Holder struct{ Mail string }
		}
	}
	c.MustQuery(t, `query { competences { holder { mail } } }`, nil, &out)
	mails := make([]string, 0, len(out.Competences))
	for _, c := range out.Competences {
		mails = append(mails, c.Holder.Mail)
	}
	return mails
}

// The rule the area rests on: a colleague sees nothing, through either door, and the owner sees
// their own through both.
func TestAColleagueSeesNoCompetence(t *testing.T) {
	t.Parallel()

	f := competenceHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Zwei, []string{"LECTURER"}},
	)
	f.join(t, testdata.Eins)
	f.join(t, testdata.Zwei)
	f.state(t, testdata.Eins, "WOULD_LIKE")

	graphqltest.EachDoor(t, f.handler, testdata.Zwei.Mail, testdata.Zwei.Token,
		func(t *testing.T, c *graphqltest.Client) {
			if got := competencesSeen(t, c); len(got) != 0 {
				t.Errorf("a colleague — in the same subject group — sees %v", got)
			}
		})

	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var out struct {
				MyCompetences []struct{ Level string }
			}
			c.MustQuery(t, `query { myCompetences { level } }`, nil, &out)
			if len(out.MyCompetences) != 1 || out.MyCompetences[0].Level != "WOULD_LIKE" {
				t.Errorf("the owner reads %+v, want their one statement", out.MyCompetences)
			}
		})
}

// The lead of the module's subject group reads it in the browser — and not through a token.
func TestALeadReadsCompetencesOnlyInTheBrowser(t *testing.T) {
	t.Parallel()

	f := competenceHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Drei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
	)
	f.join(t, testdata.Eins)
	f.state(t, testdata.Eins, "CAN_TEACH")

	f.leadGroup(t, testdata.Drei, f.other)
	if got := competencesSeen(t, graphqltest.New(f.handler).AsUser(testdata.Drei.Mail)); len(got) != 0 {
		t.Errorf("the lead of another group sees %v", got)
	}

	f.leadGroup(t, testdata.Drei, f.group)
	if got := competencesSeen(t, graphqltest.New(f.handler).AsUser(testdata.Drei.Mail)); len(got) != 1 {
		t.Errorf("the lead of the group sees %v in the browser, want the one statement", got)
	}
	if got := competencesSeen(t, graphqltest.New(f.handler).WithToken(testdata.Drei.Token)); len(got) != 0 {
		t.Errorf("the lead sees %v through a token — the silent export the rule closes", got)
	}
}

// Stating one's own needs the membership, through either door; and the refusal says which.
func TestStatingACompetenceNeedsTheSubjectGroup(t *testing.T) {
	t.Parallel()

	f := competenceHandler(t, grants{testdata.Eins, []string{"LECTURER"}})

	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			resp := c.Do(t, setMyCompetenceMutation, map[string]any{"m": f.module, "l": "CAN_TEACH"})
			if code := errorCode(t, resp); code != "COMPETENCE_OUTSIDE_GROUPS" {
				t.Errorf("outside the group: code %q, want COMPETENCE_OUTSIDE_GROUPS", code)
			}
		})

	f.join(t, testdata.Eins)
	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var out struct {
				SetMyCompetence struct {
					Holder struct{ Mail string }
					Module struct {
						Compulsory   bool
						SubjectGroup struct{ Code string }
					}
				}
			}
			c.MustQuery(t, setMyCompetenceMutation, map[string]any{"m": f.module, "l": "CAN_TEACH"}, &out)
			got := out.SetMyCompetence
			if got.Holder.Mail != testdata.Eins.Mail || !got.Module.Compulsory ||
				got.Module.SubjectGroup.Code != "MATHE" {
				t.Errorf("setMyCompetence answered %+v", got)
			}
		})
}

// Withdrawing somebody else's statement answers "not found", and the answer names nobody.
func TestWithdrawingSomebodyElsesCompetenceSaysNothing(t *testing.T) {
	t.Parallel()

	f := competenceHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Zwei, []string{"LECTURER"}},
	)
	f.join(t, testdata.Eins)
	id := f.state(t, testdata.Eins, "WOULD_LIKE")

	graphqltest.EachDoor(t, f.handler, testdata.Zwei.Mail, testdata.Zwei.Token,
		func(t *testing.T, c *graphqltest.Client) {
			resp := c.Do(t, `mutation($id: ID!) { withdrawMyCompetence(id: $id) }`, map[string]any{"id": id})
			if code := errorCode(t, resp); code != "COMPETENCE_NOT_FOUND" {
				t.Errorf("code %q, want COMPETENCE_NOT_FOUND", code)
			}
			for _, message := range resp.Messages() {
				graphqltest.AssertNoLeak(t, message,
					append(graphqltest.DatabaseNoise(), testdata.Eins.Mail, testdata.Eins.Name)...)
			}
		})
}

// The lead enters for a teacher without an account — in the browser only.
func TestTheLeadEntersForATeacherInTheBrowserOnly(t *testing.T) {
	t.Parallel()

	f := competenceHandler(t, grants{testdata.Drei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}})
	f.leadGroup(t, testdata.Drei, f.group)
	teacher := teacherIDOf(t, f.schema, storetest.FixtureTeacherWithoutMail)

	const mutation = `mutation($m: ID!, $t: ID!) {
		setTeacherCompetence(moduleId: $m, teacherId: $t, level: CAN_TEACH) { id holder { teacherId name } }
	}`
	vars := map[string]any{"m": f.module, "t": teacher.String()}

	resp := graphqltest.New(f.handler).WithToken(testdata.Drei.Token).Do(t, mutation, vars)
	if code := errorCode(t, resp); code != "INTERACTIVE_ONLY" {
		t.Errorf("through a token: code %q, want INTERACTIVE_ONLY", code)
	}

	var out struct {
		SetTeacherCompetence struct {
			Holder struct {
				TeacherID string
				Name      string
			}
		}
	}
	graphqltest.New(f.handler).AsUser(testdata.Drei.Mail).MustQuery(t, mutation, vars, &out)
	if out.SetTeacherCompetence.Holder.TeacherID != teacher.String() {
		t.Errorf("the row names %+v, want the teacher", out.SetTeacherCompetence.Holder)
	}
}

// The counts over a group are the lead's, interactively.
func TestTheGroupCountsAreTheLeads(t *testing.T) {
	t.Parallel()

	f := competenceHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Drei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
	)
	f.join(t, testdata.Eins)
	f.state(t, testdata.Eins, "CAN_TEACH")
	f.leadGroup(t, testdata.Drei, f.group)

	const query = `query($g: ID!) {
		competenceMemberStatus(subjectGroup: $g) { member { mail } canTeachCompulsory minimum }
		modulesWithoutCompetence(subjectGroup: $g) { id }
	}`
	vars := map[string]any{"g": f.group.String()}

	resp := graphqltest.New(f.handler).AsUser(testdata.Eins.Mail).Do(t, query, vars)
	if code := errorCode(t, resp); code != "COMPETENCE_GROUP_REFUSED" {
		t.Errorf("a member: code %q, want COMPETENCE_GROUP_REFUSED", code)
	}
	resp = graphqltest.New(f.handler).WithToken(testdata.Drei.Token).Do(t, query, vars)
	if code := errorCode(t, resp); code != "INTERACTIVE_ONLY" {
		t.Errorf("the lead through a token: code %q, want INTERACTIVE_ONLY", code)
	}

	var out struct {
		CompetenceMemberStatus []struct {
			Member             struct{ Mail string }
			CanTeachCompulsory int
			Minimum            int
		}
	}
	graphqltest.New(f.handler).AsUser(testdata.Drei.Mail).MustQuery(t, query, vars, &out)
	if len(out.CompetenceMemberStatus) != 1 || out.CompetenceMemberStatus[0].CanTeachCompulsory != 1 {
		t.Errorf("member status = %+v, want Eins with 1", out.CompetenceMemberStatus)
	}
}

func teacherIDOf(t *testing.T, s *storetest.Schema, zpaID int64) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := s.Pool.QueryRow(t.Context(),
		`SELECT id FROM teacher WHERE zpa_teacher_ref = $1`, zpaID).Scan(&id); err != nil {
		t.Fatalf("cannot find teacher %d: %v", zpaID, err)
	}
	return id
}
