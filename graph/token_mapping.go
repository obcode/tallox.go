package graph

import (
	"errors"
	"fmt"

	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/obcode/tallox.go/graph/model"
	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/policy"
)

// This file holds what the token resolvers translate with. It is separate because gqlgen
// rewrites *.resolvers.go from the schema on every generate and moves anything else out —
// a helper left in there survives exactly until the next `go generate`.

// tokenModel reshapes a domain record for the wire. The zero time becomes null, which is what
// "never used" and "not revoked" mean.
func tokenModel(record domain.TokenRecord) *model.PersonalAccessToken {
	out := &model.PersonalAccessToken{
		ID:          record.ID,
		Description: record.Description,
		Scopes:      record.Scopes,
		CreatedAt:   record.CreatedAt,
		ExpiresAt:   record.ExpiresAt,
	}
	if !record.LastUsedAt.IsZero() {
		lastUsed := record.LastUsedAt
		out.LastUsedAt = &lastUsed
	}
	if !record.RevokedAt.IsZero() {
		revoked := record.RevokedAt
		out.RevokedAt = &revoked
	}
	return out
}

// userFacing decides what a caller is told about a failure.
//
// The domain's own refusals are things a person can act on and get a sentence of their own,
// in German like everything else a person reads. Anything else — a failing query, a driver
// error — is replaced by a generic one, because those messages carry table names, constraint
// names and, on a write path, the fact that a row already exists.
//
// That last one is not hypothetical: the wish workflow will hit a UNIQUE violation whose
// verbatim text reveals that somebody else has already registered interest, which is
// precisely what the confidentiality rule withholds. Establishing the habit on the first
// mutation is cheaper than remembering it on that one.
func userFacing(err error) error {
	switch {
	case errors.Is(err, domain.ErrNoDescription):
		return refusal("TOKEN_DESCRIPTION_REQUIRED",
			"Bitte eine Beschreibung angeben, damit das Token später wiedererkennbar ist.")
	case errors.Is(err, domain.ErrDescriptionTooLong):
		return refusal("TOKEN_DESCRIPTION_TOO_LONG",
			fmt.Sprintf("Die Beschreibung darf höchstens %d Zeichen lang sein.",
				domain.MaxDescriptionLength))
	case errors.Is(err, domain.ErrLifetimeOutOfRange):
		return refusal("TOKEN_LIFETIME_OUT_OF_RANGE",
			fmt.Sprintf("Die Gültigkeit muss zwischen %d und %d Tagen liegen.",
				domain.MinTokenDays, domain.MaxTokenDays))
	case errors.Is(err, domain.ErrNoSuchToken):
		// The same code and the same sentence for "does not exist" and "belongs to somebody
		// else" — the difference is not information the caller is entitled to.
		return refusal("TOKEN_NOT_FOUND", "Dieses Token existiert nicht.")
	case errors.Is(err, domain.ErrScopeUnknown):
		return refusal("SCOPE_UNKNOWN", "Diesen Scope kennt der Server nicht.")
	case errors.Is(err, domain.ErrScopeRepeated):
		return refusal("SCOPE_REPEATED", "Jeder Scope darf nur einmal angegeben werden.")
	case errors.Is(err, domain.ErrNotAuthenticated):
		return refusal("UNAUTHENTICATED", "Nicht angemeldet.")
	case errors.Is(err, domain.ErrInteractiveOnly):
		return refusal("INTERACTIVE_ONLY", policy.InteractiveOnlyReason)
	default:
		return refusal("INTERNAL", "Die Aktion konnte nicht ausgeführt werden.")
	}
}

// refusal builds the error shape a GraphQL client can act on.
//
// A *gqlerror.Error rather than errors.New, and the extensions code rather than the sentence,
// because the GUI has to branch on *what went wrong* and the sentence is the one part of this
// that will be reworded. Matching German prose in TypeScript is a coupling that breaks the
// first time somebody improves a message.
//
// It also settles a real conflict: Go says an error string does not end in punctuation, and a
// sentence a person reads in a dialogue does. These are presentation strings that happen to
// travel through the error channel because that is what GraphQL offers, and gqlerror is the
// type that says so.
func refusal(code, message string) error {
	return &gqlerror.Error{
		Message:    message,
		Extensions: map[string]any{"code": code},
	}
}

// requestedScopes turns the input list into the policy's own type.
//
// nil in, nil out — and that matters: the argument is optional, so "not supplied" and "supplied
// as an empty list" both arrive here as an empty slice, and both mean the same thing. An
// unrestricted token is what somebody who did not think about scopes gets, which is the
// behaviour that existed before scopes could be chosen at all.
//
// No validation here. The enums have already refused anything the schema does not know, and
// what remains — a repeated entry — is internal/domain's to reject, because a resolver that
// decided it would be a second place holding the rule.
func requestedScopes(input []*model.ScopeGrantInput) []policy.Scope {
	out := make([]policy.Scope, 0, len(input))
	for _, entry := range input {
		if entry == nil {
			continue
		}
		out = append(out, policy.Scope{Area: entry.Area, Verb: entry.Verb})
	}
	return out
}
