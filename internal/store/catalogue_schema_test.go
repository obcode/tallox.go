package store_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/domain"
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

	var id uuid.UUID
	err := s.Pool.QueryRow(t.Context(),
		`INSERT INTO semester (code) VALUES ($1) RETURNING id`, code).Scan(&id)
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
