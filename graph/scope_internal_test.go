package graph

import (
	"testing"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/validator/rules"

	"github.com/obcode/tallox.go/graph/generated"
	"github.com/obcode/tallox.go/internal/policy"
)

// parseOperation parses and validates a document against the real schema and returns its only
// operation.
//
// Validation is the part that matters: it is what attaches Definition to every field and
// fragment spread, and requiredScopes reads exactly those. A test that parsed without
// validating would exercise the fail-closed branches by accident and prove nothing about the
// ordinary path — the failure mode of testing an authorization walk against a half-built tree.
func parseOperation(t *testing.T, query string) *ast.OperationDefinition {
	t.Helper()

	schema := generated.NewExecutableSchema(generated.Config{Resolvers: &Resolver{}}).Schema()

	doc, errs := gqlparser.LoadQueryWithRules(schema, query, rules.NewDefaultRules())
	if len(errs) > 0 {
		t.Fatalf("the test query does not parse against the real schema: %v", errs)
	}
	if len(doc.Operations) != 1 {
		t.Fatalf("expected exactly one operation, got %d", len(doc.Operations))
	}
	return doc.Operations[0]
}

func rendered(scopes []policy.Scope) []string {
	out := make([]string, 0, len(scopes))
	for _, s := range scopes {
		out = append(out, s.String())
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestRequiredScopes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{
			name:  "a single field",
			query: `{ buildInfo { version } }`,
			want:  []string{"PUBLIC:READ"},
		},
		{
			name:  "two fields, in the order they appear",
			query: `{ buildInfo { version } me { id } }`,
			want:  []string{"PUBLIC:READ", "PROFILE:READ"},
		},
		{
			name: "nested selections are not root fields",
			// session.person.mail is three levels deep and requires nothing of its own. Only
			// the entry point is scoped; what hangs off it is the resolvers' and the store's
			// business, which is where the role check lives.
			query: `{ session { person { mail name } effectiveRoles } }`,
			want:  []string{"PROFILE:READ"},
		},
		{
			name:  "a mutation",
			query: `mutation { revokePersonalAccessToken(id: "x") { id } }`,
			want:  []string{"TOKENS:WRITE"},
		},
		{
			name:  "an inline fragment is punctuation, not a hiding place",
			query: `{ ... on Query { me { id } } }`,
			want:  []string{"PROFILE:READ"},
		},
		{
			name:  "a fragment spread is followed",
			query: `{ ...f } fragment f on Query { me { id } }`,
			want:  []string{"PROFILE:READ"},
		},
		{
			name:  "a fragment inside a fragment",
			query: `{ ...outer } fragment outer on Query { ...inner } fragment inner on Query { me { id } }`,
			want:  []string{"PROFILE:READ"},
		},
		{
			name:  "the same fragment spread twice is walked once",
			query: `{ ...f ...f } fragment f on Query { me { id } }`,
			want:  []string{"PROFILE:READ"},
		},
		{
			name: "the sibling project's bug: a mutation hidden in an inline fragment",
			// Its root-field walk returns nothing here, so the operation is classified as not
			// data-changing and the write check never runs. Two independent things have to be
			// true for that to be impossible here, and this asserts the first: the walk finds
			// the field.
			query: `mutation { ... on Mutation { setPersonActive(id: "x", active: false) { id } } }`,
			want:  []string{"ADMIN:WRITE"},
		},
		{
			name:  "and the same through a named fragment",
			query: `mutation { ...f } fragment f on Mutation { setPersonActive(id: "x", active: false) { id } }`,
			want:  []string{"ADMIN:WRITE"},
		},
		{
			name:  "introspection alone requires nothing",
			query: `{ __schema { queryType { name } } }`,
			want:  []string{},
		},
		{
			name:  "__typename alone requires nothing",
			query: `{ __typename }`,
			want:  []string{},
		},
		{
			name:  "introspection alongside a real field requires what the field requires",
			query: `{ __typename buildInfo { version } }`,
			want:  []string{"PUBLIC:READ"},
		},
		{
			name:  "an interactive-only field still carries its scope",
			query: `{ myTokens { id } }`,
			want:  []string{"TOKENS:READ"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := rendered(requiredScopes(parseOperation(t, tt.query)))
			if !equal(got, tt.want) {
				t.Errorf("requiredScopes(%s) = %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}

// TestRequiredScopesFailsClosed covers the branches a valid document cannot reach.
//
// The operations here are hand-built rather than parsed, because that is the point: each one
// stands in for the walk being wrong about the document shape. They are unreachable today, and
// the whole argument for writing them is that "unreachable" is a property of the current
// gqlparser and the current schema, neither of which this file controls.
func TestRequiredScopesFailsClosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		op   *ast.OperationDefinition
		want []string
	}{
		{
			name: "a mutation whose fields the walk did not find is still a write",
			// The structural rule on its own, with the walk contributing nothing. This is the
			// half that holds even if the fragment recursion above is one day broken by a
			// refactor.
			op:   &ast.OperationDefinition{Operation: ast.Mutation},
			want: []string{"ADMIN:WRITE"},
		},
		{
			name: "a field the validator did not resolve",
			op: &ast.OperationDefinition{
				Operation:    ast.Query,
				SelectionSet: ast.SelectionSet{&ast.Field{Name: "somethingUnvalidated"}},
			},
			want: []string{"ADMIN:WRITE"},
		},
		{
			name: "a field whose definition carries no @scope",
			op: &ast.OperationDefinition{
				Operation: ast.Query,
				SelectionSet: ast.SelectionSet{&ast.Field{
					Name:       "unannotated",
					Definition: &ast.FieldDefinition{Name: "unannotated"},
				}},
			},
			want: []string{"ADMIN:WRITE"},
		},
		{
			name: "a field whose @scope names an area this build does not know",
			op: &ast.OperationDefinition{
				Operation: ast.Query,
				SelectionSet: ast.SelectionSet{&ast.Field{
					Name: "fromTheFuture",
					Definition: &ast.FieldDefinition{
						Name: "fromTheFuture",
						Directives: ast.DirectiveList{{
							Name: scopeDirectiveName,
							Arguments: ast.ArgumentList{
								{Name: "area", Value: &ast.Value{Raw: "ASSIGNMENTS"}},
								{Name: "verb", Value: &ast.Value{Raw: "READ"}},
							},
						}},
					},
				}},
			},
			want: []string{"ADMIN:WRITE"},
		},
		{
			name: "a selection set the walk understood nothing of",
			op: &ast.OperationDefinition{
				Operation:    ast.Query,
				SelectionSet: ast.SelectionSet{&ast.FragmentSpread{Name: "unresolved"}},
			},
			want: []string{"ADMIN:WRITE"},
		},
		{
			name: "an empty query, which asks for nothing and gets nothing",
			// Not a refusal: validation rejects this long before the middleware, and inventing
			// a requirement for it would mean the fallback fires on a document that selects
			// nothing at all. The refusals above are for documents that select *something*.
			op:   &ast.OperationDefinition{Operation: ast.Query},
			want: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := rendered(requiredScopes(tt.op))
			if !equal(got, tt.want) {
				t.Errorf("requiredScopes = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestTheStructuralRuleOverridesTheAnnotation pins the precedence that makes the sibling
// project's bug unreachable from the other side.
//
// A mutation field annotated READ — which TestScopeVerbMatchesTheOperationType forbids in the
// schema, but which this function must not depend on — still requires WRITE.
func TestTheStructuralRuleOverridesTheAnnotation(t *testing.T) {
	t.Parallel()

	op := &ast.OperationDefinition{
		Operation: ast.Mutation,
		SelectionSet: ast.SelectionSet{&ast.Field{
			Name: "misannotated",
			Definition: &ast.FieldDefinition{
				Name: "misannotated",
				Directives: ast.DirectiveList{{
					Name: scopeDirectiveName,
					Arguments: ast.ArgumentList{
						{Name: "area", Value: &ast.Value{Raw: string(policy.ScopeAreaProfile)}},
						{Name: "verb", Value: &ast.Value{Raw: string(policy.ScopeVerbRead)}},
					},
				}},
			},
		}},
	}

	got := rendered(requiredScopes(op))
	want := []string{"PROFILE:WRITE"}
	if !equal(got, want) {
		t.Errorf("requiredScopes = %v, want %v — the operation type decides the verb, never "+
			"the annotation", got, want)
	}
}
