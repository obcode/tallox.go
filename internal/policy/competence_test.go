package policy_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/golden"
	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
	"github.com/obcode/tallox.go/internal/testdata"
)

// TestCompetenceGuardAndFilterAgree is the bridge between the two forms of the competence rule,
// over the full cartesian product — the same test the wish and the assignment rule each have.
func TestCompetenceGuardAndFilterAgree(t *testing.T) {
	t.Parallel()

	owners := []struct {
		name string
		id   uuid.UUID
	}{
		{"my own statement", testdata.Eins.ID()},
		{"somebody else's", testdata.Zwei.ID()},
		// A teacher without an account: nobody's own, and exactly where "actor.ID == owner" would
		// hand an anonymous caller the row, because both sides are uuid.Nil.
		{"a teacher without an account", uuid.Nil},
	}
	programmes := []uuid.UUID{programmeOne, programmeTwo, uuid.Nil}
	groups := []uuid.UUID{groupOne, groupTwo, uuid.Nil}

	checked := 0
	for _, actor := range everyActor() {
		for _, owner := range owners {
			for _, programme := range programmes {
				for _, group := range groups {
					c := policy.Competence{OwnerID: owner.id, ProgrammeID: programme, SubjectGroupID: group}

					guard := policy.CanSeeCompetence(actor, c)
					filter := policy.CompetenceVisibility(actor).Matches(c)
					checked++

					if guard != filter {
						t.Errorf("guard and filter disagree:\n"+
							"  actor:      %s roles=%v scopes=%v\n"+
							"  competence: %s programme=%s group=%s\n"+
							"  CanSeeCompetence=%v  CompetenceVisibility(...).Matches=%v",
							actor, actor.Roles, actor.RoleScopes,
							owner.name, programme, group, guard, filter)
					}
				}
			}
		}
	}

	if checked == 0 {
		t.Fatal("the cartesian product was empty — this test checked nothing")
	}
}

// Through a token, nobody reads anybody else's statement — not even the dean's office. The same
// decision the wish rule makes, and here it never lifts, because there is no publication.
func TestATokenReadsOnlyOwnCompetences(t *testing.T) {
	t.Parallel()

	for _, role := range policy.AllRoles() {
		actor := testdata.Eins.Actor(principal.KindToken, string(role))
		actor.RoleScopes = []principal.RoleScope{
			{Role: string(policy.RoleProgrammeLead), ProgrammeID: programmeOne},
			{Role: string(policy.RoleSubjectGroupLead), SubjectGroupID: groupOne},
		}

		filter := policy.CompetenceVisibility(actor)
		if filter.Scope != policy.CompetenceScopeOwn || filter.OwnerID != actor.ID {
			t.Errorf("%s through a token gets %+v, want own only", role, filter)
		}
	}
}

// A programme lead reads the competences on the modules of their programme, but may not count
// over a subject group: the programme's modules cut across groups, so the count would include
// rows they cannot see one by one.
func TestOnlyTheGroupAxisCountsOverAGroup(t *testing.T) {
	t.Parallel()

	programmeLead := testdata.Eins.Actor(principal.KindInteractive, string(policy.RoleProgrammeLead))
	programmeLead.RoleScopes = []principal.RoleScope{
		{Role: string(policy.RoleProgrammeLead), ProgrammeID: programmeOne},
	}
	groupLead := testdata.Eins.Actor(principal.KindInteractive, string(policy.RoleSubjectGroupLead))
	groupLead.RoleScopes = []principal.RoleScope{
		{Role: string(policy.RoleSubjectGroupLead), SubjectGroupID: groupOne},
	}
	deans := testdata.Eins.Actor(principal.KindInteractive, string(policy.RoleDeansOffice))
	groupLeadByToken := groupLead
	groupLeadByToken.Kind = principal.KindToken

	for _, tc := range []struct {
		name  string
		actor principal.Actor
		group uuid.UUID
		want  bool
	}{
		{"the programme lead", programmeLead, groupOne, false},
		{"the lead of the group", groupLead, groupOne, true},
		{"the lead of another group", groupLead, groupTwo, false},
		{"the lead of the group, through a token", groupLeadByToken, groupOne, false},
		{"the dean's office", deans, groupTwo, true},
		{"nobody, for a module in no group", deans, uuid.Nil, true},
		{"anonymous", principal.Anonymous, groupOne, false},
	} {
		if got := policy.MayReadSubjectGroupCompetences(tc.actor, tc.group); got != tc.want {
			t.Errorf("%s: MayReadSubjectGroupCompetences = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// Writing for a teacher without an account: the lead of the module's group or the dean's office,
// interactively. A lecturer may not, a programme lead may not, a token may not.
func TestWhoMayStateACompetenceForATeacher(t *testing.T) {
	t.Parallel()

	for _, c := range callers() {
		got := policy.MayStateCompetenceForTeacher(c.actor, groupOne)

		roles := policy.RolesOf(c.actor)
		want := c.actor.Interactive() &&
			(roles.Has(policy.RoleDeansOffice) ||
				(roles.Has(policy.RoleSubjectGroupLead) && c.scoped == "group 1"))
		if got != want {
			t.Errorf("%s %s scoped=%q: MayStateCompetenceForTeacher = %v, want %v",
				c.label, c.door, c.scoped, got, want)
		}
	}

	// A module in no subject group has no lead to write for it — only the dean's office.
	lead := testdata.Eins.Actor(principal.KindInteractive, string(policy.RoleSubjectGroupLead))
	lead.RoleScopes = []principal.RoleScope{
		{Role: string(policy.RoleSubjectGroupLead), SubjectGroupID: groupOne},
	}
	if policy.MayStateCompetenceForTeacher(lead, uuid.Nil) {
		t.Error("a subject group lead may write for a module in no subject group")
	}
}

// One's own statement needs nothing but the membership — no role, and either door.
func TestStatingOwnCompetenceNeedsOnlyMembership(t *testing.T) {
	t.Parallel()

	for _, kind := range []principal.Kind{principal.KindInteractive, principal.KindToken} {
		lecturer := testdata.Eins.Actor(kind, string(policy.RoleLecturer))
		if !policy.MayStateOwnCompetence(lecturer, true) {
			t.Errorf("a lecturer (%s) may not state a competence in their own group", kind)
		}
		if policy.MayStateOwnCompetence(lecturer, false) {
			t.Errorf("a lecturer (%s) may state a competence outside their groups", kind)
		}
	}
	if policy.MayStateOwnCompetence(principal.Anonymous, true) {
		t.Error("an anonymous caller may state a competence")
	}
}

// TestCompetenceVisibilityMatrix renders who reads whose competences.
//
// Re-record with: go test ./internal/policy/ -update-golden — and then read the diff.
func TestCompetenceVisibilityMatrix(t *testing.T) {
	t.Parallel()

	golden.Assert(t, "competence_visibility_matrix", renderCompetenceVisibilityMatrix())
}

var competenceVisibilityWidths = []int{20, 13, 17, 6, 9, 9, 9, 9, 16}

func competenceVisibilityRow(cells ...string) string {
	var b strings.Builder
	for i, c := range cells {
		if i == len(cells)-1 {
			b.WriteString(c)
			break
		}
		b.WriteString(pad(c, competenceVisibilityWidths[i]))
	}
	b.WriteString("\n")
	return b.String()
}

func renderCompetenceVisibilityMatrix() string {
	var b strings.Builder

	b.WriteString(competenceMatrixPreamble)

	header := competenceVisibilityRow("Role", "Door", "Responsible for",
		"own", "prog. 1", "group 1", "teacher", "neither", "Filter")
	b.WriteString(header)
	b.WriteString(rule(header))

	previous := ""
	for _, c := range callers() {
		if previous != "" && c.label != previous {
			b.WriteString("\n")
		}
		previous = c.label

		own := policy.Competence{OwnerID: c.actor.ID, ProgrammeID: programmeTwo, SubjectGroupID: groupTwo}
		inProgramme := policy.Competence{
			OwnerID: testdata.Zwei.ID(), ProgrammeID: programmeOne, SubjectGroupID: groupTwo,
		}
		inGroup := policy.Competence{
			OwnerID: testdata.Zwei.ID(), ProgrammeID: programmeTwo, SubjectGroupID: groupOne,
		}
		teacher := policy.Competence{OwnerID: uuid.Nil, ProgrammeID: programmeTwo, SubjectGroupID: groupOne}
		neither := policy.Competence{
			OwnerID: testdata.Zwei.ID(), ProgrammeID: programmeTwo, SubjectGroupID: groupTwo,
		}

		scoped := c.scoped
		if scoped == "" {
			scoped = "—"
		}

		b.WriteString(competenceVisibilityRow(
			c.label,
			c.door,
			scoped,
			answer(policy.CanSeeCompetence(c.actor, own)),
			answer(policy.CanSeeCompetence(c.actor, inProgramme)),
			answer(policy.CanSeeCompetence(c.actor, inGroup)),
			answer(policy.CanSeeCompetence(c.actor, teacher)),
			answer(policy.CanSeeCompetence(c.actor, neither)),
			competenceFilterLabel(policy.CompetenceVisibility(c.actor)),
		))
	}

	return b.String()
}

func competenceFilterLabel(f policy.CompetenceFilter) string {
	switch f.Scope {
	case policy.CompetenceScopeAll:
		return "all"
	case policy.CompetenceScopeOwnOrScoped:
		return "own + scoped"
	case policy.CompetenceScopeOwn:
		return "own only"
	case policy.CompetenceScopeNone:
		return "no access"
	default:
		return fmt.Sprintf("?? %s", f.Scope)
	}
}

const competenceMatrixPreamble = `Competence visibility — who reads who can teach what
===================================================

Generated from internal/policy (TestCompetenceVisibilityMatrix). Do not edit by hand:

    go test ./internal/policy/ -update-golden

The rule
--------

A competence — "I can teach this module" or "I would like to, one day" — is visible if and only if

  · it is the caller's own statement, or
  · the caller is responsible for the module — they lead its home study programme, or the
    subject group it belongs to, or they are the dean's office — and then only in an
    interactive session, never through a Personal Access Token.

The wish rule without its publication clause. "Would like to" is a wish without a semester: a
colleague who can read it knows who else is eyeing a subject, which is the first-come race the
wish rule exists to end. And because a competence belongs to no semester, there is no date on
which it becomes public. Decided 2026-09-26.

Statements about a teacher without an account
---------------------------------------------

The subject group lead enters those, because a lecturer on contract cannot sign in to do it. Such
a row is nobody's own, so it is read only through responsibility — the "teacher" column.

Counting over a subject group
-----------------------------

"Who in this group can teach fewer than three compulsory modules" and "which modules can nobody
teach" count rows. They are answered only to somebody who may read every competence of that
group: its lead and the dean's office. A programme lead reaches the modules of their programme,
which cut across groups, and so may read some of a group's rows but not count them all.

The columns
-----------

  own               The caller's own statement.
  prog. 1 / group 1 A colleague's statement on a module reached through one axis or the other.
  teacher           A statement about a teacher without an account, on a module in group 1.
  neither           A colleague's statement on a module on neither axis.
  Filter            What CompetenceVisibility narrows a query to.

`
