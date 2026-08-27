package store_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/obcode/tallox.go/internal/auth"
	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
	"github.com/obcode/tallox.go/internal/store"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// constraintDef reads one CHECK constraint out of the schema this test owns.
//
// Scoped to current_schema(): every parallel test has a constraint of the same name, and they
// come and go while this query runs.
func constraintDef(t *testing.T, s *storetest.Schema, name string) string {
	t.Helper()

	var definition string
	err := s.Pool.QueryRow(t.Context(),
		`SELECT pg_get_constraintdef(c.oid)
		   FROM pg_constraint c
		   JOIN pg_class rel ON rel.oid = c.conrelid
		   JOIN pg_namespace n ON n.oid = rel.relnamespace
		  WHERE c.conname = $1 AND n.nspname = current_schema()`, name).Scan(&definition)
	if err != nil {
		t.Fatalf("cannot read the constraint %s — has it been renamed? %v", name, err)
	}
	return definition
}

// assertVocabulariesAgree compares a CHECK constraint's list of literals with the list the Go
// package knows, in both directions.
//
// Both directions, because the two failures are different. A value Go knows and the database
// rejects is an insert that fails at runtime; a value the database accepts and Go does not know
// is a row nothing can interpret, which is the quieter and worse of the two.
func assertVocabulariesAgree(t *testing.T, definition string, known []string, parses func(string) bool) {
	t.Helper()

	for _, value := range known {
		if !strings.Contains(definition, "'"+value+"'") {
			t.Errorf("internal/domain knows %q and the constraint does not list it:\n  %s",
				value, definition)
		}
	}

	for _, literal := range strings.Split(definition, "'") {
		if literal == "" || strings.ContainsAny(literal, " (),=:") {
			continue // SQL between the quoted literals
		}
		if !parses(literal) {
			t.Errorf("the database accepts %q, which internal/domain does not know", literal)
		}
	}
}

func TestDatabaseAndDomainAgreeOnFrequencies(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)

	known := make([]string, 0, len(domain.AllFrequencies()))
	for _, f := range domain.AllFrequencies() {
		known = append(known, string(f))
	}
	assertVocabulariesAgree(t, constraintDef(t, s, "module_frequency_is_known"), known,
		func(v string) bool { _, ok := domain.ParseFrequency(v); return ok })
}

func TestDatabaseAndDomainAgreeOnCourseTypes(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)

	known := make([]string, 0, len(domain.AllCourseTypes()))
	for _, c := range domain.AllCourseTypes() {
		known = append(known, string(c))
	}
	assertVocabulariesAgree(t, constraintDef(t, s, "module_course_type_is_known"), known,
		func(v string) bool { _, ok := domain.ParseCourseType(v); return ok })
}

// The part kinds have two constraints rather than one, because module_component and
// instance_part are separate tables holding the same vocabulary. That they agree with each
// other matters as much as that each agrees with Go: an instance's parts are made from the
// module's components, and a kind that only one of them accepts breaks that at the moment of
// creation rather than at review.
func TestDatabaseAndDomainAgreeOnInstancePartKinds(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)

	known := make([]string, 0, len(domain.AllInstancePartKinds()))
	for _, k := range domain.AllInstancePartKinds() {
		known = append(known, string(k))
	}
	parses := func(v string) bool { _, ok := domain.ParseInstancePartKind(v); return ok }

	component := constraintDef(t, s, "module_component_kind_is_known")
	part := constraintDef(t, s, "instance_part_kind_is_known")

	assertVocabulariesAgree(t, component, known, parses)
	assertVocabulariesAgree(t, part, known, parses)

	for _, k := range known {
		inComponent := strings.Contains(component, "'"+k+"'")
		inPart := strings.Contains(part, "'"+k+"'")
		if inComponent != inPart {
			t.Errorf("the kind %q is accepted by one of module_component and instance_part and "+
				"not the other, so a part cannot always be made from its component", k)
		}
	}
}

// seedProgramme puts one programme in and returns its id. Enough of a fixture for the
// constraint tests, which are about what the schema refuses rather than about the catalogue.
func seedProgramme(t *testing.T, s *storetest.Schema, code string) uuid.UUID {
	t.Helper()

	var id uuid.UUID
	err := s.Pool.QueryRow(t.Context(),
		`INSERT INTO programme (code) VALUES ($1) RETURNING id`, code).Scan(&id)
	if err != nil {
		t.Fatalf("cannot seed the programme %s: %v", code, err)
	}
	return id
}

func seedModule(t *testing.T, s *storetest.Schema, home uuid.UUID, name string) uuid.UUID {
	t.Helper()

	var id uuid.UUID
	err := s.Pool.QueryRow(t.Context(),
		`INSERT INTO module (home_programme_id, name) VALUES ($1, $2) RETURNING id`,
		home, name).Scan(&id)
	if err != nil {
		t.Fatalf("cannot seed the module %s: %v", name, err)
	}
	return id
}

func seedSemester(t *testing.T, s *storetest.Schema, code string) uuid.UUID {
	t.Helper()

	// An upsert, like EnsureSemester, because a semester may already be there without this
	// test having put it there: the migration that introduced the planning mark records one.
	// Insisting on an insert would make these tests depend on which semesters the schema
	// happens to have been seeded with.
	var id uuid.UUID
	err := s.Pool.QueryRow(t.Context(),
		`INSERT INTO semester (code) VALUES ($1)
		 ON CONFLICT (code) DO UPDATE SET code = EXCLUDED.code
		 RETURNING id`, code).Scan(&id)
	if err != nil {
		t.Fatalf("cannot seed the semester %s: %v", code, err)
	}
	return id
}

// Each of these constraints was written to stop something specific. The test names the thing.
func TestCatalogueConstraintsRejectTheThingsTheyName(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	ctx := t.Context()

	if_ := seedProgramme(t, s, "IF")
	module := seedModule(t, s, if_, "Algorithmen und Datenstrukturen")
	semester := seedSemester(t, s, "2026-WS")

	cases := []struct {
		name  string
		why   string
		query string
		args  []any
	}{
		{
			name: "a programme code that is really a name",
			why: "the code column must not quietly become a second title field — it is what " +
				"appears in URLs and in colleagues' scripts",
			query: `INSERT INTO programme (code) VALUES ($1)`,
			args:  []any{"Informatik Bachelor"},
		},
		{
			name:  "a lower-case programme code",
			why:   "IF and if would be two programmes, and the second would be invisible",
			query: `INSERT INTO programme (code) VALUES ($1)`,
			args:  []any{"if"},
		},
		{
			name:  "a frequency the domain does not know",
			why:   "an unmapped phrase has to become UNKNOWN plus a report line, never a new value",
			query: `INSERT INTO module (home_programme_id, frequency) VALUES ($1, $2)`,
			args:  []any{if_, "SOMETIMES"},
		},
		{
			name:  "a course type the domain does not know",
			why:   "same reason as the frequency",
			query: `INSERT INTO module (home_programme_id, course_type) VALUES ($1, $2)`,
			args:  []any{if_, "SU MIT ALLEM"},
		},
		{
			name:  "a module with no home programme",
			why:   "every module is planned by exactly one programme; the three the source leaves blank are skipped, not stored",
			query: `INSERT INTO module (home_programme_id, name) VALUES (NULL, $1)`,
			args:  []any{"Waise"},
		},
		{
			name: "a component with no hours",
			why: "the split exists to state hours; a zero row would satisfy the " +
				"is-it-decomposed check while saying nothing",
			query: `INSERT INTO module_component (module_id, kind, teaching_hours) VALUES ($1, 'LECTURE', 0)`,
			args:  []any{module},
		},
		{
			name:  "a track that is a sentence",
			why:   "the cohort is a letter; free text here would end up in the identity of an instance",
			query: `INSERT INTO course_instance (semester_id, module_id, programme_id, track) VALUES ($1, $2, $3, $4)`,
			args:  []any{semester, module, if_, "Zug A, dienstags"},
		},
		{
			name:  "a programme semester outside a degree",
			why:   "a typo in the cohort year would sort the demand list into a place nobody looks",
			query: `INSERT INTO course_instance (semester_id, module_id, programme_id, programme_semester) VALUES ($1, $2, $3, 99)`,
			args:  []any{semester, module, if_},
		},
		{
			name:  "a projection note with a code nobody handles",
			why:   "the report is only worth having if every line means something the GUI can render",
			query: `INSERT INTO zpa_catalogue_projection_note (projection_id, code, count) VALUES (gen_random_uuid(), 'SOMETHING_ODD', 1)`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := s.Pool.Exec(ctx, tc.query, tc.args...); err == nil {
				t.Errorf("the schema accepted %s. It must not: %s", tc.name, tc.why)
			}
		})
	}
}

// The identity of an instance, asserted as a property of the schema rather than of a service.
func TestAnInstanceIsUniquePerSemesterModuleProgrammeAndTrack(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	ctx := t.Context()

	if_ := seedProgramme(t, s, "IF")
	dc := seedProgramme(t, s, "DC")
	module := seedModule(t, s, if_, "Algorithmen und Datenstrukturen")
	ws := seedSemester(t, s, "2026-WS")
	ss := seedSemester(t, s, "2027-SS")

	insert := func(semester, programme uuid.UUID, track string) error {
		_, err := s.Pool.Exec(ctx,
			`INSERT INTO course_instance (semester_id, module_id, programme_id, track)
			 VALUES ($1, $2, $3, $4)`, semester, module, programme, track)
		return err
	}

	// The single-cohort case: an empty track.
	if err := insert(ws, if_, ""); err != nil {
		t.Fatalf("cannot declare the ordinary single-cohort instance: %v", err)
	}
	if err := insert(ws, if_, ""); err == nil {
		t.Error("the same module was declared twice for one semester and programme")
	}

	// Two cohorts, which is the whole reason the track is in the key.
	if err := insert(ws, if_, "A"); err != nil {
		t.Fatalf("cannot declare a second cohort: %v", err)
	}
	if err := insert(ws, if_, "B"); err != nil {
		t.Fatalf("cannot declare a third cohort: %v", err)
	}
	if err := insert(ws, if_, "A"); err == nil {
		t.Error("cohort A was declared twice")
	}

	// The same module in another programme is another programme's demand — and the difference
	// between the two is what the import/export figures are made of.
	if err := insert(ws, dc, ""); err != nil {
		t.Fatalf("cannot declare the same module for a second programme: %v", err)
	}
	// And in another semester it is simply another semester.
	if err := insert(ss, if_, ""); err != nil {
		t.Fatalf("cannot declare the same module in another semester: %v", err)
	}
}

// The security-shaped one. Without the composite foreign key, revoking the role would leave the
// programmes standing, and granting it again would silently restore access somebody had
// deliberately taken away.
func TestAScopeCannotOutliveItsGrant(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	ctx := t.Context()

	storetest.SeedPerson(t, s, testdata.Vier, "PROGRAMME_LEAD")
	if_ := seedProgramme(t, s, "IF")

	if _, err := s.Pool.Exec(ctx,
		`INSERT INTO person_programme_scope (person_id, role, programme_id)
		 VALUES ($1, 'PROGRAMME_LEAD', $2)`, testdata.Vier.ID(), if_); err != nil {
		t.Fatalf("cannot scope the lead to a programme: %v", err)
	}

	// Revoking the role is what /verwaltung/personen does when somebody sets a role list
	// without PROGRAMME_LEAD in it.
	if _, err := s.Pool.Exec(ctx,
		`DELETE FROM person_role WHERE person_id = $1 AND role = 'PROGRAMME_LEAD'`,
		testdata.Vier.ID()); err != nil {
		t.Fatalf("cannot revoke the role: %v", err)
	}

	var left int
	if err := s.Pool.QueryRow(ctx,
		`SELECT count(*) FROM person_programme_scope WHERE person_id = $1`,
		testdata.Vier.ID()).Scan(&left); err != nil {
		t.Fatalf("cannot count the scopes: %v", err)
	}
	if left != 0 {
		t.Errorf("%d programme scope(s) survived the revocation of the role they scope. "+
			"Granting the role again would silently restore them.", left)
	}

	// And a scope cannot be created without the grant in the first place.
	_, err := s.Pool.Exec(ctx,
		`INSERT INTO person_programme_scope (person_id, role, programme_id)
		 VALUES ($1, 'PROGRAMME_LEAD', $2)`, testdata.Zwei.ID(), if_)
	if err == nil {
		t.Error("a programme was scoped to somebody who does not hold the role")
	}

	// Only roles that have a scope dimension may appear here at all.
	_, err = s.Pool.Exec(ctx,
		`INSERT INTO person_programme_scope (person_id, role, programme_id)
		 VALUES ($1, 'LECTURER', $2)`, testdata.Vier.ID(), if_)
	if err == nil {
		t.Error("a role with no programme dimension was scoped to a programme")
	}
}

// Retiring a module or a programme must not take a semester's planning with it. Declaring the
// demand is a decision somebody recorded; the catalogue changing underneath is not a reason to
// erase it.
func TestRetiringACatalogueRowCannotDeleteAPlan(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	ctx := t.Context()

	if_ := seedProgramme(t, s, "IF")
	module := seedModule(t, s, if_, "Algorithmen und Datenstrukturen")
	ws := seedSemester(t, s, "2026-WS")

	var instance uuid.UUID
	err := s.Pool.QueryRow(ctx,
		`INSERT INTO course_instance (semester_id, module_id, programme_id)
		 VALUES ($1, $2, $3) RETURNING id`, ws, module, if_).Scan(&instance)
	if err != nil {
		t.Fatalf("cannot declare the instance: %v", err)
	}

	if _, err := s.Pool.Exec(ctx, `DELETE FROM module WHERE id = $1`, module); err == nil {
		t.Error("deleting a module took a declared instance with it")
	}
	if _, err := s.Pool.Exec(ctx, `DELETE FROM programme WHERE id = $1`, if_); err == nil {
		t.Error("deleting a programme took a declared instance with it")
	}
	if _, err := s.Pool.Exec(ctx, `DELETE FROM semester WHERE id = $1`, ws); err == nil {
		t.Error("deleting a semester took a declared instance with it")
	}

	// Withdrawing the instance itself, on the other hand, takes its parts — they have no
	// meaning without it.
	if _, err := s.Pool.Exec(ctx,
		`INSERT INTO instance_part (course_instance_id, kind, teaching_hours)
		 VALUES ($1, 'LECTURE', 2)`, instance); err != nil {
		t.Fatalf("cannot add a part: %v", err)
	}
	if _, err := s.Pool.Exec(ctx, `DELETE FROM course_instance WHERE id = $1`, instance); err != nil {
		t.Fatalf("cannot withdraw the instance: %v", err)
	}

	var parts int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM instance_part`).Scan(&parts); err != nil {
		t.Fatalf("cannot count the parts: %v", err)
	}
	if parts != 0 {
		t.Errorf("%d part(s) survived the instance they belong to", parts)
	}
}

// An offering is a statement about somebody else's regulations, and it is the one catalogue
// table the projection is allowed to delete from. That is safe precisely because nothing points
// at it — which is a property worth asserting rather than assuming.
func TestNothingReferencesAnOffering(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)

	rows, err := s.Pool.Query(t.Context(),
		`SELECT t.relname, c.conname
		   FROM pg_constraint c
		   JOIN pg_class t ON t.oid = c.conrelid
		   JOIN pg_class r ON r.oid = c.confrelid
		   JOIN pg_namespace n ON n.oid = t.relnamespace
		  WHERE c.contype = 'f'
		    AND n.nspname = current_schema()
		    AND r.relname = 'module_offering'`)
	if err != nil {
		t.Fatalf("cannot read the foreign keys: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var table, constraint string
		if err := rows.Scan(&table, &constraint); err != nil {
			t.Fatalf("cannot read a row: %v", err)
		}
		t.Errorf("%s.%s references module_offering. Offerings are rebuilt from the source on "+
			"every projection and removed when the source drops them; anything pointing at one "+
			"would be deleted with it. An instance carries its programme directly.",
			table, constraint)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("cannot read the foreign keys: %v", err)
	}
}

// The scope is only worth anything if it reaches the rule, and it reaches the rule by being
// read at authentication. Asserted through both doors, because the realistic failure is a
// lookup somebody extends for the browser and forgets on the token path.
func TestProgrammeScopesReachTheActorThroughBothDoors(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	ctx := t.Context()

	storetest.SeedPerson(t, s, testdata.Vier, "LECTURER", "PROGRAMME_LEAD")
	storetest.SeedToken(t, s, testdata.Vier, auth.HashSecret("example-secret"), storetest.TokenOptions{})

	if_ := seedProgramme(t, s, "IF")
	seedProgramme(t, s, "IG")

	if _, err := s.Pool.Exec(ctx,
		`INSERT INTO person_programme_scope (person_id, role, programme_id)
		 VALUES ($1, 'PROGRAMME_LEAD', $2)`, testdata.Vier.ID(), if_); err != nil {
		t.Fatalf("cannot assign the programme: %v", err)
	}

	directory := store.NewDirectory(s.Pool)

	t.Run("browser door", func(t *testing.T) {
		person, err := directory.PersonByMail(ctx, testdata.Vier.Mail)
		if err != nil || person == nil {
			t.Fatalf("cannot resolve the person: %v", err)
		}
		assertScopedTo(t, person.RoleScopes, if_)
	})

	t.Run("token door", func(t *testing.T) {
		token, err := directory.TokenByID(ctx, testdata.Vier.TokenID)
		if err != nil || token == nil {
			t.Fatalf("cannot resolve the token: %v", err)
		}
		assertScopedTo(t, token.Owner.RoleScopes, if_)
	})
}

func assertScopedTo(t *testing.T, scopes []principal.RoleScope, programme uuid.UUID) {
	t.Helper()

	if len(scopes) != 1 {
		t.Fatalf("the actor carries %d programme scope(s), want 1", len(scopes))
	}
	if scopes[0].Role != "PROGRAMME_LEAD" {
		t.Errorf("the scope is for %s", scopes[0].Role)
	}
	if scopes[0].ProgrammeID != programme {
		t.Errorf("the scope names %s, want %s", scopes[0].ProgrammeID, programme)
	}
	if !policy.MayPlanProgramme(
		principal.Actor{Roles: []string{"PROGRAMME_LEAD"}, RoleScopes: scopes}, programme) {
		t.Error("the scope reached the actor but does not permit planning the programme")
	}
}

// A grant that ran out takes its scopes with it. The composite foreign key covers a *revoked*
// grant; nothing in the database covers one that merely expired, so the query has to.
func TestAnExpiredGrantCarriesNoProgrammes(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	ctx := t.Context()

	storetest.SeedPerson(t, s, testdata.Vier, "LECTURER", "PROGRAMME_LEAD")
	if_ := seedProgramme(t, s, "IF")

	if _, err := s.Pool.Exec(ctx,
		`INSERT INTO person_programme_scope (person_id, role, programme_id)
		 VALUES ($1, 'PROGRAMME_LEAD', $2)`, testdata.Vier.ID(), if_); err != nil {
		t.Fatalf("cannot assign the programme: %v", err)
	}
	// granted_at moves with it: the schema refuses a grant that expired before it was made,
	// which is the constraint saying the same thing this test does from the other side.
	if _, err := s.Pool.Exec(ctx,
		`UPDATE person_role
		    SET granted_at = now() - interval '2 hours', expires_at = now() - interval '1 hour'
		  WHERE person_id = $1 AND role = 'PROGRAMME_LEAD'`, testdata.Vier.ID()); err != nil {
		t.Fatalf("cannot expire the grant: %v", err)
	}

	person, err := store.NewDirectory(s.Pool).PersonByMail(ctx, testdata.Vier.Mail)
	if err != nil || person == nil {
		t.Fatalf("cannot resolve the person: %v", err)
	}

	for _, role := range person.Roles {
		if role == "PROGRAMME_LEAD" {
			t.Error("an expired grant is still in the role set")
		}
	}
	if len(person.RoleScopes) != 0 {
		t.Errorf("an expired grant still carries %d programme(s). A grant the database "+
			"considers over must not still take effect, and a scope of one is exactly that.",
			len(person.RoleScopes))
	}
}

// The projection findings, third side of the same triangle as the roles and the phases.
//
// The list lives in internal/domain, in this CHECK constraint and in the GraphQL enum. graph
// compares the first and the third; this compares the first and the second. Two of them drifted
// once already — two findings were added to the domain and to the constraint and forgotten in
// the schema — which is what these three comparisons exist to make impossible in every
// direction.
func TestDatabaseAndDomainAgreeOnProjectionFindings(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)

	known := make([]string, 0, len(domain.AllProjectionNoteCodes()))
	for _, c := range domain.AllProjectionNoteCodes() {
		known = append(known, string(c))
	}
	assertVocabulariesAgree(t,
		constraintDef(t, s, "zpa_catalogue_projection_note_code_is_known"), known,
		func(v string) bool { _, ok := domain.ParseProjectionNoteCode(v); return ok })
}

// The three rules that keep a course the faculty entered apart from one the import owns.
//
// Each of them stops something specific, and each is a property of the table rather than of the
// statements that happen to write it today — which is the point: the statements get rewritten.
func TestLocalModuleConstraintsRejectTheThingsTheyName(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	ctx := t.Context()

	pa := seedProgramme(t, s, "PA")

	local := func(name string, extra string, args ...any) error {
		params := append([]any{pa, name}, args...)
		_, err := s.Pool.Exec(ctx,
			`INSERT INTO module (home_programme_id, name, source, kind`+extra, params...)
		return err
	}

	// A local row with a ZPA reference would be a row the projection could then adopt, silently,
	// on the next run — and this constraint is what the projection tests are allowed to lean on.
	if err := local("Eigene LV", `, zpa_module_ref)
		 VALUES ($1, $2, 'LOCAL', 'MODULE', $3)`, int64(999_001)); err == nil {
		t.Error("a local course was stored with a ZPA reference")
	} else if !strings.Contains(err.Error(), "module_local_has_no_zpa_ref") {
		t.Errorf("the refusal came from somewhere else: %v", err)
	}

	// retired_at means "a successful import stopped mentioning it". About a local row the import
	// says nothing, so the honest way to retire one is active = false.
	if err := local("Zweite eigene LV", `, retired_at)
		 VALUES ($1, $2, 'LOCAL', 'MODULE', now())`); err == nil {
		t.Error("a local course was marked as retired by an import that never saw it")
	} else if !strings.Contains(err.Error(), "module_local_is_never_retired") {
		t.Errorf("the refusal came from somewhere else: %v", err)
	}

	// A local row has no identity but its name, so two clicks on "anlegen" have to be one row.
	if err := local("Dritte eigene LV", `)
		 VALUES ($1, $2, 'LOCAL', 'MODULE')`); err != nil {
		t.Fatalf("cannot store an ordinary local course: %v", err)
	}
	if err := local("dritte eigene lv", `)
		 VALUES ($1, $2, 'LOCAL', 'MODULE')`); err == nil {
		t.Error("the same course was stored twice under a different capitalisation")
	} else if !strings.Contains(err.Error(), "module_local_name_idx") {
		t.Errorf("the refusal came from somewhere else: %v", err)
	}

	// And the index is partial: two imported modules may share a name, and several real ones do.
	for i := range 2 {
		if _, err := s.Pool.Exec(ctx,
			`INSERT INTO module (home_programme_id, name, zpa_module_ref)
			 VALUES ($1, 'Projektstudium', $2)`, pa, int64(999_100+i)); err != nil {
			t.Fatalf("two imported modules of one name were refused: %v", err)
		}
	}
}

func TestDatabaseAndDomainAgreeOnModuleSources(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)

	known := make([]string, 0, len(domain.AllModuleSources()))
	for _, v := range domain.AllModuleSources() {
		known = append(known, string(v))
	}
	assertVocabulariesAgree(t, constraintDef(t, s, "module_source_is_known"), known,
		func(v string) bool { _, ok := domain.ParseModuleSource(v); return ok })
}

func TestDatabaseAndDomainAgreeOnModuleKinds(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)

	known := make([]string, 0, len(domain.AllModuleKinds()))
	for _, v := range domain.AllModuleKinds() {
		known = append(known, string(v))
	}
	assertVocabulariesAgree(t, constraintDef(t, s, "module_kind_is_known"), known,
		func(v string) bool { _, ok := domain.ParseModuleKind(v); return ok })
}

func TestDatabaseAndDomainAgreeOnProgrammeStatuses(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)

	known := make([]string, 0, len(domain.AllProgrammeStatuses()))
	for _, v := range domain.AllProgrammeStatuses() {
		known = append(known, string(v))
	}
	assertVocabulariesAgree(t, constraintDef(t, s, "programme_planning_status_is_known"), known,
		func(v string) bool { _, ok := domain.ParseProgrammeStatus(v); return ok })
}

// The coverage key is one foreign key carrying four invariants, and this is the list of them.
//
// Two of the four are about a row the writer is not looking at — the host's programme and whether
// the host is itself covered — which is exactly why they are in the schema rather than in a Go
// guard: a check written in Go runs where somebody remembered to call it, and the write that
// forgets is the one nobody reviewed.
//
// Each case asserts the refusal by SQLSTATE class rather than by message. The classes differ and
// the difference is load-bearing for internal/store: the programme rule raises 23514 while the
// other three raise 23503, so the Go side cannot tell them apart from the error alone and reads
// the host first in order to say a sentence. If that ever collapses to one class, the specific
// refusals become unreachable and this test is where it shows.
func TestCoverageConstraintsRejectTheThingsTheyName(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	ctx := t.Context()

	de := seedProgramme(t, s, "DE")
	gs := seedProgramme(t, s, "GS")
	id := seedProgramme(t, s, "ID")
	dc := seedProgramme(t, s, "DC")
	module := seedModule(t, s, dc, "IT-Sicherheit und technischer Datenschutz")
	other := seedModule(t, s, dc, "Rechnernetze")
	ws := seedSemester(t, s, "2026-WS")
	ss := seedSemester(t, s, "2027-SS")

	declare := func(semester, mod, programme uuid.UUID, track string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := s.Pool.QueryRow(ctx,
			`INSERT INTO course_instance (semester_id, module_id, programme_id, track)
			 VALUES ($1, $2, $3, $4) RETURNING id`,
			semester, mod, programme, track).Scan(&id); err != nil {
			t.Fatalf("cannot declare an instance: %v", err)
		}
		return id
	}

	// cover points the guest at the host, naming the host's programme the way the writer would.
	cover := func(guest, host, hostProgramme uuid.UUID) error {
		_, err := s.Pool.Exec(ctx,
			`UPDATE course_instance
			    SET covered_by_instance_id = $2, covered_by_programme_id = $3,
			        covered_by_is_covered = false, covered_requested_at = now()
			  WHERE id = $1`, guest, host, hostProgramme)
		return err
	}

	sqlState := func(err error) string {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			return pgErr.Code
		}
		return ""
	}

	host := declare(ws, module, de, "A")
	guest := declare(ws, module, gs, "")

	// The case the whole migration exists for.
	if err := cover(guest, host, de); err != nil {
		t.Fatalf("GS could not be covered by DE, which is the case this exists for: %v", err)
	}

	// Same programme is what serves_sibling_tracks is for. Two mechanisms describing one case is
	// how one of them stops meaning anything.
	sibling := declare(ws, module, de, "B")
	if err := cover(sibling, host, de); err == nil {
		t.Error("an instance was covered by one of its own programme's instances")
	} else if got := sqlState(err); got != "23514" {
		t.Errorf("same-programme coverage raised %s, want 23514 (check_violation) — "+
			"internal/store tells the two refusals apart by more than the class, "+
			"but the class is what tells it a constraint spoke at all", got)
	}

	// The host must be the same offering: same semester, same module.
	otherSemester := declare(ss, module, id, "")
	if err := cover(otherSemester, host, de); err == nil {
		t.Error("an instance was covered by one in another semester")
	} else if got := sqlState(err); got != "23503" {
		t.Errorf("cross-semester coverage raised %s, want 23503", got)
	}

	otherModule := declare(ws, other, id, "")
	if err := cover(otherModule, host, de); err == nil {
		t.Error("an instance was covered by one of another module")
	} else if got := sqlState(err); got != "23503" {
		t.Errorf("cross-module coverage raised %s, want 23503", got)
	}

	// No chains. The guest above is covered, so nothing may hang off it.
	third := declare(ws, module, id, "")
	if err := cover(third, guest, gs); err == nil {
		t.Error("a chain was built: an instance was covered by one that is itself covered")
	} else if got := sqlState(err); got != "23503" {
		t.Errorf("a chain raised %s, want 23503", got)
	}

	// Naming a programme the host is not in. Without this the denormalised column would be a
	// second, editable opinion about which programme holds the teaching.
	//
	// The lie names GS rather than ID: naming the guest's *own* programme is refused one
	// constraint earlier, by the cross-programme CHECK, and would test that rule a second time
	// instead of this one.
	if err := cover(third, host, gs); err == nil {
		t.Error("a guest named a host programme the host is not in")
	} else if got := sqlState(err); got != "23503" {
		t.Errorf("a lie about the host's programme raised %s, want 23503", got)
	}
}

// The direction a Go guard would miss: not "may this become a guest" but "may this *stop* being a
// host". Somebody who has agreed to hold an event for another programme cannot quietly become
// somebody else's guest, and the generated is_covered column is what makes the UPDATE refuse.
func TestAHostCannotBecomeAGuestWhileItCoversSomebody(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	ctx := t.Context()

	de := seedProgramme(t, s, "DE")
	gs := seedProgramme(t, s, "GS")
	id := seedProgramme(t, s, "ID")
	dc := seedProgramme(t, s, "DC")
	module := seedModule(t, s, dc, "IT-Sicherheit und technischer Datenschutz")
	ws := seedSemester(t, s, "2026-WS")

	declare := func(programme uuid.UUID) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := s.Pool.QueryRow(ctx,
			`INSERT INTO course_instance (semester_id, module_id, programme_id, track)
			 VALUES ($1, $2, $3, '') RETURNING id`, ws, module, programme).Scan(&id); err != nil {
			t.Fatalf("cannot declare an instance: %v", err)
		}
		return id
	}

	host := declare(de)
	guest := declare(gs)
	third := declare(id)

	if _, err := s.Pool.Exec(ctx,
		`UPDATE course_instance
		    SET covered_by_instance_id = $2, covered_by_programme_id = $3,
		        covered_by_is_covered = false, covered_requested_at = now()
		  WHERE id = $1`, guest, host, de); err != nil {
		t.Fatalf("cannot cover GS by DE: %v", err)
	}

	// DE now holds the event for GS. It may not become ID's guest.
	if _, err := s.Pool.Exec(ctx,
		`UPDATE course_instance
		    SET covered_by_instance_id = $2, covered_by_programme_id = $3,
		        covered_by_is_covered = false, covered_requested_at = now()
		  WHERE id = $1`, host, third, id); err == nil {
		t.Error("a host that covers somebody became a guest itself, which is a chain " +
			"built from the other end")
	}

	// And it cannot be withdrawn while somebody's demand hangs off it.
	if _, err := s.Pool.Exec(ctx, `DELETE FROM course_instance WHERE id = $1`, host); err == nil {
		t.Error("a host was withdrawn while another programme's demand depended on it")
	}

	// The guest, on the other hand, is nobody's dependency and goes freely.
	if _, err := s.Pool.Exec(ctx, `DELETE FROM course_instance WHERE id = $1`, guest); err != nil {
		t.Errorf("a covered instance could not be withdrawn: %v", err)
	}
}
