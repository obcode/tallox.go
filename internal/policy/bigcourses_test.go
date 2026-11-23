package policy_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
	"github.com/obcode/tallox.go/internal/testdata"
)

// The two forms of the rule over every actor and every kind of row.
func TestBigCourseGuardAndFilterAgree(t *testing.T) {
	t.Parallel()

	rows := []uuid.UUID{uuid.Nil, testdata.Eins.ID(), testdata.Zwei.ID()}
	checked := 0
	for _, actor := range everyActor() {
		for _, row := range rows {
			guard := policy.CanSeeBigCourseRow(actor, row)
			filter := policy.BigCourseVisibility(actor).Matches(row)
			checked++
			if guard != filter {
				t.Errorf("guard and filter disagree for %s roles=%v scopes=%v on row=%s: guard=%v, filter=%v",
					actor, actor.Roles, actor.RoleScopes, row, guard, filter)
			}
		}
	}
	if checked == 0 {
		t.Fatal("the cartesian product was empty — this test checked nothing")
	}
}

// Who reads somebody else's row, said as cases.
func TestWhoReadsSomebodyElsesBigCourses(t *testing.T) {
	t.Parallel()

	scoped := func(kind principal.Kind, role policy.Role, scope principal.RoleScope) principal.Actor {
		a := testdata.Eins.Actor(kind, string(role))
		if scope.Role != "" {
			a.RoleScopes = []principal.RoleScope{scope}
		}
		return a
	}
	groupScope := principal.RoleScope{Role: string(policy.RoleSubjectGroupLead), SubjectGroupID: groupOne}
	programmeScope := principal.RoleScope{Role: string(policy.RoleProgrammeLead), ProgrammeID: programmeOne}

	groupLead := scoped(principal.KindInteractive, policy.RoleSubjectGroupLead, groupScope)
	groupLeadPlainToken := scoped(principal.KindToken, policy.RoleSubjectGroupLead, groupScope)
	groupLeadFullToken := groupLeadPlainToken
	groupLeadFullToken.FullReach = true
	programmeLead := scoped(principal.KindInteractive, policy.RoleProgrammeLead, programmeScope)
	programmeLeadFullToken := scoped(principal.KindToken, policy.RoleProgrammeLead, programmeScope)
	programmeLeadFullToken.FullReach = true
	unscopedProgrammeLead := scoped(principal.KindInteractive, policy.RoleProgrammeLead, principal.RoleScope{})
	unscopedGroupLead := scoped(principal.KindInteractive, policy.RoleSubjectGroupLead, principal.RoleScope{})
	deans := testdata.Eins.Actor(principal.KindInteractive, string(policy.RoleDeansOffice))
	deansPlainToken := testdata.Eins.Actor(principal.KindToken, string(policy.RoleDeansOffice))
	colleague := testdata.Eins.Actor(principal.KindInteractive, string(policy.RoleLecturer))
	admin := testdata.Eins.Actor(principal.KindInteractive, string(policy.RoleAdmin))
	partner := scoped(principal.KindInteractive, policy.RolePartnerReader,
		principal.RoleScope{Role: string(policy.RolePartnerReader), ProgrammeID: programmeOne})
	observer := testdata.Eins.Actor(principal.KindInteractive, string(policy.RoleObserver))

	for _, tc := range []struct {
		name  string
		actor principal.Actor
		want  bool
	}{
		{"the dean's office", deans, true},
		{"a subject group lead", groupLead, true},
		{"a subject group lead with a full-reach token", groupLeadFullToken, true},
		{"a programme lead", programmeLead, true},
		{"a programme lead with a full-reach token", programmeLeadFullToken, true},
		{"a subject group lead with a plain token", groupLeadPlainToken, false},
		{"the dean's office with a plain token", deansPlainToken, false},
		{"a programme lead without a programme", unscopedProgrammeLead, false},
		{"a subject group lead without a group", unscopedGroupLead, false},
		{"a colleague", colleague, false},
		{"an administrator", admin, false},
		{"a partner", partner, false},
		{"an observer", observer, false},
		{"nobody", principal.Anonymous, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := policy.CanSeeBigCourseRow(tc.actor, testdata.Zwei.ID()); got != tc.want {
				t.Errorf("CanSeeBigCourseRow = %v, want %v", got, tc.want)
			}
			// Everybody but the anonymous caller reads their own row, through either door.
			wantOwn := tc.actor.Authenticated()
			if got := policy.CanSeeBigCourseRow(tc.actor, tc.actor.ID); got != wantOwn {
				t.Errorf("own row: CanSeeBigCourseRow = %v, want %v", got, wantOwn)
			}
			// A professor without an account is nobody's own row.
			if got := policy.CanSeeBigCourseRow(tc.actor, uuid.Nil); got != tc.want {
				t.Errorf("row without account: CanSeeBigCourseRow = %v, want %v", got, tc.want)
			}
		})
	}
}
