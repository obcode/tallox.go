package store_test

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/principal"
	"github.com/obcode/tallox.go/internal/store"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// The file the one permitted sum over assignments lives in: every read carries the person filter.
func TestEveryAssignedLoadQueryIsFiltered(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../db/queries/assigned_load.sql")
	if err != nil {
		t.Fatalf("cannot read the queries: %v", err)
	}
	sql := string(raw)
	blocks := regexp.MustCompile(`(?m)^-- name: (\w+) :(\w+)$`).FindAllStringSubmatchIndex(sql, -1)
	if len(blocks) == 0 {
		t.Fatal("no query in the file — this test checked nothing")
	}
	for i, block := range blocks {
		name := sql[block[2]:block[3]]
		end := len(sql)
		if i+1 < len(blocks) {
			end = blocks[i+1][0]
		}
		body := sql[block[0]:end]
		for _, required := range []string{
			`sqlc.arg('scope')::text = 'all'`,
			`a.person_id = sqlc.arg(owner_id)::uuid`,
			`psg.person_id = a.person_id`,
			`psg.subject_group_id = ANY (sqlc.arg(subject_group_ids)::uuid[])`,
		} {
			if !strings.Contains(body, required) {
				t.Errorf("the query %s sums assignments without %q", name, required)
			}
		}
	}
}

// loadFixture is the assignment fixture with two more modules — one in SWE, one in no subject
// group — and Eins holding a part of each, plus Zwei holding one in SWE.
//
// Eins is a member of MATHE and SWE, Zwei of SWE only, and Drei leads MATHE.
type loadFixture struct {
	assignmentFixture
	sweLab uuid.UUID
}

func newLoadFixture(t *testing.T) loadFixture {
	t.Helper()

	f := loadFixture{assignmentFixture: newAssignmentFixture(t)}
	s, ctx := f.schema, t.Context()
	modules := store.NewModules(s.Pool)

	instance := func(zpaID int64, lecture, lab float64, group uuid.UUID) domain.CourseInstance {
		t.Helper()
		module := moduleID(t, s, zpaID)
		if _, err := modules.SetModuleComponents(ctx, module, []domain.ModuleComponent{
			{Kind: domain.PartKindLecture, TeachingHours: lecture, Position: 0},
			{Kind: domain.PartKindLab, TeachingHours: lab, Position: 1},
		}, uuid.Nil); err != nil {
			t.Fatalf("cannot state the split of %d: %v", zpaID, err)
		}
		if group != uuid.Nil {
			f.exec(t, `INSERT INTO module_subject_group (module_id, subject_group_id) VALUES ($1, $2)`,
				module, group)
		}
		ci, err := store.NewDemand(s.Pool, modules).CreateCourseInstance(ctx, domain.NewCourseInstance{
			SemesterID: f.semester.ID, ModuleID: module, ProgrammeID: f.programme,
		})
		if err != nil || len(ci.Parts) < 2 {
			t.Fatalf("cannot declare the instance of %d: %v", zpaID, err)
		}
		return *ci
	}
	swe := instance(storetest.FixtureModuleTwoSlots, 3, 1, f.otherGroup)
	unsorted := instance(storetest.FixtureModuleDutyDiffers, 2, 1.5, uuid.Nil)
	f.sweLab = swe.Parts[1].ID

	eins := domain.Assignee{PersonID: testdata.Eins.ID()}
	f.fill(t, f.lecture, eins)            // MATHE, 2
	f.fill(t, swe.Parts[0].ID, eins)      // SWE, 3
	f.fill(t, unsorted.Parts[1].ID, eins) // no group, 1.5
	f.fill(t, f.sweLab, domain.Assignee{PersonID: testdata.Zwei.ID()})

	for _, m := range []struct {
		who   testdata.Persona
		group uuid.UUID
	}{{testdata.Eins, f.group}, {testdata.Eins, f.otherGroup}, {testdata.Zwei, f.otherGroup}} {
		f.exec(t, `INSERT INTO person_subject_group (person_id, subject_group_id) VALUES ($1, $2)`,
			m.who.ID(), m.group)
	}
	f.exec(t, `INSERT INTO person_role (person_id, role) VALUES ($1, 'SUBJECT_GROUP_LEAD')`,
		testdata.Drei.ID())
	f.exec(t, `INSERT INTO person_subject_group_scope (person_id, role, subject_group_id)
		VALUES ($1, 'SUBJECT_GROUP_LEAD', $2)`, testdata.Drei.ID(), f.group)
	return f
}

func (f loadFixture) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := f.schema.Pool.Exec(t.Context(), sql, args...); err != nil {
		t.Fatalf("cannot run %q: %v", sql, err)
	}
}

func (f loadFixture) loads(t *testing.T, actor principal.Actor) map[uuid.UUID]domain.AssignedLoad {
	t.Helper()
	list, err := f.service.AssignedLoad(t.Context(), actor, f.semester.Code, uuid.Nil)
	if err != nil {
		t.Fatalf("cannot read the load: %v", err)
	}
	out := map[uuid.UUID]domain.AssignedLoad{}
	for _, l := range list {
		out[l.Person.ID] = l
	}
	return out
}

// The lead of MATHE reads, for a member of MATHE and SWE, the load in both groups and in none —
// the exception — and nothing for somebody who is only in SWE.
func TestALeadReadsTheWholeLoadOfTheirMembers(t *testing.T) {
	t.Parallel()

	f := newLoadFixture(t)
	lead := testdata.Drei.Actor(principal.KindInteractive, "LECTURER", "SUBJECT_GROUP_LEAD")
	lead.RoleScopes = []principal.RoleScope{{Role: "SUBJECT_GROUP_LEAD", SubjectGroupID: f.group}}

	got := f.loads(t, lead)
	if len(got) != 1 {
		t.Fatalf("the lead of MATHE reads the load of %d people, want only Eins: %+v", len(got), got)
	}
	eins, ok := got[testdata.Eins.ID()]
	if !ok || eins.Total != 6.5 {
		t.Fatalf("Eins's load reads as %+v, want 6.5", eins)
	}
	shares := map[string]float64{}
	for _, part := range eins.BySubjectGroup {
		code := ""
		if part.SubjectGroup != nil {
			code = part.SubjectGroup.Code
		}
		shares[code] = part.Hours
	}
	if shares["MATHE"] != 2 || shares["SWE"] != 3 || shares[""] != 1.5 || len(shares) != 3 {
		t.Errorf("Eins's shares read as %v, want MATHE 2, SWE 3, none 1.5", shares)
	}
	if last := eins.BySubjectGroup[len(eins.BySubjectGroup)-1]; last.SubjectGroup != nil {
		t.Errorf("the modules in no group are not last: %+v", eins.BySubjectGroup)
	}

	// Through a token the same lead reads only their own, and holds nothing.
	byToken := lead
	byToken.Kind = principal.KindToken
	if got := f.loads(t, byToken); len(got) != 0 {
		t.Errorf("the lead reads %+v through a token", got)
	}
}

// A colleague reads their own before publication and everybody's after, in the browser — through a
// token only their own; the dean's office reads everybody's throughout.
func TestTheAssignedLoadIsPublishedWithTheAssignments(t *testing.T) {
	t.Parallel()

	f := newLoadFixture(t)
	zwei := testdata.Zwei.Actor(principal.KindInteractive, "LECTURER")
	vier := testdata.Vier.Actor(principal.KindToken, "LECTURER")
	deans := testdata.Fuenf.Actor(principal.KindInteractive, "DEANS_OFFICE")

	if got := f.loads(t, zwei); len(got) != 1 || got[testdata.Zwei.ID()].Total != 1 {
		t.Errorf("Zwei reads %+v before publication, want only their own 1", got)
	}
	if got := f.loads(t, vier); len(got) != 0 {
		t.Errorf("Vier, who holds nothing, reads %+v before publication", got)
	}
	if got := f.loads(t, deans); len(got) != 2 {
		t.Errorf("the dean's office reads %d loads, want 2", len(got))
	}

	f.publish(t)
	if got := f.loads(t, vier); len(got) != 0 {
		t.Errorf("after publication Vier reads %+v through a token, want none of somebody else's", got)
	}
	vierInBrowser := testdata.Vier.Actor(principal.KindInteractive, "LECTURER")
	if got := f.loads(t, vierInBrowser); len(got) != 2 || got[testdata.Eins.ID()].Total != 6.5 {
		t.Errorf("after publication Vier reads %+v in the browser, want both", got)
	}
}
