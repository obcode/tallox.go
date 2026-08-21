package store_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/store"
	"github.com/obcode/tallox.go/internal/store/storetest"
)

// project runs the projection against the synthetic catalogue and fails the test if it does not
// succeed. Every test below starts here, because a projection that errored proves nothing about
// what it would have written.
func project(t *testing.T, s *storetest.Schema) domain.CatalogueProjection {
	t.Helper()

	result, err := store.NewCatalogue(s.Pool).Project(t.Context(), nil)
	if err != nil {
		t.Fatalf("the projection failed: %v", err)
	}
	if result.Status != domain.ProjectionSucceeded {
		t.Fatalf("the projection ended as %s, want SUCCEEDED", result.Status)
	}
	return result
}

// note returns the finding with this code, or nil if the projection did not report it.
func note(p domain.CatalogueProjection, code domain.ProjectionNoteCode) *domain.CatalogueProjectionNote {
	for i := range p.Notes {
		if p.Notes[i].Code == code {
			return &p.Notes[i]
		}
	}
	return nil
}

func seededSchema(t *testing.T) *storetest.Schema {
	t.Helper()

	s := storetest.New(t)
	storetest.SeedZPACatalogue(t, s)
	return s
}

// The property the nightly job rests on. Running it twice must leave the catalogue exactly as
// once did — otherwise the second night's report is about churn rather than about change.
func TestProjectionIsIdempotent(t *testing.T) {
	t.Parallel()

	s := seededSchema(t)
	ctx := t.Context()

	first := project(t, s)
	second := project(t, s)

	if first.ModulesWritten != second.ModulesWritten ||
		first.OfferingsWritten != second.OfferingsWritten ||
		first.ProgrammesWritten != second.ProgrammesWritten {
		t.Errorf("the second run wrote a different number of rows: %+v then %+v", first, second)
	}
	if second.OfferingsRemoved != 0 {
		t.Errorf("the second run removed %d offering(s); nothing changed between them",
			second.OfferingsRemoved)
	}

	var programmes, modules, offerings int
	err := s.Pool.QueryRow(ctx,
		`SELECT (SELECT count(*) FROM programme),
		        (SELECT count(*) FROM module),
		        (SELECT count(*) FROM module_offering)`).Scan(&programmes, &modules, &offerings)
	if err != nil {
		t.Fatalf("cannot count the catalogue: %v", err)
	}

	// Four programmes: two with regulations and modules, one with regulations and no modules,
	// one with modules and no regulations.
	if programmes != 4 {
		t.Errorf("%d programmes after two runs, want 4", programmes)
	}
	// Eight of the nine fixture modules. The ninth has no home programme.
	if modules != 8 {
		t.Errorf("%d modules after two runs, want 8", modules)
	}
	if offerings == 0 {
		t.Fatal("no offerings at all")
	}
}

// The home programme is NOT NULL by decision, so a module the source gives no owner for cannot
// be stored. What must not happen is that it disappears without a word.
func TestProjectionSkipsModulesWithoutAHomeProgramme(t *testing.T) {
	t.Parallel()

	s := seededSchema(t)

	result := project(t, s)

	var present int
	if err := s.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM module WHERE zpa_module_ref = $1`,
		storetest.FixtureModuleWithoutHome).Scan(&present); err != nil {
		t.Fatalf("cannot look for the module: %v", err)
	}
	if present != 0 {
		t.Error("a module with no home programme was stored; the column is NOT NULL by decision")
	}

	n := note(result, domain.NoteModuleWithoutHomeProgramme)
	if n == nil {
		t.Fatal("the module was dropped and the report says nothing. A silent drop is " +
			"indistinguishable from a catalogue that never had it.")
	}
	if n.Count != 1 {
		t.Errorf("the report counts %d, want 1", n.Count)
	}
	if len(n.Sample) == 0 {
		t.Error("the report has no example, so nobody can go and look at the module in question")
	}
}

// The real catalogue has exactly one programme in this state, with six modules behind it and
// four of them active. Dropping it costs those four; treating it as ordinary puts a programme
// with no students into every picker.
func TestProjectionCreatesAnInactiveProgrammeForAnOwnerWithoutRegulations(t *testing.T) {
	t.Parallel()

	s := seededSchema(t)

	result := project(t, s)
	ctx := t.Context()

	var active bool
	var ref *int64
	err := s.Pool.QueryRow(ctx,
		`SELECT active, zpa_programme_ref FROM programme WHERE code = $1`,
		storetest.FixtureProgrammeZ).Scan(&active, &ref)
	if err != nil {
		t.Fatalf("the programme named only by its modules was not created: %v", err)
	}
	if active {
		t.Error("a programme the source publishes no regulations for reads as active")
	}
	if ref != nil {
		t.Error("it carries a reference to a programme object the source never returned")
	}

	// Its module is planable, which is the entire point of keeping the programme.
	var owned int
	if err := s.Pool.QueryRow(ctx,
		`SELECT count(*) FROM module m JOIN programme p ON p.id = m.home_programme_id
		  WHERE p.code = $1`, storetest.FixtureProgrammeZ).Scan(&owned); err != nil {
		t.Fatalf("cannot count its modules: %v", err)
	}
	if owned != 1 {
		t.Errorf("%d module(s) survived with it, want 1", owned)
	}

	if n := note(result, domain.NoteProgrammeWithoutRegulations); n == nil {
		t.Error("the report does not mention the programme with no regulations")
	}

	// And the mirror image: regulations with no module of their own is not a problem and is
	// simply a programme.
	var otherActive bool
	if err := s.Pool.QueryRow(ctx,
		`SELECT active FROM programme WHERE code = $1`,
		storetest.FixtureProgrammeR).Scan(&otherActive); err != nil {
		t.Fatalf("a programme with regulations and no modules was lost: %v", err)
	}
	if !otherActive {
		t.Error("a programme with published regulations reads as inactive")
	}
}

// The source's module objects carry no name field. Skipping the ones that cannot borrow a name
// would mean a programme lead searching for a module they are responsible for and not finding
// it, which costs more than an ugly row.
func TestProjectionKeepsAModuleWithoutAName(t *testing.T) {
	t.Parallel()

	s := seededSchema(t)

	result := project(t, s)

	var name string
	if err := s.Pool.QueryRow(t.Context(),
		`SELECT name FROM module WHERE zpa_module_ref = $1`,
		storetest.FixtureModuleWithoutName).Scan(&name); err != nil {
		t.Fatalf("the module with no name was dropped: %v", err)
	}
	if name != "" {
		t.Errorf("the module has the name %q; the fixture has grown an association", name)
	}

	if n := note(result, domain.NoteModuleWithoutName); n == nil {
		t.Error("a module was stored without a name and the report does not say so")
	}
}

// The fold module_offering's grain exists to perform: a module in two catalogue slots of one
// set of regulations becomes one offering, keeping both codes and both specialisations.
func TestProjectionCollapsesTheBasketsOfOneSetOfRegulations(t *testing.T) {
	t.Parallel()

	s := seededSchema(t)

	project(t, s)

	var codes, focuses []string
	var isDuty bool
	var minSemester *int32
	var sourceRows int32
	err := s.Pool.QueryRow(t.Context(),
		`SELECT o.module_codes, o.focuses, o.is_duty, o.min_programme_semester, o.source_rows
		   FROM module_offering o
		   JOIN module m ON m.id = o.module_id
		   JOIN spo s ON s.id = o.spo_id
		  WHERE m.zpa_module_ref = $1 AND s.zpa_spo_ref = $2`,
		storetest.FixtureModuleTwoSlots, storetest.FixtureSpoANew).
		Scan(&codes, &focuses, &isDuty, &minSemester, &sourceRows)
	if err != nil {
		t.Fatalf("the two slots did not fold into one offering: %v", err)
	}

	if sourceRows != 2 {
		t.Errorf("the offering says it folded %d source row(s), want 2", sourceRows)
	}
	if len(codes) != 2 {
		t.Errorf("the offering carries %v; both codes belong to it, because the specialisation "+
			"is inside the code", codes)
	}
	if len(focuses) != 1 {
		t.Errorf("the offering carries the specialisations %v, want exactly the one slot that "+
			"has one", focuses)
	}
	if isDuty {
		t.Error("both slots are elective and the fold made the offering compulsory")
	}
	// The two slots disagree about the earliest semester; the lower one wins, and it is
	// reported so nobody has to wonder which.
	if minSemester == nil || *minSemester != 4 {
		t.Errorf("the earliest semester folded to %v, want the lower of the two", minSemester)
	}
}

// Compulsory-or-elective is determined by (module, regulations) and not one level up — measured
// at 0 conflicts over 2386 real pairs. The whole grain rests on it, so if it ever breaks, the
// fold silently picks an answer and the report has to be what says so.
func TestProjectionReportsConflictingDutyFlagsRatherThanHidingThem(t *testing.T) {
	t.Parallel()

	s := seededSchema(t)
	ctx := t.Context()

	if n := note(project(t, s), domain.NoteDutyConflict); n != nil {
		t.Fatalf("the untouched fixture already reports %d duty conflict(s); the case below "+
			"would then prove nothing", n.Count)
	}

	// Put the module of two slots into a compulsory one as well, so that one set of regulations
	// says both things about it.
	if _, err := s.Pool.Exec(ctx,
		`UPDATE zpa_object
		    SET payload = jsonb_set(payload, '{basket}', '{"id": "21", "name": "PA: Pflicht"}')
		  WHERE kind = 'MSBA' AND zpa_id = 1007`); err != nil {
		t.Fatalf("cannot contradict the source: %v", err)
	}

	result := project(t, s)

	n := note(result, domain.NoteDutyConflict)
	if n == nil {
		t.Fatal("one set of regulations now calls a module both compulsory and elective, the " +
			"fold picked one silently, and the report says nothing. This is the assumption " +
			"module_offering's grain rests on.")
	}
	if n.Count != 1 {
		t.Errorf("the report counts %d conflict(s), want 1", n.Count)
	}

	// And the fold takes the weaker claim, so a disagreement never invents an obligation.
	var isDuty bool
	err := s.Pool.QueryRow(ctx,
		`SELECT o.is_duty FROM module_offering o
		   JOIN module m ON m.id = o.module_id
		   JOIN spo s ON s.id = o.spo_id
		  WHERE m.zpa_module_ref = $1 AND s.zpa_spo_ref = $2`,
		storetest.FixtureModuleTwoSlots, storetest.FixtureSpoANew).Scan(&isDuty)
	if err != nil {
		t.Fatalf("cannot read the offering: %v", err)
	}
	if isDuty {
		t.Error("a contradictory source produced a compulsory offering. bool_and resolves " +
			"towards the weaker claim on purpose: telling a programme lead a module is " +
			"compulsory when the regulations disagree is the worse of the two errors.")
	}
}

// 665 real rows point at regulations the endpoint stopped returning. There is no path from one
// to a programme, so it cannot become an offering — and swallowing them would make the
// catalogue look like it had lost a fifth of itself for no reason.
func TestProjectionReportsAssociationsIntoVanishedRegulations(t *testing.T) {
	t.Parallel()

	s := seededSchema(t)

	result := project(t, s)

	n := note(result, domain.NoteAssociationWithUnknownRegulations)
	if n == nil {
		t.Fatal("associations pointing at regulations the source no longer returns were " +
			"dropped without a word")
	}
	if n.Count != 2 {
		t.Errorf("the report counts %d, want the 2 in the fixture", n.Count)
	}
	// Grouped by regulations, because the useful thing to see is which ones, not which rows.
	if len(n.Sample) != 1 {
		t.Errorf("the sample names %v; it should name the sets of regulations rather than the "+
			"associations", n.Sample)
	}

	var offerings int
	if err := s.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM module_offering o JOIN spo s ON s.id = o.spo_id
		  WHERE s.zpa_spo_ref = $1`, storetest.FixtureSpoVanished).Scan(&offerings); err != nil {
		t.Fatalf("cannot count the offerings: %v", err)
	}
	if offerings != 0 {
		t.Errorf("%d offering(s) were created for regulations that have no programme", offerings)
	}
}

// An offering is a claim about somebody else's regulations. When they stop making it, the claim
// goes — which is safe only because nothing references an offering, asserted separately.
func TestProjectionDeletesOfferingsTheSourceDropped(t *testing.T) {
	t.Parallel()

	s := seededSchema(t)
	ctx := t.Context()

	project(t, s)

	var before int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM module_offering`).Scan(&before); err != nil {
		t.Fatalf("cannot count the offerings: %v", err)
	}

	// The importer marks an object gone rather than deleting it; the projection has to notice.
	storetest.RetireZPAObject(t, s, "MSBA", 1003)

	result := project(t, s)

	if result.OfferingsRemoved != 1 {
		t.Errorf("the projection removed %d offering(s), want 1", result.OfferingsRemoved)
	}

	var after int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM module_offering`).Scan(&after); err != nil {
		t.Fatalf("cannot count the offerings: %v", err)
	}
	if after != before-1 {
		t.Errorf("%d offerings remain, want %d", after, before-1)
	}
}

// A module is what an instance, and eventually a wish, points at. Deleting one would cascade
// towards the records this system exists to keep.
func TestProjectionNeverDeletesAModule(t *testing.T) {
	t.Parallel()

	s := seededSchema(t)
	ctx := t.Context()

	project(t, s)

	storetest.RetireZPAObject(t, s, "MODULE", storetest.FixtureModuleOrdinary)

	project(t, s)

	var retiredAt *string
	err := s.Pool.QueryRow(ctx,
		`SELECT retired_at::text FROM module WHERE zpa_module_ref = $1`,
		storetest.FixtureModuleOrdinary).Scan(&retiredAt)
	if err != nil {
		t.Fatalf("the module was deleted when the source stopped mentioning it: %v", err)
	}
	if retiredAt == nil {
		t.Error("the module is still marked as current although the source stopped returning it")
	}

	// And it comes back when the source does, with the same row.
	if _, err := s.Pool.Exec(ctx,
		`UPDATE zpa_object SET gone_at = NULL WHERE kind = 'MODULE' AND zpa_id = $1`,
		storetest.FixtureModuleOrdinary); err != nil {
		t.Fatalf("cannot bring the module back: %v", err)
	}
	project(t, s)

	if err := s.Pool.QueryRow(ctx,
		`SELECT retired_at::text FROM module WHERE zpa_module_ref = $1`,
		storetest.FixtureModuleOrdinary).Scan(&retiredAt); err != nil {
		t.Fatalf("cannot read the module: %v", err)
	}
	if retiredAt != nil {
		t.Error("a module the source returns again is still marked as retired")
	}
}

// The source's `title` is the code again, so the long name is a human's to write. A projection
// that refreshed it would overwrite what they typed on the next nightly run.
func TestProjectionDoesNotOverwriteAHandEditedTitle(t *testing.T) {
	t.Parallel()

	s := seededSchema(t)
	ctx := t.Context()

	project(t, s)

	const typed = "Studiengang mit einem richtigen Namen"
	if _, err := s.Pool.Exec(ctx,
		`UPDATE programme SET title = $1 WHERE code = $2`,
		typed, storetest.FixtureProgrammeA); err != nil {
		t.Fatalf("cannot name the programme: %v", err)
	}

	project(t, s)

	var title string
	if err := s.Pool.QueryRow(ctx,
		`SELECT title FROM programme WHERE code = $1`,
		storetest.FixtureProgrammeA).Scan(&title); err != nil {
		t.Fatalf("cannot read the programme: %v", err)
	}
	if title != typed {
		t.Errorf("the nightly projection overwrote the long name with %q", title)
	}
}

// The split of a module into teachable units is the faculty's knowledge and the source has none.
// An import that could overwrite it would make it unsafe to enter, which would defeat the
// requirement that it be entered.
func TestProjectionDoesNotTouchModuleComponents(t *testing.T) {
	t.Parallel()

	s := seededSchema(t)
	ctx := t.Context()

	project(t, s)

	var moduleID string
	if err := s.Pool.QueryRow(ctx,
		`SELECT id::text FROM module WHERE zpa_module_ref = $1`,
		storetest.FixtureModuleOrdinary).Scan(&moduleID); err != nil {
		t.Fatalf("cannot find the module: %v", err)
	}
	if _, err := s.Pool.Exec(ctx,
		`INSERT INTO module_component (module_id, kind, teaching_hours, position)
		 VALUES ($1, 'LECTURE', 2, 0), ($1, 'LAB', 2, 1)`, moduleID); err != nil {
		t.Fatalf("cannot state the split: %v", err)
	}

	// Change the module in the source, so the projection has a reason to write its row.
	if _, err := s.Pool.Exec(ctx,
		`UPDATE zpa_object SET payload = jsonb_set(payload, '{sws}', '"6"')
		  WHERE kind = 'MODULE' AND zpa_id = $1`, storetest.FixtureModuleOrdinary); err != nil {
		t.Fatalf("cannot change the source: %v", err)
	}
	project(t, s)

	var components int
	if err := s.Pool.QueryRow(ctx,
		`SELECT count(*) FROM module_component WHERE module_id = $1`, moduleID).Scan(&components); err != nil {
		t.Fatalf("cannot count the components: %v", err)
	}
	if components != 2 {
		t.Errorf("%d component(s) survived a projection that rewrote the module, want 2", components)
	}
}

// An unrecognised phrase must become the safe value AND a report line. Only the safe value is
// the dangerous outcome: UNKNOWN is what the term filter treats as "show it anyway", so a
// phrase that quietly fell to it would hide nothing and explain nothing.
func TestProjectionReportsVocabularyItDoesNotRecognise(t *testing.T) {
	t.Parallel()

	s := seededSchema(t)

	result := project(t, s)
	ctx := t.Context()

	var frequency, frequencySource, courseType, courseTypeSource string
	err := s.Pool.QueryRow(ctx,
		`SELECT frequency, frequency_source, course_type, course_type_source
		   FROM module WHERE zpa_module_ref = $1`,
		storetest.FixtureModuleUnknownVocabulary).
		Scan(&frequency, &frequencySource, &courseType, &courseTypeSource)
	if err != nil {
		t.Fatalf("cannot read the module: %v", err)
	}

	if frequency != string(domain.FrequencyUnknown) {
		t.Errorf("an unrecognised frequency became %s rather than UNKNOWN", frequency)
	}
	if courseType != string(domain.CourseTypeDependsOnSubject) {
		t.Errorf("an unrecognised course type became %s rather than the safe value", courseType)
	}
	// The raw phrase is kept, which is what makes the report able to name it.
	if frequencySource != "alle drei Jahre" || courseTypeSource != "Blockveranstaltung" {
		t.Errorf("the phrases were not kept: %q / %q", frequencySource, courseTypeSource)
	}

	// The sample carries the phrase itself rather than a module identifier, and that is the
	// difference between a line somebody can act on and a line somebody has to investigate:
	// adding the mapping needs the words the source used.
	for code, phrase := range map[domain.ProjectionNoteCode]string{
		domain.NoteFrequencyUnmapped:  "alle drei Jahre",
		domain.NoteCourseTypeUnmapped: "Blockveranstaltung",
	} {
		n := note(result, code)
		if n == nil {
			t.Errorf("%s is not reported, so the phrase fell to the default silently", code)
			continue
		}
		if !slices.Contains(n.Sample, phrase) {
			t.Errorf("%s reports %v, which does not name %q — nobody can add a mapping for a "+
				"phrase the report does not print", code, n.Sample, phrase)
		}
	}
}

// "je nach Fach" maps to the same value as an unrecognised phrase, legitimately, for 23 real
// modules. Reporting those every night would train people to ignore the line that exists to be
// noticed.
func TestTheDeliberateFallbackPhraseIsNotReportedAsUnmapped(t *testing.T) {
	t.Parallel()

	s := storetest.New(t)
	storetest.SeedZPACatalogue(t, s)
	ctx := t.Context()

	if _, err := s.Pool.Exec(ctx,
		`UPDATE zpa_object SET payload = jsonb_set(payload, '{course_type}', '"je nach Fach"')
		  WHERE kind = 'MODULE' AND zpa_id = $1`, storetest.FixtureModuleOrdinary); err != nil {
		t.Fatalf("cannot set the phrase: %v", err)
	}

	result := project(t, s)

	n := note(result, domain.NoteCourseTypeUnmapped)
	if n != nil {
		for _, phrase := range n.Sample {
			if phrase == domain.DependsOnSubjectPhrase {
				t.Errorf("%q is reported as unrecognised. The source writes it for 23 modules "+
					"and it maps to the intended value.", phrase)
			}
		}
	}
}

// Both repositories are public and the source's module objects carry a colleague's mail address.
// A comment is not enough here: this walks every text column of every catalogue table and looks.
//
// `teacher` is deliberately not in the list, and that absence is the point rather than an
// oversight: it is the table *about people*, its `mail` column is the link to whoever signs in,
// and it holds exactly the addresses this test keeps out of everywhere else. The list below is
// the catalogue — what a module is, where it counts, how it is split — and none of that is ever
// about a person.
func TestProjectionNeverCopiesTheResponsibleMail(t *testing.T) {
	t.Parallel()

	s := seededSchema(t)
	ctx := t.Context()

	project(t, s)

	tables := []string{
		"programme", "spo", "module", "module_component", "module_offering",
		"course_instance", "instance_part",
	}

	for _, table := range tables {
		rows, err := s.Pool.Query(ctx,
			`SELECT column_name, data_type FROM information_schema.columns
			  WHERE table_schema = current_schema() AND table_name = $1
			    AND data_type IN ('text', 'ARRAY', 'character varying')`, table)
		if err != nil {
			t.Fatalf("cannot read the columns of %s: %v", table, err)
		}

		type column struct{ name, dataType string }
		var columns []column
		for rows.Next() {
			var c column
			if err := rows.Scan(&c.name, &c.dataType); err != nil {
				rows.Close()
				t.Fatalf("cannot read a column: %v", err)
			}
			columns = append(columns, c)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatalf("cannot read the columns of %s: %v", table, err)
		}

		for _, c := range columns {
			expression := `"` + c.name + `"`
			if c.dataType == "ARRAY" {
				expression = `array_to_string(` + expression + `, ' ')`
			}
			var found int
			err := s.Pool.QueryRow(ctx,
				`SELECT count(*) FROM `+table+` WHERE `+expression+` LIKE '%@%'`).Scan(&found)
			if err != nil {
				t.Fatalf("cannot search %s.%s: %v", table, c.name, err)
			}
			if found > 0 {
				t.Errorf("%s.%s holds %d value(s) containing '@'. The source's `responsible` "+
					"field is a colleague's mail address and both code repositories are public.",
					table, c.name, found)
			}
		}
	}
}

// The link the whole fifth endpoint exists for: the source writes an address into the module,
// and the module ends up pointing at a row about that person.
func TestProjectionLinksTheResponsibleTeacher(t *testing.T) {
	t.Parallel()

	s := seededSchema(t)
	ctx := t.Context()

	result := project(t, s)

	if result.TeachersWritten == 0 {
		t.Fatal("no teachers were projected")
	}

	var shortName, mail string
	err := s.Pool.QueryRow(ctx,
		`SELECT t.short_name, t.mail::text
		   FROM module m JOIN teacher t ON t.id = m.responsible_teacher_id
		  WHERE m.zpa_module_ref = $1`, storetest.FixtureModuleOrdinary).Scan(&shortName, &mail)
	if err != nil {
		t.Fatalf("the module is not linked to anybody: %v", err)
	}
	if shortName != "Eins, Prof." {
		t.Errorf("the module points at %q", shortName)
	}
	if mail != "prof.eins@example.org" {
		t.Errorf("the linked teacher carries the address %q", mail)
	}

	// The address is matched case-insensitively on both sides, because the source writes it as
	// somebody typed it and the identity provider decides the casing on the other.
	if _, err := s.Pool.Exec(ctx,
		`UPDATE zpa_object
		    SET payload = jsonb_set(payload, '{responsible}', '"PROF.Eins@Example.ORG"')
		  WHERE kind = 'MODULE' AND zpa_id = $1`, storetest.FixtureModuleOrdinary); err != nil {
		t.Fatalf("cannot change the spelling: %v", err)
	}
	project(t, s)

	var stillLinked int
	if err := s.Pool.QueryRow(ctx,
		`SELECT count(*) FROM module WHERE zpa_module_ref = $1 AND responsible_teacher_id IS NOT NULL`,
		storetest.FixtureModuleOrdinary).Scan(&stillLinked); err != nil {
		t.Fatalf("cannot read the module: %v", err)
	}
	if stillLinked != 1 {
		t.Error("a differently cased address broke the link. The casing comes from the identity " +
			"provider on one side and from whoever typed it on the other; neither decides whether " +
			"a colleague is connected to her own modules.")
	}
}

// Sixteen of 506 real modules name somebody the teacher list does not contain — seven
// placeholders and nine addresses. Neither is stored: an address belongs in the table about
// people, and a placeholder is not a person.
func TestProjectionReportsAResponsibleItCannotResolve(t *testing.T) {
	t.Parallel()

	s := seededSchema(t)
	ctx := t.Context()

	result := project(t, s)

	var linked *string
	if err := s.Pool.QueryRow(ctx,
		`SELECT responsible_teacher_id::text FROM module WHERE zpa_module_ref = $1`,
		storetest.FixtureModuleUnknownVocabulary).Scan(&linked); err != nil {
		t.Fatalf("cannot read the module: %v", err)
	}
	if linked != nil {
		t.Error("a module whose responsible person is a placeholder was linked to somebody")
	}

	n := note(result, domain.NoteModuleResponsibleUnknown)
	if n == nil {
		t.Fatal("the module names nobody the teacher list knows and the report says nothing")
	}
	if n.Count != 1 {
		t.Errorf("the report counts %d, want the one in the fixture", n.Count)
	}
	// Module identifiers, not addresses. The reason the value is not stored is that a mail
	// address belongs in the table about people, and a report is not an exception to that.
	for _, sample := range n.Sample {
		if strings.Contains(sample, "@") {
			t.Errorf("the report carries the address %q", sample)
		}
	}
}

// A link that the source withdraws has to be withdrawn here too. Leaving the old one would make
// a module keep pointing at somebody who is no longer responsible for it — the quiet kind of
// wrong, since nothing about the row would look unusual.
func TestProjectionUnlinksAResponsibleTheSourceChanged(t *testing.T) {
	t.Parallel()

	s := seededSchema(t)
	ctx := t.Context()

	project(t, s)

	if _, err := s.Pool.Exec(ctx,
		`UPDATE zpa_object SET payload = jsonb_set(payload, '{responsible}', '"N.N"')
		  WHERE kind = 'MODULE' AND zpa_id = $1`, storetest.FixtureModuleOrdinary); err != nil {
		t.Fatalf("cannot change the source: %v", err)
	}
	project(t, s)

	var linked *string
	if err := s.Pool.QueryRow(ctx,
		`SELECT responsible_teacher_id::text FROM module WHERE zpa_module_ref = $1`,
		storetest.FixtureModuleOrdinary).Scan(&linked); err != nil {
		t.Fatalf("cannot read the module: %v", err)
	}
	if linked != nil {
		t.Error("the module still points at the person the source no longer names")
	}
}

// Three of 257 real teachers carry no address. They are kept — the source publishes them and a
// module could name one — and reported, because the address is the only link to somebody who
// signs in and without it there can never be one.
func TestProjectionKeepsATeacherWithoutAnAddress(t *testing.T) {
	t.Parallel()

	s := seededSchema(t)
	ctx := t.Context()

	result := project(t, s)

	var mail *string
	var shortName string
	err := s.Pool.QueryRow(ctx,
		`SELECT mail::text, short_name FROM teacher WHERE zpa_teacher_ref = $1`,
		storetest.FixtureTeacherWithoutMail).Scan(&mail, &shortName)
	if err != nil {
		t.Fatalf("the teacher with no address was dropped: %v", err)
	}
	if mail != nil {
		t.Errorf("the teacher carries the address %q; the fixture has grown one", *mail)
	}
	if shortName == "" {
		t.Error("the teacher has no name either, so nothing identifies the row")
	}

	if note(result, domain.NoteTeacherWithoutMail) == nil {
		t.Error("a teacher who can never be connected to a sign-in is not reported")
	}
}

// The decision this migration is really about. Importing the examination office's list of
// teachers must not admit anybody: `person` is the access control of this installation, and
// moving that decision into another institution's database as a side effect of an import is
// exactly what the table structure exists to prevent.
func TestProjectingTeachersAdmitsNobody(t *testing.T) {
	t.Parallel()

	s := seededSchema(t)
	ctx := t.Context()

	project(t, s)

	var people, teachers int
	err := s.Pool.QueryRow(ctx,
		`SELECT (SELECT count(*) FROM person), (SELECT count(*) FROM teacher)`).
		Scan(&people, &teachers)
	if err != nil {
		t.Fatalf("cannot count: %v", err)
	}
	if teachers == 0 {
		t.Fatal("no teachers were projected, so this proves nothing")
	}
	if people != 0 {
		t.Errorf("%d person row(s) appeared from an import. Whoever may use this installation "+
			"is a decision somebody makes; six of the 257 real teachers carry addresses the "+
			"identity provider will never assert.", people)
	}

	// And no foreign key runs from a teacher to a person: the two are connected by the mail
	// address, which is always current, rather than by a column that is as fresh as the last
	// projection.
	rows, err := s.Pool.Query(ctx,
		`SELECT c.conname FROM pg_constraint c
		   JOIN pg_class t ON t.oid = c.conrelid
		   JOIN pg_class r ON r.oid = c.confrelid
		   JOIN pg_namespace n ON n.oid = t.relnamespace
		  WHERE c.contype = 'f' AND n.nspname = current_schema()
		    AND t.relname = 'teacher' AND r.relname = 'person'`)
	if err != nil {
		t.Fatalf("cannot read the foreign keys: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("cannot read a row: %v", err)
		}
		t.Errorf("teacher.%s points at person. The link is the mail address, so that somebody "+
			"admitted this morning is connected to their own modules now rather than tonight.",
			name)
	}
}

// "2026 WS" in the source, `2026-WS` here, so that it can be compared with a semester code —
// and NULL for the thirteen that say "unknown", which is what every guarded coercion in this
// layer does with something it cannot parse.
func TestATeachersLastSemesterIsTranslatedIntoThisSystemsSpelling(t *testing.T) {
	t.Parallel()

	s := seededSchema(t)
	ctx := t.Context()

	project(t, s)

	var code *string
	if err := s.Pool.QueryRow(ctx,
		`SELECT last_semester FROM teacher WHERE zpa_teacher_ref = $1`,
		storetest.FixtureTeacherOrdinary).Scan(&code); err != nil {
		t.Fatalf("cannot read the teacher: %v", err)
	}
	if code == nil || *code != "2026-WS" {
		t.Errorf("last_semester is %v, want 2026-WS", code)
	}

	if _, err := s.Pool.Exec(ctx,
		`UPDATE zpa_object SET payload = jsonb_set(payload, '{last_semester}', '"unknown"')
		  WHERE kind = 'TEACHER' AND zpa_id = $1`, storetest.FixtureTeacherOrdinary); err != nil {
		t.Fatalf("cannot change the source: %v", err)
	}
	project(t, s)

	if err := s.Pool.QueryRow(ctx,
		`SELECT last_semester FROM teacher WHERE zpa_teacher_ref = $1`,
		storetest.FixtureTeacherOrdinary).Scan(&code); err != nil {
		t.Fatalf("cannot read the teacher: %v", err)
	}
	if code != nil {
		t.Errorf("an unparseable semester became %q rather than nothing", *code)
	}
}
