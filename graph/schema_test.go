package graph_test

import (
	"testing"

	"github.com/obcode/tallox.go/graph"
	"github.com/obcode/tallox.go/graph/generated"
	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/policy"
)

// TestSchemaAndPolicyAgreeOnRoles is the third side of a triangle.
//
// The list of roles exists in three places that cannot import one another: the GraphQL schema,
// internal/policy, and the CHECK constraint in db/migrations. Two of the three are compared
// here; the database and the policy are compared in internal/store.
//
// The drift is silent in both directions and both are unpleasant. A role in the schema that
// the policy does not know is a value the GUI can offer and nothing will honour. A role the
// policy knows that the schema lacks cannot be marshalled at all, so the field errors — for
// exactly the people who hold that grant, and only for them, which is the kind of bug that
// gets reported as "it works for me".
func TestSchemaAndPolicyAgreeOnRoles(t *testing.T) {
	t.Parallel()

	schema := generated.NewExecutableSchema(generated.Config{Resolvers: &graph.Resolver{}}).Schema()

	enum, ok := schema.Types["Role"]
	if !ok {
		t.Fatal("the schema has no Role enum — has it been renamed?")
	}

	inSchema := map[string]bool{}
	for _, value := range enum.EnumValues {
		inSchema[value.Name] = true
	}

	for _, role := range policy.AllRoles() {
		if !inSchema[string(role)] {
			t.Errorf("internal/policy knows the role %s and the schema does not — the field "+
				"errors for exactly the people who hold that grant", role)
		}
		delete(inSchema, string(role))
	}
	for leftover := range inSchema {
		t.Errorf("the schema offers the role %s, which internal/policy does not know — the "+
			"GUI can offer a grant that nothing will honour", leftover)
	}
}

// strs flattens a list of string-shaped constants. A package-level function because Go has no
// generic function literals.
func strs[T ~string](values []T) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, string(v))
	}
	return out
}

// The three homes of a catalogue vocabulary, and this is the pair the compiler cannot see.
//
// Frequency, CourseType and InstancePartKind are bound to their Go types in gqlgen.yml, which
// removes one copy but not the other: binding makes the *values* Go strings and leaves the enum
// *members* a list in the schema. Nothing checks that the two lists match — a value the schema
// offers and Go does not know arrives as a string nothing can interpret, and a value Go knows
// and the schema omits cannot be asked for at all.
//
// The projection findings are the case that made this test exist. Two were added to the domain,
// to the CHECK constraint and to the report, and forgotten in the schema — so the import page
// would have rendered a finding it had no name for, on the day the source produced one.
func TestSchemaAndDomainAgreeOnTheCatalogueVocabularies(t *testing.T) {
	t.Parallel()

	schema := generated.NewExecutableSchema(generated.Config{Resolvers: &graph.Resolver{}}).Schema()

	known := func(values []string) map[string]bool {
		out := make(map[string]bool, len(values))
		for _, v := range values {
			out[v] = true
		}
		return out
	}

	for _, tc := range []struct {
		enum   string
		values []string
	}{
		{"Frequency", strs(domain.AllFrequencies())},
		{"CourseType", strs(domain.AllCourseTypes())},
		{"InstancePartKind", strs(domain.AllInstancePartKinds())},
		{"DutyStatus", strs(domain.AllDutyStatuses())},
		{"ZpaObjectKind", strs(domain.AllZPAKinds())},
		{"ZpaProjectionFinding", strs(domain.AllProjectionNoteCodes())},
	} {
		t.Run(tc.enum, func(t *testing.T) {
			t.Parallel()

			enum, ok := schema.Types[tc.enum]
			if !ok {
				t.Fatalf("the schema has no %s enum — has it been renamed?", tc.enum)
			}

			inSchema := map[string]bool{}
			for _, value := range enum.EnumValues {
				inSchema[value.Name] = true
			}

			for value := range known(tc.values) {
				if !inSchema[value] {
					t.Errorf("internal/domain knows %s.%s and the schema does not — nothing "+
						"outside can ask for it, and a row carrying it cannot be rendered",
						tc.enum, value)
				}
				delete(inSchema, value)
			}
			for leftover := range inSchema {
				t.Errorf("the schema offers %s.%s, which internal/domain does not know — it "+
					"arrives as a string nothing can interpret", tc.enum, leftover)
			}
		})
	}
}
