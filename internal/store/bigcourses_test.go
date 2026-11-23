package store_test

import (
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

// Every query in big_courses.sql sums assignments per person, so every one carries the filter.
func TestEveryBigCourseQueryIsFiltered(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../db/queries/big_courses.sql")
	if err != nil {
		t.Fatalf("cannot read the queries: %v", err)
	}
	sql := string(raw)
	blocks := regexp.MustCompile(`(?m)^-- name: (\w+) :(\w+)$`).FindAllStringSubmatchIndex(sql, -1)
	if len(blocks) == 0 {
		t.Fatal("no query in the file — this test checked nothing")
	}
	for i, block := range blocks {
		name := sql[block[2]:block[3]]
		end := len(sql)
		if i+1 < len(blocks) {
			end = blocks[i+1][0]
		}
		body := sql[block[0]:end]
		for _, required := range []string{
			`sqlc.arg('scope')::text = 'all'`,
			`sqlc.arg('scope')::text = 'own'`,
			`= sqlc.arg(owner_id)::uuid`,
		} {
			if !strings.Contains(body, required) {
				t.Errorf("the query %s sums assignments without %q", name, required)
			}
		}
	}
}

// bigCourseFixture is a study year (2027-WS, 2028-SS) with the cases the faculty council's rule
// turns on. Eins and Zwei are professors with accounts, Drei an honorary professor, Vier a lecturer
// on contract, a sixth professor has no account, a seventh no account and nothing to teach, and
// Fuenf has an account but no record in the examination office's list.
type bigCourseFixture struct {
	schema    *storetest.Schema
	service   *domain.AssignmentService
	noAccount uuid.UUID
	idle      uuid.UUID
}

func newBigCourseFixture(t *testing.T) bigCourseFixture {
	t.Helper()

	s := storetest.New(t)
	ctx := t.Context()
	one := func(sql string, args ...any) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := s.Pool.QueryRow(ctx, sql, args...).Scan(&id); err != nil {
			t.Fatalf("cannot run %q: %v", sql, err)
		}
		return id
	}

	for _, p := range []testdata.Persona{testdata.Eins, testdata.Zwei, testdata.Drei, testdata.Vier,
		testdata.Fuenf} {
		storetest.SeedPerson(t, s, p, "LECTURER")
	}
	ref := 900
	teacher := func(mail, short string, prof, honorary, contract bool) uuid.UUID {
		ref++
		var m any
		if mail != "" {
			m = mail
		}
		return one(`INSERT INTO teacher (mail, full_name, short_name, is_professor,
			is_honorary_professor, is_lecturer_on_contract, zpa_teacher_ref)
			VALUES ($1, $2, $2, $3, $4, $5, $6) RETURNING id`,
			m, short, prof, honorary, contract, ref)
	}
	teacher(testdata.Eins.Mail, "Eins, Prof", true, false, false)
	teacher(testdata.Zwei.Mail, "Zwei, Prof", true, false, false)
	teacher(testdata.Drei.Mail, "Drei, Hon", true, true, false)
	teacher(testdata.Vier.Mail, "Vier, Lba", false, false, true)
	noAccount := teacher("prof.ohne.konto@example.org", "Ohnekonto, Prof", true, false, false)
	idle := teacher("prof.ruhend@example.org", "Ruhend, Prof", true, false, false)

	pa := one(`INSERT INTO programme (code) VALUES ('PA') RETURNING id`)
	pb := one(`INSERT INTO programme (code) VALUES ('PB') RETURNING id`)
	spoA := one(`INSERT INTO spo (programme_id, version) VALUES ($1, 2025) RETURNING id`, pa)
	spoB := one(`INSERT INTO spo (programme_id, version) VALUES ($1, 2025) RETURNING id`, pb)
	winter := one(`INSERT INTO semester (code, phase) VALUES ('2027-WS', 'ASSIGNMENT') RETURNING id`)
	summer := one(`INSERT INTO semester (code) VALUES ('2028-SS') RETURNING id`)

	module := func(name string, spo uuid.UUID, duty bool, baskets ...string) uuid.UUID {
		id := one(`INSERT INTO module (home_programme_id, name) VALUES ($1, $2) RETURNING id`, pa, name)
		if baskets == nil {
			baskets = []string{}
		}
		one(`INSERT INTO module_offering (module_id, spo_id, is_duty, baskets) VALUES ($1, $2, $3, $4)
			RETURNING id`, id, spo, duty, baskets)
		return id
	}
	instance := func(semester, module, programme uuid.UUID, track string) uuid.UUID {
		return one(`INSERT INTO course_instance (semester_id, module_id, programme_id, track)
			VALUES ($1, $2, $3, $4) RETURNING id`, semester, module, programme, track)
	}
	part := func(instance uuid.UUID, kind string, hours float64, shared bool) uuid.UUID {
		return one(`INSERT INTO instance_part (course_instance_id, kind, teaching_hours, serves_sibling_tracks)
			VALUES ($1, $2, $3, $4) RETURNING id`, instance, kind, hours, shared)
	}
	byPerson := func(part uuid.UUID, p testdata.Persona) {
		one(`INSERT INTO assignment (instance_part_id, person_id) VALUES ($1, $2) RETURNING id`, part, p.ID())
	}
	byTeacher := func(part, teacher uuid.UUID) {
		one(`INSERT INTO assignment (instance_part_id, teacher_id) VALUES ($1, $2) RETURNING id`, part, teacher)
	}

	// A lecture held once for both cohorts: Eins holds it, two hours, big by cohort.
	shared := module("Geteilt", spoA, true)
	sharedA, sharedB := instance(winter, shared, pa, "A"), instance(winter, shared, pa, "B")
	byPerson(part(sharedA, "LECTURE", 2, true), testdata.Eins)
	part(sharedA, "LAB", 2, false)
	part(sharedB, "LAB", 2, false)

	// Two cohorts with a lecture each: Zwei holds lecture and laboratory of A, four hours, small.
	// The lecturer on contract and the honorary professor hold B.
	small := module("Klein", spoA, true)
	smallA, smallB := instance(winter, small, pa, "A"), instance(winter, small, pa, "B")
	byPerson(part(smallA, "LECTURE", 2, false), testdata.Zwei)
	byPerson(part(smallA, "LAB", 2, false), testdata.Zwei)
	byPerson(part(smallB, "LECTURE", 2, false), testdata.Vier)
	byPerson(part(smallB, "LAB", 2, false), testdata.Drei)

	// A single cohort in another programme: the lecturer's course is big, the laboratory's is not.
	master := module("Master", spoB, true)
	masterI := instance(winter, master, pb, "")
	byPerson(part(masterI, "LECTURE", 4, false), testdata.Eins)
	byPerson(part(masterI, "LAB", 2, false), testdata.Zwei)

	// A basket course, eight hours held by a professor without an account: big by hours.
	basket := module("Korb", spoA, false, "PA: Vertiefung")
	basketI := instance(winter, basket, pa, "")
	byTeacher(part(basketI, "LECTURE", 4, false), noAccount)
	byTeacher(part(basketI, "LAB", 2, false), noAccount)
	byTeacher(part(basketI, "LAB", 2, false), noAccount)

	// An FWP elective: eight hours and a single cohort, and still not big. Counts as hours.
	fwp := module("Wahlfach", spoA, false, "FWP")
	fwpI := instance(winter, fwp, pa, "")
	byPerson(part(fwpI, "LECTURE", 4, false), testdata.Eins)
	byPerson(part(fwpI, "LAB", 4, false), testdata.Eins)

	// The summer: a compulsory course with eight hours, and the shared course again — twice.
	summerDuty := module("Sommer", spoA, true)
	summerI := instance(summer, summerDuty, pa, "")
	byPerson(part(summerI, "LECTURE", 4, false), testdata.Eins)
	byPerson(part(summerI, "LAB", 4, false), testdata.Eins)
	sharedSummer := instance(summer, shared, pa, "")
	byPerson(part(sharedSummer, "LECTURE", 2, false), testdata.Eins)
	byPerson(part(sharedSummer, "LAB", 2, false), testdata.Eins)
	// Another faculty's part counts for nobody.
	if _, err := s.Pool.Exec(ctx, `INSERT INTO instance_part (course_instance_id, kind, teaching_hours, taught_by)
		VALUES ($1, 'LAB', 4, 'FK 04')`, sharedSummer); err != nil {
		t.Fatalf("cannot hand a part to another faculty: %v", err)
	}

	return bigCourseFixture{
		schema: s,
		service: domain.NewAssignmentService(store.NewAssignments(s.Pool),
			domain.NewSemesterService(store.NewSemesters(s.Pool), nil)),
		noAccount: noAccount,
		idle:      idle,
	}
}

func (f bigCourseFixture) rows(t *testing.T, actor principal.Actor) (*domain.BigCourseOverview,
	map[string]domain.BigCourseRow) {
	t.Helper()
	o, err := f.service.BigCourseOverview(t.Context(), actor, 2027)
	if err != nil {
		t.Fatalf("cannot read the overview: %v", err)
	}
	out := map[string]domain.BigCourseRow{}
	for _, r := range o.Rows {
		out[r.Holder.Name] = r
	}
	return o, out
}

func TestTheBigCourseOverviewCountsTheCouncilsCases(t *testing.T) {
	t.Parallel()

	f := newBigCourseFixture(t)
	deans := testdata.Fuenf.Actor(principal.KindInteractive, "DEANS_OFFICE")
	o, rows := f.rows(t, deans)

	if !o.Provisional {
		t.Error("the summer has not reached the assignment, and the overview is not provisional")
	}
	if len(rows) != 3 {
		t.Fatalf("the overview lists %d professors, want Eins, Zwei and the one without an account: %v",
			len(rows), rows)
	}

	eins, ok := rows["Prof Eins"]
	if !ok {
		t.Fatalf("Eins is missing: %v", rows)
	}
	if eins.Holder.PersonID != testdata.Eins.ID() {
		t.Errorf("Eins's row names account %s", eins.Holder.PersonID)
	}
	if eins.WinterHours != 14 || eins.SummerHours != 12 || eins.AverageHours() != 13 {
		t.Errorf("Eins holds %v + %v hours, want 14 + 12 (the other faculty's part not counted)",
			eins.WinterHours, eins.SummerHours)
	}
	got := []string{}
	for _, c := range eins.Courses {
		got = append(got, c.SemesterCode+" "+c.Programme.Code+" "+c.ModuleName+" "+string(c.Reason))
	}
	want := []string{
		"2027-WS PA Geteilt BY_COHORT",
		"2027-WS PB Master BY_COHORT",
		"2028-SS PA Geteilt BY_COHORT",
		"2028-SS PA Sommer BY_HOURS",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("Eins's big courses are\n  %v\nwant\n  %v", got, want)
	}
	if eins.Target() != 1 || eins.Status() != domain.BigCourseFulfilled {
		t.Errorf("Eins: target %d, status %s; want 1, FULFILLED", eins.Target(), eins.Status())
	}

	zwei := rows["Prof Zwei"]
	if zwei.WinterHours != 6 || len(zwei.Courses) != 0 || zwei.Status() != domain.BigCourseNotApplicable {
		t.Errorf("Zwei: %v hours, %d big courses, %s; want 6, none (two cohorts with own lectures, "+
			"a laboratory only), NOT_APPLICABLE", zwei.WinterHours, len(zwei.Courses), zwei.Status())
	}

	other := rows["Prof Ohnekonto"]
	if other.Holder.TeacherID != f.noAccount || other.Holder.PersonID != uuid.Nil {
		t.Errorf("the professor without an account reads as %+v", other.Holder)
	}
	if other.WinterHours != 8 || len(other.Courses) != 1 || other.Courses[0].Reason != domain.BigCourseByHours {
		t.Errorf("the professor without an account: %v hours, courses %+v; want 8 and the basket by hours",
			other.WinterHours, other.Courses)
	}
	if _, listed := rows["Prof Ruhend"]; listed {
		t.Error("a professor with no account and nothing to teach is listed")
	}
}

// Who reads which rows: the leads and the dean's office everybody's, everybody else their own —
// and the hours of the others reach them neither as a row nor inside their own.
func TestTheBigCourseOverviewIsFiltered(t *testing.T) {
	t.Parallel()

	f := newBigCourseFixture(t)
	for _, tc := range []struct {
		name  string
		actor principal.Actor
		want  []string
	}{
		{"a colleague", testdata.Zwei.Actor(principal.KindInteractive, "LECTURER"), []string{"Prof Zwei"}},
		{"the dean's office by plain token", testdata.Eins.Actor(principal.KindToken, "DEANS_OFFICE"),
			[]string{"Prof Eins"}},
		{"somebody who is not a professor", testdata.Fuenf.Actor(principal.KindInteractive, "LECTURER"), nil},
		{"the honorary professor", testdata.Drei.Actor(principal.KindInteractive, "LECTURER"), nil},
		{"the dean's office", testdata.Fuenf.Actor(principal.KindInteractive, "DEANS_OFFICE"),
			[]string{"Prof Eins", "Prof Ohnekonto", "Prof Zwei"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			o, _ := f.rows(t, tc.actor)
			got := []string{}
			for _, r := range o.Rows {
				got = append(got, r.Holder.Name)
			}
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("reads %v, want %v", got, tc.want)
			}
		})
	}

	// The own row of a colleague carries their own hours and nothing of anybody else's.
	o, _ := f.rows(t, testdata.Zwei.Actor(principal.KindToken, "LECTURER"))
	if len(o.Rows) != 1 || o.Rows[0].WinterHours != 6 {
		t.Errorf("Zwei by token reads %+v, want their own 6 hours", o.Rows)
	}
}
