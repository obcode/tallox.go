package domain_test

import (
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
