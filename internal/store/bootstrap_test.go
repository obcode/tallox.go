package store_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/store"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// TestReconcileCreatesTheFirstAdministrator covers the case the mechanism exists for at all: a
// database that has just been created, where nobody can sign in and therefore nobody can be
// given access either.
func TestReconcileCreatesTheFirstAdministrator(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)

	outcomes, err := store.ReconcileProtectedAdmins(t.Context(), s.Pool,
		[]store.ProtectedAdmin{{Mail: "admin@example.org", Name: "Admin"}})
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if len(outcomes) != 1 || !outcomes[0].Created {
		t.Fatalf("reconcile reported %+v on an empty database, want Created", outcomes)
	}

	got, err := s.Queries().PersonByMail(t.Context(), "admin@example.org")
	if err != nil {
		t.Fatalf("the reconciled person is not readable: %v", err)
	}
	if !got.Active {
		t.Error("the first administrator is not active")
	}
	if len(got.Roles) != 1 || got.Roles[0] != string(policy.RoleAdmin) {
		t.Errorf("the first administrator holds %v, want only %s — an administrator without "+
			"the role is the one outcome worse than no administrator", got.Roles, policy.RoleAdmin)
	}
}

// TestReconcileIsIdempotent: the list stays in the configuration file, so this runs on every
// restart. Every start after the first has to be a silent no-op — not an error that stops the
// server, and not a write that churns granted_at.
func TestReconcileIsIdempotent(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	entries := []store.ProtectedAdmin{{Mail: "admin@example.org", Name: "Admin"}}

	first, err := store.ReconcileProtectedAdmins(t.Context(), s.Pool, entries)
	if err != nil || !first[0].Created {
		t.Fatalf("first run: %+v err=%v", first, err)
	}
	id := mustPersonID(t, s, "admin@example.org")
	before, err := s.Queries().RoleGrantsByPerson(t.Context(), id)
	if err != nil {
		t.Fatalf("cannot read the grant: %v", err)
	}

	second, err := store.ReconcileProtectedAdmins(t.Context(), s.Pool, entries)
	if err != nil {
		t.Fatalf("second run failed: %v — the server would refuse to start on every restart "+
			"after the first", err)
	}
	if second[0].Changed() {
		t.Errorf("the second run reported %+v, want no change", second[0])
	}

	after, err := s.Queries().RoleGrantsByPerson(t.Context(), id)
	if err != nil {
		t.Fatalf("cannot re-read the grant: %v", err)
	}
	if !after[0].GrantedAt.Equal(before[0].GrantedAt) {
		t.Error("granted_at moved on a no-op restart — the audit trail would say the grant " +
			"was made at the last deploy rather than when it was actually made")
	}
}

// TestReconcileLetsARemovedAdministratorBackIn is the failure this was asked for: one
// administrator removes another by accident, on an installation reachable only through a VPN.
//
// The recovery has to be a restart. Anything that requires psql on the host is a procedure
// somebody follows at the worst possible moment, seeing the schema for the first time.
func TestReconcileLetsARemovedAdministratorBackIn(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		damage func(t *testing.T, s *storetest.Schema, id uuid.UUID)
		want   func(o store.ReconcileOutcome) bool
	}{
		{
			name: "role revoked",
			damage: func(t *testing.T, s *storetest.Schema, id uuid.UUID) {
				if err := s.Queries().RevokeRole(t.Context(), store.RevokeRoleParams{
					PersonID: id, Role: string(policy.RoleAdmin),
				}); err != nil {
					t.Fatalf("cannot revoke: %v", err)
				}
			},
			want: func(o store.ReconcileOutcome) bool { return o.Granted },
		},
		{
			name: "person deactivated",
			damage: func(t *testing.T, s *storetest.Schema, id uuid.UUID) {
				if err := s.Queries().SetPersonActive(t.Context(), store.SetPersonActiveParams{
					ID: id, Active: false,
				}); err != nil {
					t.Fatalf("cannot deactivate: %v", err)
				}
			},
			want: func(o store.ReconcileOutcome) bool { return o.Reactivated },
		},
		{
			name: "person removed outright",
			damage: func(t *testing.T, s *storetest.Schema, id uuid.UUID) {
				// Not something the interface offers — there is no deletePerson and there
				// will not be one — but psql on the host does, and that is precisely the
				// afternoon this mechanism is for.
				if _, err := s.Pool.Exec(t.Context(), `DELETE FROM person WHERE id = $1`, id); err != nil {
					t.Fatalf("cannot delete: %v", err)
				}
			},
			want: func(o store.ReconcileOutcome) bool { return o.Created && o.Granted },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := storetest.New(t)
			// Somebody else runs the installation, so the guard against removing the *last*
			// administrator would not have fired and the accident is possible.
			storetest.SeedPerson(t, s, testdata.Zwei, string(policy.RoleAdmin))
			storetest.SeedPerson(t, s, testdata.Eins, string(policy.RoleAdmin))

			tc.damage(t, s, mustPersonID(t, s, testdata.Eins.Mail))

			outcomes, err := store.ReconcileProtectedAdmins(t.Context(), s.Pool,
				[]store.ProtectedAdmin{{Mail: testdata.Eins.Mail}})
			if err != nil {
				t.Fatalf("reconcile failed: %v", err)
			}
			if !tc.want(outcomes[0]) {
				t.Fatalf("reconcile reported %+v, want the damage repaired", outcomes[0])
			}

			got, err := s.Queries().PersonByMail(t.Context(), testdata.Eins.Mail)
			if err != nil {
				t.Fatalf("cannot read back: %v", err)
			}
			if !got.Active || !hasRoleString(got.Roles, string(policy.RoleAdmin)) {
				t.Errorf("after the restart: active=%v roles=%v — still locked out",
					got.Active, got.Roles)
			}
		})
	}
}

// TestReconcileCannotMeetAnExpiredAdminGrant.
//
// Until 2026-10-28 this test seeded an ADMIN grant that had run out and asserted that the
// reconciliation treated it as no grant. That state can no longer exist: ADMIN is granted
// without an expiry or not at all (migration 20261028100000, audit finding L1), so the one way a
// protected administrator is locked out is a revocation — covered above. What stays here is the
// precondition, so that the migration's constraint cannot go missing without a test noticing.
func TestReconcileCannotMeetAnExpiredAdminGrant(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	storetest.SeedPerson(t, s, testdata.Eins)

	_, err := s.Pool.Exec(t.Context(),
		`INSERT INTO person_role (person_id, role, granted_at, expires_at)
		 VALUES ($1, 'ADMIN', now() - interval '2 days', now() - interval '1 day')`,
		mustPersonID(t, s, testdata.Eins.Mail))
	if err == nil || !strings.Contains(err.Error(), "person_role_admin_never_expires") {
		t.Errorf("an expiring ADMIN grant was written: %v", err)
	}
}

// TestReconcileNeverRevokesAnything is the property that makes this safe to run unattended.
//
// The list says who must be able to get in. If it also said who may, then deleting a line — or
// a typo in an address — would become a silent mass demotion, discovered at the next restart
// by the people who suddenly cannot do their job.
func TestReconcileNeverRevokesAnything(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	storetest.SeedPerson(t, s, testdata.Vier,
		string(policy.RoleAdmin), string(policy.RoleProgrammeLead))

	// A list that does not mention Vier at all.
	if _, err := store.ReconcileProtectedAdmins(t.Context(), s.Pool,
		[]store.ProtectedAdmin{{Mail: testdata.Eins.Mail}}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	got, err := s.Queries().PersonByMail(t.Context(), testdata.Vier.Mail)
	if err != nil {
		t.Fatalf("cannot read back: %v", err)
	}
	if len(got.Roles) != 2 {
		t.Errorf("an administrator not on the list now holds %v — the list revoked something",
			got.Roles)
	}
	if !got.Active {
		t.Error("an administrator not on the list was deactivated by the reconciliation")
	}
}

// TestReconcileDoesNotRenameSomebodyBack.
//
// The name in the configuration file is a starting value, not a source of truth. Somebody who
// corrects their own name in the interface — or whom a future ZPA import corrects — must not
// find it reverted after the next deploy by a string a colleague typed into a YAML file once.
func TestReconcileDoesNotRenameSomebodyBack(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	entries := []store.ProtectedAdmin{{Mail: "admin@example.org", Name: "Placeholder"}}

	if _, err := store.ReconcileProtectedAdmins(t.Context(), s.Pool, entries); err != nil {
		t.Fatalf("first reconcile failed: %v", err)
	}

	id := mustPersonID(t, s, "admin@example.org")
	if err := s.Queries().SetPersonName(t.Context(), store.SetPersonNameParams{
		ID: id, Name: "Corrected Name",
	}); err != nil {
		t.Fatalf("cannot rename: %v", err)
	}

	if _, err := store.ReconcileProtectedAdmins(t.Context(), s.Pool, entries); err != nil {
		t.Fatalf("second reconcile failed: %v", err)
	}

	got, err := s.Queries().PersonByMail(t.Context(), "admin@example.org")
	if err != nil {
		t.Fatalf("cannot read back: %v", err)
	}
	if got.Name != "Corrected Name" {
		t.Errorf("name is %q after a restart, want the corrected one to survive", got.Name)
	}
}

// TestReconcileAcceptsAnEntryWithoutAName documents that the mail address is enough.
//
// It is the whole requirement on the administration surface too: the address is what the login
// asserts, so it is the only thing that has to be right. An empty name is rendered as the mail
// address by the interface rather than being invented here — deriving a name from an address
// guesses at how people write their own and gets it wrong for anybody with a title, a double
// name or an umlaut.
func TestReconcileAcceptsAnEntryWithoutAName(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)

	if _, err := store.ReconcileProtectedAdmins(t.Context(), s.Pool,
		[]store.ProtectedAdmin{{Mail: "admin@example.org"}}); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	got, err := s.Queries().PersonByMail(t.Context(), "admin@example.org")
	if err != nil {
		t.Fatalf("cannot read back: %v", err)
	}
	if got.Name != "" {
		t.Errorf("name is %q, want it left empty for the interface to fill in", got.Name)
	}
}

// TestReconcileRefusesAnEntryWithoutAMail: the address is what the proxy matches against, so a
// blank one would create an account nobody can ever sign in to — and it would do it on every
// restart.
func TestReconcileRefusesAnEntryWithoutAMail(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)

	for _, mail := range []string{"", "   ", "dek anat@example.org"} {
		if _, err := store.ReconcileProtectedAdmins(t.Context(), s.Pool,
			[]store.ProtectedAdmin{{Mail: mail}}); err == nil {
			t.Errorf("mail %q was accepted; it could never match a header", mail)
		}
	}
}

// TestReconcileTrimsTheMail: a stray space in tallox.yaml is the one mistake this list could
// make that looks like success — created, granted, logged healthy, and never matching the
// proxy. The row has to be the address the proxy will send.
func TestReconcileTrimsTheMail(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)

	if _, err := store.ReconcileProtectedAdmins(t.Context(), s.Pool,
		[]store.ProtectedAdmin{{Mail: "  " + testdata.Sechs.Mail + "\n", Name: testdata.Sechs.Name}}); err != nil {
		t.Fatalf("a padded mail was refused rather than trimmed: %v", err)
	}
	if _, err := s.Queries().PersonByMail(t.Context(), testdata.Sechs.Mail); err != nil {
		t.Errorf("the row does not carry the trimmed address: %v", err)
	}
}

func mustPersonID(t *testing.T, s *storetest.Schema, mail string) uuid.UUID {
	t.Helper()
	row, err := s.Queries().PersonByMail(t.Context(), mail)
	if err != nil {
		t.Fatalf("cannot resolve %s: %v", mail, err)
	}
	return row.ID
}

func hasRoleString(roles []string, want string) bool {
	for _, r := range roles {
		if r == want {
			return true
		}
	}
	return false
}
