package store_test

import (
	"testing"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/store"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// teacherByMail returns the fixture teacher with this address, or fails the test.
func teacherByMail(t *testing.T, teachers []domain.Teacher, mail string) domain.Teacher {
	t.Helper()

	for _, teacher := range teachers {
		if teacher.Mail == mail {
			return teacher
		}
	}
	t.Fatalf("the teacher list does not contain %s", mail)
	return domain.Teacher{}
}

// Teacher.isUser answers "may somebody of this address sign in here", and deactivating is how
// this installation takes everything away at once — tokens included. So a deactivated account
// is not an answer of yes.
//
// The teacher stays in the list either way. Who teaches at the faculty is the examination
// office's statement and does not change when this installation withdraws an account; the two
// facts sit in the same row and mean different things.
func TestAnInactiveAccountIsNotSomebodyWhoMaySignIn(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	ctx := t.Context()

	// Before the projection, so that the derived link has something to find.
	storetest.SeedPerson(t, s, testdata.Eins, "LECTURER")
	storetest.SeedZPACatalogue(t, s)
	project(t, s)

	modules := store.NewModules(s.Pool)
	filter := domain.TeacherFilter{IncludeInactive: true}

	before, err := modules.Teachers(ctx, filter)
	if err != nil {
		t.Fatalf("cannot read the teachers: %v", err)
	}
	if !teacherByMail(t, before, testdata.Eins.Mail).IsUser {
		t.Fatal("the seeded persona does not read as a user, so the link is not exercised")
	}

	if err := store.NewPeople(s.Pool).SetPersonActive(ctx, testdata.Eins.ID(), false); err != nil {
		t.Fatalf("cannot deactivate the account: %v", err)
	}

	after, err := modules.Teachers(ctx, filter)
	if err != nil {
		t.Fatalf("cannot read the teachers: %v", err)
	}
	if len(after) != len(before) {
		t.Errorf("the list lost %d rows when an account was deactivated, want the same people",
			len(before)-len(after))
	}
	if teacherByMail(t, after, testdata.Eins.Mail).IsUser {
		t.Error("a deactivated account still reads as somebody who may sign in")
	}
}
