package store_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/auth"
	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
	"github.com/obcode/tallox.go/internal/store"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// seedSubjectGroup puts one subject group in and returns its id.
func seedSubjectGroup(t *testing.T, s *storetest.Schema, code, name string) uuid.UUID {
	t.Helper()

	var id uuid.UUID
	err := s.Pool.QueryRow(t.Context(),
		`INSERT INTO subject_group (code, name) VALUES ($1, $2) RETURNING id`, code, name).Scan(&id)
	if err != nil {
		t.Fatalf("cannot seed the subject group %s: %v", code, err)
	}
	return id
}

// leadSubjectGroup makes somebody the lead of a group.
func leadSubjectGroup(t *testing.T, s *storetest.Schema, person, group uuid.UUID) {
	t.Helper()

	if _, err := s.Pool.Exec(t.Context(),
		`INSERT INTO person_subject_group_scope (person_id, role, subject_group_id)
		 VALUES ($1, 'SUBJECT_GROUP_LEAD', $2)`, person, group); err != nil {
		t.Fatalf("cannot assign the subject group: %v", err)
	}
}

// The scope is only worth anything if it reaches the rule, and it reaches the rule by being read
// at authentication. Asserted through both doors, because the realistic failure is a lookup
// somebody extends for the browser and forgets on the token path — which is exactly what would
// have happened here, since the two doors read the scopes from two different queries.
func TestSubjectGroupScopesReachTheActorThroughBothDoors(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	ctx := t.Context()

	storetest.SeedPerson(t, s, testdata.Drei, "LECTURER", "SUBJECT_GROUP_LEAD")
	storetest.SeedToken(t, s, testdata.Drei, auth.HashSecret("example-secret"), storetest.TokenOptions{})

	maths := seedSubjectGroup(t, s, "MATHE", "Mathematik")
	seedSubjectGroup(t, s, "SWE", "Softwarefächer")
	leadSubjectGroup(t, s, testdata.Drei.ID(), maths)

	directory := store.NewDirectory(s.Pool)

	assert := func(t *testing.T, scopes []principal.RoleScope) {
		t.Helper()

		if len(scopes) != 1 {
			t.Fatalf("the actor carries %d scope(s), want 1", len(scopes))
		}
		if scopes[0].Role != "SUBJECT_GROUP_LEAD" {
			t.Errorf("the scope is for %s", scopes[0].Role)
		}
		if scopes[0].SubjectGroupID != maths {
			t.Errorf("the scope names %s, want %s", scopes[0].SubjectGroupID, maths)
		}
		if scopes[0].ProgrammeID != uuid.Nil {
			t.Errorf("a subject group scope arrived carrying a programme: %s", scopes[0].ProgrammeID)
		}
		actor := principal.Actor{Roles: []string{"SUBJECT_GROUP_LEAD"}, RoleScopes: scopes}
		if !policy.MayActInSubjectGroup(actor, maths) {
			t.Error("the scope reached the actor but does not permit acting in the group")
		}
	}

	t.Run("browser door", func(t *testing.T) {
		person, err := directory.PersonByMail(ctx, testdata.Drei.Mail)
		if err != nil || person == nil {
			t.Fatalf("cannot resolve the person: %v", err)
		}
		assert(t, person.RoleScopes)
	})

	t.Run("token door", func(t *testing.T) {
		token, err := directory.TokenByID(ctx, testdata.Drei.TokenID)
		if err != nil || token == nil {
			t.Fatalf("cannot resolve the token: %v", err)
		}
		assert(t, token.Owner.RoleScopes)
	})
}

// Both kinds of scope on one person, and neither wearing the other's target.
//
// This is the invariant principal.RoleScope's two named fields exist for. A row whose role
// string is wrong would be indistinguishable from a well-formed scope of the other kind if both
// shared one anonymous id column, and the rule reading it would grant something nobody granted.
func TestRoleScopesNameExactlyOneThing(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	ctx := t.Context()

	storetest.SeedPerson(t, s, testdata.Drei, "LECTURER", "PROGRAMME_LEAD", "SUBJECT_GROUP_LEAD")

	if_ := seedProgramme(t, s, "IF")
	maths := seedSubjectGroup(t, s, "MATHE", "Mathematik")

	if _, err := s.Pool.Exec(ctx,
		`INSERT INTO person_programme_scope (person_id, role, programme_id)
		 VALUES ($1, 'PROGRAMME_LEAD', $2)`, testdata.Drei.ID(), if_); err != nil {
		t.Fatalf("cannot assign the programme: %v", err)
	}
	leadSubjectGroup(t, s, testdata.Drei.ID(), maths)

	person, err := store.NewDirectory(s.Pool).PersonByMail(ctx, testdata.Drei.Mail)
	if err != nil || person == nil {
		t.Fatalf("cannot resolve the person: %v", err)
	}

	if len(person.RoleScopes) != 2 {
		t.Fatalf("the actor carries %d scope(s), want 2", len(person.RoleScopes))
	}
	for _, scope := range person.RoleScopes {
		if scope.NamesNothing() {
			t.Errorf("the scope for %s names no thing at all", scope.Role)
			continue
		}
		named := 0
		if scope.ProgrammeID != uuid.Nil {
			named++
		}
		if scope.SubjectGroupID != uuid.Nil {
			named++
		}
		if named != 1 {
			t.Errorf("the scope for %s names %d things, want exactly 1", scope.Role, named)
		}
	}

	actor := principal.Actor{
		Roles:      []string{"PROGRAMME_LEAD", "SUBJECT_GROUP_LEAD"},
		RoleScopes: person.RoleScopes,
	}
	if !policy.MayPlanProgramme(actor, if_) || !policy.MayActInSubjectGroup(actor, maths) {
		t.Error("holding both scoped roles lost one of the scopes")
	}
	if policy.MayActInSubjectGroup(actor, uuid.New()) {
		t.Error("a scope reached a subject group nobody granted")
	}
}

// A grant that ran out takes its subject groups with it. The composite foreign key covers a
// *revoked* grant; nothing in the database covers one that merely expired, so person_role_scope
// has to — and it does so in one place for both scoped roles, which is why it is a view.
func TestAnExpiredGrantCarriesNoSubjectGroups(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	ctx := t.Context()

	storetest.SeedPerson(t, s, testdata.Drei, "LECTURER", "SUBJECT_GROUP_LEAD")
	maths := seedSubjectGroup(t, s, "MATHE", "Mathematik")
	leadSubjectGroup(t, s, testdata.Drei.ID(), maths)

	if _, err := s.Pool.Exec(ctx,
		`UPDATE person_role
		 SET granted_at = now() - interval '2 days', expires_at = now() - interval '1 day'
		 WHERE person_id = $1 AND role = 'SUBJECT_GROUP_LEAD'`, testdata.Drei.ID()); err != nil {
		t.Fatalf("cannot expire the grant: %v", err)
	}

	person, err := store.NewDirectory(s.Pool).PersonByMail(ctx, testdata.Drei.Mail)
	if err != nil || person == nil {
		t.Fatalf("cannot resolve the person: %v", err)
	}
	for _, scope := range person.RoleScopes {
		if scope.SubjectGroupID != uuid.Nil {
			t.Errorf("an expired grant still carries the subject group %s", scope.SubjectGroupID)
		}
	}
}

// The load-bearing constraint of person_subject_group_scope, from the side that matters.
//
// Without the composite foreign key, revoking SUBJECT_GROUP_LEAD would leave the scope rows
// standing, and granting the role again would silently restore the groups somebody deliberately
// took away — a widening nobody performed.
func TestRevokingTheRoleTakesTheSubjectGroupsWithIt(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	ctx := t.Context()

	storetest.SeedPerson(t, s, testdata.Drei, "LECTURER", "SUBJECT_GROUP_LEAD")
	maths := seedSubjectGroup(t, s, "MATHE", "Mathematik")
	leadSubjectGroup(t, s, testdata.Drei.ID(), maths)

	if _, err := s.Pool.Exec(ctx,
		`DELETE FROM person_role WHERE person_id = $1 AND role = 'SUBJECT_GROUP_LEAD'`,
		testdata.Drei.ID()); err != nil {
		t.Fatalf("cannot revoke the role: %v", err)
	}

	var left int
	if err := s.Pool.QueryRow(ctx,
		`SELECT count(*) FROM person_subject_group_scope WHERE person_id = $1`,
		testdata.Drei.ID()).Scan(&left); err != nil {
		t.Fatalf("cannot count the scopes: %v", err)
	}
	if left != 0 {
		t.Errorf("%d subject group scope(s) outlived the revoked grant. Granting the role "+
			"again would silently restore groups somebody deliberately took away.", left)
	}
}

// Assigning 506 modules is weeks of somebody's judgement, so losing it must not be something a
// DELETE can do quietly. Retiring a group is active = false and leaves the assignment intact.
func TestASubjectGroupWithModulesCannotBeDeleted(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	ctx := t.Context()

	if_ := seedProgramme(t, s, "IF")
	module := seedModule(t, s, if_, "Analysis")
	maths := seedSubjectGroup(t, s, "MATHE", "Mathematik")

	if _, err := s.Pool.Exec(ctx,
		`INSERT INTO module_subject_group (module_id, subject_group_id) VALUES ($1, $2)`,
		module, maths); err != nil {
		t.Fatalf("cannot assign the module: %v", err)
	}

	if _, err := s.Pool.Exec(ctx, `DELETE FROM subject_group WHERE id = $1`, maths); err == nil {
		t.Error("a subject group with modules was deleted, taking the assignment with it")
	}

	if _, err := s.Pool.Exec(ctx,
		`UPDATE subject_group SET active = false WHERE id = $1`, maths); err != nil {
		t.Fatalf("cannot retire the group: %v", err)
	}

	var group uuid.UUID
	if err := s.Pool.QueryRow(ctx,
		`SELECT subject_group_id FROM module_subject_group WHERE module_id = $1`,
		module).Scan(&group); err != nil {
		t.Fatalf("retiring the group lost the module assignment: %v", err)
	}
}

// Splitting a group is what the faculty already expects to do — mathematics into a classical and
// a machine-learning group — and open-questions.md asks that it happen in service without data
// loss. With the assignment in its own table that is an UPDATE, which is what this pins.
func TestSplittingASubjectGroupIsAnUpdate(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	ctx := t.Context()

	if_ := seedProgramme(t, s, "IF")
	analysis := seedModule(t, s, if_, "Analysis")
	learning := seedModule(t, s, if_, "Maschinelles Lernen")

	maths := seedSubjectGroup(t, s, "MATHE", "Mathematik")
	for _, module := range []uuid.UUID{analysis, learning} {
		if _, err := s.Pool.Exec(ctx,
			`INSERT INTO module_subject_group (module_id, subject_group_id) VALUES ($1, $2)`,
			module, maths); err != nil {
			t.Fatalf("cannot assign the module: %v", err)
		}
	}

	ml := seedSubjectGroup(t, s, "MATHE-ML", "Mathematik (Machine Learning)")
	if _, err := s.Pool.Exec(ctx,
		`UPDATE module_subject_group SET subject_group_id = $1 WHERE module_id = $2`,
		ml, learning); err != nil {
		t.Fatalf("cannot move the module: %v", err)
	}

	var group uuid.UUID
	if err := s.Pool.QueryRow(ctx,
		`SELECT subject_group_id FROM module_subject_group WHERE module_id = $1`,
		learning).Scan(&group); err != nil {
		t.Fatalf("cannot read the module back: %v", err)
	}
	if group != ml {
		t.Errorf("the module stayed in %s, want %s", group, ml)
	}

	if err := s.Pool.QueryRow(ctx,
		`SELECT subject_group_id FROM module_subject_group WHERE module_id = $1`,
		analysis).Scan(&group); err != nil {
		t.Fatalf("cannot read the untouched module back: %v", err)
	}
	if group != maths {
		t.Errorf("moving one module moved the other one too")
	}
}

// A module belongs to exactly one subject group, and the primary key is what says so. Without
// it "whose group fills this instance" would have no single answer — and that is the sentence
// the whole assignment phase is made of.
func TestAModuleBelongsToExactlyOneSubjectGroup(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	ctx := t.Context()

	if_ := seedProgramme(t, s, "IF")
	module := seedModule(t, s, if_, "Analysis")
	maths := seedSubjectGroup(t, s, "MATHE", "Mathematik")
	ml := seedSubjectGroup(t, s, "MATHE-ML", "Mathematik (Machine Learning)")

	if _, err := s.Pool.Exec(ctx,
		`INSERT INTO module_subject_group (module_id, subject_group_id) VALUES ($1, $2)`,
		module, maths); err != nil {
		t.Fatalf("cannot assign the module: %v", err)
	}
	if _, err := s.Pool.Exec(ctx,
		`INSERT INTO module_subject_group (module_id, subject_group_id) VALUES ($1, $2)`,
		module, ml); err == nil {
		t.Error("a module was assigned to two subject groups")
	}
}
