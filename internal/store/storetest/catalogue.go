package storetest

import (
	"encoding/json"
	"fmt"
	"testing"
)

// The synthetic module catalogue every projection test shares.
//
// # Why synthetic and not the real payloads
//
// There are 3861 real objects sitting in the private development repository, and they must stay
// there. The examination office's module objects carry a `responsible` field holding a
// colleague's mail address, and this repository is public — a fixture copied across that line
// would publish the teaching responsibilities of people who never agreed to appear in it.
//
// # Why it is built rather than checked in as JSON
//
// The projection has to make a decision about nine kinds of untidy input, and each one exists
// in the source in small numbers among thousands of ordinary rows. A file of real data proves
// the projection copes with the data that happened to be there on the day it was captured. This
// catalogue instead contains each case *by construction*, one or two rows each, named after the
// decision it forces — so a test can say "the module with no home programme" and a reader can
// see which row that is.
//
// The shapes are faithful to the source, including the parts that look like mistakes and are
// not: everything is a string, booleans are Python's words for them, absent values arrive as
// the four-letter string "None", and the module objects carry no name at all.
//
// # The cast
//
//	Programmes   PA  the ordinary one, two versions of its regulations
//	             PB  a second one, so that a module can count in two places
//	             PR  regulations but not a single module of its own
//	             PZ  a module of its own but no regulations at all
//	Modules      101 ordinary, compulsory in PA, also counting in PB
//	             102 elective in PA, and compulsory in PA's newer regulations — the case where
//	                 the answer to "compulsory?" depends on which version is being asked about
//	             103 sits in two catalogue slots of one version, which is what folding must cope
//	                 with, and the two slots disagree about the earliest semester
//	             104 no association row at all, therefore no name anywhere
//	             105 the examination office has retired it
//	             106 its home programme is the string "None"
//	             107 belongs to PZ, and its only association points at vanished regulations
//	             108 a frequency and a course type this version has never seen
//	Teachers     201 the person every module names as responsible
//	             202 no address at all — can never be connected to somebody who signs in
//	Module 108 names a responsible person the teacher list does not contain.
const (
	// FixtureProgrammeA is the ordinary programme: two sets of regulations, both real.
	FixtureProgrammeA = "PA"
	// FixtureProgrammeB is the second programme, so that a module can count in two of them and
	// the import/export case has something to be about.
	FixtureProgrammeB = "PB"
	// FixtureProgrammeR has regulations and not one module of its own. Harmless, and worth
	// having: a projection that derived the programme list from the modules would lose it, and
	// the real catalogue has exactly one.
	FixtureProgrammeR = "PR"
	// FixtureProgrammeZ is the mirror image, and it is the awkward one: modules name it as
	// their home and the source publishes no regulations for it at all. The real catalogue has
	// exactly one, with six modules behind it, four of them still active — so dropping it as a
	// data error would cost four active modules, and treating it as an ordinary programme would
	// put one with no students into every picker.
	FixtureProgrammeZ = "PZ"
)

// Fixture object ids, so a test can name the row it is asserting about.
const (
	FixtureSpoAOld     = 11 // PA, version 2019
	FixtureSpoANew     = 12 // PA, version 2025
	FixtureSpoB        = 13 // PB, version 2024
	FixtureSpoR        = 14 // PR, version 2024 — regulations with no module of their own
	FixtureSpoVanished = 19 // referenced by an association, never returned by the SPO endpoint

	FixtureModuleOrdinary          = 101
	FixtureModuleDutyDiffers       = 102
	FixtureModuleTwoSlots          = 103
	FixtureModuleWithoutName       = 104
	FixtureModuleRetired           = 105
	FixtureModuleWithoutHome       = 106
	FixtureModuleOfProgrammeZ      = 107
	FixtureModuleUnknownVocabulary = 108

	// FixtureTeacherOrdinary is named as responsible by every module but one.
	FixtureTeacherOrdinary = 201
	// FixtureTeacherWithoutMail is the case that can never be linked to a person in this
	// installation, because the address is the link. Three of 257 real ones are like this.
	FixtureTeacherWithoutMail = 202
)

// zpaObject is one row of the landing table, in the shape the importer writes.
type zpaObject struct {
	kind    string
	zpaID   int64
	payload map[string]any
	label   string
}

// SeedZPACatalogue fills the ZPA cache with the synthetic catalogue described above.
//
// It writes zpa_object rows directly rather than going through the sync service: what is under
// test is the projection from the cache into the domain tables, and driving an HTTP client to
// arrange the cache would put the thing being tested behind the thing being arranged.
func SeedZPACatalogue(t *testing.T, s *Schema) {
	t.Helper()

	for _, o := range catalogueObjects() {
		payload, err := json.Marshal(o.payload)
		if err != nil {
			t.Fatalf("cannot encode the fixture %s %d: %v", o.kind, o.zpaID, err)
		}
		var label any
		if o.label != "" {
			label = o.label
		}
		if _, err := s.Pool.Exec(t.Context(),
			`INSERT INTO zpa_object (kind, zpa_id, payload, label) VALUES ($1, $2, $3, $4)`,
			o.kind, o.zpaID, payload, label); err != nil {
			t.Fatalf("cannot seed the fixture %s %d: %v", o.kind, o.zpaID, err)
		}
	}
}

// RetireZPAObject marks one cached object as no longer returned by a successful fetch, the way
// the importer does. For the half of the projection that has to notice something went away.
func RetireZPAObject(t *testing.T, s *Schema, kind string, zpaID int64) {
	t.Helper()

	tag, err := s.Pool.Exec(t.Context(),
		`UPDATE zpa_object SET gone_at = now() WHERE kind = $1 AND zpa_id = $2`, kind, zpaID)
	if err != nil {
		t.Fatalf("cannot retire %s %d: %v", kind, zpaID, err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("retiring %s %d touched %d rows — is it in the fixture?",
			kind, zpaID, tag.RowsAffected())
	}
}

func spo(id int64, programmeID int64, programme string, version int, validFrom, primussID string) zpaObject {
	return zpaObject{
		kind:  "SPO",
		zpaID: id,
		payload: map[string]any{
			"spo_id":     fmt.Sprint(id),
			"version":    fmt.Sprint(version),
			"valid_from": validFrom,
			// Empty while a set of regulations is still being entered — the only reliable
			// signal that a version is not finished, and the reason the newest one is not
			// automatically the current one.
			"primuss_id": primussID,
			// The source's `title` is the code again. A projection that wrote it into a long
			// name column would overwrite whatever a human typed there.
			"program":      map[string]any{"id": fmt.Sprint(programmeID), "title": programme},
			"baskets":      []any{},
			"schwerpunkte": []any{},
		},
	}
}

func basket(id int64, name string, isDuty bool, focus string) zpaObject {
	payload := map[string]any{
		"basket_id": fmt.Sprint(id),
		"basket":    name,
		// The only real JSON boolean in the entire source.
		"is_duty": isDuty,
	}
	if focus == "" {
		payload["schwerpunkt"] = nil
	} else {
		payload["schwerpunkt"] = map[string]any{
			"sp_id": "1", "sp_short": focus, "sp_title": focus + " im Langen",
		}
	}
	return zpaObject{kind: "BASKET", zpaID: id, payload: payload}
}

func teacher(id int64, mail, fullName, shortName string, isProf, active bool, faculty string) zpaObject {
	return zpaObject{
		kind:  "TEACHER",
		zpaID: id,
		payload: map[string]any{
			// The one endpoint that types its values: a real integer id and real booleans, not
			// the strings every other kind arrives as.
			"person_id":        id,
			"email":            mail,
			"person_fullname":  fullName,
			"person_shortname": shortName,
			"is_prof":          isProf,
			"is_lba":           false,
			"is_profhc":        false,
			"is_staff":         !isProf,
			"is_active":        active,
			"fk":               faculty,
			// The source's own spelling, with a space. The view turns it into this system's.
			"last_semester": "2026 WS",
		},
		label: shortName,
	}
}

func module(id int64, owner, courseType, frequency string, sws, credits int, active bool) zpaObject {
	return moduleResponsibleTo(id, owner, courseType, frequency, sws, credits, active,
		"prof.eins@example.org")
}

// moduleResponsibleTo is module() with the responsible person spelled out, for the one fixture
// that names somebody the teacher list does not contain.
func moduleResponsibleTo(id int64, owner, courseType, frequency string, sws, credits int,
	active bool, responsible string,
) zpaObject {
	return zpaObject{
		kind:  "MODULE",
		zpaID: id,
		payload: map[string]any{
			// Note what is not here: a name. The module objects carry none, and the name has
			// to be borrowed from an association row — which is why 104 below has none at all.
			"module_id":   fmt.Sprint(id),
			"owner":       owner,
			"course_type": courseType,
			"frequency":   frequency,
			// Numbers as strings, booleans as Python's words for them.
			"sws":           fmt.Sprint(sws),
			"credits":       fmt.Sprint(credits),
			"active":        pythonBool(active),
			"official":      "True",
			"repeater_exam": "False",
			// A mail address, in the shape the source publishes it. It is here on purpose twice
			// over: the projection has to resolve it to a teacher, and the test that asserts it
			// never lands in the module table needs a row that has one.
			"responsible": responsible,
			"languages":   "DE",
			"std_lang":    "DE",
			"effort":      "30 Präsenzstunden Vorlesung, 30 Präsenzstunden Praktikum",
			"content":     "Beispielhafte Inhalte.",
		},
	}
}

func msba(id, moduleID int64, name string, spoID int64, spoVersion int, spoValidFrom string,
	basketID int64, basketName, moduleCode string, minSemester int,
) zpaObject {
	return zpaObject{
		kind:  "MSBA",
		zpaID: id,
		payload: map[string]any{
			"msba_id": fmt.Sprint(id),
			"module":  map[string]any{"id": fmt.Sprint(moduleID), "name": name},
			// The version and validity are embedded here as well as in the SPO object, and that
			// is not redundancy: a fifth of the real association rows point at regulations the
			// SPO endpoint does not return, and this copy is the only place that information
			// exists for them.
			"spo": map[string]any{
				"id": fmt.Sprint(spoID), "version": fmt.Sprint(spoVersion),
				"valid_from": spoValidFrom,
			},
			"basket":               map[string]any{"id": fmt.Sprint(basketID), "name": basketName},
			"module_code":          moduleCode,
			"min_program_semester": fmt.Sprint(minSemester),
			"exam_types":           []any{},
		},
	}
}

func pythonBool(b bool) string {
	if b {
		return "True"
	}
	return "False"
}

// catalogueObjects is the fixture itself: every row, with the decision it exists to force.
func catalogueObjects() []zpaObject {
	const (
		basketDutyA     = 21 // PA: compulsory
		basketElectiveA = 22 // PA: elective
		basketFocusA    = 23 // PA: elective, inside a specialisation
		basketDutyB     = 24 // PB: compulsory
		basketVanished  = 25 // the slot of the regulations the source no longer publishes
	)

	return []zpaObject{
		// --- Regulations -------------------------------------------------------------------
		//
		// PA has two finished versions. That a module can be compulsory in one and elective in
		// the other is the reason the unfiltered catalogue has a third answer, MIXED.
		spo(FixtureSpoAOld, 1, FixtureProgrammeA, 2019, "2019-10-01", "07-PA-2019"),
		spo(FixtureSpoANew, 1, FixtureProgrammeA, 2025, "2025-10-01", "07-PA-2025"),
		spo(FixtureSpoB, 2, FixtureProgrammeB, 2024, "2024-10-01", "07-PB-2024"),
		// Regulations that no module names as its home. The programme is real and has to appear
		// in the list; nothing is wrong with it.
		spo(FixtureSpoR, 3, FixtureProgrammeR, 2024, "2024-10-01", "07-PR-2024"),
		// Note two absences. There is no SPO object for FixtureSpoVanished, although an
		// association below points at it — that is 665 real rows, over 12 sets of regulations
		// the source has stopped returning. And there is none for PZ at all, in any version:
		// its modules name it as their home and the regulations endpoint has never heard of it.

		// --- Catalogue slots ---------------------------------------------------------------
		basket(basketDutyA, "PA: Pflicht", true, ""),
		basket(basketElectiveA, "PA: Wahlpflicht", false, ""),
		basket(basketFocusA, "PA: Vertiefung", false, "SWE"),
		basket(basketDutyB, "PB: Pflicht", true, ""),
		basket(basketVanished, "Alter Katalog", true, ""),

		// --- Modules -----------------------------------------------------------------------
		module(FixtureModuleOrdinary, FixtureProgrammeA, "SU mit Praktikum",
			"in jedem Wintersemester", 4, 5, true),
		module(FixtureModuleDutyDiffers, FixtureProgrammeA, "SU mit Übung",
			"in jedem Sommersemester", 4, 5, true),
		module(FixtureModuleTwoSlots, FixtureProgrammeA, "Seminar",
			"nach Ankündigung", 2, 3, true),
		// No association row anywhere, so nothing to borrow a name from. Active, which is why
		// skipping it would cost a programme lead a module they are responsible for.
		module(FixtureModuleWithoutName, FixtureProgrammeA, "SU", "in jedem Semester", 4, 5, true),
		module(FixtureModuleRetired, FixtureProgrammeA, "SU mit Praktikum",
			"in jedem Wintersemester", 4, 5, false),
		// "None" is a Python None that came through as text. It is not a programme called None,
		// and the difference is the whole reason zpa_text() exists.
		module(FixtureModuleWithoutHome, "None", "SU", "nach Ankündigung", 4, 5, true),
		module(FixtureModuleOfProgrammeZ, FixtureProgrammeZ, "Projekt",
			"nach Ankündigung", 4, 5, true),
		// A phrase in each vocabulary that this version has never seen. Both must become the
		// safe value *and* a report line — a silent fall to the default is how a filter starts
		// hiding the wrong modules.
		// Also the module whose responsible person cannot be resolved: the source writes a
		// placeholder rather than an address for seven real modules, and nine more name an
		// address the teacher list does not contain.
		moduleResponsibleTo(FixtureModuleUnknownVocabulary, FixtureProgrammeA,
			"Blockveranstaltung", "alle drei Jahre", 4, 5, true, "N.N"),

		// --- Teachers -----------------------------------------------------------------------
		teacher(FixtureTeacherOrdinary, "prof.eins@example.org", "Prof. Dr. Eins",
			"Eins, Prof.", true, true, "FK07"),
		// No address. The link to a person in this installation is the address, so this one
		// cannot have one — and that is worth reporting rather than refusing.
		teacher(FixtureTeacherWithoutMail, "", "Ohne Adresse", "Adresse, Ohne", false, false, ""),

		// --- Associations ------------------------------------------------------------------
		//
		// 101: compulsory in both of PA's versions, and it also counts in PB. Two programmes,
		// two demands, and the difference between them is the export figure.
		msba(1001, FixtureModuleOrdinary, "Ordentliches Modul",
			FixtureSpoAOld, 2019, "2019-10-01", basketDutyA, "PA: Pflicht", "PA-M-101", 1),
		msba(1002, FixtureModuleOrdinary, "Ordentliches Modul",
			FixtureSpoANew, 2025, "2025-10-01", basketDutyA, "PA: Pflicht", "PA-M-101", 1),
		msba(1003, FixtureModuleOrdinary, "Ordentliches Modul",
			FixtureSpoB, 2024, "2024-10-01", basketDutyB, "PB: Pflicht", "PB-M-101", 2),

		// 102: elective in the old version, compulsory in the new one. Asked about PA without
		// naming a version, the honest answer is neither of the two.
		msba(1004, FixtureModuleDutyDiffers, "Modul mit wechselnder Pflicht",
			FixtureSpoAOld, 2019, "2019-10-01", basketElectiveA, "PA: Wahlpflicht", "PA-M-102", 4),
		msba(1005, FixtureModuleDutyDiffers, "Modul mit wechselnder Pflicht",
			FixtureSpoANew, 2025, "2025-10-01", basketDutyA, "PA: Pflicht", "PA-M-102", 3),

		// 103: two slots of the *same* version — an ordinary catalogue entry and a
		// specialisation. Folding them is what module_offering's grain is for. They agree about
		// compulsory-or-elective, as every pair in the real catalogue does, and disagree about
		// the earliest semester, as two of them do. Two codes, because the specialisation is
		// inside the code.
		msba(1006, FixtureModuleTwoSlots, "Modul in zwei Körben",
			FixtureSpoANew, 2025, "2025-10-01", basketElectiveA, "PA: Wahlpflicht", "PA-M-103", 5),
		msba(1007, FixtureModuleTwoSlots, "Modul in zwei Körben",
			FixtureSpoANew, 2025, "2025-10-01", basketFocusA, "PA: Vertiefung", "PA-M-SWE-103", 4),

		// 105 is retired by the examination office but still associated. Retired is a flag, not
		// an absence: "what did this programme offer in 2024" stays answerable.
		msba(1008, FixtureModuleRetired, "Zurückgezogenes Modul",
			FixtureSpoAOld, 2019, "2019-10-01", basketDutyA, "PA: Pflicht", "PA-M-105", 1),

		// 107's only association points at the regulations the source no longer returns, which
		// is exactly where the real catalogue parks the modules of a programme it has stopped
		// teaching. It gets a name from here and can never get an offering, because there is no
		// path from these regulations to a programme.
		msba(1009, FixtureModuleOfProgrammeZ, "Modul eines eingestellten Studiengangs",
			FixtureSpoVanished, 2011, "2011-10-01", basketVanished, "Alter Katalog", "", 1),

		// A second association into the vanished regulations, this time for a module that is
		// otherwise perfectly ordinary. It must be reported rather than dropped: 665 real rows
		// are in this state, and a projection that swallowed them would look like a catalogue
		// that lost a fifth of itself for no reason.
		msba(1010, FixtureModuleOrdinary, "Ordentliches Modul",
			FixtureSpoVanished, 2011, "2011-10-01", basketVanished, "Alter Katalog", "", 6),

		// 108 carries the unmapped vocabulary, and is otherwise an ordinary elective.
		msba(1011, FixtureModuleUnknownVocabulary, "Modul mit unbekanntem Vokabular",
			FixtureSpoANew, 2025, "2025-10-01", basketElectiveA, "PA: Wahlpflicht", "PA-M-108", 6),
	}
}
