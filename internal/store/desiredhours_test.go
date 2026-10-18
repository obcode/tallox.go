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
	schema  *storetest.Schema
	store   *store.Wishes
	service *domain.WishService
	group   uuid.UUID
	other   uuid.UUID
}

// Eins and Zwei in MATHE, Drei leads MATHE, Vier is in SWE.
func newDesiredFixture(t *testing.T) desiredFixture {
	t.Helper()

	s := storetest.New(t)
	storetest.SeedPerson(t, s, testdata.Eins, "LECTURER")
	storetest.SeedPerson(t, s, testdata.Zwei, "LECTURER")
	storetest.SeedPerson(t, s, testdata.Drei, "LECTURER", "SUBJECT_GROUP_LEAD")
	storetest.SeedPerson(t, s, testdata.Vier, "LECTURER")

	wishes := store.NewWishes(s.Pool)
	f := desiredFixture{
		schema:  s,
		store:   wishes,
		service: domain.NewWishService(wishes, domain.NewSemesterService(store.NewSemesters(s.Pool), nil)),
		group:   seedSubjectGroup(t, s, "MATHE", "Mathematik"),
		other:   seedSubjectGroup(t, s, "SWE", "Softwarefächer"),
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
	if _, err := f.service.SetDesiredHours(t.Context(), actor, desiredSemester, &hours, ""); err != nil {
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
		out[r.Person.ID] = r.Hours
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
		if kind != "many" || !strings.Contains(body, "FROM desired_teaching_hours d") {
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
	if _, err := f.service.SetDesiredHours(ctx, actor, desiredSemester, &first, ""); err != nil {
		t.Fatalf("cannot state: %v", err)
	}
	got, err := f.service.SetDesiredHours(ctx, actor, desiredSemester, &second, "  gern Block  ")
	if err != nil {
		t.Fatalf("cannot correct: %v", err)
	}
	if got.Hours != 14.5 || got.Note != "gern Block" {
		t.Errorf("the correction reads back as %+v", got)
	}
	if n := len(f.seen(t, actor)); n != 1 {
		t.Errorf("stating twice left %d rows, want 1", n)
	}

	if _, err := f.service.SetDesiredHours(ctx, actor, desiredSemester, nil, ""); err != nil {
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
		_, err := f.service.SetDesiredHours(t.Context(), actor, desiredSemester, &h, "")
		if !errors.Is(err, domain.ErrDesiredHoursOutOfRange) {
			t.Errorf("%v hours: got %v, want ErrDesiredHoursOutOfRange", h, err)
		}
	}
	if _, err := f.store.SetDesiredHours(t.Context(), desiredSemester, testdata.Eins.ID(), 41, ""); err == nil {
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
		_, err := f.service.SetDesiredHours(t.Context(), actor, desiredSemester, hours, "")
		if !errors.Is(err, domain.ErrDesiredHoursPhaseClosed) {
			t.Errorf("writing %v in FINAL: got %v, want ErrDesiredHoursPhaseClosed", hours, err)
		}
	}
}
