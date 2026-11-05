package store_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/store"
	"github.com/obcode/tallox.go/internal/store/storetest"
)

// The programme the examination office renamed keeps its row: the migration changes the code,
// and everything that points at the id — demand, instances, budgets, scopes — comes along.
func TestMigrationRenamesTheProgrammeAndKeepsItsRow(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	ctx := t.Context()

	if err := store.MigrateDownTo(ctx, s.DB, 20261105190000); err != nil {
		t.Fatalf("cannot roll back to before the alias: %v", err)
	}

	var id uuid.UUID
	if err := s.Pool.QueryRow(ctx,
		`INSERT INTO programme (code, title) VALUES ('DA', 'typed by a human') RETURNING id`,
	).Scan(&id); err != nil {
		t.Fatalf("cannot write the programme in the old shape: %v", err)
	}

	if _, err := store.Migrate(ctx, s.DB); err != nil {
		t.Fatalf("cannot migrate up again: %v", err)
	}

	var code, title string
	if err := s.Pool.QueryRow(ctx,
		`SELECT code, title FROM programme WHERE id = $1`, id).Scan(&code, &title); err != nil {
		t.Fatalf("the row is gone: %v", err)
	}
	if code != "DCM" || title != "typed by a human" {
		t.Errorf("after the migration the row reads %q / %q, want DCM and its title", code, title)
	}

	for source, want := range map[string]string{"DA": "DCM", "DC-M": "DCM", "IF": "IF"} {
		var got string
		if err := s.Pool.QueryRow(ctx, `SELECT programme_code_for($1)`, source).Scan(&got); err != nil {
			t.Fatalf("cannot map %s: %v", source, err)
		}
		if got != want {
			t.Errorf("%s maps to %s, want %s", source, got, want)
		}
	}

	// And back: the previous projection knows no aliases, so the name returns with the table.
	if err := store.MigrateDownTo(ctx, s.DB, 20261105190000); err != nil {
		t.Fatalf("cannot roll back: %v", err)
	}
	if err := s.Pool.QueryRow(ctx,
		`SELECT code FROM programme WHERE id = $1`, id).Scan(&code); err != nil {
		t.Fatalf("the row is gone after the rollback: %v", err)
	}
	if code != "DA" {
		t.Errorf("after the rollback the row reads %q, want DA", code)
	}
}

// The alias table holds the column's rule on its target and refuses a loop onto itself.
func TestProgrammeCodeAliasRefusesWhatItCannotMean(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)

	for _, row := range []struct{ name, from, to string }{
		{"a target the programme column could not hold", "X-Y", "X-Y2"},
		{"a code aliased to itself", "PQ", "PQ"},
	} {
		if _, err := s.Pool.Exec(t.Context(),
			`INSERT INTO programme_code_alias (zpa_code, programme_code) VALUES ($1, $2)`,
			row.from, row.to); err == nil {
			t.Errorf("%s was accepted", row.name)
		}
	}
}
