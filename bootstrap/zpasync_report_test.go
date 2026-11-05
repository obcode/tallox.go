package bootstrap_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/bootstrap"
	"github.com/obcode/tallox.go/internal/domain"
)

// A programme code the catalogue cannot store drops every module the programme owns. That has to
// reach the nightly mail, not only the import page.
func TestTheNightlyReportNamesAProgrammeCodeItCouldNotStore(t *testing.T) {
	t.Parallel()

	run := domain.ZPASyncRun{ID: uuid.New()}
	other := uuid.New()
	malformed := []domain.CatalogueProjectionNote{
		{Code: domain.NoteModuleWithoutName, Count: 3},
		{Code: domain.NoteProgrammeCodeMalformed, Count: 2, Sample: []string{"A_B", "x"}},
	}

	cases := []struct {
		name        string
		projections []domain.CatalogueProjection
		want        string
	}{
		{
			name:        "this run's projection found one",
			projections: []domain.CatalogueProjection{{RunID: &run.ID, Notes: malformed}},
			want:        "  HINWEIS: 2 Studiengangskürzel nicht übernommen (A_B, x), ihre Module fehlen im Modulkatalog\n",
		},
		{
			name:        "the latest projection belongs to another run",
			projections: []domain.CatalogueProjection{{RunID: &other, Notes: malformed}},
		},
		{
			name:        "the latest projection was asked for on its own",
			projections: []domain.CatalogueProjection{{Notes: malformed}},
		},
		{
			name:        "only other findings",
			projections: []domain.CatalogueProjection{{RunID: &run.ID, Notes: malformed[:1]}},
		},
		{name: "no projection at all"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var out strings.Builder
			bootstrap.PrintProjectionWarningsForTest(&out, run, c.projections)
			if out.String() != c.want {
				t.Errorf("got %q, want %q", out.String(), c.want)
			}
			// The cron wrapper reads this word as a failed endpoint.
			if strings.Contains(out.String(), "FEHLER") {
				t.Error("the line would make the wrapper report a failed endpoint")
			}
		})
	}
}
