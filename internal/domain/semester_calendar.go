package domain

import (
	"fmt"
	"time"
)

// The calendar half of the semester: what a code is made of, and which semesters are near.
//
// Separate from the workflow in semester.go because it is the only part of the domain that
// looks at a clock, and because it is what makes "semesters are simply there" true: nobody
// creates a semester, the same way nobody creates next March. A row appears when the first
// decision about a semester is recorded, and until then the semester exists exactly as much as
// it does in the faculty's calendar.
//
// Nothing here decides anything about the *process*. It decides which semesters the list
// offers and how far ahead one may plan — see the note on the turnover dates below.

// Term is the half of the academic year a semester runs in.
type Term string

const (
	// TermSummer is the summer semester: SS 2027 is 2027-SS.
	TermSummer Term = "SS"
	// TermWinter is the winter semester, which spans two calendar years: WS 2026/27 is 2026-WS,
	// named after the year it starts in.
	TermWinter Term = "WS"
)

// The turnover dates: the summer semester runs from 15 March to 30 September, the winter
// semester from 1 October to 14 March.
//
// This is the only place in the system where a date is allowed to say anything about a
// semester, and it is worth being exact about what it may say. It decides which semesters the
// list offers and how far ahead a plan may reach — never which phase anything is in. That
// distinction is the whole reason the phase sits in a column: a fortnight of imprecision here
// moves an entry in a list, where the same imprecision in a phase would move a deadline.
const (
	summerStartMonth = time.March
	summerStartDay   = 15
	winterStartMonth = time.October
)

// How far the list reaches: a year back and three years forward.
//
// Backwards, because "what did we do last winter" is asked while the current semester runs.
// Forwards, because a programme lead deciding today that a module will be offered in three
// years is doing something ordinary, and the tool that answers "that semester does not exist
// yet" is the tool they stop using. Anything anybody has actually touched is listed regardless
// of this window — see SemesterService.List.
const (
	windowBack    = 2
	windowForward = 6
)

// How far a decision may reach: ten years in either direction.
//
// Wider than the window on purpose — the window is what gets offered, this is what is
// *possible* — and bounded rather than open on purpose. Without a bound a mistyped year in
// somebody's script would silently record a decision about the year 9999, and since there is
// no un-publishing and no delete, that row would then be part of the faculty's planning
// forever.
const (
	plannableBack    = 20
	plannableForward = 20
)

// SemesterCode renders a year and a term as the code everything else uses.
func SemesterCode(year int, term Term) string {
	return fmt.Sprintf("%04d-%s", year, term)
}

// CurrentSemester is the semester the given moment falls in.
func CurrentSemester(at time.Time) string {
	year, term := semesterAt(at)
	return SemesterCode(year, term)
}

// SemestersAround lists the semesters near the given moment, newest first.
//
// back and forward count semesters rather than years, because that is the unit the faculty
// plans in and because it keeps the two halves of the range symmetric across a turnover.
func SemestersAround(at time.Time, back, forward int) []string {
	year, term := semesterAt(at)

	// Start at the far end of the future and walk backwards, so the result is newest first
	// without a second pass — the same order as ORDER BY code DESC, which is what the list of
	// recorded semesters arrives in.
	year, term = shift(year, term, forward)

	out := make([]string, 0, back+forward+1)
	for range back + forward + 1 {
		out = append(out, SemesterCode(year, term))
		year, term = shift(year, term, -1)
	}
	return out
}

// IsPlannable reports whether a decision about this semester may be recorded at all.
func IsPlannable(at time.Time, code string) bool {
	for _, plannable := range SemestersAround(at, plannableBack, plannableForward) {
		if plannable == code {
			return true
		}
	}
	return false
}

// semesterAt is the calendar rule: which semester a moment belongs to.
func semesterAt(at time.Time) (int, Term) {
	year := at.Year()

	switch {
	case at.Month() >= winterStartMonth:
		// October to December: the winter semester of this year.
		return year, TermWinter
	case at.Month() > summerStartMonth,
		at.Month() == summerStartMonth && at.Day() >= summerStartDay:
		// Mid-March to September: the summer semester of this year.
		return year, TermSummer
	default:
		// January to mid-March: still the winter semester that began last October.
		return year - 1, TermWinter
	}
}

// shift moves a semester by n steps, forwards or backwards.
func shift(year int, term Term, n int) (int, Term) {
	// Count in half-years and let integer arithmetic carry the year over, rather than looping:
	// a loop here is where an off-by-one at the turn of the year hides.
	half := year*2 + termOffset(term) + n
	if half%2 == 0 {
		return half / 2, TermSummer
	}
	return (half - 1) / 2, TermWinter
}

func termOffset(term Term) int {
	if term == TermWinter {
		return 1
	}
	return 0
}
