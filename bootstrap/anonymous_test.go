package bootstrap_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/obcode/tallox.go/bootstrap"
	"github.com/obcode/tallox.go/graph"
	"github.com/obcode/tallox.go/graph/generated"
	"github.com/obcode/tallox.go/internal/auth"
	"github.com/obcode/tallox.go/internal/buildinfo"
	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/graphqltest"
	"github.com/obcode/tallox.go/internal/mail"
	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/store"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// The token door lets a request without any credential through, as Anonymous, so that buildInfo
// can answer a smoke test that holds no token. That makes Anonymous a caller every root field
// meets, and the scope check is no help against it: policy.ScopesAllow narrows tokens and lets
// every other kind pass. What keeps an anonymous caller out is the role check inside each
// resolver and service — one per field, written by whoever wrote the field.
//
// The tests per area each check a few of those. This one checks all of them, including the
// ones that do not exist yet: every Query and Mutation field in the executable schema is called
// anonymously, and a field added without a role check fails here rather than in production.

// answersAnonymously is the complete list of root fields that may return something to a caller
// with no credential. Adding to it is a decision about what the public sees.
var answersAnonymously = map[string]bool{
	// The build stamp. What the footer and the deploy smoke check read before anybody signs in.
	"Query.buildInfo": true,
	// Who you are — for an anonymous caller: nobody, no roles. Asserted below.
	"Query.session": true,
}

// anonymousRefusals are the codes a field may answer an anonymous caller with. Anything else —
// a validation error, a "not found" — means the arguments were looked at before the caller was,
// and a caller who guesses better arguments might get further.
var anonymousRefusals = map[string]bool{
	"UNAUTHENTICATED":     true,
	"FORBIDDEN":           true,
	"INTERACTIVE_ONLY":    true,
	"FULL_REACH_REQUIRED": true,
}

// TestNoRootFieldAnswersAnAnonymousCaller calls every root field through the token door with no
// credential and asserts that each one is refused, and that nothing but the access log changed.
//
// The arguments are real where the fixture has something real — the instance, its part, the
// group, the semester, a person — so that a refusal is a refusal of the caller and not of an
// id that names nothing. Where the fixture has nothing, a well-formed uuid stands in.
//
// What this cannot see: a by-id lookup that answers null for an id it does not know, before it
// asks who is calling, passes here as long as the fixture has no row behind that id. The
// per-area tests with seeded rows are the ones that close that.
func TestNoRootFieldAnswersAnAnonymousCaller(t *testing.T) {
	t.Parallel()

	f := assignmentHandler(t, grants{testdata.Eins,
		[]string{string(policy.RoleLecturer), string(policy.RoleAdmin)}})
	h := everyServiceHandler(f.schema)

	values := anonymousArgValues{
		"instance":     f.instance,
		"part":         f.lecture,
		"subjectgroup": f.group.String(),
		"group":        f.group.String(),
		"semester":     f.semester,
		"programme":    f.programme,
		"person":       testdata.Eins.ID().String(),
		"mail":         testdata.Eins.Mail,
	}

	schema := generated.NewExecutableSchema(generated.Config{Resolvers: &graph.Resolver{}}).Schema()
	before := tableFingerprints(t, f.schema)

	c := graphqltest.New(h).On(graphqltest.Token).Anonymous()
	for _, root := range []*ast.Definition{schema.Query, schema.Mutation} {
		for _, field := range root.Fields {
			if strings.HasPrefix(field.Name, "__") {
				continue
			}
			where := root.Name + "." + field.Name

			query, vars := anonymousCall(schema, root, field, values)
			resp := c.Do(t, query, vars)

			var data map[string]json.RawMessage
			if len(resp.Data) > 0 {
				if err := json.Unmarshal(resp.Data, &data); err != nil {
					t.Errorf("%s: response data is not an object: %v\n%s", where, err, resp.Body)
					continue
				}
			}
			value := string(data[field.Name])

			if answersAnonymously[where] {
				if resp.Failed() {
					t.Errorf("%s must answer an anonymous caller, got %v", where, resp.Messages())
				}
				continue
			}

			if value != "" && value != "null" {
				t.Errorf("%s answered an anonymous caller: %s", where, value)
			}
			for _, e := range resp.Errors {
				if code, _ := e.Extensions["code"].(string); !anonymousRefusals[code] {
					t.Errorf("%s answered an anonymous caller with %q (%s) — it looked at the "+
						"arguments before it looked at the caller", where, code, e.Message)
				}
			}
		}
	}

	// Nobody, with no roles.
	var out struct {
		Session struct {
			Person         *struct{ Mail string } `json:"person"`
			EffectiveRoles []string               `json:"effectiveRoles"`
		} `json:"session"`
	}
	c.MustQuery(t, `{ session { person { mail } effectiveRoles } }`, nil, &out)
	if out.Session.Person != nil || len(out.Session.EffectiveRoles) != 0 {
		t.Errorf("an anonymous session is somebody: %+v", out.Session)
	}

	// The refusals themselves are recorded; nothing else may have moved.
	after := tableFingerprints(t, f.schema)
	for table, fp := range before {
		if table == "access_log" {
			continue
		}
		if after[table] != fp {
			t.Errorf("table %s changed after anonymous calls only", table)
		}
	}
}

// everyServiceHandler is the handler Serve builds, every service wired, over an already seeded
// schema. A field whose service is missing would fail for that reason and prove nothing.
func everyServiceHandler(s *storetest.Schema) http.Handler {
	pool := s.Pool
	planning := domain.NewSemesterService(store.NewSemesters(pool), nil)
	modules := store.NewModules(pool)
	notifications := store.NewNotifications(pool)
	catalogue := store.NewCatalogue(pool)

	return bootstrap.Handler(bootstrap.Options{
		Build: buildinfo.Info{Version: "test"},
		Auth: auth.Config{
			Mode:   auth.ModeProxy,
			Users:  store.NewDirectory(pool),
			Tokens: store.NewDirectory(pool),
		},
		Tokens:        domain.NewTokenService(store.NewTokens(pool), nil),
		People:        domain.NewPeopleService(store.NewPeople(pool), nil),
		Planning:      planning,
		Import:        domain.NewZPASyncService(store.NewZPA(pool), nil, store.NewZPALock(pool), catalogue),
		Catalogue:     domain.NewCatalogueService(modules, domain.ModuleHandbook{}),
		Demand:        domain.NewDemandService(store.NewDemand(pool, modules), modules, planning),
		SubjectGroups: domain.NewSubjectGroupService(store.NewSubjectGroups(pool)),
		Wishes:        domain.NewWishService(store.NewWishes(pool), planning),
		Staffing:      domain.NewAssignmentService(store.NewAssignments(pool), planning),
		Expertise:     domain.NewCompetenceService(store.NewCompetences(pool)),
		Marks:         domain.NewPlanningMarkService(store.NewPlanningMarks(pool), planning),
		Access:        domain.NewAccessService(store.NewAccess(pool)),
		Notifications: domain.NewNotificationService(notifications),
		Announcements: domain.NewAnnouncementService(notifications,
			mail.NewRenderer("https://tallox.example.org"), nil),
	})
}

// anonymousArgValues maps a fragment of an argument name, lower-cased, to a real fixture value.
type anonymousArgValues map[string]string

// placeholderID is well-formed and names nothing, so that a refusal is never a parse error.
const placeholderID = "00000000-0000-4000-8000-000000000001"

func (v anonymousArgValues) forName(name string) (string, bool) {
	lower := strings.ToLower(name)
	// The longest matching fragment wins, so that "subjectGroupId" is a subject group even though
	// "group" is inside it too.
	best := ""
	for fragment := range v {
		if strings.Contains(lower, fragment) && len(fragment) > len(best) {
			best = fragment
		}
	}
	if best == "" {
		return "", false
	}
	return v[best], true
}

// anonymousCall builds the operation for one root field: every required argument as a variable,
// filled from the fixture where the name says what it is.
func anonymousCall(schema *ast.Schema, root *ast.Definition, field *ast.FieldDefinition,
	values anonymousArgValues,
) (string, map[string]any) {
	op := "query"
	if root == schema.Mutation {
		op = "mutation"
	}

	var decl, use []string
	vars := map[string]any{}
	for _, a := range field.Arguments {
		if !a.Type.NonNull {
			continue
		}
		decl = append(decl, fmt.Sprintf("$%s: %s", a.Name, a.Type.String()))
		use = append(use, fmt.Sprintf("%s: $%s", a.Name, a.Name))
		vars[a.Name] = anonymousValue(schema, a.Name, a.Type, values, 0)
	}

	var b strings.Builder
	b.WriteString(op)
	if len(decl) > 0 {
		b.WriteString("(" + strings.Join(decl, ", ") + ")")
	}
	b.WriteString(" { " + field.Name)
	if len(use) > 0 {
		b.WriteString("(" + strings.Join(use, ", ") + ")")
	}
	named := field.Type
	for named.Elem != nil {
		named = named.Elem
	}
	switch schema.Types[named.NamedType].Kind {
	case ast.Object, ast.Interface, ast.Union:
		b.WriteString(" { __typename }")
	}
	b.WriteString(" }")
	return b.String(), vars
}

func anonymousValue(schema *ast.Schema, name string, t *ast.Type, values anonymousArgValues,
	depth int,
) any {
	if t.Elem != nil {
		return []any{anonymousValue(schema, name, t.Elem, values, depth+1)}
	}
	def := schema.Types[t.NamedType]
	switch def.Kind {
	case ast.Enum:
		return def.EnumValues[0].Name
	case ast.InputObject:
		m := map[string]any{}
		if depth > 8 {
			return m
		}
		for _, f := range def.Fields {
			if f.Type.NonNull {
				m[f.Name] = anonymousValue(schema, f.Name, f.Type, values, depth+1)
			}
		}
		return m
	}

	switch def.Name {
	case "Int":
		return 1
	case "Float":
		return 1.0
	case "Boolean":
		return true
	case "Date":
		return "2028-10-01"
	case "Time":
		return "2028-10-01T00:00:00Z"
	}
	if v, ok := values.forName(name); ok {
		return v
	}
	return placeholderID
}

// tableFingerprints is every table's row count and content hash, to compare before and after.
func tableFingerprints(t *testing.T, s *storetest.Schema) map[string]string {
	t.Helper()

	ctx := t.Context()
	rows, err := s.DB.QueryContext(ctx, `SELECT table_name FROM information_schema.tables
		WHERE table_schema = current_schema() AND table_type = 'BASE TABLE'`)
	if err != nil {
		t.Fatalf("cannot list the tables: %v", err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("cannot read a table name: %v", err)
		}
		tables = append(tables, name)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("cannot list the tables: %v", err)
	}

	out := make(map[string]string, len(tables))
	for _, table := range tables {
		ident := pgx.Identifier{table}.Sanitize()
		var fp string
		if err := s.DB.QueryRowContext(ctx,
			`SELECT count(*)::text || ':' || coalesce(md5(string_agg(t::text, '|' ORDER BY t::text)), '') FROM `+
				ident+` t`).Scan(&fp); err != nil {
			t.Fatalf("cannot fingerprint %s: %v", table, err)
		}
		out[table] = fp
	}
	return out
}
