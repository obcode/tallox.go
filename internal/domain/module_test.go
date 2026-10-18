package domain_test

import (
	"slices"
	"testing"

	"github.com/obcode/tallox.go/internal/domain"
)

// What one laboratory group is, for adding the next one. A split of 2 + 2 laboratory is one group
// held divided, so a second group is all four hours — not one of the two entries.
func TestPracticalKindOfSumsAKindNamedTwice(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		components []domain.ModuleComponent
		kind       domain.InstancePartKind
		hours      float64
		ok         bool
	}{
		{"lecture and laboratory", []domain.ModuleComponent{
			{Kind: domain.PartKindLecture, TeachingHours: 2}, {Kind: domain.PartKindLab, TeachingHours: 2},
		}, domain.PartKindLab, 2, true},
		{"both named twice", []domain.ModuleComponent{
			{Kind: domain.PartKindLecture, TeachingHours: 2}, {Kind: domain.PartKindLecture, TeachingHours: 2},
			{Kind: domain.PartKindLab, TeachingHours: 2}, {Kind: domain.PartKindLab, TeachingHours: 1.5},
		}, domain.PartKindLab, 3.5, true},
		{"only a lecture", []domain.ModuleComponent{
			{Kind: domain.PartKindLecture, TeachingHours: 2}, {Kind: domain.PartKindLecture, TeachingHours: 2},
		}, "", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kind, hours, ok := domain.PracticalKindOf(tc.components)
			if kind != tc.kind || hours != tc.hours || ok != tc.ok {
				t.Errorf("got %q, %v, %v; want %q, %v, %v", kind, hours, ok, tc.kind, tc.hours, tc.ok)
			}
		})
	}
}

// Baskets folds over one programme's versions and only those: a renamed basket shows both names,
// another programme's slots do not leak in, and a repeat is listed once.
func TestBasketsAreTheUnionOverOneProgrammesVersions(t *testing.T) {
	t.Parallel()

	in := func(code string, baskets ...string) domain.ModuleOffering {
		return domain.ModuleOffering{
			Spo:     domain.Spo{Programme: domain.Programme{Code: code}},
			Baskets: baskets,
		}
	}
	m := domain.Module{Offerings: []domain.ModuleOffering{
		in("DC", "WPF Anwendungen des maschinellen Lernens"),
		in("DC", "WPF Maschinelles Lernen", "WPF Anwendungen des maschinellen Lernens"),
		in("IF", "Wahlpflichtmodule"),
	}}

	want := []string{"WPF Anwendungen des maschinellen Lernens", "WPF Maschinelles Lernen"}
	if got := m.Baskets("DC"); !slices.Equal(got, want) {
		t.Errorf("DC has the baskets %v, want %v", got, want)
	}
	if got := m.Baskets("DA"); got != nil {
		t.Errorf("a programme whose catalogue does not list the module has the baskets %v, want nil", got)
	}
}
