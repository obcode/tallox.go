package domain

import "sort"

// The vocabularies of the module catalogue.
//
// Three of them, and all three arrive as German prose from the examination office. Each gets a
// Go type here, a CHECK constraint in migration 8 and a GraphQL enum — three homes that cannot
// import one another, so internal/store carries a test comparing this one against the
// constraint, the same way the roles and the phases are kept in step.
//
// Every mapping keeps the phrase it came from alongside the value it produced. That is what
// turns the day the source invents a sixth phrase into a report line rather than a silent fall
// to the default — and a silent fall is the dangerous outcome here, because UNKNOWN is a value
// the demand filter treats as "show it anyway".

// Frequency is how often a module is offered.
//
// The one imported field that is genuinely useful as a filter: a module taught only in the
// summer is not a candidate for a winter semester's demand, and 89 of them are.
type Frequency string

const (
	// FrequencyEverySemester is offered in both terms.
	FrequencyEverySemester Frequency = "EVERY_SEMESTER"
	// FrequencyEveryWinterSemester is offered in winter terms only.
	FrequencyEveryWinterSemester Frequency = "EVERY_WINTER_SEMESTER"
	// FrequencyEverySummerSemester is offered in summer terms only.
	FrequencyEverySummerSemester Frequency = "EVERY_SUMMER_SEMESTER"
	// FrequencyAlternatingWithinSubjectGroup takes turns with other subjects of the same
	// subject group. Which term it lands in is a decision inside the group, not a rule.
	FrequencyAlternatingWithinSubjectGroup Frequency = "ALTERNATING_WITHIN_SUBJECT_GROUP"
	// FrequencyOnAnnouncement is offered when it is announced. The largest group by far — 209
	// modules — and the reason the term filter must never hide what it cannot place.
	FrequencyOnAnnouncement Frequency = "ON_ANNOUNCEMENT"
	// FrequencyUnknown is the source saying nothing, or saying something this version does not
	// recognise. Distinguishable from the two above only by frequency_source.
	FrequencyUnknown Frequency = "UNKNOWN"
)

// AllFrequencies returns every frequency, most definite first.
//
// The order is how much the value constrains a term, which is the order a filter renders them
// in. It carries no other authority.
func AllFrequencies() []Frequency {
	return []Frequency{
		FrequencyEveryWinterSemester,
		FrequencyEverySummerSemester,
		FrequencyEverySemester,
		FrequencyAlternatingWithinSubjectGroup,
		FrequencyOnAnnouncement,
		FrequencyUnknown,
	}
}

// ParseFrequency reports whether s is a frequency this package knows.
func ParseFrequency(s string) (Frequency, bool) {
	for _, f := range AllFrequencies() {
		if string(f) == s {
			return f, true
		}
	}
	return "", false
}

// frequencyPhrases maps what the examination office writes to what this program stores.
//
// Exactly the five phrases the source uses, counted over the whole catalogue: 209 on
// announcement, 90 winter, 89 summer, 73 alternating, 34 every semester, 11 absent. Matched on
// the exact string rather than on a substring — "in jedem Wintersemester" and "in jedem
// Semester" share a prefix, and a substring rule would map a third of them wrongly while
// looking like it worked.
var frequencyPhrases = map[string]Frequency{
	"in jedem Semester":                                      FrequencyEverySemester,
	"in jedem Wintersemester":                                FrequencyEveryWinterSemester,
	"in jedem Sommersemester":                                FrequencyEverySummerSemester,
	"im Wechsel mit anderen Fächern der gleichen Fachgruppe": FrequencyAlternatingWithinSubjectGroup,
	"nach Ankündigung":                                       FrequencyOnAnnouncement,
}

// FrequencyFromSource maps a phrase from the examination office, reporting whether it was one
// this version recognises.
//
// An empty phrase is UNKNOWN and *recognised*: absent is a state the source is entitled to be
// in, and reporting it as unmapped would bury the phrases that genuinely are new under eleven
// modules that simply say nothing.
func FrequencyFromSource(phrase string) (Frequency, bool) {
	if phrase == "" {
		return FrequencyUnknown, true
	}
	if f, ok := frequencyPhrases[phrase]; ok {
		return f, true
	}
	return FrequencyUnknown, false
}

// MatchesTerm reports whether a module of this frequency could be offered in the given term.
//
// Deliberately generous: everything except the wrong one of the two term-specific values
// matches. Alternating, on-announcement and unknown are together 57 % of the catalogue and say
// nothing about the term — hiding them would remove more than half of what a programme lead is
// looking for, in the name of a filter that was meant to help.
//
// term is the letter of a semester code: "W" or "S".
func (f Frequency) MatchesTerm(term string) bool {
	switch f {
	case FrequencyEveryWinterSemester:
		return term == "W"
	case FrequencyEverySummerSemester:
		return term == "S"
	default:
		return true
	}
}

// CourseType is how the teaching of a module is broken up, as the catalogue describes it.
//
// The template the parts of an instance are proposed from, and nothing more: it does not say
// how many laboratory groups there are, and it does not say how the hours divide. Both of those
// are decisions the faculty makes — the first per instance, the second once per module in
// module_component.
type CourseType string

const (
	// CourseTypeSUWithLab is seminar-style teaching with a laboratory. The largest group: 212
	// modules.
	CourseTypeSUWithLab CourseType = "SU_WITH_LAB"
	// CourseTypeSUWithExercise is seminar-style teaching with an exercise class.
	CourseTypeSUWithExercise CourseType = "SU_WITH_EXERCISE"
	// CourseTypeSeminar is a seminar.
	CourseTypeSeminar CourseType = "SEMINAR"
	// CourseTypeLab is a laboratory on its own.
	CourseTypeLab CourseType = "LAB"
	// CourseTypeSU is seminar-style teaching on its own.
	CourseTypeSU CourseType = "SU"
	// CourseTypeExercise is an exercise class on its own.
	CourseTypeExercise CourseType = "EXERCISE"
	// CourseTypeProject is a project.
	CourseTypeProject CourseType = "PROJECT"
	// CourseTypeSelfStudy is independent work — a thesis, a study project. Twelve modules, ten
	// of which carry no hours at all, which is why the hours are nullable.
	CourseTypeSelfStudy CourseType = "SELF_STUDY"
	// CourseTypeDependsOnSubject is the source declining to say, for a slot whose content
	// varies. Also the default for anything unrecognised.
	CourseTypeDependsOnSubject CourseType = "DEPENDS_ON_SUBJECT"
)

// AllCourseTypes returns every course type, most common first.
func AllCourseTypes() []CourseType {
	return []CourseType{
		CourseTypeSUWithLab,
		CourseTypeSUWithExercise,
		CourseTypeSeminar,
		CourseTypeLab,
		CourseTypeSU,
		CourseTypeExercise,
		CourseTypeProject,
		CourseTypeSelfStudy,
		CourseTypeDependsOnSubject,
	}
}

// ParseCourseType reports whether s is a course type this package knows.
func ParseCourseType(s string) (CourseType, bool) {
	for _, c := range AllCourseTypes() {
		if string(c) == s {
			return c, true
		}
	}
	return "", false
}

// courseTypePhrases maps what the examination office writes to what this program stores.
var courseTypePhrases = map[string]CourseType{
	"SU mit Praktikum":       CourseTypeSUWithLab,
	"SU mit Übung":           CourseTypeSUWithExercise,
	"Seminar":                CourseTypeSeminar,
	"Praktikum":              CourseTypeLab,
	"SU":                     CourseTypeSU,
	"Übung":                  CourseTypeExercise,
	"Projekt":                CourseTypeProject,
	"selbständiges Arbeiten": CourseTypeSelfStudy,
	"je nach Fach":           CourseTypeDependsOnSubject,
}

// CourseTypeFromSource maps a phrase from the examination office, reporting whether it was one
// this version recognises.
func CourseTypeFromSource(phrase string) (CourseType, bool) {
	if phrase == "" {
		return CourseTypeDependsOnSubject, true
	}
	if c, ok := courseTypePhrases[phrase]; ok {
		return c, true
	}
	return CourseTypeDependsOnSubject, false
}

// InstancePartKind is what one assignable unit is.
//
// Shared by module_component — the canonical split of a module — and instance_part, the units
// an actual offering runs. The same vocabulary on purpose: the parts of an instance are made
// from the components, and two lists that had to agree without a compiler saying so would drift.
type InstancePartKind string

const (
	// PartKindLecture is a lecture. The part that can be shared between parallel cohorts.
	PartKindLecture InstancePartKind = "LECTURE"
	// PartKindLab is a laboratory group. Usually the part that exists several times over.
	PartKindLab InstancePartKind = "LAB"
	// PartKindExercise is an exercise group.
	PartKindExercise InstancePartKind = "EXERCISE"
	// PartKindSeminar is a seminar group.
	PartKindSeminar InstancePartKind = "SEMINAR"
	// PartKindProject is a project group.
	PartKindProject InstancePartKind = "PROJECT"
	// PartKindOther is anything the five above do not name.
	PartKindOther InstancePartKind = "OTHER"
)

// AllInstancePartKinds returns every kind, in the order the parts of an instance are listed.
func AllInstancePartKinds() []InstancePartKind {
	return []InstancePartKind{
		PartKindLecture,
		PartKindLab,
		PartKindExercise,
		PartKindSeminar,
		PartKindProject,
		PartKindOther,
	}
}

// ParseInstancePartKind reports whether s is a kind this package knows.
func ParseInstancePartKind(s string) (InstancePartKind, bool) {
	for _, k := range AllInstancePartKinds() {
		if string(k) == s {
			return k, true
		}
	}
	return "", false
}

// ProposedComponents is the split this course type suggests, before anybody has stated the real
// one.
//
// # What it is for
//
// The examination office publishes one total and no split, so every division of it is a guess.
// The guess is good enough to plan with — an instance declared from it holds the right number of
// parts and very nearly the right hours — and it is marked as a guess everywhere it is shown, so
// that confirming it stays a deliberate act. What it is never is a stored value: nothing writes
// these to module_component without a person saying so, which is the property that makes that
// table trustworthy.
//
// # The rule, and why the lecture is always even
//
// The laboratory or exercise is the small, fixed quantity — two hours, because that is the unit
// a group is actually taught in — and the lecture takes the rest. Measured against the faculty's
// own planning sheet for the winter of 2026/27, that is what the recurring shapes are: 44
// compulsory modules with six hours and two groups (4+2), 28 with four and one group (2+2), 11
// with eight and three groups (6+2), 14 with three hours (2+1).
//
// Expressed as "the largest even number at most hours-1", which produces exactly those and never
// hands the lecture an odd figure — there is no such thing as a three-hour lecture here, and the
// even split this function used to make produced one for every module with six.
//
// Where that would leave the lecture at zero — a two-hour module whose course type names two
// halves — the proposal is a single part instead, because a part with no hours is not a part.
//
// hours is the module's total contact hours; zero or absent yields no proposal at all rather
// than a row of zeroes, because twelve modules carry no hours and an empty form is a more honest
// starting point than a wrong one.
//
// The returned components carry no id: nobody has stated them, so there is no row to point at.
func (c CourseType) ProposedComponents(hours int) []ModuleComponent {
	if hours <= 0 {
		return nil
	}

	kinds := c.proposedKinds()
	if len(kinds) == 1 {
		return []ModuleComponent{{Kind: kinds[0], TeachingHours: float64(hours), Position: 0}}
	}

	// The largest even number at most hours-1: 4 -> 2, 6 -> 4, 8 -> 6, 3 -> 2, 5 -> 4, 2 -> 0.
	lecture := (hours - 1) / 2 * 2
	if lecture <= 0 {
		return []ModuleComponent{{Kind: kinds[0], TeachingHours: float64(hours), Position: 0}}
	}

	return []ModuleComponent{
		{Kind: kinds[0], TeachingHours: float64(lecture), Position: 0},
		{Kind: kinds[1], TeachingHours: float64(hours - lecture), Position: 1},
	}
}

// proposedKinds is which teachable units this course type names, in the order a split is written.
//
// Separate from the arithmetic above so that "what does SU mit Praktikum consist of" and "how do
// the hours divide" stay two questions. The first is a translation of the source's vocabulary;
// the second is a judgement about teaching.
func (c CourseType) proposedKinds() []InstancePartKind {
	switch c {
	case CourseTypeSUWithLab:
		return []InstancePartKind{PartKindLecture, PartKindLab}
	case CourseTypeSUWithExercise:
		return []InstancePartKind{PartKindLecture, PartKindExercise}
	case CourseTypeSeminar:
		return []InstancePartKind{PartKindSeminar}
	case CourseTypeLab:
		return []InstancePartKind{PartKindLab}
	case CourseTypeExercise:
		return []InstancePartKind{PartKindExercise}
	case CourseTypeProject:
		return []InstancePartKind{PartKindProject}
	case CourseTypeSU:
		return []InstancePartKind{PartKindLecture}
	default:
		return []InstancePartKind{PartKindOther}
	}
}

// FrequencyPhraseMapping returns the phrase-to-value pairs as two parallel slices, sorted by
// phrase so the order is stable.
//
// It exists so that the projection can carry the mapping into SQL as a pair of arrays rather
// than restating it as a CASE expression. That matters: the vocabulary already has three homes
// that cannot import one another, and a fourth — inside a query — would be the one nobody
// remembers to update, in the place where being wrong is silent.
func FrequencyPhraseMapping() (phrases, values []string) {
	phrases = make([]string, 0, len(frequencyPhrases))
	for phrase := range frequencyPhrases {
		phrases = append(phrases, phrase)
	}
	sort.Strings(phrases)

	values = make([]string, 0, len(phrases))
	for _, phrase := range phrases {
		values = append(values, string(frequencyPhrases[phrase]))
	}
	return phrases, values
}

// CourseTypePhraseMapping is FrequencyPhraseMapping for the course type, and exists for the
// same reason.
func CourseTypePhraseMapping() (phrases, values []string) {
	phrases = make([]string, 0, len(courseTypePhrases))
	for phrase := range courseTypePhrases {
		phrases = append(phrases, phrase)
	}
	sort.Strings(phrases)

	values = make([]string, 0, len(phrases))
	for _, phrase := range phrases {
		values = append(values, string(courseTypePhrases[phrase]))
	}
	return phrases, values
}
