package store_test

import (
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/principal"
	"github.com/obcode/tallox.go/internal/store"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

const desiredSemester = "2027-SS"

type desiredFixture struct {
	schema    *storetest.Schema
	store     *store.Wishes
	service   *domain.WishService
	semesters *domain.SemesterService
	group     uuid.UUID
	other     uuid.UUID
}

// Eins and Zwei in MATHE, Drei leads MATHE, Vier is in SWE.
func newDesiredFixture(t *testing.T) desiredFixture {
	t.Helper()

	s := storetest.New(t)
	storetest.SeedPerson(t, s, testdata.Eins, "LECTURER")
	storetest.SeedPerson(t, s, testdata.Zwei, "LECTURER")
	storetest.SeedPerson(t, s, testdata.Drei, "LECTURER", "SUBJECT_GROUP_LEAD")
	storetest.SeedPerson(t, s, testdata.Vier, "LECTURER")

	// The dean's office, and nothing else: not a colleague who teaches, so not in the overview.
	storetest.SeedPerson(t, s, testdata.Fuenf, "DEANS_OFFICE")

	wishes := store.NewWishes(s.Pool)
	semesters := domain.NewSemesterService(store.NewSemesters(s.Pool), nil)
	f := desiredFixture{
		schema:    s,
		store:     wishes,
		service:   domain.NewWishService(wishes, semesters),
		semesters: semesters,
		group:     seedSubjectGroup(t, s, "MATHE", "Mathematik"),
		other:     seedSubjectGroup(t, s, "SWE", "Softwarefächer"),
	}
	for _, m := range []struct {
		who   testdata.Persona
		group uuid.UUID
	}{{testdata.Eins, f.group}, {testdata.Zwei, f.group}, {testdata.Vier, f.other}} {
		f.exec(t, `INSERT INTO person_subject_group (person_id, subject_group_id) VALUES ($1, $2)`,
			m.who.ID(), m.group)
	}
	f.exec(t, `INSERT INTO person_subject_group_scope (person_id, role, subject_group_id)
		VALUES ($1, 'SUBJECT_GROUP_LEAD', $2)`, testdata.Drei.ID(), f.group)
	return f
}

func (f desiredFixture) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := f.schema.Pool.Exec(t.Context(), sql, args...); err != nil {
		t.Fatalf("cannot run %q: %v", sql, err)
	}
}

func (f desiredFixture) state(t *testing.T, who testdata.Persona, hours float64) {
	t.Helper()
	actor := who.Actor(principal.KindInteractive, "LECTURER")
	if _, err := f.service.SetDesiredHours(t.Context(), actor, desiredSemester, nil, &hours, ""); err != nil {
		t.Fatalf("%s cannot state %v hours: %v", who.Name, hours, err)
	}
}

func (f desiredFixture) seen(t *testing.T, actor principal.Actor) map[uuid.UUID]float64 {
	t.Helper()
	rows, err := f.service.DesiredHours(t.Context(), actor, desiredSemester, uuid.Nil)
	if err != nil {
		t.Fatalf("cannot list: %v", err)
	}
	out := map[uuid.UUID]float64{}
	for _, r := range rows {
		out[r.Person.ID] = r.MaxHours
	}
	return out
}

// The file the rule lives in, read like wish.sql is: every read of the table carries the filter,
// and there is no aggregate.
func TestEveryDesiredHoursReadIsFiltered(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../db/queries/desired_hours.sql")
	if err != nil {
		t.Fatalf("cannot read the queries: %v", err)
	}
	sql := string(raw)
	if strings.Contains(strings.ToUpper(sql), "COUNT(") || strings.Contains(strings.ToUpper(sql), "SUM(") {
		t.Error("db/queries/desired_hours.sql aggregates. A sum over figures the caller may not " +
			"read one by one is the same leak as the rows.")
	}

	blocks := regexp.MustCompile(`(?m)^-- name: (\w+) :(\w+)$`).FindAllStringSubmatchIndex(sql, -1)
	checked := 0
	for i, block := range blocks {
		name, kind := sql[block[2]:block[3]], sql[block[4]:block[5]]
		end := len(sql)
		if i+1 < len(blocks) {
			end = blocks[i+1][0]
		}
		body := sql[block[0]:end]
		// FROM or LEFT JOIN: the overview reads the table as the right-hand side of a join, and
		// the filter sits in its ON clause.
		if kind != "many" || !strings.Contains(body, "desired_teaching_hours d") {
			continue
		}
		checked++
		for _, required := range []string{
			`sqlc.arg('scope')::text = 'all'`,
			`d.person_id = sqlc.arg(owner_id)::uuid`,
			`psg.subject_group_id = ANY (sqlc.arg(subject_group_ids)::uuid[])`,
		} {
			if !strings.Contains(body, required) {
				t.Errorf("the query %s reads desired_teaching_hours without %q", name, required)
			}
		}
		// The note has a rule of its own: a query that returns it carries the second predicate.
		if strings.Contains(body, "d.note") {
			for _, required := range []string{
				`sqlc.arg('note_scope')::text = 'all'`,
				`d.person_id = sqlc.arg(note_owner_id)::uuid`,
				`npsg.subject_group_id = ANY (sqlc.arg(note_subject_group_ids)::uuid[])`,
				`CASE WHEN note_visible.yes THEN d.note`,
			} {
				if !strings.Contains(body, required) {
					t.Errorf("the query %s returns the note without %q", name, required)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no query reads the table — this test checked nothing")
	}
}

// Who reads whose figure, at the layer where it is enforced.
func TestDesiredHoursAreReadByTheOwnerTheirLeadAndTheDeansOffice(t *testing.T) {
	t.Parallel()

	f := newDesiredFixture(t)
	f.state(t, testdata.Eins, 12)
	f.state(t, testdata.Zwei, 9.5)
	f.state(t, testdata.Vier, 18)

	lead := testdata.Drei.Actor(principal.KindInteractive, "LECTURER", "SUBJECT_GROUP_LEAD")
	lead.RoleScopes = []principal.RoleScope{{Role: "SUBJECT_GROUP_LEAD", SubjectGroupID: f.group}}
	leadByToken := lead
	leadByToken.Kind = principal.KindToken

	for _, tc := range []struct {
		name  string
		actor principal.Actor
		want  map[uuid.UUID]float64
	}{
		{"a colleague in the same group", testdata.Eins.Actor(principal.KindInteractive, "LECTURER"),
			map[uuid.UUID]float64{testdata.Eins.ID(): 12}},
		{"the lead of MATHE", lead,
			map[uuid.UUID]float64{testdata.Eins.ID(): 12, testdata.Zwei.ID(): 9.5}},
		{"the lead of MATHE, through a token", leadByToken, map[uuid.UUID]float64{}},
		{"the dean's office", testdata.Fuenf.Actor(principal.KindInteractive, "DEANS_OFFICE"),
			map[uuid.UUID]float64{testdata.Eins.ID(): 12, testdata.Zwei.ID(): 9.5, testdata.Vier.ID(): 18}},
		{"the administration", testdata.Fuenf.Actor(principal.KindInteractive, "ADMIN"),
			map[uuid.UUID]float64{}},
	} {
		got := f.seen(t, tc.actor)
		if len(got) != len(tc.want) {
			t.Errorf("%s sees %v, want %v", tc.name, got, tc.want)
			continue
		}
		for id, hours := range tc.want {
			if got[id] != hours {
				t.Errorf("%s sees %v, want %v", tc.name, got, tc.want)
			}
		}
	}

	// Membership is read at query time: Zwei moves to SWE and leaves the MATHE lead's view.
	f.exec(t, `UPDATE person_subject_group SET subject_group_id = $1 WHERE person_id = $2`,
		f.other, testdata.Zwei.ID())
	if _, ok := f.seen(t, lead)[testdata.Zwei.ID()]; ok {
		t.Error("after leaving MATHE, Zwei's figure is still read by its lead")
	}
}

// Stating twice is a correction, nil takes it back, and a semester nobody has decided anything
// about gets its row from the first statement.
func TestStatingDesiredHoursIsACorrectionAndCanBeTakenBack(t *testing.T) {
	t.Parallel()

	f := newDesiredFixture(t)
	actor := testdata.Eins.Actor(principal.KindInteractive, "LECTURER")
	ctx := t.Context()

	first, second := 10.0, 14.5
	if _, err := f.service.SetDesiredHours(ctx, actor, desiredSemester, nil, &first, ""); err != nil {
		t.Fatalf("cannot state: %v", err)
	}
	got, err := f.service.SetDesiredHours(ctx, actor, desiredSemester, nil, &second, "  gern Block  ")
	if err != nil {
		t.Fatalf("cannot correct: %v", err)
	}
	if got.MaxHours != 14.5 || got.MinHours != 14.5 || got.Note != "gern Block" {
		t.Errorf("the correction reads back as %+v", got)
	}
	if n := len(f.seen(t, actor)); n != 1 {
		t.Errorf("stating twice left %d rows, want 1", n)
	}

	if _, err := f.service.SetDesiredHours(ctx, actor, desiredSemester, nil, nil, ""); err != nil {
		t.Fatalf("cannot take it back: %v", err)
	}
	if n := len(f.seen(t, actor)); n != 0 {
		t.Errorf("after taking it back there are %d rows, want none", n)
	}
}

// The domain says it in a sentence before the CHECK says it as a violation, and the CHECK is
// still there for a writer that skips the domain.
func TestDesiredHoursOutsideTheRangeAreRefused(t *testing.T) {
	t.Parallel()

	f := newDesiredFixture(t)
	actor := testdata.Eins.Actor(principal.KindInteractive, "LECTURER")
	for _, h := range []float64{-1, 40.5, 7.3} {
		_, err := f.service.SetDesiredHours(t.Context(), actor, desiredSemester, nil, &h, "")
		if !errors.Is(err, domain.ErrDesiredHoursOutOfRange) {
			t.Errorf("%v hours: got %v, want ErrDesiredHoursOutOfRange", h, err)
		}
	}
	if err := f.store.SetDesiredHours(t.Context(), desiredSemester, testdata.Eins.ID(), 41, 41, "",
		uuid.Nil); err == nil {
		t.Error("the table took 41 hours; desired_teaching_hours_in_range is gone")
	}
}

// A finished semester takes no figure, in either direction.
func TestAFinishedSemesterTakesNoDesiredHours(t *testing.T) {
	t.Parallel()

	f := newDesiredFixture(t)
	f.state(t, testdata.Eins, 12)
	f.exec(t, `UPDATE semester SET phase = 'FINAL' WHERE code = $1`, desiredSemester)

	actor := testdata.Eins.Actor(principal.KindInteractive, "LECTURER")
	h := 8.0
	for _, hours := range []*float64{&h, nil} {
		_, err := f.service.SetDesiredHours(t.Context(), actor, desiredSemester, nil, hours, "")
		if !errors.Is(err, domain.ErrDesiredHoursPhaseClosed) {
			t.Errorf("writing %v in FINAL: got %v, want ErrDesiredHoursPhaseClosed", hours, err)
		}
	}
}

func deansOffice() principal.Actor {
	return testdata.Fuenf.Actor(principal.KindInteractive, "DEANS_OFFICE")
}

func hoursPtr(h float64) *float64 { return &h }

// One figure is both ends, a range reads back as stated, and a minimum above the maximum is a
// sentence. A row the previous image wrote — hours only — reads as a single figure, and one it
// shortened from above has its lower end clamped.
func TestDesiredHoursAreARange(t *testing.T) {
	t.Parallel()

	f := newDesiredFixture(t)
	ctx := t.Context()
	actor := testdata.Eins.Actor(principal.KindInteractive, "LECTURER")

	got, err := f.service.SetDesiredHours(ctx, actor, desiredSemester, hoursPtr(6), hoursPtr(10), "")
	if err != nil {
		t.Fatalf("cannot state a range: %v", err)
	}
	if got.MinHours != 6 || got.MaxHours != 10 {
		t.Errorf("the range reads back as %v–%v, want 6–10", got.MinHours, got.MaxHours)
	}
	got, err = f.service.SetDesiredHours(ctx, actor, desiredSemester, hoursPtr(8), nil, "")
	if err != nil {
		t.Fatalf("cannot state a single figure: %v", err)
	}
	if got.MinHours != 8 || got.MaxHours != 8 {
		t.Errorf("a single figure reads back as %v–%v, want 8–8", got.MinHours, got.MaxHours)
	}
	if _, err := f.service.SetDesiredHours(ctx, actor, desiredSemester, hoursPtr(12), hoursPtr(10),
		""); !errors.Is(err, domain.ErrDesiredHoursRange) {
		t.Errorf("12–10: got %v, want ErrDesiredHoursRange", err)
	}

	// The previous image updates hours and never names min_hours.
	if _, err := f.service.SetDesiredHours(ctx, actor, desiredSemester, hoursPtr(6), hoursPtr(10),
		""); err != nil {
		t.Fatalf("cannot state a range: %v", err)
	}
	f.exec(t, `UPDATE desired_teaching_hours SET hours = 4 WHERE person_id = $1`, testdata.Eins.ID())
	rows, err := f.service.DesiredHours(ctx, actor, desiredSemester, testdata.Eins.ID())
	if err != nil || len(rows) != 1 {
		t.Fatalf("cannot read back: %v, %d rows", err, len(rows))
	}
	if rows[0].MinHours != 4 || rows[0].MaxHours != 4 {
		t.Errorf("after the previous image shortened it: %v–%v, want 4–4",
			rows[0].MinHours, rows[0].MaxHours)
	}
}

// Before the freeze the figure is the person's and a correction is refused; after it, the other
// way round. Publishing needs the freeze first.
func TestTheFreezeHandsTheFigureToTheDeansOffice(t *testing.T) {
	t.Parallel()

	f := newDesiredFixture(t)
	ctx := t.Context()
	eins := testdata.Eins.Actor(principal.KindInteractive, "LECTURER")

	if _, err := f.service.SetDesiredHours(ctx, eins, desiredSemester, hoursPtr(4), hoursPtr(6),
		"erst einmal wenig"); err != nil {
		t.Fatalf("cannot state: %v", err)
	}
	if _, err := f.service.CorrectDesiredHours(ctx, deansOffice(), desiredSemester,
		testdata.Eins.ID(), hoursPtr(8), hoursPtr(10), nil); !errors.Is(err, domain.ErrDesiredHoursNotFrozen) {
		t.Errorf("a correction before the freeze: got %v, want ErrDesiredHoursNotFrozen", err)
	}
	if _, err := f.semesters.PublishDesiredHours(ctx, deansOffice(),
		desiredSemester); !errors.Is(err, domain.ErrDesiredHoursNotFrozen) {
		t.Errorf("publishing before the freeze: got %v, want ErrDesiredHoursNotFrozen", err)
	}
	frozen, err := f.semesters.FreezeDesiredHours(ctx, deansOffice(), desiredSemester)
	if err != nil {
		t.Fatalf("cannot freeze: %v", err)
	}
	again, err := f.semesters.FreezeDesiredHours(ctx, deansOffice(), desiredSemester)
	if err != nil || !again.DesiredHoursFrozenAt.Equal(frozen.DesiredHoursFrozenAt) {
		t.Errorf("freezing twice moved the timestamp or failed: %v", err)
	}

	for _, end := range []*float64{hoursPtr(20), nil} {
		if _, err := f.service.SetDesiredHours(ctx, eins, desiredSemester, end, end,
			""); !errors.Is(err, domain.ErrDesiredHoursFrozen) {
			t.Errorf("writing %v after the freeze: got %v, want ErrDesiredHoursFrozen", end, err)
		}
	}
	byToken := testdata.Fuenf.Actor(principal.KindToken, "DEANS_OFFICE")
	if _, err := f.service.CorrectDesiredHours(ctx, byToken, desiredSemester, testdata.Eins.ID(),
		hoursPtr(8), hoursPtr(10), nil); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("a correction through a token: got %v, want ErrForbidden", err)
	}
	if _, err := f.service.CorrectDesiredHours(ctx, deansOffice(), desiredSemester, uuid.New(),
		hoursPtr(8), hoursPtr(10), nil); !errors.Is(err, domain.ErrDesiredHoursUnknownPerson) {
		t.Errorf("a correction for nobody: got %v, want ErrDesiredHoursUnknownPerson", err)
	}

	// Without a note the person's own stays.
	corrected, err := f.service.CorrectDesiredHours(ctx, deansOffice(), desiredSemester,
		testdata.Eins.ID(), hoursPtr(8), hoursPtr(10), nil)
	if err != nil {
		t.Fatalf("the dean's office cannot correct: %v", err)
	}
	if corrected.MinHours != 8 || corrected.MaxHours != 10 || corrected.CorrectedBy == "" ||
		corrected.Note != "erst einmal wenig" {
		t.Errorf("the correction reads back as %+v", corrected)
	}
	agreed := "abgesprochen"
	corrected, err = f.service.CorrectDesiredHours(ctx, deansOffice(), desiredSemester,
		testdata.Eins.ID(), hoursPtr(8), hoursPtr(10), &agreed)
	if err != nil || corrected.Note != "abgesprochen" {
		t.Errorf("a correction with a note reads back as %+v, %v", corrected, err)
	}
	own, err := f.service.DesiredHours(ctx, eins, desiredSemester, uuid.Nil)
	if err != nil || len(own) != 1 || own[0].CorrectedBy == "" {
		t.Errorf("the person does not see who corrected their figure: %+v, %v", own, err)
	}

	if _, err := f.semesters.PublishDesiredHours(ctx, deansOffice(), desiredSemester); err != nil {
		t.Fatalf("cannot publish a frozen semester: %v", err)
	}
	// The database holds that publication never precedes the freeze, for a writer that skips
	// the query.
	f.exec(t, `INSERT INTO semester (code) VALUES ('2028-WS')`)
	if _, err := f.schema.Pool.Exec(ctx, `UPDATE semester SET desired_hours_published_at = now()
		WHERE code = '2028-WS'`); err == nil {
		t.Error("a semester was published without being frozen; the CHECK is gone")
	}
}

// Published, the range is everybody's through either door; the note and who corrected it keep
// the rule from before.
func TestPublishedDesiredHoursAreEverybodysButNotTheirNotes(t *testing.T) {
	t.Parallel()

	f := newDesiredFixture(t)
	ctx := t.Context()
	eins := testdata.Eins.Actor(principal.KindInteractive, "LECTURER")
	if _, err := f.service.SetDesiredHours(ctx, eins, desiredSemester, hoursPtr(6), hoursPtr(10),
		"Forschungssemester im Herbst"); err != nil {
		t.Fatalf("cannot state: %v", err)
	}

	colleague := testdata.Vier.Actor(principal.KindInteractive, "LECTURER")
	colleagueByToken := testdata.Vier.Actor(principal.KindToken, "LECTURER")
	lead := testdata.Drei.Actor(principal.KindInteractive, "LECTURER", "SUBJECT_GROUP_LEAD")
	lead.RoleScopes = []principal.RoleScope{{Role: "SUBJECT_GROUP_LEAD", SubjectGroupID: f.group}}

	if _, ok := f.seen(t, colleague)[testdata.Eins.ID()]; ok {
		t.Fatal("a colleague in another group reads the figure before publication")
	}

	if _, err := f.semesters.FreezeDesiredHours(ctx, deansOffice(), desiredSemester); err != nil {
		t.Fatalf("cannot freeze: %v", err)
	}
	if _, ok := f.seen(t, colleague)[testdata.Eins.ID()]; ok {
		t.Fatal("freezing published the figure")
	}
	if _, err := f.semesters.PublishDesiredHours(ctx, deansOffice(), desiredSemester); err != nil {
		t.Fatalf("cannot publish: %v", err)
	}

	for _, actor := range []principal.Actor{colleague, colleagueByToken} {
		rows, err := f.service.DesiredHours(ctx, actor, desiredSemester, testdata.Eins.ID())
		if err != nil || len(rows) != 1 {
			t.Fatalf("%s (%s) does not read the published figure: %v, %d rows",
				actor, actor.Kind, err, len(rows))
		}
		if rows[0].MinHours != 6 || rows[0].MaxHours != 10 {
			t.Errorf("%s reads %v–%v, want 6–10", actor, rows[0].MinHours, rows[0].MaxHours)
		}
		if rows[0].Note != "" {
			t.Errorf("%s (%s) reads the note %q", actor, actor.Kind, rows[0].Note)
		}
	}
	rows, err := f.service.DesiredHours(ctx, lead, desiredSemester, testdata.Eins.ID())
	if err != nil || len(rows) != 1 || rows[0].Note == "" {
		t.Errorf("the lead of the person's group lost the note: %+v, %v", rows, err)
	}
}

// The overview lists everybody who teaches, whatever they stated, and a figure the reader may not
// see is the same as none.
func TestTheOverviewListsEverybodyAndOnlyWhatMayBeSeen(t *testing.T) {
	t.Parallel()

	f := newDesiredFixture(t)
	ctx := t.Context()
	f.state(t, testdata.Eins, 12)
	f.state(t, testdata.Zwei, 9.5)
	// Somebody who left: no row in the overview, whatever they stated.
	storetest.SeedPerson(t, f.schema, testdata.Sechs, "LECTURER")
	f.exec(t, `UPDATE person SET active = false WHERE id = $1`, testdata.Sechs.ID())

	overview := func(actor principal.Actor) map[uuid.UUID]domain.DesiredHoursRow {
		t.Helper()
		rows, err := f.service.DesiredHoursOverview(ctx, actor, desiredSemester)
		if err != nil {
			t.Fatalf("cannot read the overview: %v", err)
		}
		out := map[uuid.UUID]domain.DesiredHoursRow{}
		for _, r := range rows {
			out[r.Person.ID] = r
		}
		return out
	}

	zwei := testdata.Zwei.Actor(principal.KindInteractive, "LECTURER")
	before := overview(zwei)
	listed := []uuid.UUID{testdata.Eins.ID(), testdata.Zwei.ID(), testdata.Drei.ID(), testdata.Vier.ID()}
	if len(before) != len(listed) {
		t.Errorf("the overview lists %d people, want %d (no dean's office, nobody inactive)",
			len(before), len(listed))
	}
	for _, id := range listed {
		if _, ok := before[id]; !ok {
			t.Errorf("the overview leaves out %s", id)
		}
	}
	if eins := before[testdata.Eins.ID()]; eins.Stated || eins.MaxHours != 0 {
		t.Errorf("before publication a colleague reads Eins as %+v", eins)
	}
	if own := before[testdata.Zwei.ID()]; !own.Stated || own.MaxHours != 9.5 {
		t.Errorf("Zwei does not read their own figure in the overview: %+v", own)
	}
	if groups := before[testdata.Eins.ID()].SubjectGroups; len(groups) != 1 || groups[0].Code != "MATHE" {
		t.Errorf("Eins is listed in %+v, want MATHE", groups)
	}

	if _, err := f.semesters.FreezeDesiredHours(ctx, deansOffice(), desiredSemester); err != nil {
		t.Fatalf("cannot freeze: %v", err)
	}
	if _, err := f.semesters.PublishDesiredHours(ctx, deansOffice(), desiredSemester); err != nil {
		t.Fatalf("cannot publish: %v", err)
	}
	after := overview(testdata.Zwei.Actor(principal.KindToken, "LECTURER"))
	if eins := after[testdata.Eins.ID()]; !eins.Stated || eins.MaxHours != 12 {
		t.Errorf("after publication Eins reads as %+v, want 12", eins)
	}
	if drei := after[testdata.Drei.ID()]; drei.Stated {
		t.Errorf("Drei stated nothing and reads as %+v", drei)
	}
}
