package graph

import (
	"context"
	"time"
	"unicode/utf8"

	"github.com/99designs/gqlgen/graphql"
	"github.com/rs/zerolog/log"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/principal"
)

// maxOperationName is how much of a client-supplied operation name is kept.
//
// It arrives from outside and is written to a text column on every request. A cap is not
// paranoia about disk: it is what stops one caller deciding how wide the administration's log
// table renders, and how large a nightly report gets.
const maxOperationName = 200

// AccessRecorder is the write half of the access log, as this package needs it.
//
// A one-method view rather than the whole service: the GraphQL layer records and never reads
// back on this path, and a seam that could read is a seam somebody will read through.
type AccessRecorder interface {
	Record(ctx context.Context, rec domain.AccessRecord) error
}

// RecordAccess writes one access-log entry per operation.
//
// Wired in bootstrap with srv.AroundOperations, and registered BEFORE EnforceScopes, which
// makes it the outer of the two. That ordering is the whole reason a scope refusal appears in
// the log at all: EnforceScopes answers with graphql.OneShot instead of calling next, and only
// a middleware wrapped around it sees that response. Registered the other way round, the log
// would contain every operation that was allowed and no record of the ones that were not —
// which is precisely backwards for an audit trail.
//
// # What it records, and what it must never record
//
// The operation name and the root field names. Not the arguments, not the variables, not the
// response. ADMIN is deliberately not on the exception list of the wish visibility rule, and a
// log carrying arguments would hand that exception back through the side door: `wish(id: …)`
// with its argument is a copy of the confidential data with none of the policy attached.
//
// The same reasoning as internal/obs/scrub.go, and the same shape: what is written is an
// allow-list of things somebody decided to write, so a field nobody anticipated is absent
// rather than present-and-unreviewed.
//
// # Best effort
//
// A failure to record is a log line and never an error to the caller. The alternative — failing
// the operation because its audit entry could not be written — turns a full disk into an outage
// of the whole installation. It is reported at Error level, so it reaches GlitchTip and is
// visible as an operational event rather than as silence.
func RecordAccess(recorder AccessRecorder) graphql.OperationMiddleware {
	return func(ctx context.Context, next graphql.OperationHandler) graphql.ResponseHandler {
		oc := graphql.GetOperationContext(ctx)
		if oc == nil || oc.Operation == nil {
			// Nothing to describe. gqlgen sets the operation before this chain runs, so this is
			// a malformed request that was rejected earlier — there is no access to record
			// because nothing was accessed.
			return next(ctx)
		}

		fields, introspectionOnly := rootFieldNames(oc.Operation)
		if introspectionOnly {
			// An editor polls introspection in a loop, and it is deliberately public. Recording
			// it would bury the entries somebody actually wants to read under thousands that
			// say nothing about anybody.
			return next(ctx)
		}

		started := time.Now()
		response := next(ctx)

		// The response is a handler, not a value: it is called once for a query and repeatedly
		// for a subscription. Wrapping it means the entry is written when the operation is
		// answered rather than when it was accepted — which is what makes the duration and the
		// outcome real.
		return func(ctx context.Context) *graphql.Response {
			resp := response(ctx)
			record(ctx, recorder, oc, fields, time.Since(started), resp)
			return resp
		}
	}
}

func record(
	ctx context.Context,
	recorder AccessRecorder,
	oc *graphql.OperationContext,
	fields []string,
	took time.Duration,
	resp *graphql.Response,
) {
	actor := principal.From(ctx)

	rec := domain.AccessRecord{
		ActorMail: actor.Mail,
		Door:      domain.AccessDoorInteractive,
		TokenID:   actor.TokenID,
		Roles:     actor.Roles,
		Operation: truncate(operationNameOf(oc), maxOperationName),
		Fields:    fields,
		Mutation:  oc.Operation.Operation != ast.Query,
		Outcome:   domain.AccessOK,
		Duration:  took,
		SourceIP:  principal.SourceFrom(ctx),
	}
	if actor.Authenticated() {
		id := actor.ID
		rec.ActorID = &id
	}
	if actor.Kind == principal.KindToken {
		rec.Door = domain.AccessDoorToken
	}
	// Narrowed() is nil-vs-set rather than a comparison, so a session narrowed to exactly what
	// it holds still records that it was narrowed. That is the honest reading and it matches
	// what the banner in the interface says.
	if actor.Narrowed() {
		rec.NarrowedFrom = actor.NarrowedFrom
	}
	rec.Outcome, rec.ErrorCode = outcomeOf(resp)

	if err := recorder.Record(ctx, rec); err != nil {
		// Not returned anywhere: see the note on RecordAccess. Error level, so it reaches the
		// error reporter — an installation that has silently stopped logging accesses should be
		// an event somebody sees, not a discovery made in three months.
		log.Error().Err(err).
			Str("operation", rec.Operation).
			Str("door", string(rec.Door)).
			Msg("cannot record the access")
	}
}

// operationNameOf is what the operation was called.
//
// The name in the DOCUMENT first, and only then the one in the request envelope. gqlgen's
// OperationName is the `operationName` field of the JSON request, which a client sends only
// when the document holds several operations — so an ordinary named query would be recorded
// nameless, and the log would be least informative for exactly the well-written clients.
func operationNameOf(oc *graphql.OperationContext) string {
	if oc.Operation.Name != "" {
		return oc.Operation.Name
	}
	return oc.OperationName
}

// outcomeOf reads how the operation ended off the response.
//
// The first error's code, and only the code. The message is German prose that gets reworded,
// and a log that stored it would be a second place those sentences have to match.
//
// The three refusal codes map to their own outcomes because they are three different events for
// whoever reads the log: a scope refusal is a token minted too narrowly, an interactive-only
// refusal is a script reaching for personnel data, and everything else is a fault.
func outcomeOf(resp *graphql.Response) (domain.AccessOutcome, string) {
	if resp == nil || len(resp.Errors) == 0 {
		return domain.AccessOK, ""
	}

	code, _ := resp.Errors[0].Extensions["code"].(string)
	switch code {
	case "INSUFFICIENT_SCOPE":
		return domain.AccessRefusedScope, code
	case "INTERACTIVE_ONLY":
		return domain.AccessRefusedInteractive, code
	default:
		return domain.AccessError, code
	}
}

// rootFieldNames is what the operation asked for, and whether that was introspection alone.
//
// Sorted-by-appearance and deduplicated: the same field twice in one document is one thing
// asked for, and an alias does not change which field it is. Introspection fields are left out
// of the list entirely — they are public, so naming them tells a reader nothing.
func rootFieldNames(op *ast.OperationDefinition) (names []string, introspectionOnly bool) {
	seen := map[string]bool{}
	introspection := 0

	forEachRootField(op, func(field *ast.Field) {
		if IsIntrospectionField(field.Name) {
			introspection++
			return
		}
		if seen[field.Name] {
			return
		}
		seen[field.Name] = true
		names = append(names, field.Name)
	})

	if names == nil {
		names = []string{}
	}
	return names, len(names) == 0 && introspection > 0
}

// truncate caps a client-supplied string at n bytes, on a rune boundary.
//
// Bytes rather than runes, because the limit protects a column and a report width, and both are
// measured in bytes. Cutting back to a rune start keeps the result valid UTF-8, which the citext
// and text columns require and a mid-rune cut would not produce.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
