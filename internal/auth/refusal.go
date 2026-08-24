package auth

import (
	"context"
	"errors"
	"net/netip"

	"github.com/obcode/tallox.go/internal/principal"
)

// AccessRecorder records the sign-ins this package refuses.
//
// A seam of its own rather than the store, and rather than internal/domain: this package
// authenticates and nothing else, and it is deliberately below the layer that knows what an
// access log entry is. bootstrap adapts this to the real recorder, exactly as it does for
// UserLookup and TokenLookup.
//
// Nil is a legitimate value and means "do not record" — every unit test in this package runs
// that way, and so does any future entry point that authenticates without a database behind it.
type AccessRecorder interface {
	RecordRefusal(ctx context.Context, r Refusal) error
}

// Refusal is one sign-in that did not happen.
//
// It carries what the request *claimed* to be, which is the whole value of the record: the
// entry an administrator wants tonight is "somebody with an HM account and no person row tried
// to get in", and that entry has no person id by definition.
type Refusal struct {
	// Mail is the address the browser door was knocked on with, empty on the token door.
	Mail string
	// TokenID is the public half of the token presented, empty on the browser door and empty
	// when the credential was too malformed to have one.
	TokenID string
	// Door is which mount was knocked on.
	Door principal.Kind
	// Code is the machine-readable reason — see RefusalCode.
	Code string
	// Source is where the request came from.
	Source netip.Addr
}

// The refusal codes. Stable strings: they are written to the access log, which is read months
// later, and they appear in the nightly report.
//
// Machine-readable and never the German sentence. The sentences in refusal() are what a person
// reads and they get reworded; a log that stored them would be a second place they have to
// match, and a report grouping by them would fragment on the day somebody improves one.
const (
	CodeUnknownUser    = "UNKNOWN_USER"
	CodeInactiveUser   = "INACTIVE_USER"
	CodeExpiredToken   = "EXPIRED_TOKEN"
	CodeRevokedToken   = "REVOKED_TOKEN"
	CodeMalformedToken = "MALFORMED_TOKEN"
	CodeInvalidToken   = "INVALID_TOKEN"
	CodeUnavailable    = "UNAVAILABLE"
)

// RefusalCode names why a credential was declined.
//
// Note that CodeInvalidToken covers both "no such token" and "wrong secret", exactly as the
// error does: telling those apart is precisely what an attacker would like to learn, and the
// access log is not a place to publish it either.
func RefusalCode(err error) string {
	switch {
	case errors.Is(err, ErrUnavailable):
		return CodeUnavailable
	case errors.Is(err, ErrUnknownUser):
		return CodeUnknownUser
	case errors.Is(err, ErrInactiveUser):
		return CodeInactiveUser
	case errors.Is(err, ErrExpiredToken):
		return CodeExpiredToken
	case errors.Is(err, ErrRevokedToken):
		return CodeRevokedToken
	case errors.Is(err, ErrMalformedToken):
		return CodeMalformedToken
	default:
		return CodeInvalidToken
	}
}

// Asserted is what a request claimed to be, before any lookup.
//
// Read off the request rather than off the error, which is the alternative and a bad one: the
// address is in the error's *message* today, and an audit trail that works by parsing a
// sentence stops working the first time somebody rewords it.
type Asserted struct {
	Mail    string
	TokenID string
}
