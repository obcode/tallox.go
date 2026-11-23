package store

import (
	"context"
	"fmt"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/policy"
)

// BigCourseRoster lists the professors of a study year the filter allows — see
// db/queries/big_courses.sql for who counts as one and why this sum over assignments may exist.
func (a *Assignments) BigCourseRoster(ctx context.Context, winter, summer string,
	filter policy.BigCourseFilter,
) ([]domain.BigCourseProfessor, error) {
	rows, err := New(a.pool).BigCourseRoster(ctx, BigCourseRosterParams{
		Winter:  winter,
		Summer:  summer,
		Scope:   string(filter.Scope),
		OwnerID: filter.OwnerID,
	})
	if err != nil {
		return nil, fmt.Errorf("cannot read the professors of the study year: %w", err)
	}
	out := make([]domain.BigCourseProfessor, 0, len(rows))
	for _, r := range rows {
		p := domain.BigCourseProfessor{
			Holder: domain.Assignee{
				TeacherID: r.TeacherID,
				Name:      domain.PlainName(r.Name, r.SortName),
				Mail:      r.Mail,
			},
			SubjectGroups: make([]domain.SubjectGroupRef, 0, len(r.GroupIds)),
		}
		if r.PersonID.Valid {
			p.Holder.PersonID = r.PersonID.UUID
		}
		for i, id := range r.GroupIds {
			p.SubjectGroups = append(p.SubjectGroups, domain.SubjectGroupRef{
				ID: id, Code: r.GroupCodes[i], Name: r.GroupNames[i], Active: r.GroupActive[i],
			})
		}
		out = append(out, p)
	}
	return out, nil
}

// BigCourseHoldings lists what the professors the filter allows hold in a study year, one entry
// per semester, module and programme.
func (a *Assignments) BigCourseHoldings(ctx context.Context, winter, summer string,
	filter policy.BigCourseFilter,
) ([]domain.BigCourseHolding, error) {
	rows, err := New(a.pool).BigCourseHoldings(ctx, BigCourseHoldingsParams{
		Winter:  winter,
		Summer:  summer,
		Scope:   string(filter.Scope),
		OwnerID: filter.OwnerID,
	})
	if err != nil {
		return nil, fmt.Errorf("cannot read the courses of the study year: %w", err)
	}
	out := make([]domain.BigCourseHolding, 0, len(rows))
	for _, r := range rows {
		out = append(out, domain.BigCourseHolding{
			TeacherID:    r.TeacherID,
			SemesterCode: r.Semester,
			ModuleID:     r.ModuleID,
			ModuleName:   r.ModuleName,
			Programme: domain.Programme{
				ID:             r.ProgrammeID,
				Code:           r.ProgrammeCode,
				Title:          r.ProgrammeTitle,
				Active:         r.ProgrammeActive,
				PlanningStatus: domain.ProgrammeStatus(r.ProgrammePlanningStatus),
			},
			Hours:         r.Hours,
			HoldsLecture:  r.HoldsLecture,
			SharedLecture: r.SharedLecture,
			Cohorts:       int(r.Cohorts),
			Eligible:      r.Eligible,
		})
	}
	return out, nil
}
