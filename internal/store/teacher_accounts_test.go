package store_test

import (
	"errors"
	"slices"
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

// admissionSchema seeds the given personas, then the catalogue, then projects it.
//
// In that order, and it matters: the link between a teacher and an account is the mail address,
// resolved on every read, so a persona seeded afterwards would still be found — but the
// projection is what creates the teacher rows, and a test that read before it would be looking
// at an empty table.
func admissionSchema(t *testing.T, people ...testdata.Persona) *storetest.Schema {
	t.Helper()

	s := storetest.New(t)
	for _, persona := range people {
		storetest.SeedPerson(t, s, persona, string(policy.RoleLecturer))
	}
	storetest.SeedZPACatalogue(t, s)
	project(t, s)
	return s
}

// teacherID reads the id the projection gave one fixture teacher.
func teacherID(t *testing.T, s *storetest.Schema, ref int64) uuid.UUID {
	t.Helper()

	var id uuid.UUID
	if err := s.Pool.QueryRow(t.Context(),
		`SELECT id FROM teacher WHERE zpa_teacher_ref = $1`, ref).Scan(&id); err != nil {
		t.Fatalf("cannot read the fixture teacher %d: %v", ref, err)
	}
	return id
}

// accountOf finds one row of the list by the teacher's address.
func accountOf(t *testing.T, accounts []domain.TeacherAccount, mail string) domain.TeacherAccount {
	t.Helper()

	for _, account := range accounts {
		if account.Teacher.Mail == mail {
			return account
		}
	}
	t.Fatalf("the list has no teacher with the address %q", mail)
	return domain.TeacherAccount{}
}

// The two lists are joined by the mail address and by nothing else. There is no column to keep
// in step, so the answer is right the moment somebody is admitted rather than after the next
// import — and a teacher the source gives no address for can never be connected to anybody.
func TestTeacherAccountsLinkByAddressAndNotByAForeignKey(t *testing.T) {
	t.Parallel()

	s := admissionSchema(t, testdata.Eins)

	accounts, err := store.NewPeople(s.Pool).TeacherAccounts(t.Context())
	if err != nil {
		t.Fatalf("cannot list the teacher accounts: %v", err)
	}

	admitted := accountOf(t, accounts, testdata.Eins.Mail)
	if admitted.Person == nil {
		t.Fatal("the seeded persona has no account, so the address is not being joined on")
	}
	if !admitted.Admitted() {
		t.Error("the seeded persona is not admitted")
	}

	// The ordinary row: teaches, has an address, nobody has admitted them. 254 of 257.
	if account := accountOf(t, accounts, "prof.sieben@example.org"); account.Person != nil {
		t.Errorf("somebody nobody admitted has the account %v — importing a teacher granted "+
			"access, which is the one thing it must never do", account.Person.Mail)
	}

	// The one who can never be connected, because the address is the connection.
	var withoutMail int
	for _, account := range accounts {
		if account.Teacher.Mail == "" {
			withoutMail++
			if account.Person != nil {
				t.Error("a teacher with no address was matched to an account")
			}
		}
	}
	if withoutMail == 0 {
		t.Error("no teacher without an address in the list, so that case is not covered")
	}
}

// A deactivated account is not the same as no account. The screen offers "admit" for one and
// "reactivate" for the other, and answering both with a nil person would make the two look
// alike to the one list that has to keep them apart.
func TestADeactivatedAccountIsNotTheSameAsNoAccount(t *testing.T) {
	t.Parallel()

	s := admissionSchema(t, testdata.Eins)
	people := store.NewPeople(s.Pool)

	if err := people.SetPersonActive(t.Context(),
		mustPersonID(t, s, testdata.Eins.Mail), false); err != nil {
		t.Fatalf("cannot deactivate: %v", err)
	}

	accounts, err := people.TeacherAccounts(t.Context())
	if err != nil {
		t.Fatalf("cannot list the teacher accounts: %v", err)
	}

	account := accountOf(t, accounts, testdata.Eins.Mail)
	if account.Person == nil {
		t.Fatal("a deactivated account reads as no account at all")
	}
	if account.Person.Active || account.Admitted() {
		t.Error("a deactivated account still reads as admitted")
	}
	// The derived field keeps meaning what it means everywhere else.
	if account.Teacher.IsUser {
		t.Error("a deactivated account still reads as somebody who may sign in")
	}
}

// The source's own "no longer teaching" flag is a column here, not a filter. Five of the real
// ones are still named as responsible for a module, and somebody who has left still has an
// account that has to be closable.
func TestTeacherAccountsIncludeSomebodyTheSourceStoppedListing(t *testing.T) {
	t.Parallel()

	s := admissionSchema(t)

	accounts, err := store.NewPeople(s.Pool).TeacherAccounts(t.Context())
	if err != nil {
		t.Fatalf("cannot list the teacher accounts: %v", err)
	}

	account := accountOf(t, accounts, "prof.acht@example.org")
	if account.Teacher.Active {
		t.Error("the fixture's inactive teacher reads as active, so the case is not covered")
	}
}

// Retired means a successful import stopped mentioning them, which is a different question from
// the source's own flag: "no longer published" is not worth a row on the screen.
//
// By id they are still there. The id on the screen came from the list, so the only way to ask
// for a retired one is that the import withdrew them in between — and refusing a change that has
// already been written would be a worse answer than the truth.
func TestARetiredTeacherLeavesTheListAndStaysReachableByID(t *testing.T) {
	t.Parallel()

	s := admissionSchema(t)
	id := teacherID(t, s, storetest.FixtureTeacherNotAdmitted)

	storetest.RetireZPAObject(t, s, "TEACHER", storetest.FixtureTeacherNotAdmitted)
	project(t, s)

	people := store.NewPeople(s.Pool)

	accounts, err := people.TeacherAccounts(t.Context())
	if err != nil {
		t.Fatalf("cannot list the teacher accounts: %v", err)
	}
	for _, account := range accounts {
		if account.Teacher.ID == id {
			t.Error("a retired teacher is still in the list")
		}
	}

	account, err := people.TeacherAccountByID(t.Context(), id)
	if err != nil {
		t.Fatalf("cannot read the teacher account: %v", err)
	}
	if account == nil {
		t.Fatal("a retired teacher cannot be read by id, so a change to one would be refused " +
			"after it had been made")
	}
}

// The roles and the programmes come with the row, because the screen shows them in the same
// line as the switch that changes them.
func TestATeacherAccountCarriesRolesAndProgrammes(t *testing.T) {
	t.Parallel()

	s := admissionSchema(t, testdata.Eins)
	id := mustPersonID(t, s, testdata.Eins.Mail)
	people := store.NewPeople(s.Pool)

	if err := people.GrantRole(t.Context(), id, policy.RoleProgrammeLead,
		uuid.Nil, time.Time{}); err != nil {
		t.Fatalf("cannot grant: %v", err)
	}
	if err := people.SetPersonProgrammes(t.Context(), id,
		[]string{storetest.FixtureProgrammeA}, uuid.Nil); err != nil {
		t.Fatalf("cannot assign a programme: %v", err)
	}

	accounts, err := people.TeacherAccounts(t.Context())
	if err != nil {
		t.Fatalf("cannot list the teacher accounts: %v", err)
	}

	account := accountOf(t, accounts, testdata.Eins.Mail)
	if account.Person == nil {
		t.Fatal("no account")
	}
	if !slices.Contains(account.Person.Roles, policy.RoleProgrammeLead) {
		t.Errorf("roles are %v, want the lead grant among them", account.Person.Roles)
	}
	if len(account.Person.Programmes) != 1 ||
		account.Person.Programmes[0].Code != storetest.FixtureProgrammeA {
		t.Errorf("programmes are %v, want exactly %s",
			account.Person.Programmes, storetest.FixtureProgrammeA)
	}
}

// The expiry filter belongs in the JOIN condition. Written as a WHERE it would turn the LEFT
// JOIN into an inner one and drop the teacher from the list altogether — the person would
// vanish from the screen that administers them, rather than appearing with one role fewer.
func TestATeacherAccountLeavesOutAGrantThatExpired(t *testing.T) {
	t.Parallel()

	s := admissionSchema(t)
	// Seeded after the projection, with no roles at all, so that the expired grant is this
	// person's only one. Admitting somebody after the import is the ordinary case, and it
	// works because the link is the address rather than a column somebody has to refresh.
	storetest.SeedPerson(t, s, testdata.Eins)

	if _, err := s.Pool.Exec(t.Context(),
		`INSERT INTO person_role (person_id, role, granted_at, expires_at)
		 VALUES ($1, 'DEANS_OFFICE', now() - interval '2 days', now() - interval '1 day')`,
		mustPersonID(t, s, testdata.Eins.Mail)); err != nil {
		t.Fatalf("cannot seed the expired grant: %v", err)
	}

	accounts, err := store.NewPeople(s.Pool).TeacherAccounts(t.Context())
	if err != nil {
		t.Fatalf("cannot list the teacher accounts: %v", err)
	}

	account := accountOf(t, accounts, testdata.Eins.Mail)
	if account.Person == nil {
		t.Fatal("somebody whose only grant expired lost their account row in the list")
	}
	if len(account.Person.Roles) != 0 {
		t.Errorf("roles are %v, want none in force", account.Person.Roles)
	}
}

// Admitting is a statement — "this person may sign in" — and a statement made twice is the same
// statement. A screen that saves on every click gets double-clicked, and two administrators
// working through the same list arrive at the same row.
func TestAdmittingATeacherIsIdempotent(t *testing.T) {
	t.Parallel()

	s := admissionSchema(t)
	people := store.NewPeople(s.Pool)
	id := teacherID(t, s, storetest.FixtureTeacherNotAdmitted)

	first, err := people.AdmitTeacher(t.Context(), id, policy.RoleLecturer, uuid.Nil)
	if err != nil {
		t.Fatalf("cannot admit: %v", err)
	}
	second, err := people.AdmitTeacher(t.Context(), id, policy.RoleLecturer, uuid.Nil)
	if err != nil {
		t.Fatalf("admitting twice failed the second time: %v", err)
	}

	if first.Person == nil || second.Person == nil {
		t.Fatal("admitting produced no account")
	}
	if first.Person.ID != second.Person.ID {
		t.Errorf("the second admission created a second account: %s then %s",
			first.Person.ID, second.Person.ID)
	}
	if len(second.Person.Roles) != 1 || second.Person.Roles[0] != policy.RoleLecturer {
		t.Errorf("roles after admitting twice are %v, want exactly the one role admission grants",
			second.Person.Roles)
	}

	var accounts int
	if err := s.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM person WHERE mail = 'prof.sieben@example.org'`).Scan(&accounts); err != nil {
		t.Fatalf("cannot count: %v", err)
	}
	if accounts != 1 {
		t.Errorf("%d account rows for one address, want 1", accounts)
	}
}

// The same click from two administrators at the same moment. Both have to succeed, because both
// are asking for the state that results — and exactly one account may exist afterwards.
//
// Without the upsert this is a unique violation on person.mail, which reaches the caller as an
// internal error on an action that plainly worked for somebody.
func TestTwoAdministratorsAdmittingTheSameTeacherAtOnce(t *testing.T) {
	t.Parallel()

	s := admissionSchema(t)
	people := store.NewPeople(s.Pool)
	id := teacherID(t, s, storetest.FixtureTeacherNotAdmitted)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	for i := range errs {
		go func() {
			defer wg.Done()
			_, errs[i] = people.AdmitTeacher(t.Context(), id, policy.RoleLecturer, uuid.Nil)
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("administrator %d could not admit: %v", i+1, err)
		}
	}

	var accounts int
	if err := s.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM person WHERE mail = 'prof.sieben@example.org'`).Scan(&accounts); err != nil {
		t.Fatalf("cannot count: %v", err)
	}
	if accounts != 1 {
		t.Errorf("%d account rows for one address, want 1", accounts)
	}
}

// Re-admitting somebody who was deactivated is the other half of the switch. Their grants are
// still there — deactivating takes everything away by refusing authentication, and never by
// deleting a row — so the account comes back as it was.
func TestReadmittingRestoresTheAccountAndItsRoles(t *testing.T) {
	t.Parallel()

	s := admissionSchema(t, testdata.Eins)
	people := store.NewPeople(s.Pool)
	personID := mustPersonID(t, s, testdata.Eins.Mail)

	if err := people.GrantRole(t.Context(), personID, policy.RoleDeansOffice,
		uuid.Nil, time.Time{}); err != nil {
		t.Fatalf("cannot grant: %v", err)
	}
	if err := people.SetPersonActive(t.Context(), personID, false); err != nil {
		t.Fatalf("cannot deactivate: %v", err)
	}

	account, err := people.AdmitTeacher(t.Context(),
		teacherID(t, s, storetest.FixtureTeacherOrdinary), policy.RoleLecturer, uuid.Nil)
	if err != nil {
		t.Fatalf("cannot re-admit: %v", err)
	}
	if account.Person == nil || account.Person.ID != personID {
		t.Fatal("re-admitting created a second account instead of reactivating the one there")
	}
	if !account.Admitted() {
		t.Error("the account is still not admitted")
	}
	if !slices.Contains(account.Person.Roles, policy.RoleDeansOffice) {
		t.Errorf("roles after re-admitting are %v, want the grant that survived deactivation "+
			"among them", account.Person.Roles)
	}
}

// Somebody the source gives no address for cannot be admitted, and the refusal has to arrive
// before anything is written: the address is what an account is, and there is nothing to invent.
func TestATeacherWithoutAnAddressCannotBeAdmitted(t *testing.T) {
	t.Parallel()

	s := admissionSchema(t)
	people := store.NewPeople(s.Pool)

	_, err := people.AdmitTeacher(t.Context(),
		teacherID(t, s, storetest.FixtureTeacherWithoutMail), policy.RoleLecturer, uuid.Nil)
	if !errors.Is(err, domain.ErrTeacherHasNoMail) {
		t.Fatalf("admitting somebody with no address returned %v, want ErrTeacherHasNoMail", err)
	}

	var accounts int
	if err := s.Pool.QueryRow(t.Context(), `SELECT count(*) FROM person`).Scan(&accounts); err != nil {
		t.Fatalf("cannot count: %v", err)
	}
	if accounts != 0 {
		t.Errorf("%d accounts exist after a refused admission, want none", accounts)
	}
}

// Withdrawing an admission is the existing deactivation, so it is the existing guard: the last
// administrator cannot be removed, and it makes no difference which screen asks.
func TestWithdrawingAdmissionCannotRemoveTheLastAdministrator(t *testing.T) {
	t.Parallel()

	s := admissionSchema(t)
	people := store.NewPeople(s.Pool)

	account, err := people.AdmitTeacher(t.Context(),
		teacherID(t, s, storetest.FixtureTeacherNotAdmitted), policy.RoleLecturer, uuid.Nil)
	if err != nil {
		t.Fatalf("cannot admit: %v", err)
	}
	if err := people.GrantRole(t.Context(), account.Person.ID, policy.RoleAdmin,
		uuid.Nil, time.Time{}); err != nil {
		t.Fatalf("cannot grant ADMIN: %v", err)
	}

	err = people.SetPersonActive(t.Context(), account.Person.ID, false)
	if !errors.Is(err, domain.ErrLastAdmin) {
		t.Fatalf("withdrawing the last administrator returned %v, want ErrLastAdmin", err)
	}

	after, err := people.TeacherAccountByID(t.Context(), account.Teacher.ID)
	if err != nil {
		t.Fatalf("cannot read back: %v", err)
	}
	if !after.Admitted() {
		t.Error("the refusal still left the last administrator locked out")
	}
}
