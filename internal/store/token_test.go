package store_test

import (
	"bytes"
	"reflect"
	"testing"
	"time"

	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/store"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// hash is a stand-in for a SHA-256 digest: the column only cares that it is 32 bytes, and
// these tests are about the storage rules rather than about hashing, which internal/auth
// owns.
func hash(seed byte) []byte {
	b := make([]byte, 32)
	for i := range b {
		b[i] = seed + byte(i)
	}
	return b
}

// TestTokenByIDCarriesTheOwnerAndTheirRoles covers the authentication query of the token
// door, and with it the invariant that a token cannot exceed its owner.
//
// Permissions are resolved from the person on every request rather than copied onto the token
// when it is minted. That is what makes "revoking a role instantly demotes every token that
// person holds" true without touching a token row — and this test is where that stops being a
// claim: it revokes a role and reads the token again.
func TestTokenByIDCarriesTheOwnerAndTheirRoles(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	storetest.SeedPerson(t, s, testdata.Vier,
		string(policy.RoleLecturer), string(policy.RoleProgrammeLead))
	storetest.SeedToken(t, s, testdata.Vier, hash(1), storetest.TokenOptions{
		Description: "Auswertung Lehrdeputat",
		Scopes:      []string{"wishes:read"},
	})

	got, err := s.Queries().TokenByID(t.Context(), testdata.Vier.TokenID)
	if err != nil {
		t.Fatalf("cannot read the token: %v", err)
	}

	if got.OwnerID != testdata.Vier.ID() || got.Mail != testdata.Vier.Mail {
		t.Errorf("token belongs to %v/%s, want %v/%s",
			got.OwnerID, got.Mail, testdata.Vier.ID(), testdata.Vier.Mail)
	}
	if !bytes.Equal(got.SecretHash, hash(1)) {
		t.Error("the secret hash came back different from what was stored")
	}
	if len(got.Roles) != 2 {
		t.Errorf("token carries roles %v, want both of the owner's", got.Roles)
	}
	if len(got.Scopes) != 1 || got.Scopes[0] != "wishes:read" {
		t.Errorf("scopes are %v", got.Scopes)
	}

	// Demote the owner; the token must follow immediately.
	err = s.Queries().RevokeRole(t.Context(), store.RevokeRoleParams{
		PersonID: testdata.Vier.ID(),
		Role:     string(policy.RoleProgrammeLead),
	})
	if err != nil {
		t.Fatalf("cannot revoke the role: %v", err)
	}

	got, err = s.Queries().TokenByID(t.Context(), testdata.Vier.TokenID)
	if err != nil {
		t.Fatalf("cannot re-read the token: %v", err)
	}
	if len(got.Roles) != 1 || got.Roles[0] != string(policy.RoleLecturer) {
		t.Errorf("after revoking a role the token still carries %v — permissions are being "+
			"copied onto the token instead of resolved from its owner", got.Roles)
	}
}

// TestExpiredAndRevokedTokensAreStillReturned pins a deliberate non-filter.
//
// It looks like the query is missing a WHERE clause. It is not: authentication has to tell
// "no such token" from "your token expired last Tuesday", because the caller owns the token
// and can be told why it stopped working. A query that filtered would collapse both into the
// same silence, and the support question that follows ("it just stopped working") is
// expensive for everybody.
func TestExpiredAndRevokedTokensAreStillReturned(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	storetest.SeedPerson(t, s, testdata.Eins, string(policy.RoleLecturer))
	storetest.SeedToken(t, s, testdata.Eins, hash(2), storetest.TokenOptions{
		ExpiresAt: time.Now().Add(-time.Hour),
		Revoked:   true,
	})

	got, err := s.Queries().TokenByID(t.Context(), testdata.Eins.TokenID)
	if err != nil {
		t.Fatalf("an expired, revoked token is not readable: %v — authentication can then no "+
			"longer say why it stopped working", err)
	}
	if !got.RevokedAt.Valid {
		t.Error("revoked_at was not set")
	}
	if !got.ExpiresAt.Before(time.Now()) {
		t.Error("the expiry seeded in the past came back in the future")
	}
}

// TestRevocationKeepsTheFirstMoment: revoking twice is not an error and does not move the
// timestamp. The first moment is the one the audit log needs.
func TestRevocationKeepsTheFirstMoment(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	storetest.SeedPerson(t, s, testdata.Eins)
	storetest.SeedToken(t, s, testdata.Eins, hash(3), storetest.TokenOptions{})

	q := s.Queries()
	revoke := func() error {
		_, err := q.RevokeTokenOfOwner(t.Context(), store.RevokeTokenOfOwnerParams{
			TokenID: testdata.Eins.TokenID, OwnerID: testdata.Eins.ID(),
		})
		return err
	}
	if err := revoke(); err != nil {
		t.Fatalf("cannot revoke: %v", err)
	}
	first, err := q.TokenByID(t.Context(), testdata.Eins.TokenID)
	if err != nil {
		t.Fatalf("cannot read back: %v", err)
	}

	if err := revoke(); err != nil {
		t.Fatalf("revoking twice failed: %v", err)
	}
	second, err := q.TokenByID(t.Context(), testdata.Eins.TokenID)
	if err != nil {
		t.Fatalf("cannot read back: %v", err)
	}

	if !first.RevokedAt.Time.Equal(second.RevokedAt.Time) {
		t.Errorf("the revocation moment moved from %v to %v",
			first.RevokedAt.Time, second.RevokedAt.Time)
	}
}

// TestMarkTokenUsedIsCoarse covers the guard that keeps a script from serialising against
// itself.
//
// Without the five-minute condition, every API call takes a row lock and writes WAL, so a
// colleague's 500-query evaluation script queues behind its own bookkeeping. The condition is
// in the SQL rather than in Go because "have five minutes passed" has to be decided by the
// database that holds the row, not by a caller that might be one of several replicas.
func TestMarkTokenUsedIsCoarse(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	storetest.SeedPerson(t, s, testdata.Eins)
	storetest.SeedToken(t, s, testdata.Eins, hash(4), storetest.TokenOptions{})

	q := s.Queries()

	if err := q.MarkTokenUsed(t.Context(), testdata.Eins.TokenID); err != nil {
		t.Fatalf("cannot mark used: %v", err)
	}
	first, err := q.ListTokensOfPerson(t.Context(), testdata.Eins.ID())
	if err != nil || len(first) != 1 {
		t.Fatalf("cannot list tokens: %v (%d rows)", err, len(first))
	}
	if !first[0].LastUsedAt.Valid {
		t.Fatal("last_used_at was not set on first use")
	}

	if err := q.MarkTokenUsed(t.Context(), testdata.Eins.TokenID); err != nil {
		t.Fatalf("cannot mark used again: %v", err)
	}
	second, err := q.ListTokensOfPerson(t.Context(), testdata.Eins.ID())
	if err != nil {
		t.Fatalf("cannot list tokens: %v", err)
	}
	if !first[0].LastUsedAt.Time.Equal(second[0].LastUsedAt.Time) {
		t.Error("a second use within five minutes wrote the row again — every API call then " +
			"costs a row lock and a WAL write")
	}
}

// TestTokenListLeavesTheSecretHashBehind: nothing outside authentication has a use for the
// hash, and a column that is never selected cannot end up in a log line or a GraphQL error.
func TestTokenListLeavesTheSecretHashBehind(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	storetest.SeedPerson(t, s, testdata.Eins)
	storetest.SeedToken(t, s, testdata.Eins, hash(5), storetest.TokenOptions{Description: "CI"})

	rows, err := s.Queries().ListTokensOfPerson(t.Context(), testdata.Eins.ID())
	if err != nil {
		t.Fatalf("cannot list tokens: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("listed %d tokens, want 1", len(rows))
	}
	if rows[0].Description != "CI" {
		t.Errorf("description is %q", rows[0].Description)
	}

	// The projection is generated from the query, so this asserts the query: adding the
	// column back would add the field, and a hash in a listing is a hash in a log line.
	if _, hasHash := reflect.TypeOf(rows[0]).FieldByName("SecretHash"); hasHash {
		t.Error("the token listing carries the secret hash — it has no use outside " +
			"authentication, and a column that is never selected cannot leak")
	}
}

// TestTokenRulesTheDatabaseEnforces covers the constraints that exist so a bug cannot store a
// token that is nonsense: an expiry that has already passed at insert time, and a hash that
// is not a SHA-256 digest.
func TestTokenRulesTheDatabaseEnforces(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	storetest.SeedPerson(t, s, testdata.Eins)

	for _, tc := range []struct {
		name   string
		params store.CreateTokenParams
	}{
		{
			// There is no such thing as a token that expires before it exists. The 90-day
			// default and the 365-day ceiling live where tokens are minted, because a CHECK
			// against now() cannot be immutable — but this much the database can hold.
			name: "expiry before creation",
			params: store.CreateTokenParams{
				TokenID:    "ZZZZZZZZZZZZZZZZ",
				OwnerID:    testdata.Eins.ID(),
				SecretHash: hash(6),
				Scopes:     []string{},
				ExpiresAt:  time.Now().Add(-time.Hour),
			},
		},
		{
			// A truncated hash would still compare successfully against itself, so nothing
			// downstream would notice — the token would simply be weaker than advertised.
			name: "hash that is not 32 bytes",
			params: store.CreateTokenParams{
				TokenID:    "YYYYYYYYYYYYYYYY",
				OwnerID:    testdata.Eins.ID(),
				SecretHash: []byte("too short"),
				Scopes:     []string{},
				ExpiresAt:  time.Now().Add(time.Hour),
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.Queries().CreateToken(t.Context(), tc.params); err == nil {
				t.Error("the database accepted it")
			}
		})
	}
}

// TestPeopleWithTokensCannotBeDeleted covers the ON DELETE RESTRICT.
//
// People are deactivated, not deleted — their assignments stay in the history and the audit
// log has to keep resolving who did what. A cascade would make a DELETE quietly take the
// evidence of what those tokens did with it, which is the one thing an audit trail may not
// do.
func TestPeopleWithTokensCannotBeDeleted(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	storetest.SeedPerson(t, s, testdata.Eins)
	storetest.SeedToken(t, s, testdata.Eins, hash(7), storetest.TokenOptions{})

	_, err := s.Pool.Exec(t.Context(), `DELETE FROM person WHERE id = $1`, testdata.Eins.ID())
	if err == nil {
		t.Error("a person holding a token was deleted, and the token went with them")
	}

	// Deactivation is the supported path, and it is what revokes everything at once.
	err = s.Queries().SetPersonActive(t.Context(), store.SetPersonActiveParams{
		ID:     testdata.Eins.ID(),
		Active: false,
	})
	if err != nil {
		t.Fatalf("cannot deactivate: %v", err)
	}
	got, err := s.Queries().TokenByID(t.Context(), testdata.Eins.TokenID)
	if err != nil {
		t.Fatalf("cannot read the token of a deactivated person: %v", err)
	}
	if got.Active {
		t.Error("the token still reports its owner as active — authentication would let a " +
			"leaver's script keep working")
	}
}

// TestAnExpiredGrantDoesNotReachTheTokenDoor.
//
// The same rule as TestAnExpiredGrantIsInvisibleToTheLookupsButNotToTheAuditView, asserted on
// the other authentication query — because for a while it only held on one of them. The
// expiry filter arrived with the migration that added person_role.expires_at, person.sql got
// it, and this query did not: an expired DEANS_OFFICE grant kept taking effect through the
// token door long after the browser door had let it lapse.
//
// That is the exact grant the expiry exists for. An administrator who has to look at
// something grants themselves DEANS_OFFICE for an hour, visibly; the mechanism only works if
// stepping back over the threshold happens by itself, on every route in.
//
// It is also a reminder that graphqltest.EachDoor cannot catch this one on its own: the two
// doors resolve roles through *different* queries, so the assertion has to exist here too.
func TestAnExpiredGrantDoesNotReachTheTokenDoor(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	storetest.SeedPerson(t, s, testdata.Eins, string(policy.RoleLecturer))
	storetest.SeedToken(t, s, testdata.Eins, hash(9), storetest.TokenOptions{})

	if _, err := s.Pool.Exec(t.Context(),
		`INSERT INTO person_role (person_id, role, granted_at, expires_at)
		 VALUES ($1, 'DEANS_OFFICE', now() - interval '2 hours', now() - interval '1 hour')`,
		testdata.Eins.ID()); err != nil {
		t.Fatalf("cannot seed the expired grant: %v", err)
	}

	got, err := s.Queries().TokenByID(t.Context(), testdata.Eins.TokenID)
	if err != nil {
		t.Fatalf("cannot read the token: %v", err)
	}

	for _, r := range got.Roles {
		if r == string(policy.RoleDeansOffice) {
			t.Error("an expired DEANS_OFFICE grant still reaches the token door — a script " +
				"would keep reading on a permission the database considers over")
		}
	}

	// The live grant has to survive the filter. A LEFT JOIN whose condition moved into a
	// WHERE clause would drop this row entirely, and the token would authenticate as nobody.
	if !hasRoleString(got.Roles, string(policy.RoleLecturer)) {
		t.Errorf("roles are %v — the live grant was filtered out with the expired one",
			got.Roles)
	}
}
