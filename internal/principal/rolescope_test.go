package principal_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/principal"
)

// A scope that names no thing grants nothing, and the zero uuid is never a wildcard.
//
// This is the shape a malformed row takes — a grant whose subject could not be read — and the
// direction to fail in is "no subject" rather than "every subject". The rules rely on it: both
// scoped roles drop such an entry rather than treating it as unrestricted.
func TestAScopeNamingNothingIsRecognisable(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		scope principal.RoleScope
		want  bool
	}{
		{"nothing at all", principal.RoleScope{Role: "PROGRAMME_LEAD"}, true},
		{"the zero value", principal.RoleScope{}, true},
		{"a programme", principal.RoleScope{Role: "PROGRAMME_LEAD", ProgrammeID: uuid.New()}, false},
		{"a subject group", principal.RoleScope{Role: "SUBJECT_GROUP_LEAD", SubjectGroupID: uuid.New()}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.scope.NamesNothing(); got != tc.want {
				t.Errorf("NamesNothing() = %v, want %v", got, tc.want)
			}
		})
	}
}
