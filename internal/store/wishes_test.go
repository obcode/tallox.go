package store_test

import (
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/store"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// Wishes against a real database.
//
// Not against a fake, and this is the area where that rule earns its keep: the confidentiality
// filter *is* a WHERE clause, "no aggregates before publication" is the absence of a COUNT in
// db/queries/wish.sql, and the refusal to withdraw an instance somebody wants is a foreign key.
// A fake store passes all three while the shipped statements do something else.

// wishFixture is a semester with one instance of two parts, and four people.
type wishFixture struct {
	schema   *storetest.Schema
	wishes   *store.Wishes
	semester domain.Semester
	// programme and otherProgramme are two study programmes, so that "the lead of another
	// programme" is a case that can be written down.
	programme      uuid.UUID
	otherProgramme uuid.UUID
	// group and otherGroup are two subject groups; the module is in the first.
	group      uuid.UUID
	otherGroup uuid.UUID
	module     uuid.UUID
	// instance is what everybody in these tests registers interest in.
	instance uuid.UUID
	// lecture and lab are its parts. Nothing points at them any more — they are here so that
	// "re-cutting an instance somebody wants" stays a case with rows behind it.
	lecture uuid.UUID
	lab     uuid.UUID
}

func newWishFixture(t *testing.T) wishFixture {
	t.Helper()

	s := storetest.New(t)
	storetest.SeedZPACatalogue(t, s)
	project(t, s)

	ctx := t.Context()
	modules := store.NewModules(s.Pool)
	semesters := store.NewSemesters(s.Pool)

	semester, err := semesters.EnsureSemester(ctx, "2027-SS")
	if err != nil {
		t.Fatalf("cannot record the semester: %v", err)
	}

	f := wishFixture{
		schema:         s,
		wishes:         store.NewWishes(s.Pool),
		semester:       semester,
		programme:      programmeID(t, s, storetest.FixtureProgrammeA),
		otherProgramme: programmeID(t, s, storetest.FixtureProgrammeB),
		module:         moduleID(t, s, storetest.FixtureModuleOrdinary),
		group:          seedSubjectGroup(t, s, "MATHE", "Mathematik"),
		otherGroup:     seedSubjectGroup(t, s, "SWE", "Softwarefächer"),
	}

	if _, err := modules.SetModuleComponents(ctx, f.module, []domain.ModuleComponent{
		{Kind: domain.PartKindLecture, TeachingHours: 2, Position: 0},
		{Kind: domain.PartKindLab, TeachingHours: 2, Position: 1},
	}, uuid.Nil); err != nil {
		t.Fatalf("cannot state the module's split: %v", err)
	}

	if _, err := s.Pool.Exec(ctx,
		`INSERT INTO module_subject_group (module_id, subject_group_id) VALUES ($1, $2)`,
		f.module, f.group); err != nil {
		t.Fatalf("cannot put the module in a subject group: %v", err)
	}

	instance, err := store.NewDemand(s.Pool, modules).CreateCourseInstance(ctx,
		domain.NewCourseInstance{
			SemesterID:  semester.ID,
			ModuleID:    f.module,
			ProgrammeID: f.programme,
		})
	if err != nil {
		t.Fatalf("cannot declare the instance: %v", err)
	}
	if len(instance.Parts) < 2 {
		t.Fatalf("the instance has %d parts, want two", len(instance.Parts))
	}
	f.instance = instance.ID
	f.lecture, f.lab = instance.Parts[0].ID, instance.Parts[1].ID

	for _, p := range []testdata.Persona{testdata.Eins, testdata.Zwei, testdata.Drei, testdata.Vier} {
		storetest.SeedPerson(t, s, p, "LECTURER")
	}
	return f
}

// register puts one person's interest in on the instance.
func (f wishFixture) register(t *testing.T, who testdata.Persona) *domain.Wish {
	t.Helper()

	wish, err := f.wishes.SetWish(t.Context(), f.instance, who.ID(), domain.WishHappyTo, "")
	if err != nil {
		t.Fatalf("cannot register %s's interest: %v", who.Name, err)
	}
	return wish
}

// see reads the semester's wishes through one filter and returns the owners.
func (f wishFixture) see(t *testing.T, filter policy.WishFilter) []uuid.UUID {
	t.Helper()

	wishes, err := f.wishes.Wishes(t.Context(),
		domain.WishQuery{SemesterCode: f.semester.Code}, filter)
	if err != nil {
		t.Fatalf("cannot read the wishes: %v", err)
	}

	out := make([]uuid.UUID, 0, len(wishes))
	for _, w := range wishes {
		out = append(out, w.Person.ID)
	}
	return out
}

// The file this test reads is the one place a leak would be written, so this is the test that
// reads it.
//
// Every SELECT over the wish table has to carry the filter, and there has to be no COUNT at all.
// The second half is the one that is easy to get wrong later: a count that skipped the predicate
// would answer "three colleagues have already registered interest" — the confidential fact with
// the names taken out — and it would look like an ordinary convenience while doing it.
//
// Reading the file from disk rather than embedding it: db/embed.go carries the migrations because
// the server applies them at startup, and the queries have no runtime use at all. Shipping a
// hundred kilobytes of SQL in every container to satisfy a test is the wrong trade.
func TestEveryWishQueryIsFiltered(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../db/queries/wish.sql")
	if err != nil {
		t.Fatalf("cannot read the wish queries: %v", err)
	}
	sql := string(raw)

	if strings.Contains(strings.ToUpper(sql), "COUNT(") {
		t.Error("db/queries/wish.sql contains a COUNT. Before publication there are no counts " +
			"over wishes — an aggregate that skips the filter is the same failure as a list " +
			"that skips it, only harder to notice. Count the rows you were allowed to read.")
	}

	blocks := regexp.MustCompile(`(?m)^-- name: (\w+) :(\w+)$`).FindAllStringSubmatchIndex(sql, -1)
	if len(blocks) == 0 {
		t.Fatal("no queries found — this test read the wrong file")
	}

	checked := 0
	for i, block := range blocks {
		name := sql[block[2]:block[3]]
		kind := sql[block[4]:block[5]]

		end := len(sql)
		if i+1 < len(blocks) {
			end = blocks[i+1][0]
		}
		body := sql[block[0]:end]

		// Only the queries that read the wish table itself. SemesterOfCourseInstance reads the
		// semester through an instance and carries no wish, which is why it is named for what it
		// does.
		if !strings.Contains(body, "FROM wish w") {
			continue
		}
		checked++

		if kind != "many" && kind != "one" {
			continue
		}
		for _, required := range []string{
			`sqlc.arg('scope')::text = 'all'`,
			`w.person_id = sqlc.arg(owner_id)::uuid`,
			`prog.id = ANY (sqlc.arg(programme_ids)::uuid[])`,
			`msg.subject_group_id = ANY (sqlc.arg(subject_group_ids)::uuid[])`,
		} {
			if !strings.Contains(body, required) {
				t.Errorf("the query %s reads the wish table without %q. The visibility rule is "+
					"a WHERE clause; a query written without it is not a slow query, it is a "+
					"leak.", name, required)
			}
		}
	}

	if checked == 0 {
		t.Fatal("no query reads the wish table — this test checked nothing")
	}
}

// The rule the whole project rests on, at the layer where it is actually enforced.
func TestAColleagueSeesNothingBeforePublication(t *testing.T) {
	t.Parallel()

	f := newWishFixture(t)
	f.register(t, testdata.Eins)

	// Prof. Zwei, an ordinary colleague: their own filter is own-only, and they own nothing.
	seen := f.see(t, policy.WishFilter{Scope: policy.WishScopeOwn, OwnerID: testdata.Zwei.ID()})
	if len(seen) != 0 {
		t.Errorf("a colleague sees %d wishes before publication, want none", len(seen))
	}

	// The owner sees their own.
	seen = f.see(t, policy.WishFilter{Scope: policy.WishScopeOwn, OwnerID: testdata.Eins.ID()})
	if len(seen) != 1 || seen[0] != testdata.Eins.ID() {
		t.Errorf("the owner sees %v, want just their own", seen)
	}
}

// The correction this migration exists for, at the layer where it is a WHERE clause: a lead
// reaches the wishes of what they lead and no further.
func TestALeadReachesOnlyTheirOwnSubject(t *testing.T) {
	t.Parallel()

	f := newWishFixture(t)
	f.register(t, testdata.Eins)

	cases := []struct {
		name   string
		filter policy.WishFilter
		want   int
	}{
		{"the lead of the instance's programme", policy.WishFilter{
			Scope:        policy.WishScopeOwnOrScoped,
			OwnerID:      testdata.Vier.ID(),
			ProgrammeIDs: []uuid.UUID{f.programme},
		}, 1},
		{"the lead of another programme", policy.WishFilter{
			Scope:        policy.WishScopeOwnOrScoped,
			OwnerID:      testdata.Vier.ID(),
			ProgrammeIDs: []uuid.UUID{f.otherProgramme},
		}, 0},
		{"the lead of the module's subject group", policy.WishFilter{
			Scope:           policy.WishScopeOwnOrScoped,
			OwnerID:         testdata.Drei.ID(),
			SubjectGroupIDs: []uuid.UUID{f.group},
		}, 1},
		{"the lead of another subject group", policy.WishFilter{
			Scope:           policy.WishScopeOwnOrScoped,
			OwnerID:         testdata.Drei.ID(),
			SubjectGroupIDs: []uuid.UUID{f.otherGroup},
		}, 0},
		{"a lead with no subject at all", policy.WishFilter{
			Scope:   policy.WishScopeOwnOrScoped,
			OwnerID: testdata.Vier.ID(),
		}, 0},
		{"the dean's office", policy.WishFilter{Scope: policy.WishScopeAll}, 1},
		{"nobody", policy.WishFilter{Scope: policy.WishScopeNone}, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := len(f.see(t, tc.filter)); got != tc.want {
				t.Errorf("%s sees %d wishes, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// The consequence of deriving the subject group rather than storing it, pinned rather than
// discovered: moving a module into another group moves the wishes on its instances with it.
//
// This is the intended behaviour — whoever is responsible now is who may look now — and it is the
// reason there is no subject_group_id column on the wish row.
func TestMovingAModuleMovesWhoMayReadItsWishes(t *testing.T) {
	t.Parallel()

	f := newWishFixture(t)
	f.register(t, testdata.Eins)

	mathe := policy.WishFilter{
		Scope: policy.WishScopeOwnOrScoped, OwnerID: testdata.Drei.ID(),
		SubjectGroupIDs: []uuid.UUID{f.group},
	}
	swe := policy.WishFilter{
		Scope: policy.WishScopeOwnOrScoped, OwnerID: testdata.Drei.ID(),
		SubjectGroupIDs: []uuid.UUID{f.otherGroup},
	}

	if len(f.see(t, mathe)) != 1 || len(f.see(t, swe)) != 0 {
		t.Fatal("the fixture does not start with the module in the first group")
	}

	if _, err := f.schema.Pool.Exec(t.Context(),
		`UPDATE module_subject_group SET subject_group_id = $1 WHERE module_id = $2`,
		f.otherGroup, f.module); err != nil {
		t.Fatalf("cannot move the module: %v", err)
	}

	if len(f.see(t, mathe)) != 0 {
		t.Error("the old subject group still reads the wishes of a module it no longer holds")
	}
	if len(f.see(t, swe)) != 1 {
		t.Error("the new subject group does not read the wishes of the module it now holds — " +
			"responsibility was frozen at the moment the wish was registered")
	}
}

// A module nobody has sorted into a subject group yet is the ordinary state in October, and it
// fails closed: no subject group lead reaches its wishes, and its programme's lead still does.
func TestAWishOnAnUnsortedModuleReachesNoSubjectGroup(t *testing.T) {
	t.Parallel()

	f := newWishFixture(t)
	f.register(t, testdata.Eins)

	if _, err := f.schema.Pool.Exec(t.Context(),
		`DELETE FROM module_subject_group WHERE module_id = $1`, f.module); err != nil {
		t.Fatalf("cannot unsort the module: %v", err)
	}

	for _, group := range []uuid.UUID{f.group, f.otherGroup} {
		seen := f.see(t, policy.WishFilter{
			Scope: policy.WishScopeOwnOrScoped, OwnerID: testdata.Drei.ID(),
			SubjectGroupIDs: []uuid.UUID{group},
		})
		if len(seen) != 0 {
			t.Error("a subject group lead reaches a wish whose module is in no group at all")
		}
	}

	seen := f.see(t, policy.WishFilter{
		Scope: policy.WishScopeOwnOrScoped, OwnerID: testdata.Vier.ID(),
		ProgrammeIDs: []uuid.UUID{f.programme},
	})
	if len(seen) != 1 {
		t.Error("the programme lead lost a wish because the module has no subject group — the " +
			"LEFT JOIN is an inner one")
	}
}

// Registering twice is changing your mind, not an error and not a second row.
func TestRegisteringTwiceIsACorrection(t *testing.T) {
	t.Parallel()

	f := newWishFixture(t)
	ctx := t.Context()

	first, err := f.wishes.SetWish(ctx, f.instance, testdata.Eins.ID(), domain.WishIfNeeded, "erst mal")
	if err != nil {
		t.Fatalf("cannot register: %v", err)
	}

	second, err := f.wishes.SetWish(ctx, f.instance, testdata.Eins.ID(), domain.WishFirstChoice, "doch")
	if err != nil {
		t.Fatalf("cannot change the wish: %v", err)
	}

	if first.ID != second.ID {
		t.Error("registering twice produced a second wish rather than changing the first")
	}
	if second.Priority != domain.WishFirstChoice || second.Note != "doch" {
		t.Errorf("got %v/%q, want FIRST_CHOICE/doch", second.Priority, second.Note)
	}
	if seen := f.see(t, policy.WishFilter{Scope: policy.WishScopeAll}); len(seen) != 1 {
		t.Errorf("there are %d wishes, want one", len(seen))
	}
}

// Withdrawing somebody else's is refused, and refused with the same answer as one that is not
// there — because which of the two it is, is the confidential part.
func TestOnlyYourOwnWishCanBeWithdrawn(t *testing.T) {
	t.Parallel()

	f := newWishFixture(t)
	wish := f.register(t, testdata.Eins)

	err := f.wishes.WithdrawWish(t.Context(), wish.ID, testdata.Zwei.ID())
	if !errors.Is(err, domain.ErrWishNotFound) {
		t.Errorf("withdrawing somebody else's wish = %v, want ErrWishNotFound", err)
	}
	if seen := f.see(t, policy.WishFilter{Scope: policy.WishScopeAll}); len(seen) != 1 {
		t.Error("somebody else's wish was withdrawn")
	}

	missing := f.wishes.WithdrawWish(t.Context(), uuid.New(), testdata.Zwei.ID())
	if !errors.Is(missing, domain.ErrWishNotFound) {
		t.Errorf("withdrawing a wish that is not there = %v, want ErrWishNotFound", missing)
	}

	if err := f.wishes.WithdrawWish(t.Context(), wish.ID, testdata.Eins.ID()); err != nil {
		t.Errorf("the owner cannot withdraw their own wish: %v", err)
	}
}

// The branch that was unreachable until the wish table existed: an instance somebody wants cannot
// be withdrawn. One step now rather than two — the RESTRICT is on the wish's own foreign key —
// which is why the other half of this test matters as much as the first.
//
// **Removing a part of a wanted instance is allowed**, and that is a decision rather than an
// oversight. Parts are the faculty's own re-cutting of an instance: a third laboratory group, a
// lecture shared across cohorts, a split entered a week late. Interest in the subject must not
// freeze that, because nobody wished for a part in the first place. What may not happen is the
// *instance* disappearing under somebody who asked for it.
func TestAnInstanceSomebodyWantsCannotBeWithdrawn(t *testing.T) {
	t.Parallel()

	f := newWishFixture(t)
	ctx := t.Context()
	demand := store.NewDemand(f.schema.Pool, store.NewModules(f.schema.Pool))

	f.register(t, testdata.Eins)

	// Re-cutting stays open.
	if _, err := demand.DeleteInstancePart(ctx, f.lab); err != nil {
		t.Errorf("removing a part of a wanted instance = %v, want it to be allowed: parts are "+
			"how the faculty re-cuts an instance, and nobody wished for one", err)
	}

	if err := demand.DeleteCourseInstance(ctx, f.instance); !errors.Is(err, domain.ErrInstanceInUse) {
		t.Errorf("withdrawing an instance somebody wants = %v, want ErrInstanceInUse", err)
	}

	// And once the wish is gone, so is the refusal — the restriction is about the row and not
	// about the instance having ever been wanted.
	wish := f.register(t, testdata.Eins)
	if err := f.wishes.WithdrawWish(ctx, wish.ID, testdata.Eins.ID()); err != nil {
		t.Fatalf("cannot withdraw the wish: %v", err)
	}
	if err := demand.DeleteCourseInstance(ctx, f.instance); err != nil {
		t.Errorf("withdrawing an instance nobody wants any more = %v, want it to be allowed", err)
	}

	// And the refusal says nothing about what is using it — no count, no kind of thing named.
	message := domain.ErrInstanceInUse.Error()
	for _, forbidden := range []string{"Wunsch", "Wünsche", "wish", "1", "2", "3"} {
		if strings.Contains(message, forbidden) {
			t.Errorf("the refusal contains %q: %q. INSTANCE_IN_USE deliberately says only that "+
				"something hangs off the instance.", forbidden, message)
		}
	}
}

// Publication is the other half of the rule, and at this layer it is simply the filter the policy
// hands down. Asserted anyway, because "after the stichtag everybody sees everything" is the
// promise the confidentiality rule is sold on.
func TestPublicationLetsEverybodyRead(t *testing.T) {
	t.Parallel()

	f := newWishFixture(t)
	f.register(t, testdata.Eins)
	f.register(t, testdata.Zwei)

	if seen := f.see(t, policy.WishFilter{Scope: policy.WishScopeAll}); len(seen) != 2 {
		t.Errorf("after publication a colleague sees %d wishes, want 2", len(seen))
	}
}

// The list carries what a screen needs to render a row: the module, the cohort and the person. A
// wish rendered as "instance 3f2a…" is one nobody recognises.
func TestAWishCarriesWhatARowNeeds(t *testing.T) {
	t.Parallel()

	f := newWishFixture(t)
	f.register(t, testdata.Eins)

	wishes, err := f.wishes.Wishes(t.Context(),
		domain.WishQuery{SemesterCode: f.semester.Code},
		policy.WishFilter{Scope: policy.WishScopeAll})
	if err != nil {
		t.Fatalf("cannot read the wishes: %v", err)
	}
	if len(wishes) != 1 {
		t.Fatalf("got %d wishes, want one", len(wishes))
	}

	w := wishes[0]
	if w.Instance.Module.Name == "" {
		t.Error("the wish carries no module name")
	}
	if w.Instance.Programme.Code == "" {
		t.Error("the wish carries no programme")
	}
	if w.Instance.ID != f.instance {
		t.Errorf("the wish names instance %s, want %s", w.Instance.ID, f.instance)
	}
	if w.Person.Mail != testdata.Eins.Mail {
		t.Errorf("the wish names %q, want %q", w.Person.Mail, testdata.Eins.Mail)
	}
	if w.Instance.SemesterCode != f.semester.Code {
		t.Errorf("the wish names semester %q, want %q", w.Instance.SemesterCode, f.semester.Code)
	}
}

// The three levels have to agree with the CHECK constraint, which cannot import them.
func TestDatabaseAndDomainAgreeOnWishPriorities(t *testing.T) {
	t.Parallel()

	f := newWishFixture(t)
	ctx := t.Context()

	for _, priority := range domain.AllWishPriorities() {
		level, ok := priority.Level()
		if !ok {
			t.Fatalf("%s has no stored level", priority)
		}
		if _, err := f.schema.Pool.Exec(ctx,
			`INSERT INTO wish (course_instance_id, person_id, priority) VALUES ($1, $2, $3)
			 ON CONFLICT (course_instance_id, person_id) DO UPDATE SET priority = EXCLUDED.priority`,
			f.instance, testdata.Eins.ID(), level); err != nil {
			t.Errorf("the database refuses the level of %s: %v", priority, err)
		}
		if back := domain.WishPriorityFromLevel(level); back != priority {
			t.Errorf("level %d reads back as %s, want %s", level, back, priority)
		}
	}

	// And one the domain does not know: the constraint is what stops it, not the Go code.
	if _, err := f.schema.Pool.Exec(ctx,
		`INSERT INTO wish (course_instance_id, person_id, priority) VALUES ($1, $2, 4)`,
		f.instance, testdata.Zwei.ID()); err == nil {
		t.Error("the database accepted a priority outside the three levels")
	}
}

// The backfill in migration 16, run against rows in the old shape.
//
// The interesting half of that migration is not the ALTER TABLE — it is the three statements in
// the middle, which move every wish from a part onto its instance and then collapse the several
// wishes one person may have had on several parts of the same instance into one. Reversibility
// does not exercise them: an empty table migrates in both directions no matter what those
// statements say.
//
// What is asserted here is what the migration's header promises. The strongest priority survives,
// because a wish is a statement about a subject and "unbedingt für die Vorlesung, notfalls fürs
// Praktikum" means the person wants the subject. And nobody's own words are dropped: the notes of
// the collapsed rows are carried into the survivor, because a schema change that quietly deletes
// what somebody typed is the kind of thing found months later, by them.
func TestMigrationSixteenMovesWishesOntoTheirInstance(t *testing.T) {
	t.Parallel()

	f := newWishFixture(t)
	ctx := t.Context()

	// Back to the shape migration 15 left: wish.instance_part_id, no course_instance_id.
	if err := store.MigrateDownTo(ctx, f.schema.DB, 20260825110000); err != nil {
		t.Fatalf("cannot roll back to the old wish shape: %v", err)
	}

	// Eins wants both parts and says something different about each; Zwei wants only the lab.
	for _, row := range []struct {
		part     uuid.UUID
		person   uuid.UUID
		priority int16
		note     string
	}{
		{f.lecture, testdata.Eins.ID(), 3, "notfalls die Vorlesung"},
		{f.lab, testdata.Eins.ID(), 1, "unbedingt das Praktikum"},
		{f.lab, testdata.Zwei.ID(), 2, ""},
	} {
		if _, err := f.schema.Pool.Exec(ctx,
			`INSERT INTO wish (instance_part_id, person_id, priority, note)
			 VALUES ($1, $2, $3, $4)`,
			row.part, row.person, row.priority, row.note); err != nil {
			t.Fatalf("cannot write a wish in the old shape: %v", err)
		}
	}

	if _, err := store.Migrate(ctx, f.schema.DB); err != nil {
		t.Fatalf("cannot migrate up again: %v", err)
	}

	type wishRow struct {
		person   uuid.UUID
		instance uuid.UUID
		priority int16
		note     string
	}
	rows, err := f.schema.Pool.Query(ctx,
		`SELECT person_id, course_instance_id, priority, note FROM wish ORDER BY priority`)
	if err != nil {
		t.Fatalf("cannot read the migrated wishes: %v", err)
	}
	defer rows.Close()

	var got []wishRow
	for rows.Next() {
		var w wishRow
		if err := rows.Scan(&w.person, &w.instance, &w.priority, &w.note); err != nil {
			t.Fatalf("cannot read a migrated wish: %v", err)
		}
		got = append(got, w)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("cannot read the migrated wishes: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("after the migration there are %d wishes, want two: Eins's two entries on one "+
			"instance are one wish, Zwei's is another", len(got))
	}
	for _, w := range got {
		if w.instance != f.instance {
			t.Errorf("a wish points at %s, want the instance %s", w.instance, f.instance)
		}
	}

	mine := got[0]
	if mine.person != testdata.Eins.ID() {
		t.Fatalf("the strongest wish is %s's, want Eins's", mine.person)
	}
	if mine.priority != 1 {
		t.Errorf("the collapsed wish has priority %d, want 1 — the strongest of the two survives",
			mine.priority)
	}
	for _, word := range []string{"Vorlesung", "Praktikum"} {
		if !strings.Contains(mine.note, word) {
			t.Errorf("the collapsed note %q lost what was said about the %s", mine.note, word)
		}
	}
}
