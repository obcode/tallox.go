package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
	"github.com/obcode/tallox.go/internal/store"
	"github.com/obcode/tallox.go/internal/testdata"
)

// Confirming one's assignments against a real database: the state is a comparison of two digests
// computed by one view, so the only test that means anything is one that changes rows and reads
// the comparison back.

func (f assignmentFixture) mine(t *testing.T, who testdata.Persona) domain.AssignmentConfirmation {
	t.Helper()

	got, err := f.service.MyConfirmation(t.Context(),
		who.Actor(principal.KindInteractive, string(policy.RoleLecturer)), f.semester.Code)
	if err != nil {
		t.Fatalf("cannot read the confirmation: %v", err)
	}
	return got
}

func (f assignmentFixture) confirm(t *testing.T, who testdata.Persona) {
	t.Helper()

	if _, err := f.service.ConfirmMine(t.Context(),
		who.Actor(principal.KindInteractive, string(policy.RoleLecturer)), f.semester.Code); err != nil {
		t.Fatalf("cannot confirm: %v", err)
	}
}

// TestAConfirmationGoesStaleWhenTheAssignmentsChange is the derived state, row by row: every kind
// of change to what somebody holds makes their confirmation out of date, and confirming again
// makes it current.
func TestAConfirmationGoesStaleWhenTheAssignmentsChange(t *testing.T) {
	t.Parallel()

	changes := []struct {
		name   string
		change func(t *testing.T, f assignmentFixture, lecture uuid.UUID)
	}{
		{"another part is added", func(t *testing.T, f assignmentFixture, _ uuid.UUID) {
			f.fill(t, f.lab, domain.Assignee{PersonID: testdata.Eins.ID()})
		}},
		{"the part is handed to somebody else", func(t *testing.T, f assignmentFixture, lecture uuid.UUID) {
			if ok, err := f.assignments.ClearAssignment(t.Context(), lecture); err != nil || !ok {
				t.Fatalf("cannot clear: %v", err)
			}
			f.fill(t, f.lecture, domain.Assignee{PersonID: testdata.Zwei.ID()})
		}},
		{"the part's hours are corrected", func(t *testing.T, f assignmentFixture, _ uuid.UUID) {
			if _, err := f.schema.Pool.Exec(t.Context(),
				`UPDATE instance_part SET teaching_hours = 3 WHERE id = $1`, f.lecture); err != nil {
				t.Fatalf("cannot change the hours: %v", err)
			}
		}},
	}

	for _, tt := range changes {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newAssignmentFixture(t)
			lecture := f.fill(t, f.lecture, domain.Assignee{PersonID: testdata.Eins.ID()})

			if got := f.mine(t, testdata.Eins).State; got != domain.ConfirmationOpen {
				t.Fatalf("before confirming the state is %s, want OPEN", got)
			}
			f.confirm(t, testdata.Eins)
			confirmed := f.mine(t, testdata.Eins)
			if confirmed.State != domain.ConfirmationConfirmed || confirmed.ConfirmedAt.IsZero() {
				t.Fatalf("after confirming: %+v, want CONFIRMED with a time", confirmed)
			}

			tt.change(t, f, lecture)

			stale := f.mine(t, testdata.Eins)
			if stale.State != domain.ConfirmationStale {
				t.Fatalf("after the change the state is %s, want STALE", stale.State)
			}
			if !stale.ConfirmedAt.Equal(confirmed.ConfirmedAt) {
				t.Error("a stale confirmation lost the time it was given")
			}

			// Handed away entirely there is nothing left to confirm; otherwise confirming again
			// makes it current.
			if tt.name == "the part is handed to somebody else" {
				_, err := f.service.ConfirmMine(t.Context(),
					testdata.Eins.Actor(principal.KindInteractive, string(policy.RoleLecturer)),
					f.semester.Code)
				if !errors.Is(err, domain.ErrNothingToConfirm) {
					t.Fatalf("confirming nothing answered %v, want ErrNothingToConfirm", err)
				}
				return
			}
			f.confirm(t, testdata.Eins)
			if got := f.mine(t, testdata.Eins).State; got != domain.ConfirmationConfirmed {
				t.Errorf("after confirming again the state is %s, want CONFIRMED", got)
			}
		})
	}
}

// TestSomebodyElsesChangeLeavesMyConfirmationStanding: the digest is per person.
func TestSomebodyElsesChangeLeavesMyConfirmationStanding(t *testing.T) {
	t.Parallel()

	f := newAssignmentFixture(t)
	f.fill(t, f.lecture, domain.Assignee{PersonID: testdata.Eins.ID()})
	f.confirm(t, testdata.Eins)

	f.fill(t, f.lab, domain.Assignee{PersonID: testdata.Zwei.ID()})

	if got := f.mine(t, testdata.Eins).State; got != domain.ConfirmationConfirmed {
		t.Errorf("a colleague's new assignment made mine %s, want CONFIRMED", got)
	}
}

// TestConfirmingNothingIsRefused: an empty confirmation would read as "checked" on the day
// somebody is first given something.
func TestConfirmingNothingIsRefused(t *testing.T) {
	t.Parallel()

	f := newAssignmentFixture(t)
	_, err := f.service.ConfirmMine(t.Context(),
		testdata.Zwei.Actor(principal.KindInteractive, string(policy.RoleLecturer)), f.semester.Code)
	if !errors.Is(err, domain.ErrNothingToConfirm) {
		t.Fatalf("confirming without assignments answered %v, want ErrNothingToConfirm", err)
	}

	var rows int
	if err := f.schema.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM assignment_confirmation`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Errorf("a refused confirmation left %d rows behind", rows)
	}
}

// TestConfirmationsReachOnlyAsFarAsTheAssignments is the visibility rule: others' confirmations
// are listed only for people whose assignments the caller may read.
func TestConfirmationsReachOnlyAsFarAsTheAssignments(t *testing.T) {
	t.Parallel()

	f := newAssignmentFixture(t)
	f.fill(t, f.lecture, domain.Assignee{PersonID: testdata.Eins.ID()})
	f.confirm(t, testdata.Eins)

	read := func(actor principal.Actor) []domain.AssignmentConfirmation {
		t.Helper()
		list, err := f.service.Confirmations(t.Context(), actor, f.semester.Code)
		if err != nil {
			t.Fatalf("cannot list the confirmations: %v", err)
		}
		return list
	}

	stranger := testdata.Zwei.Actor(principal.KindInteractive, string(policy.RoleLecturer))
	if got := read(stranger); len(got) != 0 {
		t.Errorf("an uninvolved colleague saw %d confirmations before publication, want none", len(got))
	}

	deans := testdata.Vier.Actor(principal.KindInteractive, string(policy.RoleDeansOffice))
	got := read(deans)
	if len(got) != 1 || got[0].PersonID != testdata.Eins.ID() ||
		got[0].State != domain.ConfirmationConfirmed {
		t.Errorf("the dean's office read %+v, want Eins CONFIRMED", got)
	}

	f.publish(t)
	if got := read(stranger); len(got) != 1 {
		t.Errorf("after publication a colleague saw %d confirmations, want 1", len(got))
	}
}

// TestTheDueDateIsSetMovedAndCleared, and only before FINAL.
func TestTheDueDateIsSetMovedAndCleared(t *testing.T) {
	t.Parallel()

	f := newAssignmentFixture(t)
	semesters := domain.NewSemesterService(store.NewSemesters(f.schema.Pool), nil)
	deans := testdata.Vier.Actor(principal.KindInteractive, string(policy.RoleDeansOffice))
	due := time.Date(2028, time.February, 1, 0, 0, 0, 0, time.UTC)

	set, err := semesters.SetAssignmentConfirmationDue(t.Context(), deans, f.semester.Code, due)
	if err != nil {
		t.Fatalf("cannot set the date: %v", err)
	}
	if set.AssignmentConfirmationDue.Format("2006-01-02") != due.Format("2006-01-02") {
		t.Errorf("the date reads %v, want %v", set.AssignmentConfirmationDue, due)
	}

	lecturer := testdata.Eins.Actor(principal.KindInteractive, string(policy.RoleLecturer))
	if _, err := semesters.SetAssignmentConfirmationDue(t.Context(), lecturer, f.semester.Code,
		due); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("a lecturer setting the date answered %v, want ErrForbidden", err)
	}

	cleared, err := semesters.SetAssignmentConfirmationDue(t.Context(), deans, f.semester.Code,
		time.Time{})
	if err != nil {
		t.Fatalf("cannot clear the date: %v", err)
	}
	if !cleared.AssignmentConfirmationDue.IsZero() {
		t.Errorf("after clearing the date reads %v, want none", cleared.AssignmentConfirmationDue)
	}

	if _, err := f.schema.Pool.Exec(t.Context(), `UPDATE semester SET phase = 'FINAL' WHERE id = $1`,
		f.semester.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := semesters.SetAssignmentConfirmationDue(t.Context(), deans, f.semester.Code,
		due); !errors.Is(err, domain.ErrAssignmentPhaseClosed) {
		t.Errorf("setting the date of a FINAL semester answered %v, want ErrAssignmentPhaseClosed", err)
	}
}
