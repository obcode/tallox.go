package graph

import (
	"github.com/obcode/tallox.go/graph/model"
	"github.com/obcode/tallox.go/internal/domain"
)

// The big-course overview on the way out. Nothing here filters: which rows exist was decided by
// the WHERE clause of db/queries/big_courses.sql.

func bigCourseOverviewModel(o domain.BigCourseOverview) *model.BigCourseOverview {
	out := &model.BigCourseOverview{
		StudyYear:   o.StudyYear,
		Provisional: o.Provisional,
		Rows:        make([]*model.BigCourseRow, 0, len(o.Rows)),
	}
	for _, r := range o.Rows {
		row := &model.BigCourseRow{
			Professor:     assigneeModel(r.Holder),
			SubjectGroups: make([]*model.SubjectGroupRef, 0, len(r.SubjectGroups)),
			WinterHours:   r.WinterHours,
			SummerHours:   r.SummerHours,
			AverageHours:  r.AverageHours(),
			Target:        r.Target(),
			Actual:        r.Actual(),
			Status:        model.BigCourseStatus(r.Status()),
			Courses:       make([]*model.BigCourse, 0, len(r.Courses)),
		}
		for _, g := range r.SubjectGroups {
			row.SubjectGroups = append(row.SubjectGroups, subjectGroupRefModel(&g))
		}
		for _, c := range r.Courses {
			row.Courses = append(row.Courses, &model.BigCourse{
				Semester:      c.SemesterCode,
				ModuleID:      c.ModuleID.String(),
				ModuleName:    c.ModuleName,
				Programme:     programmeModel(c.Programme),
				TeachingHours: c.Hours,
				Reason:        model.BigCourseReason(c.Reason),
			})
		}
		out.Rows = append(out.Rows, row)
	}
	return out
}
