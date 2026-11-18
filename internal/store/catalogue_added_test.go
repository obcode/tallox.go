package store_test

import (
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/store"
	"github.com/obcode/tallox.go/internal/store/storetest"
)

// cacheNewModule puts a module into the cache that no projection has seen yet: a copy of the
// ordinary one under a new id and with a name of its own.
func cacheNewModule(t *testing.T, s *storetest.Schema, zpaID int64, name string) {
	t.Helper()

	tag, err := s.Pool.Exec(t.Context(),
		`INSERT INTO zpa_object (kind, zpa_id, payload, label)
		 SELECT kind, $1::bigint,
		        payload || jsonb_build_object('module_id', $1::bigint::text, 'module', $2::text), $2
		   FROM zpa_object WHERE kind = 'MODULE' AND zpa_id = $3`,
		zpaID, name, storetest.FixtureModuleOrdinary)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("cannot cache module %d: %v (%d rows)", zpaID, err, tag.RowsAffected())
	}
}

// A projection says which imported modules it brought into the catalogue — the answer to "is the
// module I asked the examination office for here now", which a run's counts cannot give: they
// compare the fetch with the cache.
//
// The third projection is the case found on 2026-11-18. The cache already holds the module, no
// fetch changed anything, and the catalogue still gains it — because the projection that would
// have brought it did not run. Its report has to say so.
func TestAProjectionNamesTheModulesItAdded(t *testing.T) {
	t.Parallel()

	s := seededSchema(t)

	first := project(t, s)
	var imported int
	if err := s.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM module WHERE zpa_module_ref IS NOT NULL`).Scan(&imported); err != nil {
		t.Fatal(err)
	}
	if imported == 0 || first.ModulesAdded != imported {
		t.Errorf("the first projection added %d modules, want all %d", first.ModulesAdded, imported)
	}
	if len(first.AddedModules) != min(imported, 20) {
		t.Errorf("the first projection names %d modules, want %d", len(first.AddedModules), min(imported, 20))
	}

	second := project(t, s)
	if second.ModulesAdded != 0 || len(second.AddedModules) != 0 {
		t.Errorf("a projection over an unchanged cache added %d modules: %v",
			second.ModulesAdded, second.AddedModules)
	}

	cacheNewModule(t, s, 110, "Neues Modul")
	third := project(t, s)
	want := []string{storetest.FixtureProgrammeA + " · Neues Modul"}
	if third.ModulesAdded != 1 || !slices.Equal(third.AddedModules, want) {
		t.Errorf("the projection after a newly cached module added %d: %v, want 1: %v",
			third.ModulesAdded, third.AddedModules, want)
	}

	// The record says the same as the return value.
	latest, err := store.NewCatalogue(s.Pool).LatestProjections(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(latest) != 1 || latest[0].ModulesAdded != 1 || !slices.Equal(latest[0].AddedModules, want) {
		t.Errorf("the stored projection is %+v, want 1 added: %v", latest, want)
	}
}

// A run's projection is found by the run, and a run without one has none.
func TestTheProjectionOfARun(t *testing.T) {
	t.Parallel()

	s := seededSchema(t)
	zpa := store.NewZPA(s.Pool)
	catalogue := store.NewCatalogue(s.Pool)

	run, err := zpa.StartRun(t.Context(), domain.ZPASyncTriggerManual, nil)
	if err != nil {
		t.Fatal(err)
	}
	none, err := catalogue.ProjectionOfRun(t.Context(), run.ID)
	if err != nil || none != nil {
		t.Fatalf("a run nothing projected for has projection %+v (%v)", none, err)
	}

	runID := run.ID
	projected, err := catalogue.Project(t.Context(), &runID)
	if err != nil {
		t.Fatal(err)
	}
	found, err := catalogue.ProjectionOfRun(t.Context(), run.ID)
	if err != nil || found == nil {
		t.Fatalf("the run's projection was not found: %v", err)
	}
	if found.ID != projected.ID || found.ModulesAdded != projected.ModulesAdded ||
		len(found.Notes) != len(projected.Notes) {
		t.Errorf("found %+v, want %+v", *found, projected)
	}

	other, err := catalogue.ProjectionOfRun(t.Context(), uuid.New())
	if err != nil || other != nil {
		t.Errorf("an unknown run has projection %+v (%v)", other, err)
	}
}
