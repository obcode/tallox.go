package store_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/store"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// versionBeforeGuessedPartRepair is the migration just before the one under test.
const versionBeforeGuessedPartRepair = 20261016140000

// The reported case, replayed on the schema as it was: a four-hour "je nach Fach" module declared
// from its proposal (OTHER 4), multiplied to two groups, and then given its real split (lecture 2,
// exercise 2), after which the planning sheet added two exercises next to the guessed parts —
// 12 hours where 6 are taught. The repair must leave exactly the 6, and touch nothing it cannot
// prove to be the guess.
func TestMigrationRemovesPartsMadeFromAGuess(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	storetest.SeedZPACatalogue(t, s)
	project(t, s)
	storetest.SeedPerson(t, s, testdata.Eins)

	ctx := t.Context()
	if err := store.MigrateDownTo(ctx, s.DB, versionBeforeGuessedPartRepair); err != nil {
		t.Fatalf("cannot migrate down: %v", err)
	}

	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := s.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatalf("cannot set up %q: %v", query, err)
		}
	}
	read := func(query string, args ...any) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := s.Pool.QueryRow(ctx, query, args...).Scan(&id); err != nil {
			t.Fatalf("cannot read %q: %v", query, err)
		}
		return id
	}

	module := moduleID(t, s, storetest.FixtureModuleDutyDiffers)
	programme := programmeID(t, s, storetest.FixtureProgrammeA)
	exec(`UPDATE module SET course_type = 'DEPENDS_ON_SUBJECT', contact_hours_per_week = 4
	      WHERE id = $1`, module)
	exec(`INSERT INTO module_component (module_id, kind, teaching_hours, position)
	      VALUES ($1, 'LECTURE', 2, 0), ($1, 'EXERCISE', 2, 1)`, module)

	semester := func(code, phase string) uuid.UUID {
		t.Helper()
		return read(`INSERT INTO semester (code, phase) VALUES ($1, $2)
		             ON CONFLICT (code) DO UPDATE SET phase = EXCLUDED.phase RETURNING id`, code, phase)
	}
	open := semester("2027-SS", "DEMAND_PLANNING")
	over := semester("2026-SS", "FINAL")

	instance := func(semester uuid.UUID, track string, parts ...string) uuid.UUID {
		t.Helper()
		id := read(`INSERT INTO course_instance (semester_id, module_id, programme_id, track)
		            VALUES ($1, $2, $3, $4) RETURNING id`, semester, module, programme, track)
		for i := 0; i+1 < len(parts); i += 2 {
			exec(`INSERT INTO instance_part (course_instance_id, kind, teaching_hours, position)
			      VALUES ($1, $2, $3::numeric, $4)`, id, parts[i], parts[i+1], i/2)
		}
		return id
	}

	// The reported instance.
	reported := instance(open, "A", "OTHER", "4", "OTHER", "4", "EXERCISE", "2", "EXERCISE", "2")
	// Declared from the guess and never planned again: the groups were OTHER and nothing else.
	untouched := instance(open, "B", "OTHER", "4", "OTHER", "4")
	// A part somebody added by hand, with hours the guess never had.
	byHand := instance(open, "C", "LECTURE", "2", "EXERCISE", "2", "OTHER", "1")
	// A semester that is over is history.
	history := instance(over, "A", "OTHER", "4")
	// A guessed part somebody is already assigned to.
	assigned := instance(open, "D", "OTHER", "4")
	exec(`INSERT INTO assignment (instance_part_id, person_id)
	      SELECT p.id, (SELECT id FROM person WHERE mail = $2)
	      FROM instance_part p WHERE p.course_instance_id = $1`, assigned, testdata.Eins.Mail)

	if _, err := store.Migrate(ctx, s.DB); err != nil {
		t.Fatalf("cannot migrate up: %v", err)
	}

	parts := func(instance uuid.UUID) (kinds []string, hours float64) {
		t.Helper()
		rows, err := s.Pool.Query(ctx, `SELECT kind, teaching_hours::float8 FROM instance_part
		                                WHERE course_instance_id = $1 ORDER BY position`, instance)
		if err != nil {
			t.Fatalf("cannot read the parts: %v", err)
		}
		defer rows.Close()
		for rows.Next() {
			var kind string
			var h float64
			if err := rows.Scan(&kind, &h); err != nil {
				t.Fatalf("cannot scan a part: %v", err)
			}
			kinds = append(kinds, kind)
			hours += h
		}
		return kinds, hours
	}

	for _, tc := range []struct {
		name      string
		instance  uuid.UUID
		wantKinds []string
		wantHours float64
	}{
		{"the reported instance costs what is taught", reported,
			[]string{"LECTURE", "EXERCISE", "EXERCISE"}, 6},
		{"guessed groups become groups of the real kind", untouched,
			[]string{"LECTURE", "EXERCISE", "EXERCISE"}, 6},
		{"a part added by hand stays", byHand,
			[]string{"LECTURE", "EXERCISE", "OTHER"}, 5},
		{"a semester that is over is not rewritten", history,
			[]string{"OTHER"}, 4},
		{"an assigned part stays", assigned,
			[]string{"OTHER"}, 4},
	} {
		kinds, hours := parts(tc.instance)
		if !equalStrings(kinds, tc.wantKinds) || hours != tc.wantHours {
			t.Errorf("%s: parts %v, %v hours; want %v, %v hours",
				tc.name, kinds, hours, tc.wantKinds, tc.wantHours)
		}
	}
}

func equalStrings(a, b []string) bool {
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
