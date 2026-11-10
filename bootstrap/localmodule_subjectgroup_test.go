package bootstrap_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/graphqltest"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// Choosing the subject group of a local module when it is entered, through every door.
//
// The rule is wider than filing on purpose — any active group, not only one the caller leads —
// and the tests below hold both halves of that: the choice works for a programme lead who leads no
// group, and the same person still may not re-file the module afterwards.

const createLocalInGroupMutation = `mutation($in: LocalModuleInput!, $g: ID) {
	createLocalModule(input: $in, subjectGroup: $g) {
		id name subjectGroup { id code }
	}
}`

type localModuleInGroupResult struct {
	CreateLocalModule struct {
		ID           string
		Name         string
		SubjectGroup *struct {
			ID   string
			Code string
		}
	}
}

// localGroupFixture is a catalogue with subject groups, a programme lead of PA who leads no
// group, a programme lead of PB, and the three doors of the PA lead.
type localGroupFixture struct {
	subjectGroupFixture
	software string // an active group the programme lead does not lead
	retired  string // a group that has been retired
	doors    map[string]*graphqltest.Client
}

func newLocalGroupFixture(t *testing.T) localGroupFixture {
	t.Helper()

	f := subjectGroupHandler(t,
		grants{testdata.Sechs, []string{"ADMIN"}},
		grants{testdata.Vier, []string{"LECTURER", "PROGRAMME_LEAD"}},
		grants{testdata.Zwei, []string{"LECTURER", "PROGRAMME_LEAD"}},
	)
	for mail, code := range map[string]string{
		testdata.Vier.Mail: storetest.FixtureProgrammeA,
		testdata.Zwei.Mail: storetest.FixtureProgrammeB,
	} {
		if _, err := f.schema.Pool.Exec(t.Context(),
			`INSERT INTO person_programme_scope (person_id, role, programme_id)
			 SELECT p.id, 'PROGRAMME_LEAD', pr.id FROM person p, programme pr
			  WHERE p.mail = $1 AND pr.code = $2`, mail, code); err != nil {
			t.Fatalf("cannot assign %s to %s: %v", code, mail, err)
		}
	}

	software := f.create(t, "SWE", "Softwarefächer")
	retired := f.create(t, "ALT", "Stillgelegt")
	if _, err := f.schema.Pool.Exec(t.Context(),
		`UPDATE subject_group SET active = false WHERE id = $1`, retired); err != nil {
		t.Fatalf("cannot retire the group: %v", err)
	}

	base := graphqltest.New(f.handler)
	return localGroupFixture{
		subjectGroupFixture: f,
		software:            software,
		retired:             retired,
		doors: map[string]*graphqltest.Client{
			"browser":    base.AsUser(testdata.Vier.Mail).On(graphqltest.Browser),
			"token":      base.WithToken(testdata.Vier.Token).On(graphqltest.Token),
			"full reach": base.WithToken(fullReachToken(t, f.schema, testdata.Vier)).On(graphqltest.Token),
		},
	}
}

func localInGroup(name string, group any) map[string]any {
	vars := localInput(storetest.FixtureProgrammeA, name, "FWP_PLACEHOLDER")
	vars["g"] = group
	return vars
}

// A programme lead who leads no subject group files the new course under a foreign group — and
// may not move it anywhere afterwards. The plain token is included on purpose: the module is
// brand new, so naming its group moves nobody's reading rights, and the decision was not to
// require full reach for it.
func TestAProgrammeLeadChoosesAnyGroupOnCreationButCannotRefile(t *testing.T) {
	t.Parallel()

	f := newLocalGroupFixture(t)

	for door, c := range f.doors {
		t.Run(door, func(t *testing.T) {
			var out localModuleInGroupResult
			c.MustQuery(t, createLocalInGroupMutation,
				localInGroup("FWP-Platzhalter "+door, f.software), &out)

			got := out.CreateLocalModule
			if got.SubjectGroup == nil || got.SubjectGroup.ID != f.software {
				t.Fatalf("the new course is filed under %+v, want SWE", got.SubjectGroup)
			}

			// Re-filing is still filing, and a lead of no group files nothing. A plain token
			// is stopped one step earlier, by the directive.
			want := "NOT_YOUR_SUBJECT_GROUP"
			if door == "token" {
				want = "FULL_REACH_REQUIRED"
			}
			for _, target := range []any{nil, f.retired} {
				assertRefusal(t, c.Do(t, `mutation($m: [ID!]!, $g: ID) {
					setModulesSubjectGroup(moduleIds: $m, subjectGroup: $g) { modulesAssigned }
				}`, map[string]any{"m": []string{got.ID}, "g": target}), want)
			}
		})
	}
}

// Without the argument nothing changes: the course is created unfiled, through every door.
func TestALocalModuleCreatedWithoutAGroupStaysUnfiled(t *testing.T) {
	t.Parallel()

	f := newLocalGroupFixture(t)

	for door, c := range f.doors {
		t.Run(door, func(t *testing.T) {
			var out localModuleInGroupResult
			c.MustQuery(t, createLocalInGroupMutation, localInGroup("Ohne Gruppe "+door, nil), &out)
			if out.CreateLocalModule.SubjectGroup != nil {
				t.Errorf("filed under %+v without having named a group",
					out.CreateLocalModule.SubjectGroup)
			}
		})
	}
}

// A group that does not exist or is retired is SUBJECT_GROUP_NOT_FOUND, and the course is not
// created either — a second attempt with a valid group succeeds under the same name.
func TestAnUnknownOrRetiredGroupRefusesTheWholeCreation(t *testing.T) {
	t.Parallel()

	f := newLocalGroupFixture(t)

	for door, c := range f.doors {
		t.Run(door, func(t *testing.T) {
			for name, group := range map[string]string{
				"retired":   f.retired,
				"unknown":   uuid.NewString(),
				"malformed": "not-an-id",
			} {
				course := "Fach " + name + " " + door
				refusal := c.Do(t, createLocalInGroupMutation, localInGroup(course, group))
				assertRefusal(t, refusal, "SUBJECT_GROUP_NOT_FOUND")
				graphqltest.AssertNoLeak(t, refusal.Messages()[0], graphqltest.DatabaseNoise()...)

				// Nothing was written: the same name is still free.
				c.MustQuery(t, createLocalInGroupMutation, localInGroup(course, f.software), nil)
			}
		})
	}
}

// A lead of another programme is refused for the programme, whatever group they name — the
// group is looked at only after the permission, so its existence cannot be probed.
func TestAForeignProgrammeLeadCannotProbeGroups(t *testing.T) {
	t.Parallel()

	f := newLocalGroupFixture(t)
	full := fullReachToken(t, f.schema, testdata.Zwei)

	for _, group := range []string{f.software, f.retired, uuid.NewString(), "not-an-id"} {
		graphqltest.EachDoor(t, f.handler, testdata.Zwei.Mail, testdata.Zwei.Token,
			func(t *testing.T, c *graphqltest.Client) {
				assertRefusal(t, c.Do(t, createLocalInGroupMutation,
					localInGroup("Fremdes Fach", group)), "NOT_YOUR_PROGRAMME")
			})
		assertRefusal(t, graphqltest.New(f.handler).WithToken(full).On(graphqltest.Token).Do(t,
			createLocalInGroupMutation, localInGroup("Fremdes Fach", group)), "NOT_YOUR_PROGRAMME")
	}
}
