package domain_test

import (
	"testing"

	"github.com/obcode/tallox.go/internal/domain"
)

// The one rule this file exists for: a screen must not show two registers of the same name.
func TestPlainNameDropsTitles(t *testing.T) {
	t.Parallel()

	cases := []struct {
		what     string
		name     string
		sortName string
		want     string
	}{
		{
			what:     "the written-out name carries titles, the surname-first one does not",
			name:     "Prof. Dr. Vorname Nachname",
			sortName: "Nachname, Vorname",
			want:     "Vorname Nachname",
		},
		{
			what:     "somebody without a title reads the same either way",
			name:     "Vorname Nachname",
			sortName: "Nachname, Vorname",
			want:     "Vorname Nachname",
		},
		{
			what:     "a surname of several words stays whole",
			name:     "Dr. Vorname von Nachname",
			sortName: "von Nachname, Vorname",
			want:     "Vorname von Nachname",
		},
		{
			what:     "a second comma belongs to the given names, not to a third field",
			name:     "Vorname Zweitname Nachname",
			sortName: "Nachname, Vorname, Zweitname",
			want:     "Vorname, Zweitname Nachname",
		},
		{
			what:     "no surname-first spelling: the written-out name is passed through",
			name:     "Deans Office",
			sortName: "",
			want:     "Deans Office",
		},
		{
			what:     "a spelling without a comma is not one this can turn round",
			name:     "Vorname Nachname",
			sortName: "Nachname Vorname",
			want:     "Vorname Nachname",
		},
		{
			what:     "half a spelling is not one either",
			name:     "Vorname Nachname",
			sortName: "Nachname,",
			want:     "Vorname Nachname",
		},
		{
			what:     "surrounding blanks are the source's, not the name's",
			name:     "Prof. Dr. Vorname Nachname",
			sortName: " Nachname ,  Vorname ",
			want:     "Vorname Nachname",
		},
	}

	for _, c := range cases {
		t.Run(c.what, func(t *testing.T) {
			t.Parallel()
			if got := domain.PlainName(c.name, c.sortName); got != c.want {
				t.Errorf("PlainName(%q, %q) = %q, want %q", c.name, c.sortName, got, c.want)
			}
		})
	}
}
