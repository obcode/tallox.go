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

// DesiredHours returns the figures the filter allows, with the note through noteFilter.
func (w *Wishes) DesiredHours(ctx context.Context, semesterCode string, person uuid.UUID,
	filter, noteFilter policy.DesiredHoursFilter) ([]domain.DesiredHours, error) {
	scope, owner, groups := desiredHoursFilterParams(filter)
	noteScope, noteOwner, noteGroups := desiredHoursFilterParams(noteFilter)

	params := DesiredTeachingHoursParams{
		Scope:               scope,
		OwnerID:             owner,
		SubjectGroupIds:     groups,
		NoteScope:           noteScope,
		NoteOwnerID:         noteOwner,
		NoteSubjectGroupIds: noteGroups,
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
			MinHours:    r.MinHours,
			MaxHours:    r.MaxHours,
			Note:        r.Note,
			CorrectedBy: r.CorrectedByName,
			CreatedAt:   r.CreatedAt,
			UpdatedAt:   r.UpdatedAt,
		})
	}
	return out, nil
}

// DesiredHoursOverview lists the faculty's colleagues with the ranges the filter allows.
func (w *Wishes) DesiredHoursOverview(ctx context.Context, semesterCode string,
	filter policy.DesiredHoursFilter) ([]domain.DesiredHoursRow, error) {
	scope, owner, groups := desiredHoursFilterParams(filter)
	rows, err := New(w.pool).DesiredHoursOverview(ctx, DesiredHoursOverviewParams{
		Semester:        semesterCode,
		Scope:           scope,
		OwnerID:         owner,
		SubjectGroupIds: groups,
	})
	if err != nil {
		return nil, fmt.Errorf("cannot read the desired hours overview: %w", err)
	}
	out := make([]domain.DesiredHoursRow, 0, len(rows))
	for _, r := range rows {
		refs := make([]domain.SubjectGroupRef, 0, len(r.GroupIds))
		for i, id := range r.GroupIds {
			refs = append(refs, domain.SubjectGroupRef{
				ID: id, Code: r.GroupCodes[i], Name: r.GroupNames[i], Active: r.GroupActive[i],
			})
		}
		out = append(out, domain.DesiredHoursRow{
			Person: domain.Person{
				ID:       r.PersonID,
				Mail:     r.PersonMail,
				Name:     domain.PlainName(r.PersonName, r.PersonSortName),
				SortName: r.PersonSortName,
			},
			SubjectGroups: refs,
			Stated:        r.Stated,
			MinHours:      r.MinHours,
			MaxHours:      r.MaxHours,
		})
	}
	return out, nil
}

// SetDesiredHours states or changes a person's figure. correctedBy is uuid.Nil for one's own.
func (w *Wishes) SetDesiredHours(ctx context.Context, semesterCode string, personID uuid.UUID,
	minHours, maxHours float64, note string, correctedBy uuid.UUID) error {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("cannot begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := New(tx)
	semester, err := q.EnsureSemester(ctx, semesterCode)
	if err != nil {
		return fmt.Errorf("cannot ensure the semester: %w", err)
	}
	if _, err := q.SetDesiredTeachingHours(ctx, SetDesiredTeachingHoursParams{
		SemesterID:  semester.ID,
		PersonID:    personID,
		MinHours:    minHours,
		MaxHours:    maxHours,
		Note:        note,
		CorrectedBy: nullUUID(nonNilUUID(correctedBy)),
	}); err != nil {
		if isForeignKeyViolation(err) {
			return domain.ErrDesiredHoursUnknownPerson
		}
		return fmt.Errorf("cannot store the desired teaching hours: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("cannot commit: %w", err)
	}
	return nil
}

// ClearDesiredHours takes a person's figure back.
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
