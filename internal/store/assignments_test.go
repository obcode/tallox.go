package store_test

import (
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
	"github.com/obcode/tallox.go/internal/store"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// Assignments against a real database.
//
// The same reason the wish tests are: the confidentiality filter *is* a WHERE clause, "no
// aggregates before publication" is the absence of a COUNT in db/queries/assignment.sql, and both
// writes are conditional statements rather than checks followed by writes. A fake store passes all
// three while the shipped statements do something else.

// assignmentFixture is a semester with one instance of two parts, two programmes, two subject
// groups, and the two kinds of assignee.
type assignmentFixture struct {
	schema      *storetest.Schema
	assignments *store.Assignments
	service     *domain.AssignmentService
	semester    domain.Semester

	programme      uuid.UUID
	otherProgramme uuid.UUID
	group          uuid.UUID
	otherGroup     uuid.UUID
	module         uuid.UUID
	instance       uuid.UUID
	lecture        uuid.UUID
	lab            uuid.UUID

	// withAccount is a teacher whose address matches a person — the canonicalisation case.
	withAccount uuid.UUID
	// withoutAccount is a teacher nobody has admitted: assignable, and never anybody's "own".
	withoutAccount uuid.UUID
}

func newAssignmentFixture(t *testing.T) assignmentFixture {
	t.Helper()

	s := storetest.New(t)
	storetest.SeedZPACatalogue(t, s)
	project(t, s)

	ctx := t.Context()
	modules := store.NewModules(s.Pool)
	semesters := store.NewSemesters(s.Pool)

	semester, err := semesters.EnsureSemester(ctx, "2027-WS")
	if err != nil {
		t.Fatalf("cannot record the semester: %v", err)
	}

	f := assignmentFixture{
		schema:      s,
		assignments: store.NewAssignments(s.Pool),
		semester:    semester,

		programme:      programmeID(t, s, storetest.FixtureProgrammeA),
		otherProgramme: programmeID(t, s, storetest.FixtureProgrammeB),
		module:         moduleID(t, s, storetest.FixtureModuleOrdinary),
		group:          seedSubjectGroup(t, s, "MATHE", "Mathematik"),
		otherGroup:     seedSubjectGroup(t, s, "SWE", "Softwarefächer"),

		withAccount:    teacherID(t, s, storetest.FixtureTeacherOrdinary),
		withoutAccount: teacherID(t, s, storetest.FixtureTeacherNotAdmitted),
	}
	f.service = domain.NewAssignmentService(f.assignments,
		domain.NewSemesterService(semesters, nil))

	if _, err := modules.SetModuleComponents(ctx, f.module, []domain.ModuleComponent{
		{Kind: domain.PartKindLecture, TeachingHours: 2, Position: 0},
		{Kind: domain.PartKindLab, TeachingHours: 2, Position: 1},
	}, uuid.Nil); err != nil {
		t.Fatalf("cannot state the module's split: %v", err)
	}

	if _, err := s.Pool.Exec(ctx,
		`INSERT INTO module_subject_group (module_id, subject_group_id) VALUES ($1, $2)`,
		f.module, f.group); err != nil {
		t.Fatalf("cannot put the module in a subject group: %v", err)
	}

	instance, err := store.NewDemand(s.Pool, modules).CreateCourseInstance(ctx,
		domain.NewCourseInstance{
			SemesterID:  semester.ID,
			ModuleID:    f.module,
			ProgrammeID: f.programme,
		})
	if err != nil {
		t.Fatalf("cannot declare the instance: %v", err)
	}
	if len(instance.Parts) < 2 {
		t.Fatalf("the instance has %d parts, want two", len(instance.Parts))
	}
	f.instance = instance.ID
	f.lecture, f.lab = instance.Parts[0].ID, instance.Parts[1].ID

	for _, p := range []testdata.Persona{testdata.Eins, testdata.Zwei, testdata.Drei, testdata.Vier} {
		storetest.SeedPerson(t, s, p, "LECTURER")
	}

	// The assignment phase, because that is when this area is open. A fixture in the phase a fresh
	// semester starts in would make every write test fail for the right reason and the wrong
	// case — the phase rule has its own tests in internal/policy.
	if _, err := s.Pool.Exec(ctx, `UPDATE semester SET phase = 'ASSIGNMENT' WHERE id = $1`,
		semester.ID); err != nil {
		t.Fatalf("cannot advance the phase: %v", err)
	}
	f.semester.Phase = policy.PhaseAssignment
	return f
}

// fill puts somebody on a part and returns the assignment id.
func (f assignmentFixture) fill(t *testing.T, part uuid.UUID, who domain.Assignee) uuid.UUID {
	t.Helper()

	id, err := f.assignments.FillPart(t.Context(), part, who, "", testdata.Drei.ID())
	if err != nil {
		t.Fatalf("cannot fill the part: %v", err)
	}
	return id
}

// assign goes through the service rather than the store, because the two things worth asserting
// about it — the write rule and the canonicalisation of an assignee — live there.
func (f assignmentFixture) assign(t *testing.T, actor principal.Actor, part uuid.UUID,
	who domain.Assignee) *domain.Assignment {
	t.Helper()

	written, err := f.service.Set(t.Context(), actor, part, who, "", uuid.Nil)
	if err != nil {
		t.Fatalf("cannot assign: %v", err)
	}
	return written
}

// only reads the one assignment there is, unfiltered. For asserting what was stored rather than
// who may see it.
func (f assignmentFixture) only(t *testing.T) domain.Assignment {
	t.Helper()

	list, err := f.assignments.Assignments(t.Context(),
		domain.AssignmentQuery{SemesterCode: f.semester.Code},
		policy.AssignmentFilter{Scope: policy.AssignmentReadScopeAll})
	if err != nil {
		t.Fatalf("cannot read the assignments: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("the fixture holds %d assignments, want exactly 1", len(list))
	}
	return list[0]
}

// see reads the semester's assignments through one filter and returns who holds them.
func (f assignmentFixture) see(t *testing.T, filter policy.AssignmentFilter) []uuid.UUID {
	t.Helper()

	list, err := f.assignments.Assignments(t.Context(),
		domain.AssignmentQuery{SemesterCode: f.semester.Code}, filter)
	if err != nil {
		t.Fatalf("cannot read the assignments: %v", err)
	}

	out := make([]uuid.UUID, 0, len(list))
	for _, a := range list {
		out = append(out, a.Assignee.PersonID)
	}
	return out
}

// publish closes the confidentiality window over the assignments.
func (f assignmentFixture) publish(t *testing.T) {
	t.Helper()

	if _, err := f.schema.Pool.Exec(t.Context(),
		`UPDATE semester SET assignments_published_at = now() WHERE id = $1`,
		f.semester.ID); err != nil {
		t.Fatalf("cannot publish: %v", err)
	}
}

// TestEveryAssignmentQueryIsFiltered reads the file a leak would be written in.
//
// Every SELECT over the assignment table has to carry the filter, and there has to be no COUNT at
// all. The second half is the one that is easy to get wrong later: a count that skipped the
// predicate would answer "two of the three laboratories are taken" — the confidential fact with
// the names taken out — while looking like an ordinary convenience.
//
// Reading the file from disk rather than embedding it, for the reason its wish counterpart gives:
// db/embed.go carries the migrations because the server applies them, and the queries have no
// runtime use at all.
func TestEveryAssignmentQueryIsFiltered(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../db/queries/assignment.sql")
	if err != nil {
		t.Fatalf("cannot read the query file: %v", err)
	}
	sql := string(raw)

	if strings.Contains(strings.ToUpper(sql), "COUNT(") {
		t.Error("db/queries/assignment.sql contains a COUNT. Before publication there are no " +
			"counts over assignments — an aggregate that skips the filter is the same failure " +
			"as a list that skips it, only harder to notice. Count the rows you were allowed " +
			"to read.")
	}

	blocks := regexp.MustCompile(`(?m)^-- name: (\w+) :(\w+)$`).FindAllStringSubmatchIndex(sql, -1)
	if len(blocks) == 0 {
		t.Fatal("no queries found in db/queries/assignment.sql — this test checked nothing")
	}

	checked := 0
	for i, block := range blocks {
		name := sql[block[2]:block[3]]
		end := len(sql)
		if i+1 < len(blocks) {
			end = blocks[i+1][0]
		}
		body := sql[block[1]:end]

		// Only the queries that hand out an assignment, which is what the projection gives away:
		// a row with a name on it. The two context lookups also read this table and carry no
		// filter, deliberately — they answer "may I write here" and return ids, a phase and a
		// publication date, never a note or a name. What keeps *them* from being an oracle is not
		// a WHERE clause but an order: the service reads through the visibility filter before it
		// uses either of them to refuse anything, which
		// TestClearingSomebodyElsesAssignmentSaysNothingAboutIt asserts.
		if !strings.Contains(body, "AS assignee_name") {
			continue
		}
		checked++

		for _, required := range []string{
			`sqlc.arg('scope')::text = 'all'`,
			`a.person_id = sqlc.arg(assignee_id)::uuid`,
			`prog.id = ANY (sqlc.arg(programme_ids)::uuid[])`,
			`msg.subject_group_id = ANY (sqlc.arg(subject_group_ids)::uuid[])`,
		} {
			if !strings.Contains(body, required) {
				t.Errorf("query %s reads the assignment table without %q. The visibility rule "+
					"is a WHERE clause; a query written without it is not a slow query, it is "+
					"a leak.", name, required)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no query in the file reads the assignment table — this test checked nothing")
	}
}

// TestAColleagueSeesNoAssignmentBeforePublication is the rule in its plainest form.
func TestAColleagueSeesNoAssignmentBeforePublication(t *testing.T) {
	t.Parallel()

	f := newAssignmentFixture(t)
	f.fill(t, f.lecture, domain.Assignee{PersonID: testdata.Eins.ID()})

	stranger := testdata.Zwei.Actor(principal.KindInteractive, string(policy.RoleLecturer))
	seen := f.see(t, policy.AssignmentVisibility(stranger, f.semester.State()))
	if len(seen) != 0 {
		t.Errorf("an uninvolved colleague saw %d assignments before publication, want none", len(seen))
	}

	holder := testdata.Eins.Actor(principal.KindInteractive, string(policy.RoleLecturer))
	if got := f.see(t, policy.AssignmentVisibility(holder, f.semester.State())); len(got) != 1 {
		t.Errorf("the holder saw %d of their own assignments, want 1", len(got))
	}
}

// TestPublicationLetsEverybodyReadAssignments is the other side of the same rule.
func TestPublicationLetsEverybodyReadAssignments(t *testing.T) {
	t.Parallel()

	f := newAssignmentFixture(t)
	f.fill(t, f.lecture, domain.Assignee{PersonID: testdata.Eins.ID()})
	f.publish(t)

	published, err := store.NewSemesters(f.schema.Pool).SemesterByCode(t.Context(), f.semester.Code)
	if err != nil {
		t.Fatalf("cannot re-read the semester: %v", err)
	}
	if !published.State().AssignmentsPublished() {
		t.Fatal("the semester does not read as published")
	}

	stranger := testdata.Zwei.Actor(principal.KindInteractive, string(policy.RoleLecturer))
	if got := f.see(t, policy.AssignmentVisibility(stranger, published.State())); len(got) != 1 {
		t.Errorf("after publication a colleague saw %d assignments, want 1", len(got))
	}
}

// TestALeadReachesOnlyTheirOwnSubject is the scoped half of the read rule, in SQL.
func TestALeadReachesOnlyTheirOwnSubjectsAssignments(t *testing.T) {
	t.Parallel()

	f := newAssignmentFixture(t)
	f.fill(t, f.lecture, domain.Assignee{PersonID: testdata.Eins.ID()})

	mine := policy.AssignmentFilter{
		Scope:           policy.AssignmentReadScopeOwnOrScoped,
		AssigneeID:      testdata.Drei.ID(),
		SubjectGroupIDs: []uuid.UUID{f.group},
	}
	if got := f.see(t, mine); len(got) != 1 {
		t.Errorf("the lead of the module's subject group saw %d assignments, want 1", len(got))
	}

	theirs := policy.AssignmentFilter{
		Scope:           policy.AssignmentReadScopeOwnOrScoped,
		AssigneeID:      testdata.Drei.ID(),
		SubjectGroupIDs: []uuid.UUID{f.otherGroup},
	}
	if got := f.see(t, theirs); len(got) != 0 {
		t.Errorf("the lead of another subject group saw %d assignments, want none", len(got))
	}
}

// TestMovingAModuleMovesWhoMayReadItsAssignments is the consequence of deriving responsibility
// rather than storing it.
//
// Re-cutting a subject group is an UPDATE on module_subject_group, and it changes retroactively
// who may read what hangs off its modules. That is the intended behaviour — whoever is
// responsible now is who may look now — and it only holds because nothing is copied onto the
// assignment row.
func TestMovingAModuleMovesWhoMayReadItsAssignments(t *testing.T) {
	t.Parallel()

	f := newAssignmentFixture(t)
	f.fill(t, f.lecture, domain.Assignee{PersonID: testdata.Eins.ID()})

	otherLead := policy.AssignmentFilter{
		Scope:           policy.AssignmentReadScopeOwnOrScoped,
		AssigneeID:      testdata.Drei.ID(),
		SubjectGroupIDs: []uuid.UUID{f.otherGroup},
	}
	if got := f.see(t, otherLead); len(got) != 0 {
		t.Fatalf("before the move the other lead saw %d assignments, want none", len(got))
	}

	if _, err := f.schema.Pool.Exec(t.Context(),
		`UPDATE module_subject_group SET subject_group_id = $2 WHERE module_id = $1`,
		f.module, f.otherGroup); err != nil {
		t.Fatalf("cannot move the module: %v", err)
	}

	if got := f.see(t, otherLead); len(got) != 1 {
		t.Errorf("after the move the new lead saw %d assignments, want 1", len(got))
	}
}

// TestAnAssignmentOnAnUnsortedModuleReachesNoSubjectGroupLead is the ordinary state until the
// faculty has worked through its catalogue, and it fails closed on that axis.
func TestAnAssignmentOnAnUnsortedModuleReachesNoSubjectGroupLead(t *testing.T) {
	t.Parallel()

	f := newAssignmentFixture(t)
	if _, err := f.schema.Pool.Exec(t.Context(),
		`DELETE FROM module_subject_group WHERE module_id = $1`, f.module); err != nil {
		t.Fatalf("cannot unsort the module: %v", err)
	}
	f.fill(t, f.lecture, domain.Assignee{PersonID: testdata.Eins.ID()})

	lead := policy.AssignmentFilter{
		Scope:           policy.AssignmentReadScopeOwnOrScoped,
		AssigneeID:      testdata.Drei.ID(),
		SubjectGroupIDs: []uuid.UUID{f.group, f.otherGroup},
	}
	if got := f.see(t, lead); len(got) != 0 {
		t.Errorf("a subject group lead saw %d assignments on a module in no group, want none",
			len(got))
	}

	// The programme axis still reaches it, which is the argument for having two.
	planner := policy.AssignmentFilter{
		Scope:        policy.AssignmentReadScopeOwnOrScoped,
		AssigneeID:   testdata.Vier.ID(),
		ProgrammeIDs: []uuid.UUID{f.programme},
	}
	if got := f.see(t, planner); len(got) != 1 {
		t.Errorf("the programme's lead saw %d assignments on an unsorted module, want 1", len(got))
	}
}

// TestFillingATakenPartIsRefusedRatherThanOverwriting is the conditional insert.
//
// The case it protects against is two people filling the same part at the same moment, which two
// write-eligible roles make ordinary rather than theoretical. Whoever arrives second is told, and
// the first decision stands.
func TestFillingATakenPartIsRefusedRatherThanOverwriting(t *testing.T) {
	t.Parallel()

	f := newAssignmentFixture(t)
	first := f.fill(t, f.lecture, domain.Assignee{PersonID: testdata.Eins.ID()})

	_, err := f.assignments.FillPart(t.Context(), f.lecture,
		domain.Assignee{PersonID: testdata.Zwei.ID()}, "", testdata.Vier.ID())
	if !errors.Is(err, domain.ErrPartAlreadyAssigned) {
		t.Fatalf("filling a taken part answered %v, want ErrPartAlreadyAssigned", err)
	}

	held, err := f.assignments.AssignmentByID(t.Context(), first,
		policy.AssignmentFilter{Scope: policy.AssignmentReadScopeAll})
	if err != nil || held == nil {
		t.Fatalf("cannot re-read the assignment: %v", err)
	}
	if held.Assignee.PersonID != testdata.Eins.ID() {
		t.Error("the second caller overwrote the first one's decision")
	}
}

// TestReplacingWhatIsNoLongerThereIsRefused is the compare-and-set.
//
// The caller names the assignment they were looking at. If somebody else has decided in between,
// they are told rather than taking a decision away that they never saw.
func TestReplacingWhatIsNoLongerThereIsRefused(t *testing.T) {
	t.Parallel()

	f := newAssignmentFixture(t)
	stale := f.fill(t, f.lecture, domain.Assignee{PersonID: testdata.Eins.ID()})

	// Somebody else clears it and fills it afresh — the assignment the first caller saw is gone.
	if _, err := f.assignments.ClearAssignment(t.Context(), stale); err != nil {
		t.Fatalf("cannot clear: %v", err)
	}
	current := f.fill(t, f.lecture, domain.Assignee{PersonID: testdata.Zwei.ID()})

	_, err := f.assignments.ReplaceAssignment(t.Context(), f.lecture, stale,
		domain.Assignee{PersonID: testdata.Drei.ID()}, "", testdata.Vier.ID())
	if !errors.Is(err, domain.ErrAssignmentMovedOn) {
		t.Fatalf("replacing a stale assignment answered %v, want ErrAssignmentMovedOn", err)
	}

	// And naming the current one works, so the refusal is about staleness and not about replacing.
	if _, err := f.assignments.ReplaceAssignment(t.Context(), f.lecture, current,
		domain.Assignee{PersonID: testdata.Drei.ID()}, "", testdata.Vier.ID()); err != nil {
		t.Fatalf("replacing the current assignment failed: %v", err)
	}
}

// TestATeacherWithAnAccountIsStoredAsTheAccount is the canonicalisation.
//
// Without it the same colleague would sit in this table under two identities depending on which
// list somebody picked them from, and "my assignments" — which matches the account — would find
// only half of what they teach.
func TestATeacherWithAnAccountIsStoredAsTheAccount(t *testing.T) {
	t.Parallel()

	f := newAssignmentFixture(t)
	lead := testdata.Drei.Actor(principal.KindInteractive, string(policy.RoleSubjectGroupLead))
	lead.RoleScopes = []principal.RoleScope{{Role: string(policy.RoleSubjectGroupLead), SubjectGroupID: f.group}}
	f.assign(t, lead, f.lecture, domain.Assignee{TeacherID: f.withAccount})

	held := f.only(t)
	if held.Assignee.PersonID != testdata.Eins.ID() {
		t.Errorf("the assignment names person %s, want the account of the teacher's address (%s)",
			held.Assignee.PersonID, testdata.Eins.ID())
	}
	if held.Assignee.TeacherID != uuid.Nil {
		t.Error("the assignment kept the teacher id as well, so the colleague is two identities")
	}
}

// TestSomebodyWithoutAnAccountCanHoldAPart is the case the two columns exist for.
//
// A lecturer on contract is assignable and will never sign in. What follows is only that the row
// is nobody's "own" — it is read through responsibility or after publication, like any other.
func TestSomebodyWithoutAnAccountCanHoldAPart(t *testing.T) {
	t.Parallel()

	f := newAssignmentFixture(t)
	lead := testdata.Drei.Actor(principal.KindInteractive, string(policy.RoleSubjectGroupLead))
	lead.RoleScopes = []principal.RoleScope{{Role: string(policy.RoleSubjectGroupLead), SubjectGroupID: f.group}}
	f.assign(t, lead, f.lecture, domain.Assignee{TeacherID: f.withoutAccount})

	held := f.only(t)
	if held.Assignee.TeacherID != f.withoutAccount {
		t.Errorf("the assignment names teacher %s, want %s", held.Assignee.TeacherID, f.withoutAccount)
	}
	if held.Assignee.PersonID != uuid.Nil {
		t.Error("an assignee with no account was given one")
	}
	if held.Assignee.Name == "" {
		t.Error("the assignee has no name, so no screen can render the row")
	}

	// Nobody's own: the own-only filter reaches it for nobody at all, whatever id is asked with.
	for _, who := range []uuid.UUID{testdata.Eins.ID(), testdata.Zwei.ID(), uuid.Nil} {
		got := f.see(t, policy.AssignmentFilter{
			Scope: policy.AssignmentReadScopeOwn, AssigneeID: who,
		})
		if len(got) != 0 {
			t.Errorf("the own-only filter for %s reached an assignment with no account", who)
		}
	}
}

// TestAStaffedPartCannotBeRemoved is the incoming RESTRICT migration 17 puts back.
//
// Migration 16 removed it deliberately — re-cutting the parts of an instance must not be blocked
// by somebody's *interest*. An assignment is not interest, and a lecture that is filled must not
// disappear because somebody edited the number of laboratory groups.
func TestAStaffedPartCannotBeRemoved(t *testing.T) {
	t.Parallel()

	f := newAssignmentFixture(t)
	f.fill(t, f.lab, domain.Assignee{PersonID: testdata.Eins.ID()})

	demand := store.NewDemand(f.schema.Pool, store.NewModules(f.schema.Pool))
	_, err := demand.DeleteInstancePart(t.Context(), f.lab)
	if !errors.Is(err, domain.ErrPartAssigned) {
		t.Fatalf("removing a staffed part answered %v, want ErrPartAssigned", err)
	}

	// The unstaffed part next to it still goes, so the refusal is about this part and not about
	// the instance.
	if _, err := demand.DeleteInstancePart(t.Context(), f.lecture); err != nil {
		t.Errorf("removing the unstaffed part failed: %v", err)
	}
}

// TestAnInstanceWithAStaffedPartCannotBeWithdrawn is the same protection one level up, and it
// keeps the opaque refusal.
//
// Two things can hang off an instance now — a wish on it, an assignment on one of its parts — and
// the refusal must not say which, because the caller may not be entitled to know that a wish
// exists.
func TestAnInstanceWithAStaffedPartCannotBeWithdrawn(t *testing.T) {
	t.Parallel()

	f := newAssignmentFixture(t)
	f.fill(t, f.lecture, domain.Assignee{PersonID: testdata.Eins.ID()})

	err := store.NewDemand(f.schema.Pool, store.NewModules(f.schema.Pool)).
		DeleteCourseInstance(t.Context(), f.instance)
	if !errors.Is(err, domain.ErrInstanceInUse) {
		t.Fatalf("withdrawing an instance with a staffed part answered %v, want ErrInstanceInUse", err)
	}
	if strings.Contains(strings.ToLower(err.Error()), "zuteil") ||
		strings.Contains(strings.ToLower(err.Error()), "wunsch") {
		t.Errorf("the refusal names what hangs off the instance: %q", err)
	}
}

// TestOwnAssignmentsAcrossSemesters is the query with no semester, and the one caller allowed to
// ask it.
func TestOwnAssignmentsAcrossSemesters(t *testing.T) {
	t.Parallel()

	f := newAssignmentFixture(t)
	f.fill(t, f.lecture, domain.Assignee{PersonID: testdata.Eins.ID()})
	f.fill(t, f.lab, domain.Assignee{PersonID: testdata.Zwei.ID()})

	mine, err := f.assignments.Assignments(t.Context(),
		domain.AssignmentQuery{Person: testdata.Eins.ID()},
		policy.AssignmentFilter{
			Scope: policy.AssignmentReadScopeOwn, AssigneeID: testdata.Eins.ID(),
		})
	if err != nil {
		t.Fatalf("cannot read my assignments: %v", err)
	}
	if len(mine) != 1 {
		t.Fatalf("my assignments across every semester: %d, want 1", len(mine))
	}
	if mine[0].Instance.SemesterCode != f.semester.Code {
		t.Errorf("the row carries semester %q, want %q", mine[0].Instance.SemesterCode, f.semester.Code)
	}
	if mine[0].Part.TeachingHours == nil {
		t.Error("the row carries no teaching hours, so nobody can add up what they hold")
	}
}

// TestClearingSomebodyElsesAssignmentSaysNothingAboutIt is what keeps the two write-context
// lookups from being an oracle.
//
// They read the assignment table without the visibility filter — they have to, because they
// answer "may I write here" and the answer must not depend on whether the caller may read what is
// there. What makes that safe is the order the service does things in: it reads through the
// filter first, so a caller who could not have seen the row is told it does not exist rather than
// being told it is somebody else's.
//
// The two halves below are the whole assertion. Before publication the answer is "no such
// assignment", because saying anything else would confirm one exists. After publication its
// existence is public, so the honest answer is the one that names the repair.
func TestClearingSomebodyElsesAssignmentSaysNothingAboutIt(t *testing.T) {
	t.Parallel()

	f := newAssignmentFixture(t)
	id := f.fill(t, f.lecture, domain.Assignee{PersonID: testdata.Eins.ID()})

	stranger := testdata.Zwei.Actor(principal.KindInteractive, string(policy.RoleLecturer))

	err := f.service.Clear(t.Context(), stranger, id)
	if !errors.Is(err, domain.ErrAssignmentNotFound) {
		t.Errorf("clearing a confidential assignment answered %v, want ErrAssignmentNotFound — "+
			"anything else confirms that the assignment exists", err)
	}

	f.publish(t)

	err = f.service.Clear(t.Context(), stranger, id)
	if !errors.Is(err, domain.ErrNotYourSubject) {
		t.Errorf("after publication, clearing somebody else's assignment answered %v, want "+
			"ErrNotYourSubject — its existence is public by then, so the refusal should name "+
			"the repair", err)
	}
}

// TestTheHolderCannotGiveTheirOwnPartBack is a consequence worth stating rather than discovering.
//
// Holding a part means being able to read the assignment; it does not mean being able to change
// it. Filling is a decision the subject group or the programme takes, and somebody handing their
// own teaching back would be taking it — the conversation that has to happen first is the point.
func TestTheHolderCannotGiveTheirOwnPartBack(t *testing.T) {
	t.Parallel()

	f := newAssignmentFixture(t)
	id := f.fill(t, f.lecture, domain.Assignee{PersonID: testdata.Eins.ID()})

	holder := testdata.Eins.Actor(principal.KindInteractive, string(policy.RoleLecturer))
	if err := f.service.Clear(t.Context(), holder, id); !errors.Is(err, domain.ErrNotYourSubject) {
		t.Errorf("the holder clearing their own assignment answered %v, want ErrNotYourSubject", err)
	}
}
