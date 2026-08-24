package graph

import (
	"context"
	"strings"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
)

// scopeDirectiveName is the annotation this file reads. Declared in graph/directives.graphqls
// and marked skip_runtime in gqlgen.yml, so nothing else in the generated code touches it.
const scopeDirectiveName = "scope"

// EnforceScopes refuses an operation whose root fields require a scope the caller's credential
// does not carry.
//
// It is the middle factor of the invariant, at the door:
//
//	effective permission = (what the Role allows) ∩ (what the Scopes grant) ∩ (what the Kind allows)
//
// Wired in bootstrap with srv.AroundOperations, so it runs once per operation on both mounts —
// there is one handler, so there is one place this can be missing from, and it is not per-door.
//
// # Why here and not as a field directive
//
// Two reasons, both about the operation rather than the field. An operation that asks for three
// root fields and may only reach two of them should not execute either of them: a credential
// that cannot make the call has not made part of it. And the structural rule below — a mutation
// is a write no matter what its fields say — is a property of the operation type, with no field
// to hang off.
//
// # What it does not do
//
// It does not decide whether the caller may see anything. That is the role's question, asked by
// internal/policy in the resolvers and in the store, and asked always. Passing here means the
// credential is not in the way; it never means the data is allowed.
func EnforceScopes(ctx context.Context, next graphql.OperationHandler) graphql.ResponseHandler {
	oc := graphql.GetOperationContext(ctx)

	// Nothing to judge means something upstream is not what this code assumes. gqlgen sets the
	// operation before the middleware chain runs, so this is unreachable — and it resolves
	// towards a refusal rather than a pass, because an authorization check that cannot see its
	// input has exactly one safe answer.
	if oc == nil || oc.Operation == nil {
		return refuse(policy.ScopeFallback)
	}

	actor := principal.From(ctx)
	for _, required := range requiredScopes(oc.Operation) {
		if !policy.ScopesAllow(actor, required) {
			// The first missing scope, in the order the fields appear. Reporting one rather
			// than all of them is deliberate: the caller fixes them one at a time anyway, and
			// a list invites building the union of every scope a script ever needed into every
			// token it uses.
			return refuse(required)
		}
	}

	return next(ctx)
}

// refuse turns a missing scope into a whole-operation error.
//
// The code is what the GUI and a script branch on; the German sentence is the part that gets
// reworded. Same contract as INTERACTIVE_ONLY — matching prose across a repository boundary
// breaks the first time somebody improves a message.
func refuse(missing policy.Scope) graphql.ResponseHandler {
	return graphql.OneShot(&graphql.Response{
		Errors: gqlerror.List{{
			Message: policy.InsufficientScopeReason(missing),
			Extensions: map[string]any{
				"code": "INSUFFICIENT_SCOPE",
				// The scope that was missing, so a client can act without parsing the sentence.
				// Not a disclosure: the same annotation is on the field in the schema, and
				// introspection is on.
				"scope": missing.String(),
			},
		}},
	})
}

// requiredScopes is every scope the operation needs, in the order its root fields appear.
//
// # The structural rule
//
// A mutation or a subscription requires WRITE, and that is decided from the operation type
// alone — never from what the field walk happened to find. The sibling project derives
// "is this data-changing" from its walk, and its walk does not follow fragments, so
//
//	mutation { ... on Mutation { setPersonRoles(...) } }
//
// yields an empty field list there and falls through to "not a write". Here the walk follows
// fragments *and* the verb is set independently of it, so neither half alone is load-bearing.
//
// # Fail-closed, three ways
//
// A root field with no @scope, one whose annotation does not parse, and a walk that understood
// nothing at all each resolve to policy.ScopeFallback — the most privileged combination there
// is, which no ordinary token holds. TestEveryRootFieldDeclaresAScope means the first case
// never ships; this is what happens between writing the field and CI saying so.
func requiredScopes(op *ast.OperationDefinition) []policy.Scope {
	// Mutation and subscription. Written as "not a query" so that a fourth operation type,
	// should GraphQL ever grow one, is a write here until somebody decides otherwise.
	write := op.Operation != ast.Query

	var (
		required      []policy.Scope
		fields        int
		introspection int
	)

	forEachRootField(op, func(field *ast.Field) {
		// __schema, __type and __typename. Introspection is deliberately on, in production too
		// — it is what makes editor completion and codegen work for colleagues writing their
		// own evaluations — so it carries no scope and is not counted as an unrecognised field
		// below.
		if IsIntrospectionField(field.Name) {
			introspection++
			return
		}
		fields++
		required = append(required, scopeOf(field, write))
	})

	switch {
	case write && len(required) == 0:
		// A mutation that reaches no annotated field still changes something, or it would not
		// be a mutation. This is the structural rule doing its job on its own.
		required = append(required, policy.ScopeFallback)

	case fields == 0 && introspection == 0 && len(op.SelectionSet) > 0:
		// The operation selects something and the walk recognised none of it. That is not a
		// query anybody wrote on purpose; it is this function being wrong about the document
		// shape, which is the failure the sibling project shipped.
		required = append(required, policy.ScopeFallback)
	}

	return required
}

// forEachRootField calls visit for every root field of the operation, fragments included.
//
// The one walk. Two callers ask different questions of the same document — which scopes are
// required, and which fields to record in the access log — and a second walk written for the
// second question is how the two come to disagree about what a root field is.
//
// Following fragments is the whole point. The sibling project's walk does not, so
//
//	mutation { ... on Mutation { setPersonRoles(...) } }
//
// yields an empty field list there. Here it yields setPersonRoles, for the scope check and for
// the log alike — an audit trail that went blank whenever somebody used a fragment would be an
// audit trail with a documented way around it.
func forEachRootField(op *ast.OperationDefinition, visit func(*ast.Field)) {
	// Fragment spreads are followed by name, and a name is only followed once. Validation
	// rejects fragment cycles before this runs, so the set is not what makes this terminate —
	// it is what keeps a deeply cross-referenced document from being walked exponentially.
	visited := map[string]bool{}

	var walk func(ast.SelectionSet)
	walk = func(set ast.SelectionSet) {
		for _, selection := range set {
			switch sel := selection.(type) {
			case *ast.Field:
				visit(sel)

			case *ast.InlineFragment:
				// `... on Query { … }` and `... @include(if: …) { … }`. The fields inside are
				// root fields; the fragment is only punctuation.
				walk(sel.SelectionSet)

			case *ast.FragmentSpread:
				if sel.Definition == nil || visited[sel.Name] {
					continue
				}
				visited[sel.Name] = true
				walk(sel.Definition.SelectionSet)
			}
		}
	}
	walk(op.SelectionSet)
}

// IsIntrospectionField reports whether a field name is one of GraphQL's own — __schema,
// __type, __typename.
func IsIntrospectionField(name string) bool {
	return strings.HasPrefix(name, "__")
}

// scopeOf reads one field's annotation, with the operation's verb taking precedence.
func scopeOf(field *ast.Field, write bool) policy.Scope {
	// Definition is attached by the validator, which has already run. A nil here would mean the
	// field is not in the schema — impossible after validation, and fail-closed if it happens.
	if field.Definition == nil {
		return policy.ScopeFallback
	}

	directive := field.Definition.Directives.ForName(scopeDirectiveName)
	if directive == nil {
		return policy.ScopeFallback
	}

	area, areaOK := argumentOf(directive, "area")
	verb, verbOK := argumentOf(directive, "verb")
	if !areaOK || !verbOK {
		return policy.ScopeFallback
	}

	scope := policy.Scope{Area: policy.ScopeArea(area), Verb: policy.ScopeVerb(verb)}
	if write {
		// The annotation says READ and the operation is a mutation: the operation wins. The
		// schema test rejects that combination, so this is unreachable in a healthy build —
		// but between the two of them, the one that cannot be talked out of it is this line.
		scope.Verb = policy.ScopeVerbWrite
	}
	if !scope.Valid() {
		return policy.ScopeFallback
	}
	return scope
}

// argumentOf reads a directive argument that the schema types as an enum.
//
// Enum arguments arrive as raw values with no variables to resolve — a directive on a field
// *definition* is part of the schema, not of the query, so there is nothing user-supplied here
// to substitute.
func argumentOf(directive *ast.Directive, name string) (string, bool) {
	arg := directive.Arguments.ForName(name)
	if arg == nil || arg.Value == nil {
		return "", false
	}
	return arg.Value.Raw, true
}
