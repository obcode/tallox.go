package policy_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
	"github.com/obcode/tallox.go/internal/testdata"
)

// The two forms of the rule over the full cartesian product, like the wish and the competence
// rule: every role combination and door, own and somebody else's, in no group, in the caller's
// group, in another one and in both.
func TestDesiredHoursGuardAndFilterAgree(t *testing.T) {
	t.Parallel()

	owners := []uuid.UUID{testdata.Eins.ID(), testdata.Zwei.ID()}
	memberships := [][]uuid.UUID{nil, {groupOne}, {groupTwo}, {groupOne, groupTwo}}

	checked := 0
	for _, actor := range everyActor() {
		for _, state := range desiredHoursStates() {
			for _, owner := range owners {
				for _, memberOf := range memberships {
					d := policy.DesiredHours{OwnerID: owner, MemberOf: memberOf}
					guard := policy.CanSeeDesiredHours(actor, state, d)
					filter := policy.DesiredHoursVisibility(actor, state).Matches(d)
					note := policy.CanReadDesiredHoursNote(actor, d)
					noteFilter := policy.DesiredHoursNoteVisibility(actor).Matches(d)
					checked++
					if guard != filter {
						t.Errorf("guard and filter disagree for %s roles=%v scopes=%v on owner=%s "+
							"memberOf=%v state=%+v: CanSeeDesiredHours=%v, Matches=%v",
							actor, actor.Roles, actor.RoleScopes, owner, memberOf, state, guard, filter)
					}
					if note != noteFilter {
						t.Errorf("note guard and filter disagree for %s roles=%v on owner=%s "+
							"memberOf=%v: CanReadDesiredHoursNote=%v, Matches=%v",
							actor, actor.Roles, owner, memberOf, note, noteFilter)
					}
					// The note never reaches further than the range.
					if note && !guard {
						t.Errorf("%s reads the note but not the range of owner=%s", actor, owner)
					}
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("the cartesian product was empty — this test checked nothing")
	}
}

// Who reads somebody else's figure, said as cases rather than derived.
func TestWhoReadsSomebodyElsesDesiredHours(t *testing.T) {
	t.Parallel()

	groupLead := testdata.Eins.Actor(principal.KindInteractive, string(policy.RoleSubjectGroupLead))
	groupLead.RoleScopes = []principal.RoleScope{
		{Role: string(policy.RoleSubjectGroupLead), SubjectGroupID: groupOne},
	}
	groupLeadByToken := groupLead
	groupLeadByToken.Kind = principal.KindToken
	programmeLead := testdata.Eins.Actor(principal.KindInteractive, string(policy.RoleProgrammeLead))
	programmeLead.RoleScopes = []principal.RoleScope{
		{Role: string(policy.RoleProgrammeLead), ProgrammeID: programmeOne},
	}
	unscopedLead := testdata.Eins.Actor(principal.KindInteractive, string(policy.RoleSubjectGroupLead))
	deans := testdata.Eins.Actor(principal.KindInteractive, string(policy.RoleDeansOffice))
	deansByToken := testdata.Eins.Actor(principal.KindToken, string(policy.RoleDeansOffice))
	admin := testdata.Eins.Actor(principal.KindInteractive, string(policy.RoleAdmin))
	colleague := testdata.Eins.Actor(principal.KindInteractive, string(policy.RoleLecturer))

	// Frozen and not published: the freeze is a write rule and changes nobody's reading.
	frozen := policy.SemesterState{Phase: policy.PhaseWishes, DesiredHoursFrozenAt: time.Now()}
	inGroupOne := policy.DesiredHours{OwnerID: testdata.Zwei.ID(), MemberOf: []uuid.UUID{groupOne}}
	inGroupTwo := policy.DesiredHours{OwnerID: testdata.Zwei.ID(), MemberOf: []uuid.UUID{groupTwo}}

	for _, tc := range []struct {
		name  string
		actor principal.Actor
		d     policy.DesiredHours
		want  bool
	}{
		{"the lead of the person's group", groupLead, inGroupOne, true},
		{"the lead of another group", groupLead, inGroupTwo, false},
		{"the lead of the person's group, through a token", groupLeadByToken, inGroupOne, false},
		{"a lead nobody has given a group yet", unscopedLead, inGroupOne, false},
		{"a programme lead", programmeLead, inGroupOne, false},
		{"the dean's office", deans, inGroupTwo, true},
		{"the dean's office, through a token", deansByToken, inGroupTwo, false},
		{"the administration", admin, inGroupOne, false},
		{"a colleague", colleague, inGroupOne, false},
		{"anonymous", principal.Anonymous, inGroupOne, false},
	} {
		if got := policy.CanSeeDesiredHours(tc.actor, frozen, tc.d); got != tc.want {
			t.Errorf("%s: CanSeeDesiredHours = %v, want %v", tc.name, got, tc.want)
		}
	}

	// One's own, through either door, whatever one holds.
	own := policy.DesiredHours{OwnerID: testdata.Eins.ID()}
	for _, actor := range []principal.Actor{colleague, groupLeadByToken, deansByToken} {
		if !policy.CanSeeDesiredHours(actor, policy.SemesterState{}, own) {
			t.Errorf("%s cannot read their own figure", actor)
		}
	}
}

func desiredHoursStates() []policy.SemesterState {
	now := time.Now()
	return []policy.SemesterState{
		{Phase: policy.PhaseWishes},
		{Phase: policy.PhaseWishes, DesiredHoursFrozenAt: now},
		{Phase: policy.PhaseAssignment, DesiredHoursFrozenAt: now, DesiredHoursPublishedAt: now},
		// Wishes published, figures not: the two windows are independent.
		{Phase: policy.PhaseAssignment, WishesPublishedAt: now},
	}
}

// Publishing opens the range to everybody, through either door — and not the note.
func TestPublishedDesiredHoursAreEverybodysExceptTheNote(t *testing.T) {
	t.Parallel()

	now := time.Now()
	published := policy.SemesterState{Phase: policy.PhaseAssignment,
		DesiredHoursFrozenAt: now, DesiredHoursPublishedAt: now}
	somebodyElse := policy.DesiredHours{OwnerID: testdata.Zwei.ID(), MemberOf: []uuid.UUID{groupOne}}

	colleague := testdata.Eins.Actor(principal.KindInteractive, string(policy.RoleLecturer))
	colleagueByToken := testdata.Eins.Actor(principal.KindToken, string(policy.RoleLecturer))
	partner := testdata.Eins.Actor(principal.KindInteractive, string(policy.RolePartnerReader))

	for _, actor := range []principal.Actor{colleague, colleagueByToken} {
		if !policy.CanSeeDesiredHours(actor, published, somebodyElse) {
			t.Errorf("%s (%s) does not read a published figure", actor, actor.Kind)
		}
		if policy.CanReadDesiredHoursNote(actor, somebodyElse) {
			t.Errorf("%s (%s) reads somebody else's note", actor, actor.Kind)
		}
	}
	if policy.CanSeeDesiredHours(partner, published, somebodyElse) {
		t.Error("a partner faculty reads the faculty's published figures")
	}
	if policy.CanSeeDesiredHours(colleague, policy.SemesterState{WishesPublishedAt: now}, somebodyElse) {
		t.Error("publishing the wishes published the figures")
	}
}

// Who writes, in which state. The person until the freeze, the dean's office after it.
func TestTheFreezeHandsTheFigureToTheDeansOffice(t *testing.T) {
	t.Parallel()

	now := time.Now()
	open := policy.SemesterState{Phase: policy.PhaseWishes}
	frozen := policy.SemesterState{Phase: policy.PhaseWishes, DesiredHoursFrozenAt: now}
	published := policy.SemesterState{Phase: policy.PhaseAssignment,
		DesiredHoursFrozenAt: now, DesiredHoursPublishedAt: now}
	finished := policy.SemesterState{Phase: policy.PhaseFinal, DesiredHoursFrozenAt: now}

	lecturer := testdata.Eins.Actor(principal.KindInteractive, string(policy.RoleLecturer))
	lecturerByToken := testdata.Eins.Actor(principal.KindToken, string(policy.RoleLecturer))
	deans := testdata.Fuenf.Actor(principal.KindInteractive,
		string(policy.RoleLecturer), string(policy.RoleDeansOffice))
	deansByToken := deans
	deansByToken.Kind = principal.KindToken
	partner := testdata.Eins.Actor(principal.KindInteractive, string(policy.RolePartnerReader))

	for _, tc := range []struct {
		name          string
		actor         principal.Actor
		state         policy.SemesterState
		states, fixes bool
	}{
		{"a lecturer, open", lecturer, open, true, false},
		{"a lecturer by token, open", lecturerByToken, open, true, false},
		{"a lecturer, frozen", lecturer, frozen, false, false},
		{"a lecturer, published", lecturer, published, false, false},
		{"the dean's office, open", deans, open, true, false},
		{"the dean's office, frozen", deans, frozen, false, true},
		{"the dean's office, published", deans, published, false, true},
		{"the dean's office by token, frozen", deansByToken, frozen, false, false},
		{"the dean's office, finished", deans, finished, false, false},
		{"a partner faculty, open", partner, open, false, false},
	} {
		if got := policy.MayStateDesiredHours(tc.actor, tc.state); got != tc.states {
			t.Errorf("%s: MayStateDesiredHours = %v, want %v", tc.name, got, tc.states)
		}
		if got := policy.MayCorrectDesiredHours(tc.actor, tc.state); got != tc.fixes {
			t.Errorf("%s: MayCorrectDesiredHours = %v, want %v", tc.name, got, tc.fixes)
		}
	}
}
