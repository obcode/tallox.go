package store_test

import (
	"testing"

	"github.com/obcode/tallox.go/internal/store/storetest"
)

// The fixture is only worth anything if it reproduces the source faithfully, including the
// parts that look like mistakes. These assertions are about the fixture rather than about any
// code that reads it — they are what stops a later "tidying" of the fixture from quietly
// removing the case a projection test is named after.
func TestTheSyntheticCatalogueReproducesTheAwkwardCases(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	storetest.SeedZPACatalogue(t, s)
	ctx := t.Context()

	t.Run("a module has no name of its own and borrows one", func(t *testing.T) {
		var name *string
		if err := s.Pool.QueryRow(ctx,
			`SELECT name FROM zpa_module_v WHERE module_id = $1`,
			storetest.FixtureModuleOrdinary).Scan(&name); err != nil {
			t.Fatalf("cannot read the module: %v", err)
		}
		if name == nil || *name != "Ordentliches Modul" {
			t.Errorf("the name borrowed from an association row is %v, want the module's name", name)
		}
	})

	t.Run("a module with no association has no name anywhere", func(t *testing.T) {
		var name *string
		if err := s.Pool.QueryRow(ctx,
			`SELECT name FROM zpa_module_v WHERE module_id = $1`,
			storetest.FixtureModuleWithoutName).Scan(&name); err != nil {
			t.Fatalf("cannot read the module: %v", err)
		}
		if name != nil {
			t.Errorf("the module with no association row has the name %q — the fixture has "+
				"grown an association and the case it exists for is gone", *name)
		}
	})

	t.Run(`the string "None" is an absent home programme, not a programme`, func(t *testing.T) {
		var home *string
		if err := s.Pool.QueryRow(ctx,
			`SELECT home_programme FROM zpa_module_v WHERE module_id = $1`,
			storetest.FixtureModuleWithoutHome).Scan(&home); err != nil {
			t.Fatalf("cannot read the module: %v", err)
		}
		if home != nil {
			t.Errorf(`home_programme is %q; the view's zpa_text() must turn "None" into NULL, `+
				`or a query for modules of programme "None" returns rows that look real`, *home)
		}
	})

	t.Run("an association can point at regulations the source does not return", func(t *testing.T) {
		var present int
		if err := s.Pool.QueryRow(ctx,
			`SELECT count(*) FROM zpa_spo_v WHERE spo_id = $1`,
			storetest.FixtureSpoVanished).Scan(&present); err != nil {
			t.Fatalf("cannot count the regulations: %v", err)
		}
		if present != 0 {
			t.Fatalf("the fixture now returns the vanished regulations, so the case it exists " +
				"for — 665 real rows with no path to a programme — is no longer covered")
		}

		// The embedded copy on the association is the only surviving description of it.
		var version int
		if err := s.Pool.QueryRow(ctx,
			`SELECT spo_version FROM zpa_msba_v WHERE spo_id = $1 LIMIT 1`,
			storetest.FixtureSpoVanished).Scan(&version); err != nil {
			t.Fatalf("cannot read the association: %v", err)
		}
		if version != 2011 {
			t.Errorf("the embedded version is %d, want 2011", version)
		}
	})

	t.Run("one module sits in two catalogue slots of one set of regulations", func(t *testing.T) {
		rows := 0
		if err := s.Pool.QueryRow(ctx,
			`SELECT count(*) FROM zpa_msba_v WHERE module_id = $1 AND spo_id = $2`,
			storetest.FixtureModuleTwoSlots, storetest.FixtureSpoANew).Scan(&rows); err != nil {
			t.Fatalf("cannot count the associations: %v", err)
		}
		if rows != 2 {
			t.Errorf("the module sits in %d slot(s) of one version, want 2 — this is the fold "+
				"module_offering's grain exists to perform", rows)
		}
	})

	t.Run("the two slots agree about duty and disagree about the earliest semester", func(t *testing.T) {
		var duties, semesters int
		err := s.Pool.QueryRow(ctx,
			`SELECT count(DISTINCT b.is_duty), count(DISTINCT m.min_programme_semester)
			   FROM zpa_msba_v m
			   JOIN zpa_basket_v b ON b.basket_id = m.basket_id
			  WHERE m.module_id = $1 AND m.spo_id = $2`,
			storetest.FixtureModuleTwoSlots, storetest.FixtureSpoANew).Scan(&duties, &semesters)
		if err != nil {
			t.Fatalf("cannot read the slots: %v", err)
		}
		if duties != 1 {
			t.Errorf("the two slots disagree about compulsory-or-elective. Measured over the " +
				"whole real catalogue that never happens (0 of 2386 pairs), and the grain of " +
				"module_offering rests on it")
		}
		if semesters != 2 {
			t.Errorf("the two slots agree about the earliest semester, so the fold that has to " +
				"pick one is no longer exercised")
		}
	})

	t.Run("a module is compulsory in one version and elective in another", func(t *testing.T) {
		var duties int
		err := s.Pool.QueryRow(ctx,
			`SELECT count(DISTINCT b.is_duty)
			   FROM zpa_msba_v m
			   JOIN zpa_basket_v b ON b.basket_id = m.basket_id
			   JOIN zpa_spo_v s ON s.spo_id = m.spo_id
			  WHERE m.module_id = $1 AND s.programme = $2`,
			storetest.FixtureModuleDutyDiffers, storetest.FixtureProgrammeA).Scan(&duties)
		if err != nil {
			t.Fatalf("cannot read the associations: %v", err)
		}
		if duties != 2 {
			t.Errorf("asked about the programme without naming a version, this module has %d "+
				"answer(s); the third value of the duty filter exists because it has two", duties)
		}
	})

	t.Run("the retired module is still there, flagged", func(t *testing.T) {
		var active bool
		if err := s.Pool.QueryRow(ctx,
			`SELECT active FROM zpa_module_v WHERE module_id = $1`,
			storetest.FixtureModuleRetired).Scan(&active); err != nil {
			t.Fatalf("cannot read the module: %v", err)
		}
		if active {
			t.Error("the retired module reads as active — the source writes the string 'False' " +
				"and zpa_bool must turn it into one")
		}
	})

	t.Run("a programme is named only by the modules that call it home", func(t *testing.T) {
		var versions int
		if err := s.Pool.QueryRow(ctx,
			`SELECT count(*) FROM zpa_spo_v WHERE programme = $1`,
			storetest.FixtureProgrammeZ).Scan(&versions); err != nil {
			t.Fatalf("cannot count the regulations: %v", err)
		}
		if versions != 0 {
			t.Errorf("programme %s has %d set(s) of regulations. It must have none: the real "+
				"catalogue has exactly one programme in this state, with four active modules "+
				"behind it, and it is the reason the programme list cannot be built from the "+
				"regulations alone", storetest.FixtureProgrammeZ, versions)
		}

		var owned int
		if err := s.Pool.QueryRow(ctx,
			`SELECT count(*) FROM zpa_module_v WHERE home_programme = $1`,
			storetest.FixtureProgrammeZ).Scan(&owned); err != nil {
			t.Fatalf("cannot count the modules: %v", err)
		}
		if owned == 0 {
			t.Errorf("no module calls %s home, so the case is not covered",
				storetest.FixtureProgrammeZ)
		}
	})

	t.Run("a programme has regulations and no module of its own", func(t *testing.T) {
		var versions, owned int
		if err := s.Pool.QueryRow(ctx,
			`SELECT (SELECT count(*) FROM zpa_spo_v WHERE programme = $1),
			        (SELECT count(*) FROM zpa_module_v WHERE home_programme = $1)`,
			storetest.FixtureProgrammeR).Scan(&versions, &owned); err != nil {
			t.Fatalf("cannot read the programme: %v", err)
		}
		if versions == 0 || owned != 0 {
			t.Errorf("programme %s has %d set(s) of regulations and %d module(s); want "+
				"regulations and no modules, which a projection built from the modules alone "+
				"would lose", storetest.FixtureProgrammeR, versions, owned)
		}
	})

	t.Run("a mail address is present, so the test that forbids copying it has work to do",
		func(t *testing.T) {
			var responsible *string
			if err := s.Pool.QueryRow(ctx,
				`SELECT payload ->> 'responsible' FROM zpa_object
				  WHERE kind = 'MODULE' AND zpa_id = $1`,
				storetest.FixtureModuleOrdinary).Scan(&responsible); err != nil {
				t.Fatalf("cannot read the payload: %v", err)
			}
			if responsible == nil || *responsible == "" {
				t.Fatal("no responsible address in the fixture. The projection must never copy " +
					"one into the domain tables, and a test asserting that against a fixture " +
					"with none would pass while proving nothing.")
			}
		})
}
