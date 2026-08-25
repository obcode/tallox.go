package domain_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/auth"
	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
	"github.com/obcode/tallox.go/internal/testdata"
)

// fakeStore records what the service asked it to store.
//
// Hand-written, like every other seam in this repository. What these tests are about is the
// arithmetic and the validation — the rules that live in Go — while what the queries do with
// the result is asserted against real PostgreSQL in internal/store. Splitting it that way is
// what keeps each half fast and honest.
type fakeStore struct {
	created   []createdCall
	revoked   []revokedCall
	listErr   error
	createErr error
	revokeErr error
	records   []domain.TokenRecord
}

type createdCall struct {
	tokenID     string
	ownerID     uuid.UUID
	secretHash  []byte
	description string
	scopes      []string
	expiresAt   time.Time
}

type revokedCall struct {
	tokenID string
	ownerID uuid.UUID
}

func (f *fakeStore) CreateToken(_ context.Context, tokenID string, ownerID uuid.UUID,
	secretHash []byte, description string, scopes []string, expiresAt time.Time,
) (domain.TokenRecord, error) {
	if f.createErr != nil {
		return domain.TokenRecord{}, f.createErr
	}
	f.created = append(f.created,
		createdCall{tokenID, ownerID, secretHash, description, scopes, expiresAt})
	return domain.TokenRecord{
		ID:          tokenID,
		Description: description,
		Scopes:      scopes,
		CreatedAt:   time.Now(),
		ExpiresAt:   expiresAt,
	}, nil
}

func (f *fakeStore) TokensOfOwner(context.Context, uuid.UUID) ([]domain.TokenRecord, error) {
	return f.records, f.listErr
}

func (f *fakeStore) RevokeTokenOfOwner(_ context.Context, tokenID string, ownerID uuid.UUID) (domain.TokenRecord, error) {
	f.revoked = append(f.revoked, revokedCall{tokenID, ownerID})
	if f.revokeErr != nil {
		return domain.TokenRecord{}, f.revokeErr
	}
	return domain.TokenRecord{ID: tokenID, RevokedAt: time.Now()}, nil
}

// frozen is a clock that does not move, so an expiry assertion is exact rather than
// approximately right.
var frozen = time.Date(2026, 8, 1, 12, 0, 0, 0, time.Local)

func service(store domain.TokenStore) *domain.TokenService {
	return domain.NewTokenService(store, func() time.Time { return frozen })
}

// TestCreatedTokensAreUsableAndStoredAsAHash covers the two halves that have to agree: what
// the caller is handed, and what the database keeps.
//
// If they ever disagree, every token this server issues is dead on arrival — and the symptom
// would be "invalid token" for a credential the server produced itself, which is a confusing
// place to start debugging.
func TestCreatedTokensAreUsableAndStoredAsAHash(t *testing.T) {
	t.Parallel()

	store := &fakeStore{}
	actor := testdata.Eins.Actor(principal.KindInteractive, "LECTURER")

	created, err := service(store).Create(t.Context(), actor, "Auswertung", nil, nil)
	if err != nil {
		t.Fatalf("cannot create: %v", err)
	}

	parsed, err := auth.ParseToken(created.Plaintext)
	if err != nil {
		t.Fatalf("the token handed to the caller does not parse: %v", err)
	}
	if len(store.created) != 1 {
		t.Fatalf("stored %d tokens", len(store.created))
	}
	stored := store.created[0]

	if parsed.ID != stored.tokenID {
		t.Errorf("handed out token %s, stored %s", parsed.ID, stored.tokenID)
	}
	if string(auth.HashSecret(parsed.Secret)) != string(stored.secretHash) {
		t.Error("the stored hash is not the hash of the secret handed to the caller — every " +
			"token this server issues would be rejected by it")
	}
	if strings.Contains(string(stored.secretHash), parsed.Secret) {
		t.Error("the secret itself reached the store")
	}
	if stored.ownerID != testdata.Eins.ID() {
		t.Errorf("token belongs to %v, want the caller %v", stored.ownerID, testdata.Eins.ID())
	}
}

// TestLifetimes pins the arithmetic and the refusals in one table.
//
// Out-of-range values are refused rather than clamped. Somebody asking for ten years has a
// plan this system will not support, and quietly giving them a year means they find out from
// a broken script twelve months later instead of from an error message now.
func TestLifetimes(t *testing.T) {
	t.Parallel()

	days := func(n int) *int { return &n }

	for _, tc := range []struct {
		name    string
		request *int
		want    time.Time
		wantErr error
	}{
		{"default is 90 days", nil, frozen.AddDate(0, 0, domain.DefaultTokenDays), nil},
		{"one day", days(1), frozen.AddDate(0, 0, 1), nil},
		{"the ceiling", days(domain.MaxTokenDays), frozen.AddDate(0, 0, domain.MaxTokenDays), nil},
		{"past the ceiling", days(domain.MaxTokenDays + 1), time.Time{}, domain.ErrLifetimeOutOfRange},
		{"zero", days(0), time.Time{}, domain.ErrLifetimeOutOfRange},
		{"negative", days(-30), time.Time{}, domain.ErrLifetimeOutOfRange},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{}
			actor := testdata.Eins.Actor(principal.KindInteractive, "LECTURER")

			_, err := service(store).Create(t.Context(), actor, "Auswertung", tc.request, nil)

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("got %v, want %v", err, tc.wantErr)
				}
				if len(store.created) != 0 {
					t.Error("a refused token was stored anyway")
				}
				return
			}
			if err != nil {
				t.Fatalf("cannot create: %v", err)
			}
			if !store.created[0].expiresAt.Equal(tc.want) {
				t.Errorf("expires at %v, want %v", store.created[0].expiresAt, tc.want)
			}
		})
	}
}

// TestDescriptionsAreRequiredAndBounded: an unnamed token cannot be told from the others at
// the moment it matters, which is when somebody is deciding which one to revoke.
func TestDescriptionsAreRequiredAndBounded(t *testing.T) {
	t.Parallel()

	actor := testdata.Eins.Actor(principal.KindInteractive, "LECTURER")

	for _, tc := range []struct {
		name        string
		description string
		wantErr     error
	}{
		{"empty", "", domain.ErrNoDescription},
		{"only spaces", "   \t\n ", domain.ErrNoDescription},
		{"too long", strings.Repeat("x", domain.MaxDescriptionLength+1), domain.ErrDescriptionTooLong},
		{"at the limit", strings.Repeat("x", domain.MaxDescriptionLength), nil},
		// Counted in runes, not bytes: a description of umlauts is not secretly half as long
		// as one of ASCII.
		{"umlauts at the limit", strings.Repeat("ü", domain.MaxDescriptionLength), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{}
			_, err := service(store).Create(t.Context(), actor, tc.description, nil, nil)

			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("got %v, want %v", err, tc.wantErr)
			}
		})
	}

	// Trimmed on the way in, so a leading space is not a second, different description.
	store := &fakeStore{}
	if _, err := service(store).Create(t.Context(), actor, "  CI-Lauf  ", nil, nil); err != nil {
		t.Fatalf("cannot create: %v", err)
	}
	if store.created[0].description != "CI-Lauf" {
		t.Errorf("stored description %q", store.created[0].description)
	}
}

// TestRevokeAsksOnlyForYourOwn covers what the service passes to the store, which is where
// ownership is actually enforced.
//
// The service must not read the token first and decide afterwards: that would be a race, and
// it would confirm the existence of a token id before refusing it.
func TestRevokeAsksOnlyForYourOwn(t *testing.T) {
	t.Parallel()

	store := &fakeStore{}
	actor := testdata.Zwei.Actor(principal.KindInteractive, "LECTURER")

	if _, err := service(store).Revoke(t.Context(), actor, "AAAAAAAAAAAAAAAA"); err != nil {
		t.Fatalf("cannot revoke: %v", err)
	}

	if len(store.revoked) != 1 {
		t.Fatalf("asked the store %d times", len(store.revoked))
	}
	if store.revoked[0].ownerID != testdata.Zwei.ID() {
		t.Errorf("revoked as owner %v, want the caller %v",
			store.revoked[0].ownerID, testdata.Zwei.ID())
	}
}

// TestAnonymousCallersOwnNothing: every entry point checks, because "not authenticated"
// arriving as uuid.Nil would otherwise mean "the tokens of nobody", and the nil UUID is a
// value the database will happily compare against.
func TestAnonymousCallersOwnNothing(t *testing.T) {
	t.Parallel()

	store := &fakeStore{}
	s := service(store)

	if _, err := s.Create(t.Context(), principal.Anonymous, "x", nil, nil); !errors.Is(err, domain.ErrNotAuthenticated) {
		t.Errorf("Create: %v", err)
	}
	if _, err := s.List(t.Context(), principal.Anonymous); !errors.Is(err, domain.ErrNotAuthenticated) {
		t.Errorf("List: %v", err)
	}
	if _, err := s.Revoke(t.Context(), principal.Anonymous, "x"); !errors.Is(err, domain.ErrNotAuthenticated) {
		t.Errorf("Revoke: %v", err)
	}
	if len(store.created)+len(store.revoked) != 0 {
		t.Error("an anonymous caller reached the store")
	}
}

// TestScopesAreStoredAsTheyWereAsked covers the whole of the minting side of the scope model.
//
// Written as one table because the cases are each other's context: the empty list is not a
// degenerate case of the others but the *default*, and reading it next to the narrowing ones is
// what makes that visible.
func TestScopesAreStoredAsTheyWereAsked(t *testing.T) {
	t.Parallel()

	planningRead := policy.Scope{Area: policy.ScopeAreaPlanning, Verb: policy.ScopeVerbRead}
	planningWrite := policy.Scope{Area: policy.ScopeAreaPlanning, Verb: policy.ScopeVerbWrite}
	profileRead := policy.Scope{Area: policy.ScopeAreaProfile, Verb: policy.ScopeVerbRead}

	tests := []struct {
		name    string
		scopes  []policy.Scope
		want    []string
		wantErr error
	}{
		{
			name: "nothing asked for is an unrestricted token",
			// The pre-existing default, and it stays the default. Scopes only ever narrow, so
			// "nothing selected" has to mean "nothing removed" — the other reading would have
			// made every token minted before this feature stop working.
			scopes: nil,
			want:   []string{},
		},
		{
			name:   "an empty list is the same thing",
			scopes: []policy.Scope{},
			want:   []string{},
		},
		{
			name:   "one scope",
			scopes: []policy.Scope{planningRead},
			want:   []string{"PLANNING:READ"},
		},
		{
			name: "several, in the order they were given",
			// Not sorted. The stored order is what the owner will see in their token list, and
			// re-ordering it would make the list disagree with what they ticked.
			scopes: []policy.Scope{planningWrite, profileRead},
			want:   []string{"PLANNING:WRITE", "PROFILE:READ"},
		},
		{
			name: "read and write of the same area, which is redundant but not wrong",
			// WRITE already implies READ, so this is one entry too many — and it is not this
			// layer's business to tidy it up. Refusing it would make a dialogue that ticks both
			// boxes an error, and that dialogue is a reasonable thing to build.
			scopes: []policy.Scope{planningRead, planningWrite},
			want:   []string{"PLANNING:READ", "PLANNING:WRITE"},
		},
		{
			name:    "the same scope twice",
			scopes:  []policy.Scope{planningRead, planningRead},
			wantErr: domain.ErrScopeRepeated,
		},
		{
			name:    "an area this build does not know",
			scopes:  []policy.Scope{{Area: "ASSIGNMENTS", Verb: policy.ScopeVerbRead}},
			wantErr: domain.ErrScopeUnknown,
		},
		{
			name:    "a verb this build does not know",
			scopes:  []policy.Scope{{Area: policy.ScopeAreaPlanning, Verb: "DELETE"}},
			wantErr: domain.ErrScopeUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{}
			actor := testdata.Eins.Actor(principal.KindInteractive, "LECTURER")

			_, err := service(store).Create(t.Context(), actor, "Auswertung", nil, tt.scopes)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				if len(store.created) != 0 {
					t.Errorf("a refused request still stored %d tokens", len(store.created))
				}
				return
			}

			if err != nil {
				t.Fatalf("cannot create: %v", err)
			}
			if len(store.created) != 1 {
				t.Fatalf("stored %d tokens", len(store.created))
			}
			got := store.created[0].scopes
			if got == nil {
				t.Fatal("stored nil scopes — the column is NOT NULL")
			}
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Errorf("stored %v, want %v", got, tt.want)
			}
		})
	}
}

// TestScopesAreNotCheckedAgainstTheOwnersRoles pins a decision that reads like a gap.
//
// A scope is a restriction somebody puts on their own credential, not a permission they are
// asking for. A lecturer minting an ADMIN-scoped token gets a token that reaches nothing in
// that area — the role check refuses it, exactly as it refuses the lecturer herself. Validating
// here would be a second permission model, and it would be wrong the day she is granted the
// role after minting the token.
func TestScopesAreNotCheckedAgainstTheOwnersRoles(t *testing.T) {
	t.Parallel()

	store := &fakeStore{}
	lecturer := testdata.Eins.Actor(principal.KindInteractive, string(policy.RoleLecturer))

	_, err := service(store).Create(t.Context(), lecturer, "Auswertung", nil,
		[]policy.Scope{{Area: policy.ScopeAreaAdmin, Verb: policy.ScopeVerbWrite}})
	if err != nil {
		t.Fatalf("minting a scope the owner cannot use is not an error, but it failed: %v", err)
	}

	if got := store.created[0].scopes; strings.Join(got, ",") != "ADMIN:WRITE" {
		t.Errorf("stored %v, want ADMIN:WRITE", got)
	}
}
