package policy_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
	"github.com/obcode/tallox.go/internal/testdata"
)

// published and unpublished are the two states of the confidentiality window.
var (
	unpublished = policy.SemesterState{Phase: policy.PhaseWishes}
	published   = policy.SemesterState{
		Phase:             policy.PhaseWishes,
		WishesPublishedAt: time.Date(2026, 10, 27, 12, 0, 0, 0, time.Local),
	}
)

// roleSubsets returns every combination of roles, including none at all.
//
// The power set rather than one role at a time, because holding two roles is the normal case
// here — the same colleague leads a subject group and a study programme — and a rule that reads "the
// role" instead of "the roles" is wrong for exactly those people. 32 combinations is small
// enough to enumerate, so the property test below is a proof over the domain rather than a
// sample of it.
func roleSubsets() [][]string {
	all := policy.AllRoles()
	subsets := make([][]string, 0, 1<<len(all))

	for mask := 0; mask < 1<<len(all); mask++ {
		var roles []string
		for i, r := range all {
			if mask&(1<<i) != 0 {
				roles = append(roles, string(r))
			}
		}
		subsets = append(subsets, roles)
	}
	return subsets
}

// everyActor returns every caller the rules can see: the anonymous one, plus each combination
// of roles through each door.
func everyActor() []principal.Actor {
	actors := []principal.Actor{principal.Anonymous}
	for _, roles := range roleSubsets() {
		for _, kind := range []principal.Kind{principal.KindInteractive, principal.KindToken} {
			plain := testdata.Eins.Actor(kind, roles...)
			actors = append(actors, plain)

			// The same roles, with a subject assigned to each of the two that has one. Both
			// scopes on every actor rather than one variant per role: a scope is only read for a
			// role that is actually held, so carrying the pair covers the combinations without
			// doubling the loop — and the combination is where the union of the two reaches is
			// exercised.
			scoped := plain
			scoped.RoleScopes = []principal.RoleScope{
				{Role: string(policy.RoleProgrammeLead), ProgrammeID: programmeOne},
				{Role: string(policy.RoleSubjectGroupLead), SubjectGroupID: groupOne},
			}
			actors = append(actors, scoped)
		}
	}
	return actors
}

// TestGuardAndFilterAgree is the bridge between the two forms of the rule.
//
// CanSeeWish is the rule as a sentence; WishVisibility is the rule as a WHERE clause. Both
// exist because both are needed — one for a record already in hand, one so that the predicate
// runs in the database and an index applies — and the realistic way this design fails is that
// somebody adjusts one of them and not the other. Nothing would break loudly: the list would
// keep filtering while a count, or a detail view, started answering a question it should not.
//
// So the test enumerates the complete cartesian product: every role combination × both doors
// × both publication states × every phase × own, somebody else's, and an orphaned wish. If the
// two forms ever disagree anywhere in that space, this fails with the exact case.
func TestGuardAndFilterAgree(t *testing.T) {
	t.Parallel()

	owners := []struct {
		name string
		id   uuid.UUID
	}{
		{"own wish", testdata.Eins.ID()},
		{"somebody else's wish", testdata.Zwei.ID()},
		// An owner column that was never filled in. Not a case the domain produces, and
		// exactly the case where "actor.ID == wish.OwnerID" would hand an anonymous caller
		// somebody's confidential record, because both sides are uuid.Nil.
		{"wish with no owner", uuid.Nil},
	}

	// The two things a wish can belong to, and the absence of each. uuid.Nil for the subject
	// group is not a curiosity: it is every module the faculty has not sorted yet, which in
	// October is most of them.
	programmes := []uuid.UUID{programmeOne, programmeTwo, uuid.Nil}
	groups := []uuid.UUID{groupOne, groupTwo, uuid.Nil}

	checked := 0
	for _, actor := range everyActor() {
		for _, state := range []policy.SemesterState{unpublished, published} {
			for _, phase := range policy.AllPhases() {
				state.Phase = phase
				for _, owner := range owners {
					for _, programme := range programmes {
						for _, group := range groups {
							wish := policy.Wish{
								OwnerID:        owner.id,
								ProgrammeID:    programme,
								SubjectGroupID: group,
							}

							guard := policy.CanSeeWish(actor, state, wish)
							filter := policy.WishVisibility(actor, state).Matches(wish)
							checked++

							if guard != filter {
								t.Errorf("guard and filter disagree:\n"+
									"  actor:     %s roles=%v scopes=%v\n"+
									"  semester:  phase=%s published=%v\n"+
									"  wish:      %s programme=%s group=%s\n"+
									"  CanSeeWish=%v  WishVisibility(...).Matches=%v",
									actor, actor.Roles, actor.RoleScopes,
									state.Phase, state.WishesPublished(),
									owner.name, programme, group, guard, filter)
							}
						}
					}
				}
			}
		}
	}

	// A loop that silently iterates over nothing passes, and would keep passing after somebody
	// empties AllRoles or AllPhases.
	if checked == 0 {
		t.Fatal("the cartesian product was empty — this test checked nothing")
	}
	t.Logf("compared %d combinations", checked)
}

// TestColleaguesSeeNothingBeforePublication is the rule the whole project rests on, stated
// once, in its own test, under a name that says what it is.
//
// From the kickoff: neue Kolleg:innen sollen sich eintragen können, ohne dass es wie ein
// Angriff auf eine alteingesessene Person wirkt. A colleague at the same level — no planning
// role, not the dean's office — must see nothing of somebody else's entry until publication.
func TestColleaguesSeeNothingBeforePublication(t *testing.T) {
	t.Parallel()

	wish := policy.Wish{OwnerID: testdata.Eins.ID()}
	colleague := testdata.Zwei.Actor(principal.KindInteractive, string(policy.RoleLecturer))

	if policy.CanSeeWish(colleague, unpublished, wish) {
		t.Error("a colleague sees an unpublished wish that is not theirs")
	}

	filter := policy.WishVisibility(colleague, unpublished)
	if filter.Scope != policy.WishScopeOwn {
		t.Errorf("filter scope is %q, want %q — anything wider also widens the COUNT, and "+
			"\"3 Kolleg:innen haben Interesse\" leaks the whole answer without a name",
			filter.Scope, policy.WishScopeOwn)
	}
	if filter.OwnerID != testdata.Zwei.ID() {
		t.Errorf("filter restricts to %v, want the caller %v", filter.OwnerID, testdata.Zwei.ID())
	}

	// The other half of the same rule: their own entry stays visible to them the whole time.
	// Confidentiality that also hides your own work is just a broken page.
	own := policy.Wish{OwnerID: testdata.Zwei.ID()}
	if !policy.CanSeeWish(colleague, unpublished, own) {
		t.Error("a colleague cannot see their own unpublished wish")
	}
}

// TestPublicationOpensItForEveryone covers the deadline actually arriving. The point of the
// rule is timing, not secrecy: after wishes_published_at every authenticated caller sees
// everything, through either door.
func TestPublicationOpensItForEveryone(t *testing.T) {
	t.Parallel()

	wish := policy.Wish{OwnerID: testdata.Eins.ID()}

	for _, kind := range []principal.Kind{principal.KindInteractive, principal.KindToken} {
		colleague := testdata.Zwei.Actor(kind, string(policy.RoleLecturer))

		if !policy.CanSeeWish(colleague, published, wish) {
			t.Errorf("%s: a published wish is still hidden", kind)
		}
		if scope := policy.WishVisibility(colleague, published).Scope; scope != policy.WishScopeAll {
			t.Errorf("%s: filter scope after publication is %q, want %q", kind, scope, policy.WishScopeAll)
		}
	}

	// Anonymous is not "everyone". Publication makes wishes visible to the faculty, not to the
	// internet — and although the proxy makes an unauthenticated request unlikely on the
	// browser door, the token door answers whatever arrives.
	if policy.CanSeeWish(principal.Anonymous, published, wish) {
		t.Error("an unauthenticated caller sees published wishes")
	}
	if scope := policy.WishVisibility(principal.Anonymous, published).Scope; scope != policy.WishScopeNone {
		t.Errorf("anonymous filter scope is %q, want %q", scope, policy.WishScopeNone)
	}
}

// TestPlannersSeeEarlyButOnlyInTheBrowser covers the exception and its limit together,
// because they are one decision.
//
// Planning roles have to see what is on the table before it is public — that is the job. A
// Personal Access Token belonging to the same person does not, and this is the clause most
// likely to be dropped by somebody simplifying the condition: a long-lived token in a script
// makes silent bulk export possible and decouples "who saw this" from any login event, which
// is precisely what an audited interactive session does not.
//
// What each role sees is now bounded by what it is responsible for, which is why the wish under
// test names a programme and a subject group at all.
func TestPlannersSeeEarlyButOnlyInTheBrowser(t *testing.T) {
	t.Parallel()

	// Prof. Eins's wish, on an instance of programme one, whose module is in group one.
	wish := policy.Wish{
		OwnerID:        testdata.Eins.ID(),
		ProgrammeID:    programmeOne,
		SubjectGroupID: groupOne,
	}

	for _, tc := range []struct {
		name  string
		actor func(principal.Kind) principal.Actor
	}{
		{"the programme lead of that programme", func(k principal.Kind) principal.Actor {
			return leadOf(testdata.Vier, k, programmeOne)
		}},
		{"the subject group lead of that group", func(k principal.Kind) principal.Actor {
			return headOf(testdata.Drei, k, groupOne)
		}},
		{"the dean's office", func(k principal.Kind) principal.Actor {
			return testdata.Fuenf.Actor(k, string(policy.RoleDeansOffice))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			browser := tc.actor(principal.KindInteractive)
			if !policy.CanSeeWish(browser, unpublished, wish) {
				t.Errorf("%s cannot see an unpublished wish in the browser — the process "+
					"requires it", tc.name)
			}

			token := tc.actor(principal.KindToken)
			if policy.CanSeeWish(token, unpublished, wish) {
				t.Errorf("%s sees somebody else's unpublished wish through a Personal Access "+
					"Token — that is the silent-bulk-export path @interactiveOnly exists to "+
					"close", tc.name)
			}
			if scope := policy.WishVisibility(token, unpublished).Scope; scope != policy.WishScopeOwn {
				t.Errorf("%s: token filter scope is %q, want %q", tc.name, scope, policy.WishScopeOwn)
			}

			// Their own entries stay readable through the token. It is their data, and a
			// script that cannot read back what it wrote is useless.
			own := policy.Wish{OwnerID: browser.ID, ProgrammeID: programmeTwo, SubjectGroupID: groupTwo}
			if !policy.CanSeeWish(token, unpublished, own) {
				t.Errorf("%s cannot read their own wish through a token", tc.name)
			}
		})
	}
}

// The correction this migration exists for, from the side that used to be wrong.
//
// RoleSet.Plans() made both planning roles faculty-wide readers, which was correct only while
// there was nothing for a role to be scoped to. An IG lead has no business in IF wishes, and a
// mathematics lead none in the software subjects.
func TestALeadReadsOnlyWhatTheyAreResponsibleFor(t *testing.T) {
	t.Parallel()

	// An instance of programme one, whose module is in group one.
	wish := policy.Wish{
		OwnerID:        testdata.Eins.ID(),
		ProgrammeID:    programmeOne,
		SubjectGroupID: groupOne,
	}

	for _, tc := range []struct {
		name  string
		actor principal.Actor
		want  bool
	}{
		{"the lead of that programme", leadOf(testdata.Vier, principal.KindInteractive, programmeOne), true},
		{"the lead of another programme", leadOf(testdata.Vier, principal.KindInteractive, programmeTwo), false},
		{"a programme lead with no programme", leadOf(testdata.Vier, principal.KindInteractive), false},
		{"the lead of that subject group", headOf(testdata.Drei, principal.KindInteractive, groupOne), true},
		{"the lead of another subject group", headOf(testdata.Drei, principal.KindInteractive, groupTwo), false},
		{"a subject group lead with no group", headOf(testdata.Drei, principal.KindInteractive), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := policy.CanSeeWish(tc.actor, unpublished, wish); got != tc.want {
				t.Errorf("%s sees the wish = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

// The two reaches are orthogonal, and this is the case that shows it: a subject group spans
// programmes, so the lead of the module's group reads a wish on an instance of a programme they
// have nothing to do with — and the other way round.
func TestTheTwoReachesAreOrthogonal(t *testing.T) {
	t.Parallel()

	// The lead of programme one; the wish is on an instance of programme one whose module belongs
	// to a group they do not lead.
	programmeLead := leadOf(testdata.Vier, principal.KindInteractive, programmeOne)
	// The lead of group one; the same wish, seen from the other axis.
	groupLead := headOf(testdata.Drei, principal.KindInteractive, groupOne)

	wish := policy.Wish{
		OwnerID:        testdata.Eins.ID(),
		ProgrammeID:    programmeOne,
		SubjectGroupID: groupOne,
	}

	if !policy.CanSeeWish(programmeLead, unpublished, wish) {
		t.Error("the programme lead does not reach a wish on their own programme's instance")
	}
	if !policy.CanSeeWish(groupLead, unpublished, wish) {
		t.Error("the subject group lead does not reach a wish on their own group's module")
	}

	// The same module in another programme: the group lead still reaches it, the programme lead
	// no longer does. This is the sentence "a subject group reaches across programmes".
	elsewhere := wish
	elsewhere.ProgrammeID = programmeTwo

	if policy.CanSeeWish(programmeLead, unpublished, elsewhere) {
		t.Error("the programme lead reaches an instance of a programme they do not lead")
	}
	if !policy.CanSeeWish(groupLead, unpublished, elsewhere) {
		t.Error("the subject group lead loses their own module when it is offered elsewhere — " +
			"the two reaches are not orthogonal")
	}
}

// A module nobody has sorted into a subject group yet is the ordinary state in October, and it
// fails closed: its programme's lead reads the wish, and no subject group lead does.
func TestAWishOnAnUnsortedModuleReachesNoSubjectGroupLead(t *testing.T) {
	t.Parallel()

	wish := policy.Wish{
		OwnerID:     testdata.Eins.ID(),
		ProgrammeID: programmeOne,
		// No subject group: module_subject_group has no row for this module.
		SubjectGroupID: uuid.Nil,
	}

	if !policy.CanSeeWish(leadOf(testdata.Vier, principal.KindInteractive, programmeOne),
		unpublished, wish) {
		t.Error("the programme lead loses a wish because the module has no subject group")
	}
	for _, name := range []string{"one", "two"} {
		group := groupOne
		if name == "two" {
			group = groupTwo
		}
		if policy.CanSeeWish(headOf(testdata.Drei, principal.KindInteractive, group),
			unpublished, wish) {
			t.Errorf("the lead of group %s reaches a wish whose module is in no group at all — "+
				"the nil subject group is matching a scope", name)
		}
	}
}

// Membership is not on the list. Somebody in a subject group reads the planning data of their
// subjects and none of the unpublished wishes on them; only the lead does.
//
// The kickoff sentence "jeder in einer Fachgruppe müsste alles lesen können" is about planning
// data. If it covered wishes, the confidentiality rule would switch itself off precisely inside
// the subject group — which is where the first-come-first-served race actually happens.
func TestBeingInASubjectGroupIsNotReadingItsWishes(t *testing.T) {
	t.Parallel()

	// Membership is not carried on the actor at all, because it is not a grant. A member is a
	// lecturer, and this asserts that a lecturer sees nothing — which is the same statement.
	member := testdata.Zwei.Actor(principal.KindInteractive, string(policy.RoleLecturer))
	wish := policy.Wish{
		OwnerID:        testdata.Eins.ID(),
		ProgrammeID:    programmeOne,
		SubjectGroupID: groupOne,
	}

	if policy.CanSeeWish(member, unpublished, wish) {
		t.Error("a colleague working in the same subject group reads an unpublished wish — " +
			"which is exactly the person the rule protects the owner from")
	}
}

// TestAdminIsNotAWishReader pins a decision rather than a mechanism.
//
// ADMIN administers users, roles and tokens. It is deliberately not on the list of people who
// see unpublished wishes: running the system is a different job from planning with it, and
// the exception list is worth more to the colleagues it protects when every entry on it can
// be justified. An admin who genuinely needs to look grants themselves DEANS_OFFICE — visibly, in
// the audit log.
//
// If the faculty decides otherwise, this test is the place that says so out loud, and the
// golden matrix is the diff that shows it.
func TestAdminIsNotAWishReader(t *testing.T) {
	t.Parallel()

	admin := testdata.Fuenf.Actor(principal.KindInteractive, string(policy.RoleAdmin))
	wish := policy.Wish{OwnerID: testdata.Eins.ID()}

	if policy.CanSeeWish(admin, unpublished, wish) {
		t.Error("ADMIN sees unpublished wishes — see the doc comment on " +
			"UnpublishedWishScope before changing this")
	}
}

// TestVisibilityDoesNotDependOnThePhase states an orthogonality that is easy to lose.
//
// Publication is its own timestamp, not a consequence of the phase. The process needs both
// halves independently — the wish phase can close without publishing, and publication can
// happen while the assignment runs — so a rule that read the phase instead of the timestamp
// would work right up until the first semester where those two come apart.
func TestVisibilityDoesNotDependOnThePhase(t *testing.T) {
	t.Parallel()

	for _, actor := range everyActor() {
		for _, publishedState := range []bool{false, true} {
			state := unpublished
			if publishedState {
				state = published
			}

			for _, owner := range []uuid.UUID{testdata.Eins.ID(), testdata.Zwei.ID(), uuid.Nil} {
				wish := policy.Wish{OwnerID: owner}

				var reference bool
				for i, phase := range policy.AllPhases() {
					state.Phase = phase
					got := policy.CanSeeWish(actor, state, wish)
					if i == 0 {
						reference = got
						continue
					}
					if got != reference {
						t.Fatalf("visibility changed with the phase:\n"+
							"  actor: %s roles=%v\n"+
							"  %s=%v but %s=%v\n"+
							"Publication is a timestamp of its own; if this is intended, "+
							"say so here and re-record the golden matrix.",
							actor, actor.Roles,
							policy.AllPhases()[0], reference, phase, got)
					}
				}
			}
		}
	}
}

// TestUnknownRolesGrantNothing covers the drift between the database and this package.
//
// The role column is constrained to the list in internal/policy, but a migration can widen it
// and a typo can slip through a hand-written INSERT. The safe reading of "the policy does not
// know what this grant means" is that it means nothing — the alternative, treating an
// unrecognised grant as a role, would make a typo a privilege escalation.
func TestUnknownRolesGrantNothing(t *testing.T) {
	t.Parallel()

	// "PLANNER" is the word the documentation uses for the concept, which makes it exactly the
	// string somebody will one day insert while thinking it is a role.
	impostor := testdata.Zwei.Actor(principal.KindInteractive, "PLANNER", "SUPERUSER", "")
	wish := policy.Wish{OwnerID: testdata.Eins.ID()}

	if roles := policy.RolesOf(impostor); len(roles) != 0 {
		t.Errorf("RolesOf recognised %v out of unknown grants", roles.Sorted())
	}
	if policy.CanSeeWish(impostor, unpublished, wish) {
		t.Error("an actor holding only unknown role strings sees unpublished wishes")
	}
}

// TestFilterScopesTranslateAsDocumented covers WishFilter on its own, because internal/store
// will translate it into SQL and the translation has to be mechanical.
func TestFilterScopesTranslateAsDocumented(t *testing.T) {
	t.Parallel()

	eins := policy.Wish{OwnerID: testdata.Eins.ID()}

	for _, tc := range []struct {
		name   string
		filter policy.WishFilter
		wish   policy.Wish
		want   bool
	}{
		{"all matches anything", policy.WishFilter{Scope: policy.WishScopeAll}, eins, true},
		{"none matches nothing", policy.WishFilter{Scope: policy.WishScopeNone}, eins, false},
		{
			name:   "own matches the owner",
			filter: policy.WishFilter{Scope: policy.WishScopeOwn, OwnerID: testdata.Eins.ID()},
			wish:   eins,
			want:   true,
		},
		{
			name:   "own does not match somebody else",
			filter: policy.WishFilter{Scope: policy.WishScopeOwn, OwnerID: testdata.Zwei.ID()},
			wish:   eins,
			want:   false,
		},
		{
			// Both sides nil. The filter form of the trap in principal.Actor.Owns: a filter
			// built for a caller with no identity must not match a row with no owner.
			name:   "own with no owner matches nothing",
			filter: policy.WishFilter{Scope: policy.WishScopeOwn},
			wish:   policy.Wish{},
			want:   false,
		},
		{
			// Fail closed: a scope this package does not know is not a licence to read.
			name:   "an unknown scope matches nothing",
			filter: policy.WishFilter{Scope: policy.WishScope("everything")},
			wish:   eins,
			want:   false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.filter.Matches(tc.wish); got != tc.want {
				t.Errorf("Matches() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestWishesPublishedReadsTheTimestamp covers the small type that stands in for
// semester.wishes_published_at, including the zero value that means "not yet".
func TestWishesPublishedReadsTheTimestamp(t *testing.T) {
	t.Parallel()

	if unpublished.WishesPublished() {
		t.Error("a semester with no publication timestamp reports as published")
	}
	if !published.WishesPublished() {
		t.Error("a semester with a publication timestamp reports as unpublished")
	}
	if fmt.Sprint(policy.SemesterState{}.WishesPublished()) != "false" {
		t.Error("the zero SemesterState reports as published, which is the wrong direction " +
			"for a value that arrives from an unmigrated row")
	}
}
