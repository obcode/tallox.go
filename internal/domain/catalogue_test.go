package domain_test

import (
	"testing"

	"github.com/obcode/tallox.go/internal/domain"
)

// The five phrases below are the entire vocabulary the examination office uses for the
// frequency, counted over all 506 modules of the catalogue. They are spelled out here rather
// than referenced from the map under test, so that a change to the map has to be made twice —
// once as a decision and once as this test agreeing with it.
func TestFrequencyFromSourceMapsEveryPhraseTheSourceUses(t *testing.T) {
	t.Parallel()

	cases := map[string]domain.Frequency{
		"in jedem Semester":                                      domain.FrequencyEverySemester,
		"in jedem Wintersemester":                                domain.FrequencyEveryWinterSemester,
		"in jedem Sommersemester":                                domain.FrequencyEverySummerSemester,
		"im Wechsel mit anderen Fächern der gleichen Fachgruppe": domain.FrequencyAlternatingWithinSubjectGroup,
		"nach Ankündigung":                                       domain.FrequencyOnAnnouncement,
	}

	for phrase, want := range cases {
		t.Run(phrase, func(t *testing.T) {
			t.Parallel()
			got, known := domain.FrequencyFromSource(phrase)
			if !known {
				t.Errorf("the source writes %q for a tenth of the catalogue and this version "+
					"does not recognise it", phrase)
			}
			if got != want {
				t.Errorf("FrequencyFromSource(%q) = %s, want %s", phrase, got, want)
			}
		})
	}
}

// "in jedem Wintersemester" contains "in jedem Semester" as neither a prefix nor a substring,
// but the two are close enough that a rule written with strings.Contains would be tempting.
// This is the case that would break: 90 winter modules mapped to "both terms" and silently
// offered as demand for a summer.
func TestFrequencyDoesNotConfuseTheTermSpecificPhrasesWithTheGeneralOne(t *testing.T) {
	t.Parallel()

	winter, _ := domain.FrequencyFromSource("in jedem Wintersemester")
	if winter.MatchesTerm("S") {
		t.Error("a winter-only module matches a summer semester")
	}
	summer, _ := domain.FrequencyFromSource("in jedem Sommersemester")
	if summer.MatchesTerm("W") {
		t.Error("a summer-only module matches a winter semester")
	}
	both, _ := domain.FrequencyFromSource("in jedem Semester")
	if !both.MatchesTerm("W") || !both.MatchesTerm("S") {
		t.Error("a module offered every semester does not match both terms")
	}
}

// The three indefinite values are 57 % of the catalogue. A term filter that hid them would
// remove more than half of what a programme lead is looking for, which is worse than no filter.
func TestTheIndefiniteFrequenciesMatchEveryTerm(t *testing.T) {
	t.Parallel()

	for _, f := range []domain.Frequency{
		domain.FrequencyOnAnnouncement,
		domain.FrequencyAlternatingWithinSubjectGroup,
		domain.FrequencyUnknown,
	} {
		for _, term := range []string{"W", "S"} {
			if !f.MatchesTerm(term) {
				t.Errorf("%s does not match term %s, so the filter hides a module that says "+
					"nothing about the term", f, term)
			}
		}
	}
}

// An absent value is a state the source is entitled to be in — eleven modules are in it. It has
// to be distinguishable from a phrase nobody has mapped yet, or the report that exists to
// surface the second is buried by the first.
func TestAnAbsentFrequencyIsKnownAndAnUnmappedOneIsNot(t *testing.T) {
	t.Parallel()

	if got, known := domain.FrequencyFromSource(""); !known || got != domain.FrequencyUnknown {
		t.Errorf(`FrequencyFromSource("") = %s, known=%v; want UNKNOWN, known`, got, known)
	}
	got, known := domain.FrequencyFromSource("alle drei Jahre bei Vollmond")
	if known {
		t.Error("a phrase this version has never seen is reported as recognised")
	}
	if got != domain.FrequencyUnknown {
		t.Errorf("an unmapped phrase became %s rather than UNKNOWN", got)
	}
}

func TestCourseTypeFromSourceMapsEveryPhraseTheSourceUses(t *testing.T) {
	t.Parallel()

	cases := map[string]domain.CourseType{
		"SU mit Praktikum":       domain.CourseTypeSUWithLab,
		"SU mit Übung":           domain.CourseTypeSUWithExercise,
		"Seminar":                domain.CourseTypeSeminar,
		"Praktikum":              domain.CourseTypeLab,
		"SU":                     domain.CourseTypeSU,
		"Übung":                  domain.CourseTypeExercise,
		"Projekt":                domain.CourseTypeProject,
		"selbständiges Arbeiten": domain.CourseTypeSelfStudy,
		"je nach Fach":           domain.CourseTypeDependsOnSubject,
	}

	for phrase, want := range cases {
		t.Run(phrase, func(t *testing.T) {
			t.Parallel()
			got, known := domain.CourseTypeFromSource(phrase)
			if !known {
				t.Errorf("the source writes %q and this version does not recognise it", phrase)
			}
			if got != want {
				t.Errorf("CourseTypeFromSource(%q) = %s, want %s", phrase, got, want)
			}
		})
	}
}

// "SU" is a prefix of "SU mit Praktikum" and "SU mit Übung", which together are 350 of the 506
// modules. A prefix rule would turn all three into the same thing and propose a single lecture
// part for two thirds of the catalogue.
func TestCourseTypeDoesNotConfuseSUWithItsCompounds(t *testing.T) {
	t.Parallel()

	plain, _ := domain.CourseTypeFromSource("SU")
	withLab, _ := domain.CourseTypeFromSource("SU mit Praktikum")
	withExercise, _ := domain.CourseTypeFromSource("SU mit Übung")

	if plain == withLab || plain == withExercise || withLab == withExercise {
		t.Errorf("the three SU variants are not distinguished: %s / %s / %s",
			plain, withLab, withExercise)
	}
}

// The proposal is a starting point for a form. Twelve modules carry no hours at all, and for
// those an empty form is more honest than a row that claims a number nobody stated.
func TestNoComponentsAreProposedForAModuleWithoutHours(t *testing.T) {
	t.Parallel()

	for _, hours := range []int{0, -1} {
		if got := domain.CourseTypeSUWithLab.ProposedComponents(hours); got != nil {
			t.Errorf("ProposedComponents(%d) = %v, want nothing", hours, got)
		}
	}
}

func TestTheProposalFollowsTheCourseType(t *testing.T) {
	t.Parallel()

	cases := map[domain.CourseType][]domain.InstancePartKind{
		domain.CourseTypeSUWithLab:      {domain.PartKindLecture, domain.PartKindLab},
		domain.CourseTypeSUWithExercise: {domain.PartKindLecture, domain.PartKindExercise},
		domain.CourseTypeSU:             {domain.PartKindLecture},
		domain.CourseTypeSeminar:        {domain.PartKindSeminar},
		domain.CourseTypeLab:            {domain.PartKindLab},
		// The source declining to say produces one unnamed part rather than a guess.
		domain.CourseTypeDependsOnSubject: {domain.PartKindOther},
		domain.CourseTypeSelfStudy:        {domain.PartKindOther},
	}

	for courseType, want := range cases {
		t.Run(string(courseType), func(t *testing.T) {
			t.Parallel()
			got := courseType.ProposedComponents(4)
			if len(got) != len(want) {
				t.Fatalf("%s proposes %v, want %v", courseType, got, want)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("%s proposes %v, want %v", courseType, got, want)
					break
				}
			}
		})
	}
}

// Every value has to survive the round trip through its string form, because that string is
// what the database column holds and what the GraphQL enum sends.
func TestEveryCatalogueValueParsesBackFromItsStoredForm(t *testing.T) {
	t.Parallel()

	for _, f := range domain.AllFrequencies() {
		if _, ok := domain.ParseFrequency(string(f)); !ok {
			t.Errorf("the frequency %s does not parse back from its own string", f)
		}
	}
	for _, c := range domain.AllCourseTypes() {
		if _, ok := domain.ParseCourseType(string(c)); !ok {
			t.Errorf("the course type %s does not parse back from its own string", c)
		}
	}
	for _, k := range domain.AllInstancePartKinds() {
		if _, ok := domain.ParseInstancePartKind(string(k)); !ok {
			t.Errorf("the part kind %s does not parse back from its own string", k)
		}
	}

	if _, ok := domain.ParseFrequency("SOMETIMES"); ok {
		t.Error("ParseFrequency accepts a value this package does not know")
	}
}
