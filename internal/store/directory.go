package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/obcode/tallox.go/internal/auth"
	"github.com/obcode/tallox.go/internal/principal"
)

// Directory answers the two questions internal/auth asks: who is this person, and what is
// this token.
//
// It is the one place where the storage layer knows the authentication layer exists. The
// dependency points store → auth and must never point back: auth defines the seams so that it
// can be tested without a database, and importing store from there would make that impossible
// and the seams pointless.
//
// The alternative — auth reaching for the generated query set directly — would put pgx types
// in the authenticators, and the layering test in internal/arch would be right to fail it.
type Directory struct {
	q *Queries
}

// NewDirectory binds the lookups to a pool or transaction.
func NewDirectory(db DBTX) *Directory { return &Directory{q: New(db)} }

// Compile-time proof that this type is what the middleware is wired with. Without it, a
// signature drifting apart from the interface only surfaces in bootstrap, where the error
// message is about a struct literal three layers away from the change.
var (
	_ auth.UserLookup  = (*Directory)(nil)
	_ auth.TokenLookup = (*Directory)(nil)
)

// PersonByMail resolves the identity the auth proxy asserts.
//
// "Not found" is (nil, nil), the convention throughout this repository. It matters more than
// usual here: authentication has to tell "this installation has never heard of you" (a 401
// that says so, and an import that has not run) from "the database is unreachable" (a 503,
// and nobody's credential is broken). Collapsing the two into one error is how a database
// restart becomes a wave of colleagues re-authenticating.
func (d *Directory) PersonByMail(ctx context.Context, mail string) (*auth.Person, error) {
	row, err := d.q.PersonByMail(ctx, mail)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read person by mail: %w", err)
	}

	scopes, err := roleScopesFrom(row.RoleScopes)
	if err != nil {
		return nil, fmt.Errorf("cannot read the role scopes of %s: %w", mail, err)
	}

	return &auth.Person{
		ID:         row.ID,
		Mail:       row.Mail,
		Name:       row.Name,
		Active:     row.Active,
		Roles:      row.Roles,
		RoleScopes: scopes,
	}, nil
}

// TokenByID resolves a Personal Access Token and its owner, expired and revoked ones
// included — see the query, which explains why it does not filter them out.
func (d *Directory) TokenByID(ctx context.Context, tokenID string) (*auth.Token, error) {
	row, err := d.q.TokenByID(ctx, tokenID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read token: %w", err)
	}

	scopes, err := roleScopesFrom(row.RoleScopes)
	if err != nil {
		return nil, fmt.Errorf("cannot read the role scopes behind token %s: %w", tokenID, err)
	}

	return &auth.Token{
		ID:         row.TokenID,
		SecretHash: row.SecretHash,
		Scopes:     row.Scopes,
		ExpiresAt:  row.ExpiresAt,
		RevokedAt:  nullableTime(row.RevokedAt),
		Owner: auth.Person{
			ID:         row.OwnerID,
			Mail:       row.Mail,
			Name:       row.Name,
			Active:     row.Active,
			Roles:      row.Roles,
			RoleScopes: scopes,
		},
	}, nil
}

// roleScopesFrom decodes the jsonb the two authentication queries aggregate.
//
// jsonb rather than a second query, because this runs on every request through either door and
// a second round trip to authenticate is a cost paid a great many times. jsonb rather than a
// composite array, because sqlc maps that to something nobody wants to scan into.
//
// An error here fails authentication rather than yielding an actor with no scopes, and that is
// the direction to fail in: a programme lead whose scopes could not be read would otherwise look
// exactly like one who has none, be told to ask an administrator, and be told the same thing
// again after the administrator had done it.
func roleScopesFrom(raw []byte) ([]principal.RoleScope, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	var encoded []struct {
		Role      string    `json:"role"`
		Programme uuid.UUID `json:"programme"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, err
	}
	if len(encoded) == 0 {
		return nil, nil
	}

	scopes := make([]principal.RoleScope, 0, len(encoded))
	for _, e := range encoded {
		scopes = append(scopes, principal.RoleScope{Role: e.Role, ProgrammeID: e.Programme})
	}
	return scopes, nil
}

// MarkTokenUsed records that a token was used, coarsely — the guard is in the SQL.
func (d *Directory) MarkTokenUsed(ctx context.Context, tokenID string) error {
	if err := d.q.MarkTokenUsed(ctx, tokenID); err != nil {
		return fmt.Errorf("cannot record token use: %w", err)
	}
	return nil
}

// nullableTime flattens a nullable timestamp to the zero time.
//
// The pgtype wrapper stops here on purpose: everything above this layer works with time.Time
// and IsZero, so a driver type never reaches the policy or the resolvers. That is the same
// boundary the architecture test enforces for pgx itself, applied to the types it hands out.
func nullableTime(t pgtype.Timestamptz) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time
}
