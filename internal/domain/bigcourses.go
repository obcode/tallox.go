package domain

import (
	"context"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
)

// Big courses: the faculty council's rule that whoever teaches a lot over a study year also holds
// big compulsory or basket courses in it. Decided by the council, made checkable here on
// 2026-11-23:
//
//   - The hours are the teaching hours assigned in the planning, averaged over the winter and the
//     summer of a study year — not the deputat, which also carries theses and reductions.
//   - From BigCourseLoadForTwo hours on average the target is two big courses in the study year,
//     from BigCourseLoadForOne one, below that the rule does not apply.
//   - A course is one module in one programme in one semester, across the cohorts of that
//     programme. Held in the winter and again in the summer, or in two programmes, it counts
//     twice.
//   - It is big when it is compulsory there or in a basket other than the FWP electives, and
//     either the professor holds at least BigCourseHours of it themselves, or it reaches the
//     whole cohort (a single cohort, or a lecture held once for all) and the professor holds that
//     lecture. A two-hour lecture with one laboratory group is the small course the rule is
//     drawn against.
//   - Professors only: neither honorary professors nor lecturers on contract.
//
// The status is advice for the planning and blocks nothing.

const (
	// BigCourseHours is how much of a course a professor holds themselves for it to be big by
	// hours.
	BigCourseHours = 6.0
	// BigCourseLoadForOne is the average teaching from which one big course is the target.
	BigCourseLoadForOne = 10.0
	// BigCourseLoadForTwo is the average teaching from which two big courses are the target.
	BigCourseLoadForTwo = 20.0
)

// BigCourseReason is why a course counts as big.
type BigCourseReason string

const (
	// BigCourseByHours means the professor holds at least BigCourseHours of it.
	BigCourseByHours BigCourseReason = "BY_HOURS"
	// BigCourseByCohort means it reaches the whole cohort and the professor holds the lecture.
	BigCourseByCohort BigCourseReason = "BY_COHORT"
)

// BigCourseStatus is whether a professor meets the target.
type BigCourseStatus string

const (
	// BigCourseFulfilled means at least as many big courses as the target.
	BigCourseFulfilled BigCourseStatus = "FULFILLED"
	// BigCourseNotFulfilled means fewer.
	BigCourseNotFulfilled BigCourseStatus = "NOT_FULFILLED"
	// BigCourseNotApplicable means the average is below BigCourseLoadForOne.
	BigCourseNotApplicable BigCourseStatus = "NOT_APPLICABLE"
)

// BigCourseHolding is what one professor holds of one module in one programme in one semester,
// across the programme's cohorts of it. As the store reads it; Big decides.
type BigCourseHolding struct {
	TeacherID    uuid.UUID
	SemesterCode string
	ModuleID     uuid.UUID
	ModuleName   string
	Programme    Programme
	// Hours are the teaching hours of the professor's own parts.
	Hours float64
	// HoldsLecture: the professor holds a lecture part.
	HoldsLecture bool
	// SharedLecture: that lecture is held once for every cohort.
	SharedLecture bool
	// Cohorts is how many cohorts of the module the programme plans in the semester.
	Cohorts int
	// Eligible: compulsory in the programme, or in a basket other than the FWP electives.
	Eligible bool
}

// Big reports whether the holding is a big course, and why. Hours win over the cohort when both
// hold, because they are the stronger statement about the professor's own share.
func (h BigCourseHolding) Big() (BigCourseReason, bool) {
	switch {
	case !h.Eligible:
		return "", false
	case h.Hours >= BigCourseHours:
		return BigCourseByHours, true
	case h.HoldsLecture && (h.Cohorts <= 1 || h.SharedLecture):
		return BigCourseByCohort, true
	default:
		return "", false
	}
}

// BigCourse is one big course counted for a professor.
type BigCourse struct {
	SemesterCode string
	ModuleID     uuid.UUID
	ModuleName   string
	Programme    Programme
	Hours        float64
	Reason       BigCourseReason
}

// BigCourseProfessor is one professor of the roster, as the store reads it.
type BigCourseProfessor struct {
	// Holder names the professor: the account where there is one, and the teacher record always.
	Holder        Assignee
	SubjectGroups []SubjectGroupRef
}

// BigCourseRow is one professor's study year.
type BigCourseRow struct {
	BigCourseProfessor
	WinterHours float64
	SummerHours float64
	Courses     []BigCourse
}

// AverageHours is the mean of the two semesters.
func (r BigCourseRow) AverageHours() float64 { return (r.WinterHours + r.SummerHours) / 2 }

// Target is how many big courses the rule asks of this professor.
func (r BigCourseRow) Target() int {
	switch avg := r.AverageHours(); {
	case avg >= BigCourseLoadForTwo:
		return 2
	case avg >= BigCourseLoadForOne:
		return 1
	default:
		return 0
	}
}

// Actual is how many big courses were counted.
func (r BigCourseRow) Actual() int { return len(r.Courses) }

// Status is whether the target is met.
func (r BigCourseRow) Status() BigCourseStatus {
	switch target := r.Target(); {
	case target == 0:
		return BigCourseNotApplicable
	case r.Actual() >= target:
		return BigCourseFulfilled
	default:
		return BigCourseNotFulfilled
	}
}

// BigCourseOverview is a study year's overview.
type BigCourseOverview struct {
	StudyYear int
	// Provisional is true while either semester of the study year has not reached the
	// assignment: the figures then stand on a planning that is still being made.
	Provisional bool
	Rows        []BigCourseRow
}

// BigCourseStore is what the overview needs from persistence. Part of AssignmentStore.
type BigCourseStore interface {
	// BigCourseRoster lists the professors of the study year the filter allows.
	BigCourseRoster(ctx context.Context, winter, summer string, filter policy.BigCourseFilter,
	) ([]BigCourseProfessor, error)
	// BigCourseHoldings lists what they hold, one entry per semester, module and programme.
	BigCourseHoldings(ctx context.Context, winter, summer string, filter policy.BigCourseFilter,
	) ([]BigCourseHolding, error)
}

// BigCourseOverview builds the overview of one study year for this actor: every professor for the
// dean's office and the leads, one's own row for everybody else (policy.BigCourseVisibility).
func (s *AssignmentService) BigCourseOverview(ctx context.Context, actor principal.Actor,
	studyYear int,
) (*BigCourseOverview, error) {
	if !actor.Authenticated() {
		return nil, ErrNotAuthenticated
	}
	winter, err := s.semesters.ByCode(ctx, actor, SemesterCode(studyYear, TermWinter))
	if err != nil {
		return nil, err
	}
	summer, err := s.semesters.ByCode(ctx, actor, SemesterCode(studyYear+1, TermSummer))
	if err != nil {
		return nil, err
	}

	filter := policy.BigCourseVisibility(actor)
	roster, err := s.store.BigCourseRoster(ctx, winter.Code, summer.Code, filter)
	if err != nil {
		return nil, err
	}
	holdings, err := s.store.BigCourseHoldings(ctx, winter.Code, summer.Code, filter)
	if err != nil {
		return nil, err
	}
	return &BigCourseOverview{
		StudyYear: studyYear,
		Provisional: winter.Phase.Before(policy.PhaseAssignment) ||
			summer.Phase.Before(policy.PhaseAssignment),
		Rows: bigCourseRows(roster, holdings, winter.Code),
	}, nil
}

// bigCourseRows puts holdings onto the roster, in the roster's order. A holding of somebody not on
// the roster is dropped: both reads carry the same filter, so that is a professor who left the
// roster between the two, and showing hours without a row would be worse than waiting a reload.
func bigCourseRows(roster []BigCourseProfessor, holdings []BigCourseHolding, winter string,
) []BigCourseRow {
	rows := make([]BigCourseRow, len(roster))
	byTeacher := make(map[uuid.UUID]*BigCourseRow, len(roster))
	for i, p := range roster {
		rows[i] = BigCourseRow{BigCourseProfessor: p, Courses: []BigCourse{}}
		byTeacher[p.Holder.TeacherID] = &rows[i]
	}
	for _, h := range holdings {
		row, ok := byTeacher[h.TeacherID]
		if !ok {
			continue
		}
		if h.SemesterCode == winter {
			row.WinterHours += h.Hours
		} else {
			row.SummerHours += h.Hours
		}
		if reason, big := h.Big(); big {
			row.Courses = append(row.Courses, BigCourse{
				SemesterCode: h.SemesterCode,
				ModuleID:     h.ModuleID,
				ModuleName:   h.ModuleName,
				Programme:    h.Programme,
				Hours:        h.Hours,
				Reason:       reason,
			})
		}
	}
	return rows
}
