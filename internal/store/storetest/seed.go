package storetest

import (
	"testing"
	"time"

	"github.com/obcode/tallox.go/internal/store"
	"github.com/obcode/tallox.go/internal/testdata"
)

// Queries returns a sqlc query set bound to this schema.
func (s *Schema) Queries() *store.Queries { return store.New(s.Pool) }

// SeedPerson inserts a persona and grants it the given roles.
//
// Roles are strings rather than policy.Role, for the same reason principal.Actor carries
// strings: this package sits below the rules, and a test that wants to seed a grant the
// policy does not recognise — to prove it grants nothing — has to be able to say so.
func SeedPerson(t *testing.T, s *Schema, p testdata.Persona, roles ...string) {
	t.Helper()

	q := s.Queries()

	if _, err := q.CreatePerson(t.Context(), store.CreatePersonParams{
		ID:   p.ID(),
		Mail: p.Mail,
		Name: p.Name,
	}); err != nil {
		t.Fatalf("cannot seed %s: %v", p.Name, err)
	}

	for _, role := range roles {
		if err := q.GrantRole(t.Context(), store.GrantRoleParams{
			PersonID: p.ID(),
			Role:     role,
		}); err != nil {
			t.Fatalf("cannot grant %s to %s: %v", role, p.Name, err)
		}
	}
}

// TokenOptions varies the token a test seeds. The zero value is a valid, unexpired,
// unrevoked token with no scopes.
type TokenOptions struct {
	// ExpiresAt defaults to 90 days out, the same default the server uses when minting one.
	ExpiresAt time.Time
	// Revoked marks the token as revoked at insert time, for the "a revoked token is refused"
	// half of the authentication tests.
	Revoked bool
	// Scopes are the area:verb grants carried by the token.
	Scopes []string
	// Description is what the owner called it.
	Description string
}

// SeedToken inserts a Personal Access Token belonging to a persona.
//
// The hash is an argument rather than something this package derives from persona.Token,
// because hashing is internal/auth's business and a harness that reimplemented it could seed
// a token the real authenticator would reject — the classic fixture that makes a green test
// meaningless. Callers pass auth.HashSecret(secret).
func SeedToken(t *testing.T, s *Schema, p testdata.Persona, secretHash []byte, opts TokenOptions) {
	t.Helper()

	if opts.ExpiresAt.IsZero() {
		opts.ExpiresAt = time.Now().Add(90 * 24 * time.Hour)
	}
	if opts.Scopes == nil {
		opts.Scopes = []string{}
	}

	q := s.Queries()

	// Insert with a valid expiry even when the test wants an expired token: the database
	// refuses a token that expires before it was created, and that constraint is worth
	// keeping — it is what catches a minting bug that computes a zero timestamp.
	insertExpiry := opts.ExpiresAt
	expired := insertExpiry.Before(time.Now())
	if expired {
		insertExpiry = time.Now().Add(time.Hour)
	}

	if _, err := q.CreateToken(t.Context(), store.CreateTokenParams{
		TokenID:     p.TokenID,
		OwnerID:     p.ID(),
		SecretHash:  secretHash,
		Description: opts.Description,
		Scopes:      opts.Scopes,
		ExpiresAt:   insertExpiry,
	}); err != nil {
		t.Fatalf("cannot seed a token for %s: %v", p.Name, err)
	}

	// An expired token is not one that was born expired — it is one that was created earlier
	// and has since run out. Moving both timestamps produces exactly that row, and keeps the
	// fixture inside the rules the schema enforces instead of asking the schema to relax.
	if expired {
		_, err := s.Pool.Exec(t.Context(),
			`UPDATE personal_access_token
			    SET created_at = $2::timestamptz - interval '1 hour', expires_at = $2
			  WHERE token_id = $1`,
			p.TokenID, opts.ExpiresAt)
		if err != nil {
			t.Fatalf("cannot age the token of %s: %v", p.Name, err)
		}
	}

	if opts.Revoked {
		if _, err := q.RevokeTokenOfOwner(t.Context(), store.RevokeTokenOfOwnerParams{
			TokenID: p.TokenID, OwnerID: p.ID(),
		}); err != nil {
			t.Fatalf("cannot revoke the seeded token of %s: %v", p.Name, err)
		}
	}
}
