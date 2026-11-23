package domain_test

import (
	"testing"

	"github.com/obcode/tallox.go/internal/domain"
)

// When a course is big, said as the faculty council's cases.
func TestWhenACourseIsBig(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		holding domain.BigCourseHolding
		want    domain.BigCourseReason
	}{
		{"six hours of a compulsory course",
			domain.BigCourseHolding{Eligible: true, Hours: 6, Cohorts: 2},
			domain.BigCourseByHours},
		{"six hours, lecture of a single cohort as well: hours win",
			domain.BigCourseHolding{Eligible: true, Hours: 6, HoldsLecture: true, Cohorts: 1},
			domain.BigCourseByHours},
		{"a four-hour lecture of a single cohort",
			domain.BigCourseHolding{Eligible: true, Hours: 4, HoldsLecture: true, Cohorts: 1},
			domain.BigCourseByCohort},
		{"a lecture held once for both cohorts",
			domain.BigCourseHolding{Eligible: true, Hours: 2, HoldsLecture: true, SharedLecture: true, Cohorts: 2},
			domain.BigCourseByCohort},
		{"two hours of lecture and a laboratory group in one of two cohorts",
			domain.BigCourseHolding{Eligible: true, Hours: 4, HoldsLecture: true, Cohorts: 2},
			""},
		{"only a laboratory group of a single cohort",
			domain.BigCourseHolding{Eligible: true, Hours: 2, Cohorts: 1},
			""},
		{"just under six hours",
			domain.BigCourseHolding{Eligible: true, Hours: 5.99, Cohorts: 2},
			""},
		{"an FWP elective with eight hours",
			domain.BigCourseHolding{Eligible: false, Hours: 8, HoldsLecture: true, Cohorts: 1},
			""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reason, big := tc.holding.Big()
			if big != (tc.want != "") || reason != tc.want {
				t.Errorf("Big() = %q, %v; want %q", reason, big, tc.want)
			}
		})
	}
}

// The target follows the average, at the edges the council named.
func TestBigCourseTargetAndStatus(t *testing.T) {
	t.Parallel()

	courses := func(n int) []domain.BigCourse { return make([]domain.BigCourse, n) }
	for _, tc := range []struct {
		name           string
		winter, summer float64
		actual         int
		target         int
		status         domain.BigCourseStatus
	}{
		{"nothing", 0, 0, 0, 0, domain.BigCourseNotApplicable},
		{"just under ten", 9.5, 9.5, 0, 0, domain.BigCourseNotApplicable},
		{"ten on average", 12, 8, 0, 1, domain.BigCourseNotFulfilled},
		{"ten with one", 10, 10, 1, 1, domain.BigCourseFulfilled},
		{"just under twenty with one", 19.5, 19.5, 1, 1, domain.BigCourseFulfilled},
		{"twenty with one", 18, 22, 1, 2, domain.BigCourseNotFulfilled},
		{"twenty with two", 20, 20, 2, 2, domain.BigCourseFulfilled},
		{"twenty with three", 24, 18, 3, 2, domain.BigCourseFulfilled},
		{"under ten with a big course", 4, 4, 1, 0, domain.BigCourseNotApplicable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			row := domain.BigCourseRow{WinterHours: tc.winter, SummerHours: tc.summer,
				Courses: courses(tc.actual)}
			if got := row.Target(); got != tc.target {
				t.Errorf("Target() = %d, want %d", got, tc.target)
			}
			if got := row.Status(); got != tc.status {
				t.Errorf("Status() = %s, want %s", got, tc.status)
			}
		})
	}
}
