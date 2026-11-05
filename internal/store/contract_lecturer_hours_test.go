package store_test

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/store"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// The hours lecturers on contract hold, per programme, against a real database.
//
// Two things are statements about rows: which assignee counts as a lecturer on contract (a teacher
// row, or an account linked to one by address), and that the sum follows teachingHours — a shared
// lecture is one part. The third is that the sum is filtered at all, which is what makes a sum
// over assignments safe before publication.

// TestEveryContractLecturerQueryIsFiltered reads the file a leak would be written in, the way
// TestEveryAssignmentQueryIsFiltered reads assignment.sql: every query sums assignments, so every
// query carries the assignment visibility filter.
func TestEveryContractLecturerQueryIsFiltered(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../db/queries/contract_lecturer_hours.sql")
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
			`a.person_id = sqlc.arg(assignee_id)::uuid`,
			`prog.id = ANY (sqlc.arg(programme_ids)::uuid[])`,
			`msg.subject_group_id = ANY (sqlc.arg(subject_group_ids)::uuid[])`,
		} {
			if !strings.Contains(body, required) {
				t.Errorf("the query %s sums assignments without %q", name, required)
			}
		}
	}
}

// contractFixture is the demand fixture with people who can hold parts and a way to fill them.
type contractFixture struct {
	demandFixture
	assignments *store.Assignments
	// lba is a teacher flagged as a lecturer on contract, without an account.
	lba uuid.UUID
	// linked is the teacher row of Eins' account.
	linked uuid.UUID
}

func newContractFixture(t *testing.T) contractFixture {
	t.Helper()

	f := contractFixture{
		demandFixture: newDemandFixture(t),
	}
	f.assignments = store.NewAssignments(f.schema.Pool)
	f.lba = teacherID(t, f.schema, storetest.FixtureTeacherNotAdmitted)
	f.linked = teacherID(t, f.schema, storetest.FixtureTeacherOrdinary)
	for _, p := range []testdata.Persona{testdata.Eins, testdata.Zwei} {
		storetest.SeedPerson(t, f.schema, p, "LECTURER")
	}
	f.flag(t, f.lba)
	return f
}

// flag marks a teacher as a lecturer on contract, the way the import would.
func (f contractFixture) flag(t *testing.T, teacher uuid.UUID) {
	t.Helper()

	if _, err := f.schema.Pool.Exec(t.Context(),
		`UPDATE teacher SET is_lecturer_on_contract = true WHERE id = $1`, teacher); err != nil {
		t.Fatalf("cannot flag the teacher: %v", err)
	}
}

func (f contractFixture) fill(t *testing.T, part uuid.UUID, who domain.Assignee) {
	t.Helper()

	if _, err := f.assignments.FillPart(t.Context(), part, who, "", testdata.Zwei.ID()); err != nil {
		t.Fatalf("cannot fill the part: %v", err)
	}
}

// hours reads the programme's figure through a filter.
func (f contractFixture) hours(t *testing.T, filter policy.AssignmentFilter) float64 {
	t.Helper()

	got, err := f.demand.ContractLecturerHours(t.Context(), f.semester.Code, filter)
	if err != nil {
		t.Fatalf("cannot read the hours of lecturers on contract: %v", err)
	}
	return got[f.programme]
}

var everything = policy.AssignmentFilter{Scope: policy.AssignmentReadScopeAll}

// A lecturer on contract without an account counts; somebody of the faculty does not.
func TestContractLecturerHoursCountOnlyLecturersOnContract(t *testing.T) {
	t.Parallel()

	f := newContractFixture(t)
	parts := f.declare(t, "").Parts

	f.fill(t, parts[0].ID, domain.Assignee{PersonID: testdata.Zwei.ID()})
	if got := f.hours(t, everything); got != 0 {
		t.Fatalf("a colleague of the faculty counts as %v hours on contract, want 0", got)
	}

	f.fill(t, parts[1].ID, domain.Assignee{TeacherID: f.lba})
	if got := f.hours(t, everything); got != 2 {
		t.Errorf("a laboratory held on contract counts %v hours, want 2", got)
	}
}

// An account is counted when its teacher record — linked by address — carries the flag, and not
// before. The write path stores such a teacher as the account, so the flag has to be found there.
func TestContractLecturerHoursFollowTheTeacherRecordOfAnAccount(t *testing.T) {
	t.Parallel()

	f := newContractFixture(t)
	parts := f.declare(t, "").Parts

	f.fill(t, parts[0].ID, domain.Assignee{PersonID: testdata.Eins.ID()})
	if got := f.hours(t, everything); got != 0 {
		t.Fatalf("an account whose teacher record is not flagged counts %v hours, want 0", got)
	}

	f.flag(t, f.linked)
	if got := f.hours(t, everything); got != 2 {
		t.Errorf("an account whose teacher record is flagged counts %v hours, want 2", got)
	}
}

// A lecture held once for two cohorts is one part, so it counts once — as in teachingHours.
func TestContractLecturerHoursCountASharedLectureOnce(t *testing.T) {
	t.Parallel()

	f := newContractFixture(t)
	first := f.declare(t, "A")
	f.declare(t, "B")

	lecture := first.Parts[0]
	if lecture.Kind != domain.PartKindLecture {
		t.Fatalf("the first part is a %s, want the lecture", lecture.Kind)
	}
	if _, err := f.demand.ShareInstancePartAcrossTracks(t.Context(), lecture.ID); err != nil {
		t.Fatalf("cannot share the lecture: %v", err)
	}
	f.fill(t, lecture.ID, domain.Assignee{TeacherID: f.lba})

	if got := f.hours(t, everything); got != 2 {
		t.Errorf("a shared lecture held on contract counts %v hours, want 2", got)
	}
	if load := loadOf(t, f.teachingLoad(t), storetest.FixtureProgrammeA); load.TeachingHours() != 6 {
		t.Errorf("the programme's load is %v hours, want 6 — the figure is a part of it",
			load.TeachingHours())
	}
}

// The sum is filtered in SQL, not only hidden afterwards: an own-only filter sums nothing of
// somebody else's, and the programme's lead reaches it.
func TestContractLecturerHoursAreFiltered(t *testing.T) {
	t.Parallel()

	f := newContractFixture(t)
	parts := f.declare(t, "").Parts
	f.fill(t, parts[1].ID, domain.Assignee{TeacherID: f.lba})

	own := policy.AssignmentFilter{Scope: policy.AssignmentReadScopeOwn, AssigneeID: testdata.Zwei.ID()}
	if got := f.hours(t, own); got != 0 {
		t.Errorf("an own-only filter sums %v hours of somebody else's, want 0", got)
	}
	none := policy.AssignmentFilter{Scope: policy.AssignmentReadScopeNone}
	if got := f.hours(t, none); got != 0 {
		t.Errorf("the empty filter sums %v hours, want 0", got)
	}
	lead := policy.AssignmentFilter{
		Scope:        policy.AssignmentReadScopeOwnOrScoped,
		AssigneeID:   testdata.Vier.ID(),
		ProgrammeIDs: []uuid.UUID{f.programme},
	}
	if got := f.hours(t, lead); got != 2 {
		t.Errorf("the programme's lead reaches %v hours, want 2", got)
	}
}
