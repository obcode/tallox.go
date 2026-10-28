package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/obcode/tallox.go/internal/auth"
	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/store"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// TestRevokingSomebodyElsesTokenLooksLikeNothingHappened is the ownership rule, and it is a
// query test rather than a Go test on purpose: the WHERE clause is where the rule lives.
//
// Two properties in one assertion. The token stays valid, which is the part that matters. And
// the refusal is the same one a non-existent token produces, which is the part that is easy
// to lose — a distinct "not yours" would turn the token list into an oracle for which token
// ids exist.
func TestRevokingSomebodyElsesTokenLooksLikeNothingHappened(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	storetest.SeedPerson(t, s, testdata.Eins, string(policy.RoleLecturer))
	storetest.SeedPerson(t, s, testdata.Zwei, string(policy.RoleLecturer))
	storetest.SeedToken(t, s, testdata.Eins,
		auth.HashSecret(secretOf(t, testdata.Eins)), storetest.TokenOptions{Description: "Eins"})

	tokens := store.NewTokens(s.Pool)

	// Zwei tries to revoke a token belonging to Eins.
	_, err := tokens.RevokeTokenOfOwner(t.Context(), testdata.Eins.TokenID, testdata.Zwei.ID())
	if !errors.Is(err, domain.ErrNoSuchToken) {
		t.Fatalf("got %v, want ErrNoSuchToken", err)
	}

	// And a token id that exists nowhere produces exactly the same thing.
	_, err = tokens.RevokeTokenOfOwner(t.Context(), "ZZZZZZZZZZZZZZZZ", testdata.Zwei.ID())
	if !errors.Is(err, domain.ErrNoSuchToken) {
		t.Fatalf("got %v, want ErrNoSuchToken", err)
	}

	// The token is untouched, which is what "looks like nothing happened" has to mean.
	still, err := store.NewDirectory(s.Pool).TokenByID(t.Context(), testdata.Eins.TokenID)
	if err != nil || still == nil {
		t.Fatalf("cannot read back: %v", err)
	}
	if !still.RevokedAt.IsZero() {
		t.Error("somebody else's revocation went through")
	}
}

// TestRevokingYourOwnWorksAndKeepsTheFirstMoment: the ordinary case, plus idempotence.
//
// The first moment is the one the audit log needs — "when did this credential stop being
// trustworthy" has one answer, and a second revocation must not move it.
func TestRevokingYourOwnWorksAndKeepsTheFirstMoment(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	storetest.SeedPerson(t, s, testdata.Eins, string(policy.RoleLecturer))
	storetest.SeedToken(t, s, testdata.Eins,
		auth.HashSecret(secretOf(t, testdata.Eins)), storetest.TokenOptions{Description: "Eins"})

	tokens := store.NewTokens(s.Pool)

	first, err := tokens.RevokeTokenOfOwner(t.Context(), testdata.Eins.TokenID, testdata.Eins.ID())
	if err != nil {
		t.Fatalf("cannot revoke own token: %v", err)
	}
	if first.RevokedAt.IsZero() {
		t.Fatal("the revocation set no timestamp")
	}

	second, err := tokens.RevokeTokenOfOwner(t.Context(), testdata.Eins.TokenID, testdata.Eins.ID())
	if err != nil {
		t.Fatalf("revoking twice failed: %v", err)
	}
	if !second.RevokedAt.Equal(first.RevokedAt) {
		t.Errorf("the revocation moment moved from %v to %v", first.RevokedAt, second.RevokedAt)
	}
}

// TestTheStoreRoundTripsWhatTheServiceStores covers the adapter between domain and sqlc,
// including the nullable timestamps that are the easiest thing to get backwards.
func TestTheStoreRoundTripsWhatTheServiceStores(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	storetest.SeedPerson(t, s, testdata.Eins, string(policy.RoleLecturer))

	tokens := store.NewTokens(s.Pool)
	expires := time.Now().Add(30 * 24 * time.Hour)

	created, err := tokens.CreateToken(t.Context(), "FFFFFFFFFFFFFFFF", testdata.Eins.ID(),
		auth.HashSecret("irrelevant here"), "Notebook", []string{}, false, expires)
	if err != nil {
		t.Fatalf("cannot create: %v", err)
	}
	if created.Description != "Notebook" {
		t.Errorf("description is %q", created.Description)
	}
	if !created.LastUsedAt.IsZero() || !created.RevokedAt.IsZero() {
		t.Errorf("a fresh token reports lastUsed=%v revoked=%v — both have to be the zero "+
			"time, which is what the GraphQL layer renders as null",
			created.LastUsedAt, created.RevokedAt)
	}

	listed, err := tokens.TokensOfOwner(t.Context(), testdata.Eins.ID())
	if err != nil {
		t.Fatalf("cannot list: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != "FFFFFFFFFFFFFFFF" {
		t.Fatalf("listed %+v", listed)
	}
	if !listed[0].ExpiresAt.Truncate(time.Second).Equal(expires.Truncate(time.Second)) {
		t.Errorf("expiry came back as %v, want %v", listed[0].ExpiresAt, expires)
	}
}

// TestListingIsScopedToTheOwner: the query takes an owner and has to mean it. A missing WHERE
// here would show every colleague's token metadata in the account page.
func TestListingIsScopedToTheOwner(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	storetest.SeedPerson(t, s, testdata.Eins, string(policy.RoleLecturer))
	storetest.SeedPerson(t, s, testdata.Zwei, string(policy.RoleLecturer))
	storetest.SeedToken(t, s, testdata.Eins,
		auth.HashSecret(secretOf(t, testdata.Eins)), storetest.TokenOptions{Description: "Eins"})
	storetest.SeedToken(t, s, testdata.Zwei,
		auth.HashSecret(secretOf(t, testdata.Zwei)), storetest.TokenOptions{Description: "Zwei"})

	listed, err := store.NewTokens(s.Pool).TokensOfOwner(t.Context(), testdata.Eins.ID())
	if err != nil {
		t.Fatalf("cannot list: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("listed %d tokens, want only the caller's own", len(listed))
	}
	if listed[0].Description != "Eins" {
		t.Errorf("listed %q", listed[0].Description)
	}
}
