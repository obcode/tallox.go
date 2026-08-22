package store_test

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/store"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// TestRevokingTheLastAdminIsRefused is the guard, in its simplest shape.
func TestRevokingTheLastAdminIsRefused(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	storetest.SeedPerson(t, s, testdata.Eins, string(policy.RoleAdmin))
	storetest.SeedPerson(t, s, testdata.Zwei, string(policy.RoleLecturer))

	people := store.NewPeople(s.Pool)
	id := mustPersonID(t, s, testdata.Eins.Mail)

	err := people.RevokeRole(t.Context(), id, policy.RoleAdmin)
	if !errors.Is(err, domain.ErrLastAdmin) {
		t.Fatalf("revoking the last ADMIN returned %v, want ErrLastAdmin", err)
	}

	got, err := s.Queries().PersonByMail(t.Context(), testdata.Eins.Mail)
	if err != nil {
		t.Fatalf("cannot read back: %v", err)
	}
	if !hasRoleString(got.Roles, string(policy.RoleAdmin)) {
		t.Error("the refusal did not roll back the delete")
	}
}

// TestDeactivatingTheLastAdminIsRefused: same consequence, so the same refusal.
//
// Deactivating writes the person table rather than person_role, which is exactly why it is
// easy to miss — the guard has to be about who can still administer the installation, not
// about which table is being written.
func TestDeactivatingTheLastAdminIsRefused(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	storetest.SeedPerson(t, s, testdata.Eins, string(policy.RoleAdmin))

	people := store.NewPeople(s.Pool)
	id := mustPersonID(t, s, testdata.Eins.Mail)

	err := people.SetPersonActive(t.Context(), id, false)
	if !errors.Is(err, domain.ErrLastAdmin) {
		t.Fatalf("deactivating the last ADMIN returned %v, want ErrLastAdmin", err)
	}

	got, err := s.Queries().PersonByMail(t.Context(), testdata.Eins.Mail)
	if err != nil {
		t.Fatalf("cannot read back: %v", err)
	}
	if !got.Active {
		t.Error("the refusal did not roll back the deactivation")
	}
}

// TestAnInactiveAdminDoesNotCountAsOne.
//
// "There is another administrator" has to mean somebody who can actually let people back in.
// An administrator who is deactivated cannot sign in at all, so counting them would make the
// guard pass at exactly the moment it is needed — and the second-to-last administrator would
// be able to remove the last one.
func TestAnInactiveAdminDoesNotCountAsOne(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	storetest.SeedPerson(t, s, testdata.Eins, string(policy.RoleAdmin))
	storetest.SeedPerson(t, s, testdata.Zwei, string(policy.RoleAdmin))

	// Zwei exists and holds ADMIN, but cannot use it.
	if err := s.Queries().SetPersonActive(t.Context(), store.SetPersonActiveParams{
		ID: mustPersonID(t, s, testdata.Zwei.Mail), Active: false,
	}); err != nil {
		t.Fatalf("cannot deactivate: %v", err)
	}

	people := store.NewPeople(s.Pool)
	err := people.RevokeRole(t.Context(), mustPersonID(t, s, testdata.Eins.Mail), policy.RoleAdmin)
	if !errors.Is(err, domain.ErrLastAdmin) {
		t.Errorf("revoking returned %v — a deactivated administrator was counted as one who "+
			"could still let somebody in", err)
	}
}

// TestAnExpiredAdminGrantDoesNotCountAsOne, for the same reason: a grant that has run out
// grants nothing, so somebody holding one is not a way back into this installation.
func TestAnExpiredAdminGrantDoesNotCountAsOne(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	storetest.SeedPerson(t, s, testdata.Eins, string(policy.RoleAdmin))
	storetest.SeedPerson(t, s, testdata.Zwei)

	if _, err := s.Pool.Exec(t.Context(),
		`INSERT INTO person_role (person_id, role, granted_at, expires_at)
		 VALUES ($1, 'ADMIN', now() - interval '2 days', now() - interval '1 day')`,
		mustPersonID(t, s, testdata.Zwei.Mail)); err != nil {
		t.Fatalf("cannot seed the expired grant: %v", err)
	}

	people := store.NewPeople(s.Pool)
	err := people.RevokeRole(t.Context(), mustPersonID(t, s, testdata.Eins.Mail), policy.RoleAdmin)
	if !errors.Is(err, domain.ErrLastAdmin) {
		t.Errorf("revoking returned %v — an expired ADMIN grant was counted as a live one", err)
	}
}

// TestTwoAdministratorsCannotRemoveEachOtherAtOnce is the reason the guard is a transaction
// with a lock rather than a check followed by a write.
//
// Both transactions read a state in which the other administrator still exists, and both would
// be individually correct. Committing both leaves nobody — the textbook write skew, and here
// its outcome is an installation that has to be repaired from the host.
//
// The assertion is deliberately about the *outcome* rather than about which call failed: which
// of the two loses is a scheduling detail, and a test that pinned it would fail for the wrong
// reason on a busy machine.
func TestTwoAdministratorsCannotRemoveEachOtherAtOnce(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	storetest.SeedPerson(t, s, testdata.Eins, string(policy.RoleAdmin))
	storetest.SeedPerson(t, s, testdata.Zwei, string(policy.RoleAdmin))

	people := store.NewPeople(s.Pool)
	einsID := mustPersonID(t, s, testdata.Eins.Mail)
	zweiID := mustPersonID(t, s, testdata.Zwei.Mail)

	var wg sync.WaitGroup
	wg.Add(2)
	// Each one revokes the other, at the same moment.
	go func() { defer wg.Done(); _ = people.RevokeRole(t.Context(), einsID, policy.RoleAdmin) }()
	go func() { defer wg.Done(); _ = people.RevokeRole(t.Context(), zweiID, policy.RoleAdmin) }()
	wg.Wait()

	var remaining int
	if err := s.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM person_role pr JOIN person p ON p.id = pr.person_id
		 WHERE pr.role = 'ADMIN' AND p.active
		   AND (pr.expires_at IS NULL OR pr.expires_at > now())`).Scan(&remaining); err != nil {
		t.Fatalf("cannot count: %v", err)
	}
	if remaining < 1 {
		t.Fatal("both revocations succeeded — the installation now has no administrator, " +
			"which is the write skew the advisory row lock exists to prevent")
	}
}

// TestRevokingANonAdminRoleNeedsNoGuard: the guard is about who can administer, so it must not
// get in the way of ordinary role changes. A lock taken on every revocation would serialise
// the administration screen for no reason.
func TestRevokingANonAdminRoleIsUnguarded(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	storetest.SeedPerson(t, s, testdata.Eins,
		string(policy.RoleAdmin), string(policy.RoleProgrammeLead))

	people := store.NewPeople(s.Pool)
	id := mustPersonID(t, s, testdata.Eins.Mail)

	if err := people.RevokeRole(t.Context(), id, policy.RoleProgrammeLead); err != nil {
		t.Fatalf("revoking PROGRAMME_LEAD from the only administrator failed: %v", err)
	}

	got, err := s.Queries().PersonByMail(t.Context(), testdata.Eins.Mail)
	if err != nil {
		t.Fatalf("cannot read back: %v", err)
	}
	if len(got.Roles) != 1 || got.Roles[0] != string(policy.RoleAdmin) {
		t.Errorf("roles are %v, want only ADMIN", got.Roles)
	}
}

// TestAnExpiredGrantIsInvisibleToTheLookupsButNotToTheAuditView.
//
// The two readings are both correct and they are different, which is the whole reason
// person_role.expires_at is filtered in the join and not deleted by a cron job. Permission
// lookups have to see a live set; the audit view has to answer "who could see this in
// October", and a deleted row cannot.
func TestAnExpiredGrantIsInvisibleToTheLookupsButNotToTheAuditView(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	storetest.SeedPerson(t, s, testdata.Eins, string(policy.RoleLecturer))

	id := mustPersonID(t, s, testdata.Eins.Mail)
	if _, err := s.Pool.Exec(t.Context(),
		`INSERT INTO person_role (person_id, role, granted_at, expires_at)
		 VALUES ($1, 'DEANS_OFFICE', now() - interval '2 days', now() - interval '1 day')`,
		id); err != nil {
		t.Fatalf("cannot seed: %v", err)
	}

	people := store.NewPeople(s.Pool)

	person, err := people.PersonByID(t.Context(), id)
	if err != nil {
		t.Fatalf("cannot read: %v", err)
	}
	for _, r := range person.Roles {
		if r == policy.RoleDeansOffice {
			t.Error("an expired DEANS_OFFICE grant is still in force — somebody would be " +
				"reading unpublished wishes on a permission that ran out")
		}
	}

	grants, err := people.RoleGrants(t.Context(), id)
	if err != nil {
		t.Fatalf("cannot read grants: %v", err)
	}
	var sawExpired bool
	for _, g := range grants {
		if g.Role == policy.RoleDeansOffice && g.Expired(time.Now()) {
			sawExpired = true
		}
	}
	if !sawExpired {
		t.Error("the expired grant is gone from the audit view too — the record of who could " +
			"see what, and when, is the thing this column exists for")
	}
}

// TestListPeopleKeepsSomebodyWhoseOnlyGrantExpired.
//
// The expiry filter lives in the JOIN condition rather than a WHERE clause, and this is the
// difference: a WHERE on the right-hand table of a LEFT JOIN turns it into an inner one, and
// the person disappears from the administration screen entirely. The version that leaks is the
// one that hides people from the list that is supposed to show everybody.
func TestListPeopleKeepsSomebodyWhoseOnlyGrantExpired(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	storetest.SeedPerson(t, s, testdata.Eins)

	id := mustPersonID(t, s, testdata.Eins.Mail)
	if _, err := s.Pool.Exec(t.Context(),
		`INSERT INTO person_role (person_id, role, granted_at, expires_at)
		 VALUES ($1, 'LECTURER', now() - interval '2 days', now() - interval '1 day')`,
		id); err != nil {
		t.Fatalf("cannot seed: %v", err)
	}

	people, err := store.NewPeople(s.Pool).ListPeople(t.Context(), "", false)
	if err != nil {
		t.Fatalf("cannot list: %v", err)
	}

	var found bool
	for _, p := range people {
		if p.Mail == testdata.Eins.Mail {
			found = true
			if len(p.Roles) != 0 {
				t.Errorf("roles are %v, want none in force", p.Roles)
			}
		}
	}
	if !found {
		t.Fatal("somebody whose only grant expired vanished from the list")
	}
}

// The administration list is sorted by the surname, and the surname comes from the examination
// office rather than from a guess about which word of a written-out name it is.
//
// The two orders really differ here, which is the point: by `person.name` the admitted teacher
// comes first ("Prof. Dr. Sieben" before "Prof. Zwei"), by the surname she comes second
// ("Prof. Zwei" before "Sieben, Prof."). A list sorted by the written-out name is a list sorted
// by academic title, and every professor lands under P.
func TestListPeopleSortsBySurnameWhereTheSourceKnowsOne(t *testing.T) {
	t.Parallel()

	s := admissionSchema(t)
	people := store.NewPeople(s.Pool)

	// Somebody the examination office does not publish: their own name stays the key.
	storetest.SeedPerson(t, s, testdata.Zwei, string(policy.RoleLecturer))
	// And somebody it does, admitted the way the screen admits people.
	if _, err := people.AdmitTeacher(t.Context(),
		teacherID(t, s, storetest.FixtureTeacherNotAdmitted),
		policy.RoleLecturer, uuid.Nil); err != nil {
		t.Fatalf("cannot admit: %v", err)
	}

	list, err := people.ListPeople(t.Context(), "", false)
	if err != nil {
		t.Fatalf("cannot list: %v", err)
	}

	order := make([]string, 0, len(list))
	byMail := make(map[string]domain.Person, len(list))
	for _, person := range list {
		order = append(order, person.Mail)
		byMail[person.Mail] = person
	}

	if got := byMail["prof.sieben@example.org"].SortName; got != "Sieben, Prof." {
		t.Errorf("the admitted teacher's sort name is %q, want the spelling the examination "+
			"office publishes", got)
	}
	if got := byMail[testdata.Zwei.Mail].SortName; got != "" {
		t.Errorf("somebody the examination office does not publish has the sort name %q, want "+
			"none — the fallback is their own name, not a guess at it", got)
	}

	zwei := slices.Index(order, testdata.Zwei.Mail)
	sieben := slices.Index(order, "prof.sieben@example.org")
	if zwei < 0 || sieben < 0 {
		t.Fatalf("the list is %v, want both of them in it", order)
	}
	if zwei > sieben {
		t.Errorf("the list is %v, want %s before prof.sieben@example.org: sorted by the surname "+
			"they swap places, and sorted by the written-out name they do not",
			order, testdata.Zwei.Mail)
	}
}

// Searching finds somebody by the name the screen shows them under.
func TestListPeopleSearchesTheSurnameItSortsBy(t *testing.T) {
	t.Parallel()

	s := admissionSchema(t)
	people := store.NewPeople(s.Pool)

	if _, err := people.AdmitTeacher(t.Context(),
		teacherID(t, s, storetest.FixtureTeacherNotAdmitted),
		policy.RoleLecturer, uuid.Nil); err != nil {
		t.Fatalf("cannot admit: %v", err)
	}

	found, err := people.ListPeople(t.Context(), "Sieben, Prof", false)
	if err != nil {
		t.Fatalf("cannot list: %v", err)
	}
	if len(found) != 1 || found[0].Mail != "prof.sieben@example.org" {
		t.Errorf("searching the spelling the list is sorted by found %v, want the one person",
			found)
	}
}

// TestListPeopleHidesInactivePeopleUnlessAsked.
func TestListPeopleHidesInactivePeopleUnlessAsked(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	storetest.SeedPerson(t, s, testdata.Eins, string(policy.RoleAdmin))
	storetest.SeedPerson(t, s, testdata.Zwei)

	if err := s.Queries().SetPersonActive(t.Context(), store.SetPersonActiveParams{
		ID: mustPersonID(t, s, testdata.Zwei.Mail), Active: false,
	}); err != nil {
		t.Fatalf("cannot deactivate: %v", err)
	}

	people := store.NewPeople(s.Pool)

	active, err := people.ListPeople(t.Context(), "", false)
	if err != nil {
		t.Fatalf("cannot list: %v", err)
	}
	for _, p := range active {
		if p.Mail == testdata.Zwei.Mail {
			t.Error("a deactivated person is in the default list")
		}
	}

	all, err := people.ListPeople(t.Context(), "", true)
	if err != nil {
		t.Fatalf("cannot list: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("includeInactive returned %d people, want 2", len(all))
	}
}

// TestListPeopleSearchesMailAndNameCaseInsensitively — the administration screen's search box,
// which is used with whatever fragment somebody has: half an address, half a surname.
func TestListPeopleSearchesMailAndNameCaseInsensitively(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	storetest.SeedPerson(t, s, testdata.Eins)
	storetest.SeedPerson(t, s, testdata.Zwei)

	people := store.NewPeople(s.Pool)

	for _, search := range []string{
		testdata.Eins.Mail[:6],
		strings.ToUpper(testdata.Eins.Mail[:6]),
	} {
		got, err := people.ListPeople(t.Context(), search, false)
		if err != nil {
			t.Fatalf("cannot search %q: %v", search, err)
		}
		if len(got) != 1 || got[0].Mail != testdata.Eins.Mail {
			t.Errorf("searching %q returned %d people, want exactly Eins", search, len(got))
		}
	}
}

// TestGrantRoleUpdatesTheExpiryRatherThanFailing.
//
// "Give me DEANS_OFFICE for another hour" has to be one call. The alternative — an insert that
// fails on conflict — pushes every caller into a read-then-write race, and the caller here is
// somebody extending a grant they are actively using.
func TestGrantRoleUpdatesTheExpiryRatherThanFailing(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	storetest.SeedPerson(t, s, testdata.Eins, string(policy.RoleAdmin))

	people := store.NewPeople(s.Pool)
	id := mustPersonID(t, s, testdata.Eins.Mail)

	first := time.Now().Add(1 * time.Hour)
	if err := people.GrantRole(t.Context(), id, policy.RoleDeansOffice, id, first); err != nil {
		t.Fatalf("first grant failed: %v", err)
	}

	second := time.Now().Add(3 * time.Hour)
	if err := people.GrantRole(t.Context(), id, policy.RoleDeansOffice, id, second); err != nil {
		t.Fatalf("re-granting failed: %v", err)
	}

	grants, err := people.RoleGrants(t.Context(), id)
	if err != nil {
		t.Fatalf("cannot read grants: %v", err)
	}
	for _, g := range grants {
		if g.Role != policy.RoleDeansOffice {
			continue
		}
		if g.ExpiresAt.Before(second.Add(-time.Minute)) {
			t.Errorf("expiry is %v, want it moved to %v", g.ExpiresAt, second)
		}
	}
}
