package graph

import (
	"context"
	"errors"

	"github.com/99designs/gqlgen/graphql"
	"github.com/rs/zerolog/log"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// internalMessage is the one sentence a caller gets for a failure that is ours, not theirs.
//
// Generic on purpose: a database error in the clear says things about rows nobody asked
// about — the table, the constraint, and on a write path the fact that a row already exists,
// which for a wish is the confidential fact itself. The code is the stable half of the
// contract; this sentence is the half the interface may reword.
const internalMessage = "Das hat nicht geklappt. Bitte später erneut versuchen."

// internalRefusal is the default arm of every area's error mapping: whatever reached it is
// not a refusal the caller can act on, so they get the generic sentence and the log gets
// the truth.
//
// Logged, because an INTERNAL that leaves no trace is unanswerable twice over: the person
// reading it cannot act on it, and neither can whoever they report it to. The log line is the
// other half of the refusal — it names what the sentence deliberately does not, on the one
// side of the wire where that is safe.
func internalRefusal(area string, err error) error {
	log.Error().Err(err).Str("area", area).Msg("refused with INTERNAL")
	return refusal("INTERNAL", internalMessage)
}

// PresentError is the gqlgen error presenter: the last thing an error passes before it is
// serialised, on both doors, and the one place that sees every error — resolver, directive,
// panic recovery, argument coercion.
//
// The rule is the shape of the contract: an error that carries an extensions code is one
// somebody wrote for the caller and passes unchanged. One without is nobody's sentence —
// a driver error wrapped with %w, a context deadline, gqlgen's own recovery message — and is
// replaced by the generic INTERNAL, keeping the path so the client still knows which field.
// The original is logged, which is what makes the generic sentence answerable.
//
// This is a backstop, not the mapping. Every area's error switch maps its own defaults to
// INTERNAL already; this catches the arm somebody forgets, and the errors that never pass an
// area's switch at all. The price is that the two custom scalars must refuse with a code of
// their own (model.Date does), or their German sentence would be replaced too.
func PresentError(ctx context.Context, err error) *gqlerror.Error {
	presented := graphql.DefaultErrorPresenter(ctx, err)
	if presented == nil {
		return nil
	}
	if _, coded := presented.Extensions["code"]; coded {
		return presented
	}
	// No operation yet: the transport could not read or decode the request, or a persisted
	// query was not found. Those sentences are gqlgen's, about the request's shape, and say
	// nothing about any row — and a client that sent broken JSON is better served by hearing
	// so. Every error a resolver returns arrives inside an operation.
	if !graphql.HasOperationContext(ctx) {
		return presented
	}

	log.Error().Err(err).Str("path", presented.Path.String()).Msg("uncoded error replaced by INTERNAL")
	return &gqlerror.Error{
		Message:    internalMessage,
		Path:       presented.Path,
		Locations:  presented.Locations,
		Extensions: map[string]any{"code": "INTERNAL"},
	}
}

// coded reports whether err already carries a code for the caller — a refusal built by any of
// the areas, passing through a mapping that did not produce it.
func coded(err error) bool {
	var gql *gqlerror.Error
	if !errors.As(err, &gql) {
		return false
	}
	_, ok := gql.Extensions["code"]
	return ok
}
