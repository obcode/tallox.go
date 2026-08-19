package domain_test

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/obcode/tallox.go/internal/domain"
)

// TestChangedKeysNamesWhatMoved.
//
// This is what turns "the hash differs" into a sentence somebody can read — "bei vier Modulen
// haben sich SWS und Modulverantwortliche geändert". Top-level only, deliberately: a
// structural diff is a bigger feature than the question being asked.
func TestChangedKeysNamesWhatMoved(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		before string
		after  string
		want   []string
	}{
		{
			name:   "a changed value",
			before: `{"sws":"4","credits":"5"}`,
			after:  `{"sws":"2","credits":"5"}`,
			want:   []string{"sws"},
		},
		{
			name:   "an added key",
			before: `{"sws":"4"}`,
			after:  `{"sws":"4","name":"Beispiel"}`,
			want:   []string{"name"},
		},
		{
			name:   "a removed key",
			before: `{"sws":"4","bonus":""}`,
			after:  `{"sws":"4"}`,
			want:   []string{"bonus"},
		},
		{
			name:   "nothing moved",
			before: `{"sws":"4","credits":"5"}`,
			after:  `{"sws":"4","credits":"5"}`,
			want:   []string{},
		},
		{
			name: "a nested object counts as one key",
			// Coarser than a deep diff, and correct: the association rows nest one level, so
			// "the examination types changed" is the right granularity for a report.
			before: `{"spo":{"id":"1","version":"2025"}}`,
			after:  `{"spo":{"id":"1","version":"2026"}}`,
			want:   []string{"spo"},
		},
		{
			// The regression that the first real run found. Their interface escapes non-ASCII
			// and jsonb stores the decoded character, so a byte comparison of the values calls
			// every field containing an umlaut different. Changing one field of one module
			// reported eight — and every German module description has umlauts in it, so the
			// report would have named nearly every field of nearly every change.
			name:   "an escaped umlaut is not a change",
			before: `{"frequency":"nach Ankündigung","sws":"4"}`,
			after:  `{"frequency":"nach Ank\u00fcndigung","sws":"2"}`,
			want:   []string{"sws"},
		},
		{
			// Change detection must never be the thing that fails a sync — the hash has already
			// established that something differs, and an unreadable payload is still a change
			// worth recording.
			name:   "something that is not an object",
			before: `[1,2,3]`,
			after:  `{"sws":"4"}`,
			want:   []string{""},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := domain.ChangedKeys(json.RawMessage(tc.before), json.RawMessage(tc.after))
			if !slices.Equal(got, tc.want) {
				t.Errorf("ChangedKeys = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestTheKindsAreFetchedSmallestFirst.
//
// Not tidiness: the two large fetches are the ones that can time out, and a run that fails
// after the cheap ones are written is partial in a useful way rather than empty.
func TestTheKindsAreFetchedSmallestFirst(t *testing.T) {
	t.Parallel()

	want := []domain.ZPAKind{
		domain.ZPAKindSPO, domain.ZPAKindBasket, domain.ZPAKindModule, domain.ZPAKindMSBA,
	}
	if got := domain.AllZPAKinds(); !slices.Equal(got, want) {
		t.Errorf("AllZPAKinds = %v, want %v", got, want)
	}
}

func TestParseZPAKindRejectsWhatItDoesNotKnow(t *testing.T) {
	t.Parallel()

	for _, kind := range domain.AllZPAKinds() {
		got, err := domain.ParseZPAKind(string(kind))
		if err != nil || got != kind {
			t.Errorf("ParseZPAKind(%q) = %q, %v", kind, got, err)
		}
	}
	if _, err := domain.ParseZPAKind("MODULE_INFO"); err == nil {
		t.Error("an endpoint path was accepted as a kind — the two are deliberately different " +
			"vocabularies, so that a renamed path cannot reach a released migration")
	}
}
