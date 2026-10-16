package policy_test

import (
	"testing"

	"github.com/obcode/tallox.go/internal/policy"
)

// TestAllPhasesAreTheProcessInOrder pins the sequence, because Before and After derive from
// it and every write rule that follows will read them. A reordering here silently moves a
// deadline.
func TestAllPhasesAreTheProcessInOrder(t *testing.T) {
	t.Parallel()

	want := []policy.Phase{
		policy.PhaseDemandPlanning,
		policy.PhaseWishes,
		policy.PhaseAssignment,
		policy.PhaseReview,
		policy.PhaseFinal,
	}

	got := policy.AllPhases()
	if len(got) != len(want) {
		t.Fatalf("AllPhases() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("AllPhases() = %v, want %v", got, want)
		}
	}

	for i, p := range got {
		if !p.Valid() {
			t.Errorf("%s is in AllPhases but not Valid", p)
		}
		for j, q := range got {
			if before, want := p.Before(q), i < j; before != want {
				t.Errorf("%s.Before(%s) = %v, want %v", p, q, before, want)
			}
			if after, want := p.After(q), i > j; after != want {
				t.Errorf("%s.After(%s) = %v, want %v", p, q, after, want)
			}
		}
	}
}

// TestAnUnknownPhaseIsNeitherBeforeNorAfter covers the row that says something this code
// cannot act on.
//
// The tempting implementations both fail dangerously: a rank of -1 makes an unknown phase
// earlier than everything (so every "has demand planning passed?" answers no), and a
// default of DEMAND_PLANNING makes it the most permissive phase for writes. Refusing to order
// it at all leaves the caller with a decision to make, which is the correct outcome for a
// value that should not exist.
func TestAnUnknownPhaseIsNeitherBeforeNorAfter(t *testing.T) {
	t.Parallel()

	const bogus = policy.Phase("PLANUNG")

	if bogus.Valid() {
		t.Error("an unknown phase reports as valid")
	}
	if _, ok := policy.ParsePhase("bedarfsplanung"); ok {
		t.Error("ParsePhase matched case-insensitively — a lower-cased row would then act " +
			"as a phase")
	}

	for _, known := range policy.AllPhases() {
		if bogus.Before(known) || bogus.After(known) {
			t.Errorf("unknown phase orders against %s", known)
		}
		if known.Before(bogus) || known.After(bogus) {
			t.Errorf("%s orders against an unknown phase", known)
		}
	}
}
