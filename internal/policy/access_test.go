package policy_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
)

// TestOnlyAnInteractiveAdminReadsTheAccessLog.
//
// The two cases worth reading are the dean's office and ADMIN-through-a-token. The first is
// where this rule deliberately differs from MayReadZPAImport, which is the union of the two
// roles; the second is what makes a leaked token unable to enumerate who worked when.
func TestOnlyAnInteractiveAdminReadsTheAccessLog(t *testing.T) {
	t.Parallel()

	id := uuid.New()

	for _, tc := range []struct {
		name  string
		actor principal.Actor
		want  bool
	}{
		{
			name:  "admin in a browser",
			actor: principal.Actor{ID: id, Roles: []string{"ADMIN"}, Kind: principal.KindInteractive},
			want:  true,
		},
		{
			name:  "admin through a token",
			actor: principal.Actor{ID: id, Roles: []string{"ADMIN"}, Kind: principal.KindToken},
			want:  false,
		},
		{
			// Unlike the ZPA import, which is the union of ADMIN and DEANS_OFFICE. There the
			// need to look arises inside planning; nothing here does.
			name:  "the dean's office",
			actor: principal.Actor{ID: id, Roles: []string{"DEANS_OFFICE"}, Kind: principal.KindInteractive},
			want:  false,
		},
		{
			name: "every other role, in a browser",
			actor: principal.Actor{ID: id, Kind: principal.KindInteractive, Roles: []string{
				"LECTURER", "SUBJECT_GROUP_LEAD", "PROGRAMME_LEAD",
			}},
			want: false,
		},
		{
			name:  "anonymous",
			actor: principal.Anonymous,
			want:  false,
		},
		{
			// An actor narrowed away from ADMIN is not an administrator for this rule either.
			// Roles is the effective set, which is what makes that automatic.
			name: "an admin who narrowed themselves to LECTURER",
			actor: policy.Narrow(
				principal.Actor{ID: id, Roles: []string{"ADMIN", "LECTURER"}, Kind: principal.KindInteractive},
				[]policy.Role{policy.RoleLecturer}),
			want: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := policy.MayReadAccessLog(tc.actor); got != tc.want {
				t.Errorf("MayReadAccessLog = %v, want %v", got, tc.want)
			}
		})
	}
}
