package policy_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
	"github.com/obcode/tallox.go/internal/testdata"
)

// zpaDoors builds one actor per door for the same roles and scopes.
func zpaDoors(scopes []principal.RoleScope, roles ...string) map[string]principal.Actor {
	doors := map[string]principal.Actor{
		"interactive": testdata.Vier.Actor(principal.KindInteractive, roles...),
		"token":       testdata.Vier.Actor(principal.KindToken, roles...),
		"full reach":  testdata.Vier.FullReachActor(roles...),
	}
	for door, a := range doors {
		a.RoleScopes = scopes
		doors[door] = a
	}
	return doors
}

// Who may start a run, read the runs and rebuild the catalogue, through each door.
//
// The leads joined the first two on 2026-11-18 — a new module is entered in the examination
// office's system at their initiative — and only a lead that leads something: an unscoped grant
// is the empty permission. The rebuild stays with administration and the dean's office.
func TestWhoRunsTheZPAImport(t *testing.T) {
	t.Parallel()

	programme := uuid.MustParse("00000000-0000-4000-8000-0000000000a1")
	group := uuid.MustParse("00000000-0000-4000-8000-0000000000b1")
	leadsProgramme := []principal.RoleScope{{Role: string(policy.RoleProgrammeLead), ProgrammeID: programme}}
	leadsGroup := []principal.RoleScope{{Role: string(policy.RoleSubjectGroupLead), SubjectGroupID: group}}
	// A crossed row — a programme lead's grant naming a subject group — grants nothing.
	crossed := []principal.RoleScope{{Role: string(policy.RoleProgrammeLead), SubjectGroupID: group}}

	cases := []struct {
		name                   string
		roles                  []string
		scopes                 []principal.RoleScope
		trigger, read, project bool
	}{
		{"admin", []string{string(policy.RoleAdmin)}, nil, true, true, true},
		{"dean's office", []string{string(policy.RoleDeansOffice)}, nil, true, true, true},
		{"programme lead with a programme", []string{string(policy.RoleProgrammeLead)}, leadsProgramme, true, true, false},
		{"subject group lead with a group", []string{string(policy.RoleSubjectGroupLead)}, leadsGroup, true, true, false},
		{"programme lead without a programme", []string{string(policy.RoleProgrammeLead)}, nil, false, false, false},
		{"subject group lead without a group", []string{string(policy.RoleSubjectGroupLead)}, nil, false, false, false},
		{"programme lead with a crossed scope", []string{string(policy.RoleProgrammeLead)}, crossed, false, false, false},
		{"lecturer", []string{string(policy.RoleLecturer)}, nil, false, false, false},
		{"lecturer with a stray scope", []string{string(policy.RoleLecturer)}, leadsProgramme, false, false, false},
		{"observer", []string{string(policy.RoleObserver)}, nil, false, true, false},
	}

	for _, c := range cases {
		for door, a := range zpaDoors(c.scopes, c.roles...) {
			// A plain token never reaches the import, whatever the role.
			fullReach := door != "token"
			if got, want := policy.MayTriggerZPASync(a), c.trigger && fullReach; got != want {
				t.Errorf("%s via %s: MayTriggerZPASync = %v, want %v", c.name, door, got, want)
			}
			if got, want := policy.MayReadZPAImport(a), c.read && fullReach; got != want {
				t.Errorf("%s via %s: MayReadZPAImport = %v, want %v", c.name, door, got, want)
			}
			if got, want := policy.MayProjectZPACatalogue(a), c.project && fullReach; got != want {
				t.Errorf("%s via %s: MayProjectZPACatalogue = %v, want %v", c.name, door, got, want)
			}
		}
	}
}

// Whoever may start a run may read it: a button whose result one cannot read is half a button.
func TestWhoeverStartsARunMayReadIt(t *testing.T) {
	t.Parallel()

	programme := uuid.MustParse("00000000-0000-4000-8000-0000000000a2")
	scopes := []principal.RoleScope{{Role: string(policy.RoleProgrammeLead), ProgrammeID: programme}}
	for _, role := range policy.AllRoles() {
		for door, a := range zpaDoors(scopes, string(role)) {
			if policy.MayTriggerZPASync(a) && !policy.MayReadZPAImport(a) {
				t.Errorf("%s via %s may start a run and not read it", role, door)
			}
			if policy.MayProjectZPACatalogue(a) && !policy.MayTriggerZPASync(a) {
				t.Errorf("%s via %s may rebuild the catalogue but not sync it", role, door)
			}
		}
	}
}
