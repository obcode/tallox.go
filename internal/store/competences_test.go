package store_test

import (
	"errors"
	"os"
	"regexp"
	"slices"
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

// Competences against a real database, for the reason wishes_test.go gives: the read rule is a
// WHERE clause, the membership rule is a join, and "a person's row is their own" is a CHECK.

// competenceFixture is a catalogue with three modules in one subject group, a second group, and
// three people. Eins and Zwei are members of the group; Drei is not.
type competenceFixture struct {
	schema  *storetest.Schema
	store   *store.Competences
	service *domain.CompetenceService
	group   uuid.UUID
	other   uuid.UUID
	// compulsory is compulsory in its home programme; alsoCompulsory is compulsory in the newer
	// regulations; elective is elective everywhere. All three are at home in programme A.
	compulsory     uuid.UUID
	alsoCompulsory uuid.UUID
	elective       uuid.UUID
	retired        uuid.UUID
	programme      uuid.UUID
	otherProgramme uuid.UUID
}

func newCompetenceFixture(t *testing.T) competenceFixture {
	t.Helper()

	s := storetest.New(t)
	storetest.SeedZPACatalogue(t, s)
	project(t, s)

	storetest.SeedPerson(t, s, testdata.Eins, "LECTURER")
	storetest.SeedPerson(t, s, testdata.Zwei, "LECTURER")
	storetest.SeedPerson(t, s, testdata.Drei, "LECTURER", "SUBJECT_GROUP_LEAD")

	competences := store.NewCompetences(s.Pool)
	f := competenceFixture{
		schema:         s,
		store:          competences,
		service:        domain.NewCompetenceService(competences),
		group:          seedSubjectGroup(t, s, "MATHE", "Mathematik"),
		other:          seedSubjectGroup(t, s, "SWE", "Softwarefächer"),
		compulsory:     moduleID(t, s, storetest.FixtureModuleOrdinary),
		alsoCompulsory: moduleID(t, s, storetest.FixtureModuleDutyDiffers),
		elective:       moduleID(t, s, storetest.FixtureModuleTwoSlots),
		retired:        moduleID(t, s, storetest.FixtureModuleRetired),
		programme:      programmeID(t, s, storetest.FixtureProgrammeA),
		otherProgramme: programmeID(t, s, storetest.FixtureProgrammeB),
	}

	for _, m := range []uuid.UUID{f.compulsory, f.alsoCompulsory, f.elective, f.retired} {
		f.exec(t, `INSERT INTO module_subject_group (module_id, subject_group_id) VALUES ($1, $2)`, m, f.group)
	}
	f.join(t, testdata.Eins)
	f.join(t, testdata.Zwei)
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

func (f competenceFixture) lead(t *testing.T, who testdata.Persona, group uuid.UUID) principal.Actor {
	t.Helper()
	f.exec(t, `INSERT INTO person_subject_group_scope (person_id, role, subject_group_id)
		VALUES ($1, 'SUBJECT_GROUP_LEAD', $2)`, who.ID(), group)
	actor := who.Actor(principal.KindInteractive, "LECTURER", "SUBJECT_GROUP_LEAD")
	actor.RoleScopes = []principal.RoleScope{{Role: "SUBJECT_GROUP_LEAD", SubjectGroupID: group}}
	return actor
}

func (f competenceFixture) state(t *testing.T, who testdata.Persona, module uuid.UUID,
	level domain.CompetenceLevel) *domain.Competence {
	t.Helper()
	c, err := f.service.SetMine(t.Context(), who.Actor(principal.KindInteractive, "LECTURER"),
		module, level, "")
	if err != nil {
		t.Fatalf("%s cannot state a competence: %v", who.Name, err)
	}
	return c
}

func (f competenceFixture) seen(t *testing.T, filter policy.CompetenceFilter) []string {
	t.Helper()
	rows, err := f.store.Competences(t.Context(), domain.CompetenceQuery{}, filter)
	if err != nil {
		t.Fatalf("cannot read the competences: %v", err)
	}
	mails := make([]string, 0, len(rows))
	for _, c := range rows {
		mails = append(mails, c.Holder.Mail)
	}
	return mails
}

// Every query that reads statements carries the filter. The ones that do not are named below with
// the reason they may not, so that a new one has to be argued rather than slipped in.
func TestEveryCompetenceQueryIsFiltered(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../db/queries/competence.sql")
	if err != nil {
		t.Fatalf("cannot read the competence queries: %v", err)
	}
	sql := string(raw)

	guardedElsewhere := []string{
		// Counts only the caller's own rows: person_id is the argument.
		"OwnCompulsoryCounts",
		// Refused in internal/domain unless policy.MayReadSubjectGroupCompetences.
		"MemberCompulsoryCounts", "ModulesWithoutCompetence",
		// Read the write context of one row for the write rule, and return no statement.
		"TeacherCompetenceContext", "CompetenceModuleContext",
	}

	blocks := regexp.MustCompile(`(?m)^-- name: (\w+) :(\w+)$`).FindAllStringSubmatchIndex(sql, -1)
	if len(blocks) == 0 {
		t.Fatal("no queries found — this test read the wrong file")
	}

	checked := 0
	for i, block := range blocks {
		name := sql[block[2]:block[3]]
		end := len(sql)
		if i+1 < len(blocks) {
			end = blocks[i+1][0]
		}
		body := sql[block[0]:end]

		if !strings.HasPrefix(strings.TrimSpace(stripComments(body)), "SELECT") ||
			slices.Contains(guardedElsewhere, name) {
			continue
		}
		checked++
		for _, required := range []string{
			`sqlc.arg('scope')::text = 'all'`,
			`c.person_id = sqlc.arg(owner_id)::uuid`,
			`m.home_programme_id = ANY (sqlc.arg(programme_ids)::uuid[])`,
			`msg.subject_group_id = ANY (sqlc.arg(subject_group_ids)::uuid[])`,
		} {
			if !strings.Contains(body, required) {
				t.Errorf("%s reads competences without %q — a query without the filter is a leak",
					name, required)
			}
		}
	}
	if checked != 2 {
		t.Errorf("checked %d filtered queries, want 2 (Competences, CompetenceByID) — a new "+
			"reading query either carries the filter or is named above with its reason", checked)
	}
}

func stripComments(sql string) string {
	var b strings.Builder
	for line := range strings.SplitSeq(sql, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// The read rule, as the query applies it.
func TestCompetencesAreReadByTheResponsibleOnly(t *testing.T) {
	t.Parallel()

	f := newCompetenceFixture(t)
	f.state(t, testdata.Eins, f.compulsory, domain.CompetenceWouldLike)

	for _, tc := range []struct {
		name   string
		filter policy.CompetenceFilter
		want   int
	}{
		{"a colleague", policy.CompetenceFilter{Scope: policy.CompetenceScopeOwn, OwnerID: testdata.Zwei.ID()}, 0},
		{"the owner", policy.CompetenceFilter{Scope: policy.CompetenceScopeOwn, OwnerID: testdata.Eins.ID()}, 1},
		{"the lead of the group", policy.CompetenceFilter{Scope: policy.CompetenceScopeOwnOrScoped,
			OwnerID: testdata.Drei.ID(), SubjectGroupIDs: []uuid.UUID{f.group}}, 1},
		{"the lead of another group", policy.CompetenceFilter{Scope: policy.CompetenceScopeOwnOrScoped,
			OwnerID: testdata.Drei.ID(), SubjectGroupIDs: []uuid.UUID{f.other}}, 0},
		{"the lead of the home programme", policy.CompetenceFilter{Scope: policy.CompetenceScopeOwnOrScoped,
			OwnerID: testdata.Drei.ID(), ProgrammeIDs: []uuid.UUID{f.programme}}, 1},
		// Module 101 counts in programme B as well, but B is not its home: responsibility for a
		// competence follows the module's home, since there is no demanding programme.
		{"the lead of a programme it merely counts in", policy.CompetenceFilter{
			Scope: policy.CompetenceScopeOwnOrScoped, OwnerID: testdata.Drei.ID(),
			ProgrammeIDs: []uuid.UUID{f.otherProgramme}}, 0},
		{"the dean's office", policy.CompetenceFilter{Scope: policy.CompetenceScopeAll}, 1},
		{"nobody", policy.CompetenceFilter{Scope: policy.CompetenceScopeNone}, 0},
		{"an unknown scope", policy.CompetenceFilter{Scope: "everything"}, 0},
	} {
		if got := f.seen(t, tc.filter); len(got) != tc.want {
			t.Errorf("%s sees %v, want %d rows", tc.name, got, tc.want)
		}
	}
}

// Only the subjects of one's own groups — and joining is what opens them.
func TestOwnCompetenceNeedsMembership(t *testing.T) {
	t.Parallel()

	f := newCompetenceFixture(t)
	drei := testdata.Drei.Actor(principal.KindInteractive, "LECTURER")

	_, err := f.service.SetMine(t.Context(), drei, f.compulsory, domain.CompetenceCanTeach, "")
	if !errors.Is(err, domain.ErrCompetenceOutsideGroups) {
		t.Fatalf("a non-member stating a competence: err = %v, want ErrCompetenceOutsideGroups", err)
	}

	f.join(t, testdata.Drei)
	if _, err := f.service.SetMine(t.Context(), drei, f.compulsory, domain.CompetenceCanTeach, ""); err != nil {
		t.Fatalf("after joining the group: %v", err)
	}

	// A module in no group at all cannot be stated for by anybody.
	unsorted := moduleID(t, f.schema, storetest.FixtureModuleWithoutName)
	_, err = f.service.SetMine(t.Context(), drei, unsorted, domain.CompetenceCanTeach, "")
	if !errors.Is(err, domain.ErrCompetenceOutsideGroups) {
		t.Errorf("a module in no subject group: err = %v, want ErrCompetenceOutsideGroups", err)
	}

	_, err = f.service.SetMine(t.Context(), drei, f.retired, domain.CompetenceCanTeach, "")
	if !errors.Is(err, domain.ErrCompetenceModuleRetired) {
		t.Errorf("a retired module: err = %v, want ErrCompetenceModuleRetired", err)
	}
}

// Leaving a group keeps the statement, marks it, and leaves it removable by its owner.
func TestLeavingAGroupKeepsTheStatement(t *testing.T) {
	t.Parallel()

	f := newCompetenceFixture(t)
	c := f.state(t, testdata.Eins, f.compulsory, domain.CompetenceCanTeach)
	if c.OutsideSubjectGroups {
		t.Fatal("a fresh statement in one's own group reads as outside it")
	}

	f.exec(t, `DELETE FROM person_subject_group WHERE person_id = $1`, testdata.Eins.ID())

	eins := testdata.Eins.Actor(principal.KindInteractive, "LECTURER")
	mine, err := f.service.Mine(t.Context(), eins)
	if err != nil || len(mine) != 1 {
		t.Fatalf("after leaving: %d rows, err %v — want the statement kept", len(mine), err)
	}
	if !mine[0].OutsideSubjectGroups {
		t.Error("after leaving the group the statement is not marked as outside it")
	}
	if err := f.service.WithdrawMine(t.Context(), eins, c.ID); err != nil {
		t.Errorf("the owner cannot remove a statement outside their groups: %v", err)
	}
}

// Stating twice is changing one's mind; withdrawing somebody else's is "not found".
func TestOwnStatementsAreCorrectionsAndPrivate(t *testing.T) {
	t.Parallel()

	f := newCompetenceFixture(t)
	first := f.state(t, testdata.Eins, f.compulsory, domain.CompetenceWouldLike)
	second := f.state(t, testdata.Eins, f.compulsory, domain.CompetenceCanTeach)
	if first.ID != second.ID || second.Level != domain.CompetenceCanTeach {
		t.Errorf("stating twice made %s then %s (%s), want one row changed", first.ID, second.ID, second.Level)
	}

	zwei := testdata.Zwei.Actor(principal.KindInteractive, "LECTURER")
	if err := f.service.WithdrawMine(t.Context(), zwei, first.ID); !errors.Is(err, domain.ErrCompetenceNotFound) {
		t.Errorf("withdrawing somebody else's: err = %v, want ErrCompetenceNotFound", err)
	}
}

// A person row is its owner's statement, and the database says so on its own.
func TestAPersonRowIsTheirOwnStatement(t *testing.T) {
	t.Parallel()

	f := newCompetenceFixture(t)
	_, err := f.schema.Pool.Exec(t.Context(),
		`INSERT INTO competence (module_id, person_id, level, entered_by) VALUES ($1, $2, 'CAN_TEACH', $3)`,
		f.compulsory, testdata.Eins.ID(), testdata.Drei.ID())
	if err == nil {
		t.Error("the database accepted a person's competence entered by somebody else")
	}
}

// The subject group lead enters for a teacher without an account — and only for one.
func TestTheLeadEntersForATeacherWithoutAnAccount(t *testing.T) {
	t.Parallel()

	f := newCompetenceFixture(t)
	ctx := t.Context()
	withoutAccount := teacherID(t, f.schema, storetest.FixtureTeacherWithoutMail)
	withAccount := teacherID(t, f.schema, storetest.FixtureTeacherOrdinary) // Eins' address

	storetest.SeedPerson(t, f.schema, testdata.Vier, "LECTURER", "SUBJECT_GROUP_LEAD")
	otherLead := f.lead(t, testdata.Vier, f.other)
	_, err := f.service.SetForTeacher(ctx, otherLead, f.compulsory, withoutAccount, domain.CompetenceCanTeach, "")
	if !errors.Is(err, domain.ErrCompetenceForTeacherRefused) {
		t.Fatalf("the lead of another group: err = %v, want ErrCompetenceForTeacherRefused", err)
	}

	lead := f.lead(t, testdata.Drei, f.group)
	c, err := f.service.SetForTeacher(ctx, lead, f.compulsory, withoutAccount, domain.CompetenceCanTeach, "Übung")
	if err != nil {
		t.Fatalf("the lead of the group cannot enter for a teacher: %v", err)
	}
	if c.Holder.TeacherID != withoutAccount || c.Holder.PersonID != uuid.Nil {
		t.Errorf("the row names %+v, want the teacher", c.Holder)
	}

	var enteredBy uuid.UUID
	if err := f.schema.Pool.QueryRow(ctx, `SELECT entered_by FROM competence WHERE id = $1`, c.ID).
		Scan(&enteredBy); err != nil || enteredBy != testdata.Drei.ID() {
		t.Errorf("entered_by = %s (%v), want the lead", enteredBy, err)
	}

	_, err = f.service.SetForTeacher(ctx, lead, f.compulsory, withAccount, domain.CompetenceCanTeach, "")
	if !errors.Is(err, domain.ErrCompetenceTeacherHasAccount) {
		t.Errorf("a teacher with an account: err = %v, want ErrCompetenceTeacherHasAccount", err)
	}

	// Withdrawing: the other lead is told "not found", the right one succeeds.
	if err := f.service.WithdrawForTeacher(ctx, otherLead, c.ID); !errors.Is(err, domain.ErrCompetenceNotFound) {
		t.Errorf("the other lead withdrawing: err = %v, want ErrCompetenceNotFound", err)
	}
	if err := f.service.WithdrawForTeacher(ctx, lead, c.ID); err != nil {
		t.Errorf("the lead withdrawing: %v", err)
	}

	// And a person's own statement is never a teacher row the lead could remove.
	own := f.state(t, testdata.Eins, f.compulsory, domain.CompetenceCanTeach)
	if err := f.service.WithdrawForTeacher(ctx, lead, own.ID); !errors.Is(err, domain.ErrCompetenceNotFound) {
		t.Errorf("the lead removing a person's statement: err = %v, want ErrCompetenceNotFound", err)
	}
}

// The minimum counts CAN_TEACH on compulsory, active modules — nothing else.
func TestTheMinimumCountsCompulsoryCanTeachOnly(t *testing.T) {
	t.Parallel()

	f := newCompetenceFixture(t)
	f.state(t, testdata.Eins, f.compulsory, domain.CompetenceCanTeach)
	f.state(t, testdata.Eins, f.alsoCompulsory, domain.CompetenceWouldLike)
	f.state(t, testdata.Eins, f.elective, domain.CompetenceCanTeach)

	status, err := f.service.MyStatus(t.Context(), testdata.Eins.Actor(principal.KindInteractive, "LECTURER"))
	if err != nil {
		t.Fatalf("cannot read the status: %v", err)
	}
	if len(status) != 1 || status[0].SubjectGroup.ID != f.group {
		t.Fatalf("status = %+v, want the one group", status)
	}
	if status[0].CanTeachCompulsory != 1 || status[0].Minimum != policy.MinCompulsoryCompetences {
		t.Errorf("count %d of %d, want 1 of %d", status[0].CanTeachCompulsory, status[0].Minimum,
			policy.MinCompulsoryCompetences)
	}

	lead := f.lead(t, testdata.Drei, f.group)
	members, err := f.service.MemberStatus(t.Context(), lead, f.group)
	if err != nil {
		t.Fatalf("the lead cannot read the member status: %v", err)
	}
	counts := map[uuid.UUID]int{}
	for _, m := range members {
		counts[m.Person.ID] = m.CanTeachCompulsory
	}
	if counts[testdata.Eins.ID()] != 1 || counts[testdata.Zwei.ID()] != 0 || len(members) != 2 {
		t.Errorf("member counts = %v, want Eins 1 and Zwei 0", counts)
	}

	without, err := f.service.ModulesWithoutCompetence(t.Context(), lead, f.group)
	if err != nil {
		t.Fatalf("the lead cannot read the gaps: %v", err)
	}
	ids := make([]uuid.UUID, 0, len(without))
	for _, m := range without {
		ids = append(ids, m.ID)
	}
	// 101 and 103 have a CAN_TEACH; 102 only a WOULD_LIKE; 105 is retired.
	if len(ids) != 1 || ids[0] != f.alsoCompulsory {
		t.Errorf("modules without competence = %v, want only %s", ids, f.alsoCompulsory)
	}
}

// The aggregates over a group are refused to anybody who could not read all of its rows.
func TestGroupAggregatesAreRefusedOutsideTheGroup(t *testing.T) {
	t.Parallel()

	f := newCompetenceFixture(t)
	f.state(t, testdata.Eins, f.compulsory, domain.CompetenceCanTeach)

	for name, actor := range map[string]principal.Actor{
		"a member":                testdata.Zwei.Actor(principal.KindInteractive, "LECTURER"),
		"the lead of another one": f.lead(t, testdata.Drei, f.other),
	} {
		if _, err := f.service.MemberStatus(t.Context(), actor, f.group); !errors.Is(err, domain.ErrCompetenceGroupRefused) {
			t.Errorf("%s reading the member status: err = %v", name, err)
		}
		if _, err := f.service.ModulesWithoutCompetence(t.Context(), actor, f.group); !errors.Is(err, domain.ErrCompetenceGroupRefused) {
			t.Errorf("%s reading the gaps: err = %v", name, err)
		}
	}
}

// The two lists of levels — domain and CHECK — agree.
func TestDatabaseAndDomainAgreeOnCompetenceLevels(t *testing.T) {
	t.Parallel()

	f := newCompetenceFixture(t)
	for _, level := range domain.AllCompetenceLevels() {
		f.state(t, testdata.Eins, f.compulsory, level)
	}
	if _, err := f.schema.Pool.Exec(t.Context(),
		`INSERT INTO competence (module_id, person_id, level, entered_by) VALUES ($1, $2, 'EXPERT', $2)`,
		f.elective, testdata.Eins.ID()); err == nil {
		t.Error("the database accepted a level the domain does not know")
	}
}

// A subject group's module list says which modules are compulsory — the competence page puts
// those first, and they are what the minimum counts.
func TestAGroupsModulesSayWhichAreCompulsory(t *testing.T) {
	t.Parallel()

	f := newCompetenceFixture(t)
	modules, err := store.NewSubjectGroups(f.schema.Pool).ModulesOfSubjectGroup(t.Context(), f.group)
	if err != nil {
		t.Fatalf("cannot read the group's modules: %v", err)
	}

	got := map[uuid.UUID]bool{}
	for _, m := range modules {
		got[m.ID] = m.Compulsory
	}
	for module, want := range map[uuid.UUID]bool{
		f.compulsory:     true,
		f.alsoCompulsory: true, // compulsory in the newer regulations only — that is enough
		f.elective:       false,
	} {
		if got[module] != want {
			t.Errorf("module %s: compulsory = %v, want %v", module, got[module], want)
		}
	}
}
