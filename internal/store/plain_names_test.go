package store_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/store"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// One register of names, against the real queries.
//
// Two spellings of the same colleague reach this system: the examination office's written-out
// name, which carries titles, and its surname-first one, which does not. Which of them a row
// ends up carrying depends on whether that person has an account here — a fact about this
// installation and not about them. A screen that shows both at once reads as two kinds of
// colleague, and that is the failure this file is about. It is not a crash, so nothing else
// would catch it.
//
// Against the database and not against domain.PlainName, which has its own test: the names
// arrive through a COALESCE chain in db/queries, and the point is what comes out of it.

// noTitles reports the first name in a list that still carries a title.
//
// "Dr." is enough of a probe: the fixture teachers are spelled "Prof. Dr. <Name>" written out
// and "<Name>, Prof." surname-first, so the doctorate is exactly what turning the second one
// round drops. A list that still contains one is a list reading the wrong column.
func noTitles(t *testing.T, what string, names ...string) {
	t.Helper()

	for _, name := range names {
		if strings.Contains(name, "Dr.") {
			t.Errorf("%s reads %q — the written-out spelling with its titles, "+
				"beside colleagues who are shown without", what, name)
		}
	}
}

// A person with an account and a person without one hold two parts of the same cohort. The
// first is named from person.name, the second from teacher.full_name — and the screen shows
// them one under the other.
func TestAssigneesReadAlikeWhetherOrNotTheyHaveAnAccount(t *testing.T) {
	t.Parallel()

	f := newAssignmentFixture(t)

	f.fill(t, f.lecture, domain.Assignee{PersonID: testdata.Eins.ID()})
	f.fill(t, f.lab, domain.Assignee{TeacherID: f.withoutAccount})

	list, err := f.assignments.Assignments(t.Context(),
		domain.AssignmentQuery{SemesterCode: f.semester.Code},
		policy.AssignmentFilter{Scope: policy.AssignmentReadScopeAll})
	if err != nil {
		t.Fatalf("cannot read the assignments: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("the fixture holds %d assignments, want two", len(list))
	}

	for _, a := range list {
		noTitles(t, "an assignee", a.Assignee.Name)
		if a.Assignee.Name == "" {
			t.Error("an assignee has no name at all")
		}
	}
}

// The list every candidate dropdown is built from.
func TestTheTeacherListIsSpelledWithoutTitles(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	storetest.SeedZPACatalogue(t, s)
	project(t, s)

	teachers, err := store.NewModules(s.Pool).Teachers(t.Context(),
		domain.TeacherFilter{IncludeInactive: true})
	if err != nil {
		t.Fatalf("cannot read the teachers: %v", err)
	}
	if len(teachers) == 0 {
		t.Fatal("the projection produced no teachers, so this asserts nothing")
	}

	for _, teacher := range teachers {
		noTitles(t, "a teacher", teacher.Name)
	}
	// The surname-first spelling is what a list is ordered by and is not a name anybody is
	// shown under — it stays exactly as the source publishes it.
	if teacherByMail(t, teachers, testdata.Eins.Mail).SortName != "Eins, Prof." {
		t.Errorf("the surname-first spelling was rewritten as well: %q",
			teacherByMail(t, teachers, testdata.Eins.Mail).SortName)
	}
}

// Admission is the one write that copies a name, and person.name is the one name with no
// surname-first spelling beside it at read time — `me` answers with it directly.
func TestAdmissionNamesTheAccountWithoutTitles(t *testing.T) {
	t.Parallel()

	s := admissionSchema(t)
	people := store.NewPeople(s.Pool)

	account, err := people.AdmitTeacher(t.Context(),
		teacherID(t, s, storetest.FixtureTeacherNotAdmitted), policy.RoleLecturer, uuid.Nil)
	if err != nil {
		t.Fatalf("cannot admit the teacher: %v", err)
	}
	if account.Person == nil {
		t.Fatal("admission produced no account")
	}

	var stored string
	if err := s.Pool.QueryRow(t.Context(),
		`SELECT name FROM person WHERE id = $1`, account.Person.ID).Scan(&stored); err != nil {
		t.Fatalf("cannot read the account back: %v", err)
	}
	noTitles(t, "the stored account name", stored)
	if stored != account.Person.Name {
		t.Errorf("the account reads %q and is stored as %q — two answers to one question",
			account.Person.Name, stored)
	}
}

// The backfill in migration 21, run against rows in the old shape.
//
// Reversibility does not exercise it: an empty person table migrates in both directions no
// matter what the UPDATE in the middle says. What is asserted here is what the migration's
// header promises — that it rewrites what admission wrote, and only that. An account somebody
// has renamed by hand is spelled the way they asked to be spelled, and a migration that
// "tidied" it would be undoing a decision it never saw.
func TestMigrationTwentyOneRenamesOnlyWhatAdmissionWrote(t *testing.T) {
	t.Parallel()

	s := admissionSchema(t)
	ctx := t.Context()

	// Back to the shape migration 20 left, so the backfill has something to move.
	if err := store.MigrateDownTo(ctx, s.DB, 20260830100000); err != nil {
		t.Fatalf("cannot roll back to before the backfill: %v", err)
	}

	// Two accounts of the two kinds, both linked to a teacher by their address: one named the
	// way admission named it, one named by hand.
	for _, row := range []struct{ mail, name string }{
		{"prof.sieben@example.org", "Prof. Dr. Sieben"},
		{"prof.acht@example.org", "Acht, genannt Acht"},
	} {
		if _, err := s.Pool.Exec(ctx,
			`INSERT INTO person (mail, name) VALUES ($1, $2)`, row.mail, row.name); err != nil {
			t.Fatalf("cannot write an account in the old shape: %v", err)
		}
	}

	if _, err := store.Migrate(ctx, s.DB); err != nil {
		t.Fatalf("cannot migrate up again: %v", err)
	}

	names := make(map[string]string)
	rows, err := s.Pool.Query(ctx, `SELECT mail::text, name FROM person`)
	if err != nil {
		t.Fatalf("cannot read the accounts: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var mail, name string
		if err := rows.Scan(&mail, &name); err != nil {
			t.Fatalf("cannot read an account: %v", err)
		}
		names[mail] = name
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("cannot read the accounts: %v", err)
	}

	if got := names["prof.sieben@example.org"]; got != "Prof. Sieben" {
		t.Errorf("the account admission named reads %q, want the plain spelling", got)
	}
	if got := names["prof.acht@example.org"]; got != "Acht, genannt Acht" {
		t.Errorf("an account named by hand was rewritten to %q", got)
	}
}
