package domain_test

import (
	"testing"
	"time"

	"github.com/obcode/tallox.go/internal/domain"
)

// Which modules have a page in the public handbook: imported ones the examination office still
// offers, on an installation that knows where the examination office is.
func TestModuleHandbookLinksOnlyWhatTheCatalogueLists(t *testing.T) {
	t.Parallel()

	id := int64(42)
	retired := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	ordinary := domain.Module{Source: domain.ModuleSourceZPA, ZpaID: &id, Active: true}

	inactive := ordinary
	inactive.Active = false
	gone := ordinary
	gone.RetiredAt = &retired
	local := domain.Module{Source: domain.ModuleSourceLocal, Active: true}

	cases := []struct {
		name     string
		handbook domain.ModuleHandbook
		module   domain.Module
		want     string
	}{
		{"an ordinary module", domain.NewModuleHandbook("https://zpa.example.org"), ordinary,
			"https://zpa.example.org/pub/#m=42"},
		{"a trailing slash and blanks in the address", domain.NewModuleHandbook(" https://zpa.example.org/ "),
			ordinary, "https://zpa.example.org/pub/#m=42"},
		{"no examination office configured", domain.NewModuleHandbook(""), ordinary, ""},
		{"the zero value", domain.ModuleHandbook{}, ordinary, ""},
		{"no longer offered", domain.NewModuleHandbook("https://zpa.example.org"), inactive, ""},
		{"dropped by the import", domain.NewModuleHandbook("https://zpa.example.org"), gone, ""},
		{"entered by the faculty", domain.NewModuleHandbook("https://zpa.example.org"), local, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got, ok := c.handbook.URL(c.module)
			if got != c.want || ok != (c.want != "") {
				t.Errorf("URL() = %q, %v; want %q, %v", got, ok, c.want, c.want != "")
			}
		})
	}
}
