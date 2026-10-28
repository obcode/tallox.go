package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/obcode/tallox.go/internal/domain"
)

// Tokens is the persistence behind domain.TokenService.
//
// Like Directory, it exists so that the layer above can be written against an interface it
// owns rather than against generated query structs. The dependency runs store → domain and
// never back.
type Tokens struct {
	q *Queries
}

// NewTokens binds the token queries to a pool or transaction.
func NewTokens(db DBTX) *Tokens { return &Tokens{q: New(db)} }

var _ domain.TokenStore = (*Tokens)(nil)

// CreateToken stores a freshly minted token. The secret itself never reaches this layer —
// only its hash, which is the whole point of hashing it in the layer that generated it.
func (t *Tokens) CreateToken(ctx context.Context, tokenID string, ownerID uuid.UUID,
	secretHash []byte, description string, scopes []string, fullReach bool, expiresAt time.Time,
) (domain.TokenRecord, error) {
	row, err := t.q.CreateToken(ctx, CreateTokenParams{
		TokenID:     tokenID,
		OwnerID:     ownerID,
		SecretHash:  secretHash,
		Description: description,
		Scopes:      scopes,
		ExpiresAt:   expiresAt,
		FullReach:   fullReach,
	})
	if err != nil {
		return domain.TokenRecord{}, fmt.Errorf("cannot insert token: %w", err)
	}

	return domain.TokenRecord{
		ID:          row.TokenID,
		Description: row.Description,
		Scopes:      row.Scopes,
		FullReach:   row.FullReach,
		CreatedAt:   row.CreatedAt,
		ExpiresAt:   row.ExpiresAt,
		LastUsedAt:  nullableTime(row.LastUsedAt),
		RevokedAt:   nullableTime(row.RevokedAt),
	}, nil
}

// TokensOfOwner lists somebody's tokens, newest first. The secret hash is not in the
// projection — see the query.
func (t *Tokens) TokensOfOwner(ctx context.Context, ownerID uuid.UUID) ([]domain.TokenRecord, error) {
	rows, err := t.q.ListTokensOfPerson(ctx, ownerID)
	if err != nil {
		return nil, fmt.Errorf("cannot list tokens: %w", err)
	}

	out := make([]domain.TokenRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.TokenRecord{
			ID:          row.TokenID,
			Description: row.Description,
			Scopes:      row.Scopes,
			FullReach:   row.FullReach,
			CreatedAt:   row.CreatedAt,
			ExpiresAt:   row.ExpiresAt,
			LastUsedAt:  nullableTime(row.LastUsedAt),
			RevokedAt:   nullableTime(row.RevokedAt),
		})
	}
	return out, nil
}

// RevokeTokenOfOwner withdraws a token, but only the owner's own.
//
// Ownership is enforced by the query's WHERE clause, so "no such token" and "not your token"
// arrive here as the same empty result and leave as the same error. Distinguishing them would
// tell a caller which token ids exist, which is the one thing a token id is not supposed to
// reveal.
func (t *Tokens) RevokeTokenOfOwner(ctx context.Context, tokenID string, ownerID uuid.UUID) (domain.TokenRecord, error) {
	row, err := t.q.RevokeTokenOfOwner(ctx, RevokeTokenOfOwnerParams{
		TokenID: tokenID,
		OwnerID: ownerID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.TokenRecord{}, domain.ErrNoSuchToken
	}
	if err != nil {
		return domain.TokenRecord{}, fmt.Errorf("cannot revoke token: %w", err)
	}

	return domain.TokenRecord{
		ID:          row.TokenID,
		Description: row.Description,
		Scopes:      row.Scopes,
		FullReach:   row.FullReach,
		CreatedAt:   row.CreatedAt,
		ExpiresAt:   row.ExpiresAt,
		LastUsedAt:  nullableTime(row.LastUsedAt),
		RevokedAt:   nullableTime(row.RevokedAt),
	}, nil
}
