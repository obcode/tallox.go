package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/policy"
)

// Desired teaching hours, on the wish store because they are on the wish service. Every read
// takes a policy.DesiredHoursFilter and turns it into the query's parameters, as every other
// confidential read here does.

var _ domain.DesiredHoursStore = (*Wishes)(nil)

// desiredHoursFilterParams turns the policy's filter into the parameters every read takes.
func desiredHoursFilterParams(f policy.DesiredHoursFilter) (scope string, owner uuid.UUID,
	groups []uuid.UUID) {
	groups = []uuid.UUID{}
	switch f.Scope {
	case policy.DesiredHoursScopeAll:
		return string(policy.DesiredHoursScopeAll), uuid.Nil, groups
	case policy.DesiredHoursScopeOwn:
		return string(policy.DesiredHoursScopeOwn), f.OwnerID, groups
	case policy.DesiredHoursScopeOwnOrScoped:
		if len(f.SubjectGroupIDs) > 0 {
			groups = f.SubjectGroupIDs
		}
		return string(policy.DesiredHoursScopeOwnOrScoped), f.OwnerID, groups
	default:
		return "none", uuid.Nil, groups
	}
}

// DesiredHours returns the figures the filter allows.
func (w *Wishes) DesiredHours(ctx context.Context, semesterCode string, person uuid.UUID,
	filter policy.DesiredHoursFilter) ([]domain.DesiredHours, error) {
	scope, owner, groups := desiredHoursFilterParams(filter)

	params := DesiredTeachingHoursParams{
		Scope:           scope,
		OwnerID:         owner,
		SubjectGroupIds: groups,
	}
	if semesterCode != "" {
		params.Semester = &semesterCode
	}
	if person != uuid.Nil {
		params.Person = uuid.NullUUID{UUID: person, Valid: true}
	}

	rows, err := New(w.pool).DesiredTeachingHours(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("cannot read the desired teaching hours: %w", err)
	}
	out := make([]domain.DesiredHours, 0, len(rows))
	for _, r := range rows {
		out = append(out, domain.DesiredHours{
			SemesterCode: r.SemesterCode,
			Person: domain.Person{
				ID:       r.PersonID,
				Mail:     r.PersonMail,
				Name:     domain.PlainName(r.PersonName, r.PersonSortName),
				SortName: r.PersonSortName,
			},
			Hours:     r.Hours,
			Note:      r.Note,
			CreatedAt: r.CreatedAt,
			UpdatedAt: r.UpdatedAt,
		})
	}
	return out, nil
}

// SetDesiredHours states or changes the person's own figure, and reads it back through the
// owner's filter so the answer is assembled by the query that renders a list.
func (w *Wishes) SetDesiredHours(ctx context.Context, semesterCode string, personID uuid.UUID,
	hours float64, note string) (*domain.DesiredHours, error) {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := New(tx)
	semester, err := q.EnsureSemester(ctx, semesterCode)
	if err != nil {
		return nil, fmt.Errorf("cannot ensure the semester: %w", err)
	}
	if _, err := q.SetDesiredTeachingHours(ctx, SetDesiredTeachingHoursParams{
		SemesterID: semester.ID,
		PersonID:   personID,
		Hours:      hours,
		Note:       note,
	}); err != nil {
		return nil, fmt.Errorf("cannot store the desired teaching hours: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("cannot commit: %w", err)
	}

	stored, err := w.DesiredHours(ctx, semesterCode, personID,
		policy.DesiredHoursFilter{Scope: policy.DesiredHoursScopeOwn, OwnerID: personID})
	if err != nil {
		return nil, err
	}
	if len(stored) != 1 {
		return nil, fmt.Errorf("the desired teaching hours just stored read back as %d rows", len(stored))
	}
	return &stored[0], nil
}

// ClearDesiredHours takes the person's own figure back.
func (w *Wishes) ClearDesiredHours(ctx context.Context, semesterCode string,
	personID uuid.UUID) (bool, error) {
	n, err := New(w.pool).ClearDesiredTeachingHours(ctx, ClearDesiredTeachingHoursParams{
		PersonID: personID,
		Semester: semesterCode,
	})
	if err != nil {
		return false, fmt.Errorf("cannot take the desired teaching hours back: %w", err)
	}
	return n > 0, nil
}
