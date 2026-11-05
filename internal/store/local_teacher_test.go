package store_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/graphqltest"
	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
	"github.com/obcode/tallox.go/internal/store"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// Teachers entered by hand, until the ZPA lists them.

// newcomerMail is a lecturer on contract whom the fixture's ZPA list does not contain — and,
// like FixtureTeacherNotAdmitted, none of the personas', so no seeded account collides with it.
const newcomerMail = "lb.neun@example.org"

func newcomer() domain.NewLocalTeacher {
	return domain.NewLocalTeacher{
		Surname:              "Neun",
		GivenName:            "Lehrauftrag",
		Mail:                 newcomerMail,
		IsLecturerOnContract: true,
		Active:               true,
	}
}

func localTeacherSchema(t *testing.T, people ...testdata.Persona) *storetest.Schema {
	t.Helper()

	s := storetest.New(t)
	for _, persona := range people {
		storetest.SeedPerson(t, s, persona, string(policy.RoleSubjectGroupLead))
	}
	storetest.SeedZPACatalogue(t, s)
	project(t, s)
	return s
}

func TestATeacherEnteredByHandIsListedLikeAnyOther(t *testing.T) {
	t.Parallel()

	s := localTeacherSchema(t, testdata.Vier)
	modules := store.NewModules(s.Pool)

	created, err := modules.CreateLocalTeacher(t.Context(), newcomer(), testdata.Vier.ID())
	if err != nil {
		t.Fatalf("cannot enter the teacher: %v", err)
	}
	if !created.EnteredByHand || !created.IsLecturerOnContract || !created.Active || created.IsUser {
		t.Errorf("entered teacher = %+v, want by hand, on contract, active, no account", created)
	}
	if created.Name != "Lehrauftrag Neun" || created.SortName != "Neun, Lehrauftrag" {
		t.Errorf("names = %q / %q, want the given name first for display and surname first for lists",
			created.Name, created.SortName)
	}

	listed, err := modules.Teachers(t.Context(), domain.TeacherFilter{Search: "Neun"})
	if err != nil {
		t.Fatalf("cannot list the teachers: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != created.ID || !listed[0].EnteredByHand {
		t.Errorf("the list shows %+v, want the one entered by hand", listed)
	}

	// The imported ones say so too.
	imported, err := modules.Teachers(t.Context(), domain.TeacherFilter{Search: "Sieben"})
	if err != nil {
		t.Fatalf("cannot list the teachers: %v", err)
	}
	if len(imported) != 1 || imported[0].EnteredByHand {
		t.Errorf("an imported teacher reads as %+v, want not entered by hand", imported)
	}
}

// An address that somebody who teaches, or somebody with an account, already has is one human
// twice. Both are refused with the same sentence, and neither in the database's words.
func TestATakenAddressIsRefusedWithoutSayingWhy(t *testing.T) {
	t.Parallel()

	s := localTeacherSchema(t, testdata.Vier, testdata.Zwei)
	modules := store.NewModules(s.Pool)

	cases := map[string]string{
		"somebody the ZPA lists":            "prof.sieben@example.org",
		"somebody with an account":          testdata.Zwei.Mail,
		"the same, in another casing":       "Prof.Sieben@Example.org",
		"somebody entered by hand already":  newcomerMail,
		"an account, typed with whitespace": "  " + testdata.Zwei.Mail + " ",
	}
	if _, err := modules.CreateLocalTeacher(t.Context(), newcomer(), testdata.Vier.ID()); err != nil {
		t.Fatalf("cannot enter the first teacher: %v", err)
	}

	service := domain.NewCatalogueService(modules, domain.ModuleHandbook{})
	lead := testdata.Vier.Actor(principal.KindInteractive, string(policy.RoleDeansOffice))
	for name, mail := range cases {
		t.Run(name, func(t *testing.T) {
			spec := newcomer()
			spec.Mail = mail
			_, err := service.CreateLocalTeacher(t.Context(), lead, spec)
			if !errors.Is(err, domain.ErrTeacherMailTaken) {
				t.Fatalf("entering %q: err = %v, want ErrTeacherMailTaken", mail, err)
			}
			graphqltest.AssertNoLeak(t, err.Error(), graphqltest.DatabaseNoise()...)
		})
	}
}

func TestOnlyATeacherEnteredByHandCanBeChanged(t *testing.T) {
	t.Parallel()

	s := localTeacherSchema(t, testdata.Vier, testdata.Zwei)
	modules := store.NewModules(s.Pool)

	created, err := modules.CreateLocalTeacher(t.Context(), newcomer(), testdata.Vier.ID())
	if err != nil {
		t.Fatalf("cannot enter the teacher: %v", err)
	}

	changed := newcomer()
	changed.GivenName = "Lehrauftrag Zweitname"
	changed.Active = false
	got, err := modules.UpdateLocalTeacher(t.Context(), created.ID, changed)
	if err != nil {
		t.Fatalf("cannot change the teacher: %v", err)
	}
	if got.Name != "Lehrauftrag Zweitname Neun" || got.Active {
		t.Errorf("changed teacher = %+v, want the new name and inactive", got)
	}

	// An imported row and an id naming nothing are the same answer.
	for name, id := range map[string]uuid.UUID{
		"imported": teacherID(t, s, storetest.FixtureTeacherNotAdmitted),
		"nothing":  uuid.New(),
	} {
		if _, err := modules.UpdateLocalTeacher(t.Context(), id, newcomer()); !errors.Is(err,
			domain.ErrTeacherNotFound) {
			t.Errorf("changing %s: err = %v, want ErrTeacherNotFound", name, err)
		}
	}

	// Moving the address onto an account is the same human twice.
	onto := newcomer()
	onto.Mail = testdata.Zwei.Mail
	if _, err := modules.UpdateLocalTeacher(t.Context(), created.ID, onto); !errors.Is(err,
		domain.ErrTeacherMailTaken) {
		t.Errorf("moving the address onto an account: err = %v, want ErrTeacherMailTaken", err)
	}

	// Once somebody admits the person, their own address is the link working, not a collision.
	if _, err := s.Pool.Exec(t.Context(),
		`INSERT INTO person (mail, name) VALUES ($1, 'Lehrauftrag Neun')`, newcomerMail); err != nil {
		t.Fatalf("cannot admit the teacher: %v", err)
	}
	if _, err := modules.UpdateLocalTeacher(t.Context(), created.ID, newcomer()); err != nil {
		t.Errorf("changing a teacher whose own address has an account: %v", err)
	}
}

// The night the ZPA catches up: the row entered by hand becomes the imported one. Same id, so
// whatever was planned with it stays; the source's names and flags; reported once.
func TestTheImportTakesOverATeacherEnteredByHand(t *testing.T) {
	t.Parallel()

	s := localTeacherSchema(t, testdata.Vier)
	ctx := t.Context()
	modules := store.NewModules(s.Pool)

	created, err := modules.CreateLocalTeacher(ctx, newcomer(), testdata.Vier.ID())
	if err != nil {
		t.Fatalf("cannot enter the teacher: %v", err)
	}
	module := moduleID(t, s, storetest.FixtureModuleOrdinary)
	if _, err := store.NewCompetences(s.Pool).SetForTeacher(ctx, module, created.ID,
		testdata.Vier.ID(), domain.CompetenceCanTeach, ""); err != nil {
		t.Fatalf("cannot state a competence for the teacher: %v", err)
	}

	// The source lists the same human, under the same address in its own casing.
	const ref = 299
	payload, err := json.Marshal(map[string]any{
		"person_id": ref, "email": "LB.Neun@example.org",
		"person_fullname": "Lehrauftrag Neun", "person_shortname": "Neun, Lehrauftrag",
		"is_prof": false, "is_lba": true, "is_profhc": false, "is_staff": false,
		"is_active": true, "fk": "FK07", "last_semester": "2026 WS",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx,
		`INSERT INTO zpa_object (kind, zpa_id, payload) VALUES ('TEACHER', $1, $2)`,
		ref, payload); err != nil {
		t.Fatalf("cannot seed the source's row: %v", err)
	}

	result := project(t, s)
	n := note(result, domain.NoteLocalTeacherAdopted)
	if n == nil || n.Count != 1 || len(n.Sample) != 1 || n.Sample[0] != "299" {
		t.Errorf("LOCAL_TEACHER_ADOPTED = %+v, want the one, sampled by the source's id", n)
	}

	if got := teacherID(t, s, ref); got != created.ID {
		t.Errorf("the imported row is %s, want the one entered by hand (%s)", got, created.ID)
	}
	var count, competences int
	if err := s.Pool.QueryRow(ctx,
		`SELECT count(*) FROM teacher WHERE mail = $1`, newcomerMail).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("%d rows carry the address, want one", count)
	}
	if err := s.Pool.QueryRow(ctx,
		`SELECT count(*) FROM competence WHERE teacher_id = $1`, created.ID).Scan(&competences); err != nil {
		t.Fatal(err)
	}
	if competences != 1 {
		t.Errorf("%d competences point at the teacher after the takeover, want the one", competences)
	}

	listed, err := modules.Teachers(ctx, domain.TeacherFilter{Search: "Neun"})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].EnteredByHand || listed[0].Faculty != "FK07" {
		t.Errorf("after the takeover the list shows %+v, want an imported row with the source's data",
			listed)
	}

	// No longer the faculty's to edit, and the next night has nothing to report.
	if _, err := modules.UpdateLocalTeacher(ctx, created.ID, newcomer()); !errors.Is(err,
		domain.ErrTeacherNotFound) {
		t.Errorf("changing a teacher the import took over: err = %v, want ErrTeacherNotFound", err)
	}
	if again := project(t, s); note(again, domain.NoteLocalTeacherAdopted) != nil {
		t.Error("the second night reports the takeover again")
	}
}

// The constraints, not the statements, keep a local row local.
func TestTheTeacherSourceAndReferenceStayTogether(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	for name, sql := range map[string]string{
		"local with a reference": `INSERT INTO teacher (zpa_teacher_ref, mail, source)
			VALUES (1, 'a@example.org', 'LOCAL')`,
		"imported without one":     `INSERT INTO teacher (mail, source) VALUES ('b@example.org', 'ZPA')`,
		"local without an address": `INSERT INTO teacher (source) VALUES ('LOCAL')`,
		"local and retired": `INSERT INTO teacher (mail, source, retired_at)
			VALUES ('c@example.org', 'LOCAL', now())`,
	} {
		if _, err := s.Pool.Exec(t.Context(), sql); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
