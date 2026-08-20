package domain_test

import (
	"strings"
	"testing"
	"time"

	"github.com/obcode/tallox.go/internal/domain"
)

// at is a moment in Europe/Berlin, which is what main.go sets time.Local to. The turnover
// dates are civil dates in the faculty's calendar, so the zone is part of the question.
func at(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 12, 0, 0, 0, time.Local)
}

// TestCurrentSemesterKnowsTheTurnovers is the one calendar rule this system has.
//
// The interesting half is January to mid-March: it belongs to the winter semester that began
// the previous October, so the code carries the *previous* year. A rule that took the calendar
// year would name the right semester for nine months and the wrong one for three.
func TestCurrentSemesterKnowsTheTurnovers(t *testing.T) {
	t.Parallel()

	cases := []struct {
		when time.Time
		want string
	}{
		{at(2026, time.January, 2), "2025-WS"},
		{at(2026, time.March, 14), "2025-WS"},
		{at(2026, time.March, 15), "2026-SS"},
		{at(2026, time.August, 20), "2026-SS"},
		{at(2026, time.September, 30), "2026-SS"},
		{at(2026, time.October, 1), "2026-WS"},
		{at(2026, time.December, 31), "2026-WS"},
		{at(2027, time.February, 1), "2026-WS"},
	}

	for _, c := range cases {
		if got := domain.CurrentSemester(c.when); got != c.want {
			t.Errorf("%s is in %s, want %s", c.when.Format(time.DateOnly), got, c.want)
		}
	}
}

// TestSemestersAroundWalksTheTermsInOrder covers the arithmetic across the turn of the year,
// which is where a loop over months would go wrong by one.
func TestSemestersAroundWalksTheTermsInOrder(t *testing.T) {
	t.Parallel()

	got := domain.SemestersAround(at(2026, time.August, 20), 2, 3)
	want := []string{"2027-WS", "2027-SS", "2026-WS", "2026-SS", "2025-WS", "2025-SS"}

	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("around 2026-SS = %v, want %v", got, want)
	}
}

// TestSemestersAroundIsChronologicallyDescending asserts the property the whole system's
// ordering rests on, on the generated half of the list rather than on the stored one.
//
// The list is merged with the semesters that come out of the database in `ORDER BY code DESC`,
// and the merge sorts by the code. If the two disagreed about what "newest first" means, the
// page would interleave them.
func TestSemestersAroundIsChronologicallyDescending(t *testing.T) {
	t.Parallel()

	list := domain.SemestersAround(at(2026, time.October, 5), 12, 12)

	for i := 1; i < len(list); i++ {
		if list[i-1] <= list[i] {
			t.Fatalf("%s does not sort after %s", list[i-1], list[i])
		}
	}
}

// TestPlannableIsBoundedInBothDirections is the guard that exists because nothing here can be
// undone: there is no delete and no un-publishing, so a decision recorded for a mistyped year
// would stay in the faculty's planning for good.
func TestPlannableIsBoundedInBothDirections(t *testing.T) {
	t.Parallel()

	now := at(2026, time.August, 20)

	// Ten years either side of 2026-SS, counted in semesters.
	for _, code := range []string{"2026-SS", "2026-WS", "2031-WS", "2036-SS", "2016-SS"} {
		if !domain.IsPlannable(now, code) {
			t.Errorf("%s should be plannable", code)
		}
	}
	for _, code := range []string{"2036-WS", "2015-WS", "9999-WS", "0001-SS"} {
		if domain.IsPlannable(now, code) {
			t.Errorf("%s should be out of range", code)
		}
	}
}
