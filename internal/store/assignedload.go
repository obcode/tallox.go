package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/policy"
)

// AssignedLoad sums what each person the filter allows already holds in a semester, by subject
// group. The filter is over people, not over assignments — see db/queries/assigned_load.sql for
// why this is the one sum over assignments that may exist.
func (a *Assignments) AssignedLoad(ctx context.Context, semesterCode string, person uuid.UUID,
	filter policy.AssignedLoadFilter) ([]domain.AssignedLoad, error) {
	scope, owner, groups := desiredHoursFilterParams(filter)
	params := AssignedLoadOfSemesterParams{
		Semester:        semesterCode,
		Scope:           scope,
		OwnerID:         owner,
		SubjectGroupIds: groups,
	}
	if person != uuid.Nil {
		params.Person = uuid.NullUUID{UUID: person, Valid: true}
	}
	rows, err := New(a.pool).AssignedLoadOfSemester(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("cannot read the assigned load: %w", err)
	}

	var out []domain.AssignedLoad
	for _, r := range rows {
		// The rows arrive ordered by person, so a new person starts a new entry.
		if len(out) == 0 || out[len(out)-1].Person.ID != r.PersonID {
			out = append(out, domain.AssignedLoad{Person: domain.Person{
				ID:       r.PersonID,
				Mail:     r.PersonMail,
				Name:     domain.PlainName(r.PersonName, r.PersonSortName),
				SortName: r.PersonSortName,
			}})
		}
		load := &out[len(out)-1]
		part := domain.AssignedLoadPart{Hours: r.Hours}
		if r.SubjectGroupID.Valid {
			part.SubjectGroup = &domain.SubjectGroupRef{
				ID:     r.SubjectGroupID.UUID,
				Code:   r.SubjectGroupCode,
				Name:   r.SubjectGroupName,
				Active: r.SubjectGroupActive,
			}
		}
		load.BySubjectGroup = append(load.BySubjectGroup, part)
		load.Total += r.Hours
	}
	return out, nil
}
