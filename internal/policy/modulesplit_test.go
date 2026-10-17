package policy_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
	"github.com/obcode/tallox.go/internal/testdata"
)

// Either axis is enough: the lead of the module's subject group, or the lead of its home
// programme. Added on a request from the faculty — a subject group lead was offered the split
// editor and refused on saving.
func TestAModuleSplitIsWrittenFromEitherAxis(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		actor     principal.Actor
		group     uuid.UUID
		programme uuid.UUID
		want      bool
	}{
		{"the lead of the module's subject group",
			headOf(testdata.Drei, principal.KindInteractive, groupOne), groupOne, programmeOne, true},
		{"the lead of another subject group",
			headOf(testdata.Drei, principal.KindInteractive, groupTwo), groupOne, programmeOne, false},
		{"a subject group lead, for a module in no group",
			headOf(testdata.Drei, principal.KindInteractive, groupOne), uuid.Nil, programmeOne, false},
		{"the lead of the home programme",
			leadOf(testdata.Vier, principal.KindInteractive, programmeOne), groupOne, programmeOne, true},
		{"the lead of the home programme, for a module in no group",
			leadOf(testdata.Vier, principal.KindInteractive, programmeOne), uuid.Nil, programmeOne, true},
		{"the lead of another programme",
			leadOf(testdata.Vier, principal.KindInteractive, programmeTwo), groupOne, programmeOne, false},
		{"a subject group lead through a token",
			headOf(testdata.Drei, principal.KindToken, groupOne), groupOne, programmeOne, true},
		{"the dean's office",
			testdata.Fuenf.Actor(principal.KindInteractive, string(policy.RoleDeansOffice)),
			uuid.Nil, programmeOne, true},
		{"an administrator",
			testdata.Fuenf.Actor(principal.KindInteractive, string(policy.RoleAdmin)),
			groupOne, programmeOne, false},
		{"a member of the subject group",
			memberOf(testdata.Eins, principal.KindInteractive), groupOne, programmeOne, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := policy.MayWriteModuleSplit(tc.actor, tc.group, tc.programme); got != tc.want {
				t.Errorf("MayWriteModuleSplit = %v, want %v", got, tc.want)
			}
		})
	}
}

// A lead nobody has given a group or programme is told what is missing, not that they may not.
func TestAModuleSplitRefusalNamesTheMissingScope(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		actor principal.Actor
		want  string
	}{
		{"an unscoped subject group lead", headOf(testdata.Drei, principal.KindInteractive),
			policy.SubjectGroupScopeMissingReason},
		{"an unscoped programme lead", leadOf(testdata.Vier, principal.KindInteractive),
			policy.ProgrammeScopeMissingReason},
		{"a scoped lead of something else", headOf(testdata.Drei, principal.KindInteractive, groupTwo),
			policy.ModuleSplitReason},
		{"a lecturer", memberOf(testdata.Eins, principal.KindInteractive), policy.ModuleSplitReason},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := policy.ModuleSplitRefusal(tc.actor); got != tc.want {
				t.Errorf("ModuleSplitRefusal = %q, want %q", got, tc.want)
			}
		})
	}
}
