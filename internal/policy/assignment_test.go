package policy_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
	"github.com/obcode/tallox.go/internal/testdata"
)

// assignmentsPublished is the state after publishAssignments; unpublished above is its opposite
// for both marks at once, which is what makes the two rules comparable in the same test file.
var assignmentsPublished = policy.SemesterState{
	Phase:                  policy.PhaseAssignment,
	AssignmentsPublishedAt: time.Date(2026, 10, 27, 12, 0, 0, 0, time.Local),
}

// TestAssignmentGuardAndFilterAgree is the bridge between the two forms of the assignment rule.
//
// The same property TestGuardAndFilterAgree asserts for wishes, and it fails the same way if
// somebody adjusts one form and not the other: nothing breaks loudly, the list keeps filtering,
// and a detail view starts answering a question it should not.
//
// The cartesian product is every role combination × both doors × both publication states × every
// phase × held-by-me, held-by-somebody-else and held-by-nobody-with-an-account × both
// responsibility axes including their absence.
func TestAssignmentGuardAndFilterAgree(t *testing.T) {
	t.Parallel()

	assignees := []struct {
		name string
		id   uuid.UUID
	}{
		{"my own part", testdata.Eins.ID()},
		{"somebody else's part", testdata.Zwei.ID()},
		// The ordinary case for a lecturer on contract, not a curiosity: assignable, no account,
		// and therefore no person id. Exactly where "actor.ID == assignment.AssigneeID" would hand
		// an anonymous caller the whole table, because both sides are uuid.Nil.
		{"part held by somebody without an account", uuid.Nil},
	}

	programmes := []uuid.UUID{programmeOne, programmeTwo, uuid.Nil}
	groups := []uuid.UUID{groupOne, groupTwo, uuid.Nil}

	checked := 0
	for _, actor := range everyActor() {
		for _, state := range []policy.SemesterState{unpublished, assignmentsPublished} {
			for _, phase := range policy.AllPhases() {
				state.Phase = phase
				for _, assignee := range assignees {
					for _, programme := range programmes {
						for _, group := range groups {
							a := policy.Assignment{
								AssigneeID:     assignee.id,
								ProgrammeID:    programme,
								SubjectGroupID: group,
							}

							guard := policy.CanSeeAssignment(actor, state, a)
							filter := policy.AssignmentVisibility(actor, state).Matches(a)
							checked++

							if guard != filter {
								t.Errorf("guard and filter disagree:\n"+
									"  actor:       %s roles=%v scopes=%v\n"+
									"  semester:    phase=%s published=%v\n"+
									"  assignment:  %s programme=%s group=%s\n"+
									"  CanSeeAssignment=%v  AssignmentVisibility(...).Matches=%v",
									actor, actor.Roles, actor.RoleScopes,
									state.Phase, state.AssignmentsPublished(),
									assignee.name, programme, group, guard, filter)
							}
						}
					}
				}
			}
		}
	}

	if checked == 0 {
		t.Fatal("the cartesian product was empty — this test checked nothing")
	}
	t.Logf("compared %d combinations", checked)
}

// TestTheTwoPublicationMarksAreIndependent is the reason there are two columns and not one.
//
// The ordinary case is wishes published while the assignment is still being worked on; the case
// nobody expects is the reverse, and both have to work. A single mark would make one of them
// impossible, and it would not be obvious which until somebody needed it.
func TestTheTwoPublicationMarksAreIndependent(t *testing.T) {
	t.Parallel()

	stranger := testdata.Zwei.Actor(principal.KindInteractive, string(policy.RoleLecturer))
	wish := policy.Wish{OwnerID: testdata.Eins.ID(), ProgrammeID: programmeOne, SubjectGroupID: groupOne}
	assignment := policy.Assignment{AssigneeID: testdata.Eins.ID(), ProgrammeID: programmeOne, SubjectGroupID: groupOne}

	wishesOnly := policy.SemesterState{
		Phase:             policy.PhaseAssignment,
		WishesPublishedAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.Local),
	}
	if !policy.CanSeeWish(stranger, wishesOnly, wish) {
		t.Error("published wishes are not readable")
	}
	if policy.CanSeeAssignment(stranger, wishesOnly, assignment) {
		t.Error("publishing the wishes published the assignments too")
	}

	assignmentsOnly := assignmentsPublished
	if policy.CanSeeWish(stranger, assignmentsOnly, wish) {
		t.Error("publishing the assignments published the wishes too")
	}
	if !policy.CanSeeAssignment(stranger, assignmentsOnly, assignment) {
		t.Error("published assignments are not readable")
	}
}

// TestOwnAssignmentIsReadableThroughBothDoors is the half of the rule a token keeps.
//
// What one has been given to teach is one's own timetable, and a script that builds a calendar
// from it is the first thing a colleague will write. The Kind condition narrows what a token sees
// of *other* people, never of the caller.
func TestOwnAssignmentIsReadableThroughBothDoors(t *testing.T) {
	t.Parallel()

	mine := policy.Assignment{AssigneeID: testdata.Eins.ID(), ProgrammeID: programmeOne, SubjectGroupID: groupOne}
	theirs := policy.Assignment{AssigneeID: testdata.Zwei.ID(), ProgrammeID: programmeOne, SubjectGroupID: groupOne}

	for _, kind := range []principal.Kind{principal.KindInteractive, principal.KindToken} {
		actor := testdata.Eins.Actor(kind, string(policy.RoleLecturer))
		if !policy.CanSeeAssignment(actor, unpublished, mine) {
			t.Errorf("%s cannot read their own assignment", kind)
		}
		if policy.CanSeeAssignment(actor, unpublished, theirs) {
			t.Errorf("%s reads somebody else's unpublished assignment", kind)
		}
	}
}

// TestAPlannerReadsAssignmentsEarlyButOnlyInTheBrowser is the decision a token cannot buy back.
//
// Same reasoning as for wishes: a Personal Access Token is long-lived, sits in a script, and
// decouples "who saw this" from any login event. Reading the faculty's unfinished plan out of a
// cron job is precisely the shape of access this rule declines.
func TestAPlannerReadsAssignmentsEarlyButOnlyInTheBrowser(t *testing.T) {
	t.Parallel()

	theirs := policy.Assignment{AssigneeID: testdata.Zwei.ID(), ProgrammeID: programmeOne, SubjectGroupID: groupOne}

	browser := testdata.Vier.Actor(principal.KindInteractive, string(policy.RoleProgrammeLead))
	browser.RoleScopes = []principal.RoleScope{
		{Role: string(policy.RoleProgrammeLead), ProgrammeID: programmeOne},
	}
	if !policy.CanSeeAssignment(browser, unpublished, theirs) {
		t.Error("a programme lead cannot read the unpublished assignments of their own programme")
	}

	token := browser
	token.Kind = principal.KindToken
	if policy.CanSeeAssignment(token, unpublished, theirs) {
		t.Error("a token read an unpublished assignment that is not its owner's")
	}
	if got := policy.AssignmentVisibility(token, unpublished).Scope; got != policy.AssignmentReadScopeOwn {
		t.Errorf("token scope = %q, want %q", got, policy.AssignmentReadScopeOwn)
	}
}

// TestTheTwoAssignmentReachesAreOrthogonal is the shape of the responsibility rule.
//
// A subject group reaches across study programmes and a study programme across subjects, so
// neither implies the other and a row is reached through one of them or through neither.
func TestTheTwoAssignmentReachesAreOrthogonal(t *testing.T) {
	t.Parallel()

	subjectLead := testdata.Drei.Actor(principal.KindInteractive, string(policy.RoleSubjectGroupLead))
	subjectLead.RoleScopes = []principal.RoleScope{
		{Role: string(policy.RoleSubjectGroupLead), SubjectGroupID: groupOne},
	}
	programmeLead := testdata.Vier.Actor(principal.KindInteractive, string(policy.RoleProgrammeLead))
	programmeLead.RoleScopes = []principal.RoleScope{
		{Role: string(policy.RoleProgrammeLead), ProgrammeID: programmeOne},
	}

	// My subject in another programme, and another subject in my programme.
	mySubjectElsewhere := policy.Assignment{AssigneeID: testdata.Zwei.ID(), ProgrammeID: programmeTwo, SubjectGroupID: groupOne}
	otherSubjectHere := policy.Assignment{AssigneeID: testdata.Zwei.ID(), ProgrammeID: programmeOne, SubjectGroupID: groupTwo}
	neither := policy.Assignment{AssigneeID: testdata.Zwei.ID(), ProgrammeID: programmeTwo, SubjectGroupID: groupTwo}

	if !policy.CanSeeAssignment(subjectLead, unpublished, mySubjectElsewhere) {
		t.Error("a subject group lead does not reach their subject in another programme")
	}
	if policy.CanSeeAssignment(subjectLead, unpublished, otherSubjectHere) {
		t.Error("a subject group lead reaches a subject that is not theirs")
	}
	if !policy.CanSeeAssignment(programmeLead, unpublished, otherSubjectHere) {
		t.Error("a programme lead does not reach another subject in their own programme")
	}
	if policy.CanSeeAssignment(programmeLead, unpublished, mySubjectElsewhere) {
		t.Error("a programme lead reaches another programme")
	}
	for _, a := range []principal.Actor{subjectLead, programmeLead} {
		if policy.CanSeeAssignment(a, unpublished, neither) {
			t.Errorf("%s reaches a row on neither axis", a)
		}
	}
}

// TestAdminIsNotAnAssignmentReader repeats the decision every other table in this package makes.
func TestAdminIsNotAnAssignmentReader(t *testing.T) {
	t.Parallel()

	admin := testdata.Sechs.Actor(principal.KindInteractive, string(policy.RoleAdmin))
	theirs := policy.Assignment{AssigneeID: testdata.Zwei.ID(), ProgrammeID: programmeOne, SubjectGroupID: groupOne}
	if policy.CanSeeAssignment(admin, unpublished, theirs) {
		t.Error("ADMIN read an unpublished assignment; running the system is not planning with it")
	}
}

// TestFillingIsOpenUntilTheSemesterIsFinished replaced a test that asserted the opposite, and the
// day between the two is worth recording.
//
// On 2026-08-27 the two phases before ASSIGNMENT were shut, on the argument that filling an
// instance while the wish phase runs is the first-come-first-served race the confidentiality rule
// exists to end. On 2026-08-28 the faculty answered that the wish round is not a phase of the
// faculty at all: it belongs to the subject group, is opened and closed by its lead, and that lead
// is the same person who then fills the instances. A tool that ordered the two for them would be
// ordering work by somebody who can see all of it — and the race is one that lead simply does not
// have to run.
//
// So the only closed cell is the finished semester, and what actually stops entries arriving mid
// assignment is wish_window, which is not a phase.
func TestFillingIsOpenUntilTheSemesterIsFinished(t *testing.T) {
	t.Parallel()

	lead := testdata.Drei.Actor(principal.KindInteractive, string(policy.RoleSubjectGroupLead))
	lead.RoleScopes = []principal.RoleScope{
		{Role: string(policy.RoleSubjectGroupLead), SubjectGroupID: groupOne},
	}

	for _, phase := range policy.AllPhases() {
		want := phase != policy.PhaseFinal
		if got := policy.MayWriteAssignment(lead, groupOne, programmeTwo, phase); got != want {
			t.Errorf("MayWriteAssignment in %s = %v, want %v", phase, got, want)
		}
	}
	if policy.MayWriteAssignment(lead, groupOne, programmeTwo, policy.Phase("KLAUSURTAGUNG")) {
		t.Error("an unknown phase permitted an assignment")
	}
}

// TestEitherAxisIsEnoughToFill is the decision of 2026-08-27 as an assertion.
//
// A union, not an intersection: the subject group lead reaches their subject in every programme,
// the programme lead reaches their programme in every subject, and a module in no subject group is
// reachable by its programme's lead rather than by nobody.
func TestEitherAxisIsEnoughToFill(t *testing.T) {
	t.Parallel()

	subjectLead := testdata.Drei.Actor(principal.KindInteractive, string(policy.RoleSubjectGroupLead))
	subjectLead.RoleScopes = []principal.RoleScope{
		{Role: string(policy.RoleSubjectGroupLead), SubjectGroupID: groupOne},
	}
	programmeLead := testdata.Vier.Actor(principal.KindInteractive, string(policy.RoleProgrammeLead))
	programmeLead.RoleScopes = []principal.RoleScope{
		{Role: string(policy.RoleProgrammeLead), ProgrammeID: programmeOne},
	}

	cases := []struct {
		name      string
		actor     principal.Actor
		group     uuid.UUID
		programme uuid.UUID
		want      bool
	}{
		{"subject lead, own subject, other programme", subjectLead, groupOne, programmeTwo, true},
		{"subject lead, other subject, other programme", subjectLead, groupTwo, programmeTwo, false},
		{"subject lead, unsorted module, other programme", subjectLead, uuid.Nil, programmeTwo, false},
		{"programme lead, other subject, own programme", programmeLead, groupTwo, programmeOne, true},
		{"programme lead, unsorted module, own programme", programmeLead, uuid.Nil, programmeOne, true},
		{"programme lead, own subject would be, other programme", programmeLead, groupOne, programmeTwo, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := policy.MayWriteAssignment(c.actor, c.group, c.programme, policy.PhaseAssignment)
			if got != c.want {
				t.Errorf("MayWriteAssignment = %v, want %v", got, c.want)
			}
		})
	}
}

// TestAnUnsortedModuleIsFilledByItsProgrammeAndNobodyElse is the case that settled the decision.
//
// While the catalogue is being sorted, most modules are in no subject group. With the subject
// groups alone, filling their instances would be the dean's office or nobody.
func TestAnUnsortedModuleIsFilledByItsProgrammeAndNobodyElse(t *testing.T) {
	t.Parallel()

	lecturer := testdata.Eins.Actor(principal.KindInteractive, string(policy.RoleLecturer))
	if policy.MayWriteAssignment(lecturer, uuid.Nil, programmeOne, policy.PhaseAssignment) {
		t.Error("a lecturer filled an unsorted module")
	}

	deans := testdata.Fuenf.Actor(principal.KindInteractive, string(policy.RoleDeansOffice))
	if !policy.MayWriteAssignment(deans, uuid.Nil, programmeOne, policy.PhaseAssignment) {
		t.Error("the dean's office cannot fill an unsorted module")
	}
}

// TestAssignmentRefusalNamesTheHalfThatSaidNo is why there are four sentences and not one.
//
// Each of them sends the reader somewhere different: to an administrator, to the right subject, or
// to whoever advances the phase. A single generic refusal sends all of them to the wrong person.
func TestAssignmentRefusalNamesTheHalfThatSaidNo(t *testing.T) {
	t.Parallel()

	scopedLead := testdata.Drei.Actor(principal.KindInteractive, string(policy.RoleSubjectGroupLead))
	scopedLead.RoleScopes = []principal.RoleScope{
		{Role: string(policy.RoleSubjectGroupLead), SubjectGroupID: groupOne},
	}
	unscopedLead := testdata.Drei.Actor(principal.KindInteractive, string(policy.RoleSubjectGroupLead))
	unscopedProgrammeLead := testdata.Vier.Actor(principal.KindInteractive, string(policy.RoleProgrammeLead))
	lecturer := testdata.Eins.Actor(principal.KindInteractive, string(policy.RoleLecturer))

	cases := []struct {
		name  string
		actor principal.Actor
		phase policy.Phase
		want  string
	}{
		{"responsible but too early", scopedLead, policy.PhaseWishes, policy.AssignmentPhaseClosedReason},
		{"lead without a subject group", unscopedLead, policy.PhaseAssignment, policy.SubjectGroupScopeMissingReason},
		{"lead without a programme", unscopedProgrammeLead, policy.PhaseAssignment, policy.ProgrammeScopeMissingReason},
		{"not responsible at all", lecturer, policy.PhaseAssignment, policy.AssignmentReason},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := policy.AssignmentWriteRefusal(c.actor, groupOne, programmeOne, c.phase)
			if got != c.want {
				t.Errorf("refusal = %q, want %q", got, c.want)
			}
		})
	}
}

// TestAssignmentVisibilityDoesNotDependOnThePhase is the claim the golden matrix makes when it
// leaves the phase out of its columns.
//
// Reading is decided by the publication mark and by responsibility; only writing depends on the
// phase. If that ever stops being true, the matrix silently starts showing one phase's answers for
// all four — so the claim is asserted here rather than trusted.
func TestAssignmentVisibilityDoesNotDependOnThePhase(t *testing.T) {
	t.Parallel()

	a := policy.Assignment{AssigneeID: testdata.Zwei.ID(), ProgrammeID: programmeOne, SubjectGroupID: groupOne}

	checked := 0
	for _, actor := range everyActor() {
		for _, published := range []bool{false, true} {
			var first bool
			for i, phase := range policy.AllPhases() {
				state := policy.SemesterState{Phase: phase}
				if published {
					state.AssignmentsPublishedAt = time.Date(2026, 10, 27, 12, 0, 0, 0, time.Local)
				}
				got := policy.CanSeeAssignment(actor, state, a)
				checked++
				if i == 0 {
					first = got
					continue
				}
				if got != first {
					t.Errorf("visibility changed with the phase: %s published=%v %s=%v, %s=%v",
						actor, published, policy.AllPhases()[0], first, phase, got)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("the cartesian product was empty — this test checked nothing")
	}
}
