package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/policy"
)

// Advance wishes ("Vormerkungen"), the persistence half. Read through the wish filter like the
// wishes they are going to be — see db/queries/advance_wish.sql.

// AdvanceWishes returns the advance wishes the filter allows.
func (w *Wishes) AdvanceWishes(ctx context.Context, q domain.AdvanceWishQuery,
	filter policy.WishFilter) ([]domain.AdvanceWish, error) {
	scope, owner, programmes, groups := wishFilterParams(filter)
	params := AdvanceWishesParams{
		Scope:           scope,
		OwnerID:         owner,
		ProgrammeIds:    programmes,
		SubjectGroupIds: groups,
	}
	if q.SemesterCode != "" {
		params.Semester = &q.SemesterCode
	}
	if q.Programme != "" {
		params.Programme = &q.Programme
	}
	if q.Module != uuid.Nil {
		params.Module = uuid.NullUUID{UUID: q.Module, Valid: true}
	}
	if q.SubjectGroup != uuid.Nil {
		params.SubjectGroup = uuid.NullUUID{UUID: q.SubjectGroup, Valid: true}
	}
	if q.Person != uuid.Nil {
		params.Person = uuid.NullUUID{UUID: q.Person, Valid: true}
	}

	rows, err := New(w.pool).AdvanceWishes(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("cannot read the advance wishes: %w", err)
	}
	out := make([]domain.AdvanceWish, 0, len(rows))
	for _, row := range rows {
		a := domain.AdvanceWish{
			ID: row.ID,
			Person: domain.Person{
				ID:       row.PersonID,
				Mail:     row.PersonMail,
				Name:     domain.PlainName(row.PersonName, row.PersonSortName),
				SortName: row.PersonSortName,
				Active:   true,
			},
			SemesterCode: row.SemesterCode,
			Module: domain.ModuleRef{
				ID:                row.ModuleID,
				Name:              row.ModuleName,
				HomeProgrammeCode: row.ModuleHomeProgrammeCode,
				Compulsory:        row.ModuleCompulsory,
				IndependentWork:   domain.CourseType(row.ModuleCourseType).IndependentWork(),
			},
			Programme: domain.Programme{
				ID: row.ProgrammeID, Code: row.ProgrammeCode, Title: row.ProgrammeTitle,
			},
			SubjectGroupID: row.SubjectGroupID.UUID,
			Priority:       domain.WishPriorityFromLevel(row.Priority),
			Note:           row.Note,
			Released:       row.Released,
			Offered:        row.Offered,
			CreatedAt:      row.CreatedAt,
			UpdatedAt:      row.UpdatedAt,
		}
		if row.MaxTracks != nil {
			a.MaxTracks = int(*row.MaxTracks)
		}
		if row.ConvertedAt.Valid {
			a.ConvertedAt = row.ConvertedAt.Time
		}
		out = append(out, a)
	}
	return out, nil
}

// AdvanceWishReleased is whether the programme has released its demand for the semester.
func (w *Wishes) AdvanceWishReleased(ctx context.Context, semesterCode string,
	programmeID uuid.UUID) (bool, error) {
	released, err := New(w.pool).AdvanceWishOffering(ctx, AdvanceWishOfferingParams{
		Semester: semesterCode, ProgrammeID: programmeID,
	})
	if err != nil {
		return false, fmt.Errorf("cannot read the release: %w", err)
	}
	return released, nil
}

// SetAdvanceWish stores the person's own.
func (w *Wishes) SetAdvanceWish(ctx context.Context, personID, programmeID uuid.UUID,
	a domain.NewAdvanceWish) (uuid.UUID, error) {
	// Independent work — a thesis — is never planned, so there is nothing to note down for it:
	// see domain.CourseType.IndependentWork.
	module, err := New(w.pool).ModuleByID(ctx, a.ModuleID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, domain.ErrModuleNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("cannot read the module: %w", err)
	}
	if domain.CourseType(module.CourseType).IndependentWork() {
		return uuid.Nil, domain.ErrModuleNotPlanned
	}

	level, _ := a.Priority.Level()
	params := UpsertAdvanceWishParams{
		PersonID:    personID,
		Semester:    a.SemesterCode,
		ModuleID:    a.ModuleID,
		ProgrammeID: programmeID,
		Priority:    level,
		Note:        a.Note,
	}
	if a.MaxTracks != nil {
		bound := int16(*a.MaxTracks) //nolint:gosec // bounded to 1..9 by the service and a CHECK
		params.MaxTracks = &bound
	}
	id, err := New(w.pool).UpsertAdvanceWish(ctx, params)
	if errors.Is(err, pgx.ErrNoRows) {
		// The upsert's WHERE kept a converted row as it is.
		return uuid.Nil, domain.ErrAdvanceWishConverted
	}
	if isForeignKeyViolation(err) {
		// Programme and person are resolved before this; what is left is the module.
		return uuid.Nil, domain.ErrModuleNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("cannot store the advance wish: %w", err)
	}
	return id, nil
}

// WithdrawAdvanceWish removes the person's own.
func (w *Wishes) WithdrawAdvanceWish(ctx context.Context, id, personID uuid.UUID) error {
	n, err := New(w.pool).DeleteOwnAdvanceWish(ctx, DeleteOwnAdvanceWishParams{ID: id, PersonID: personID})
	if err != nil {
		return fmt.Errorf("cannot withdraw the advance wish: %w", err)
	}
	if n == 0 {
		return domain.ErrAdvanceWishNotFound
	}
	return nil
}

// ProgrammeIDByCode resolves a code, uuid.Nil for none.
func (w *Wishes) ProgrammeIDByCode(ctx context.Context, code string) (uuid.UUID, error) {
	return NewPlanningMarks(w.pool).ProgrammeIDByCode(ctx, code)
}

// convertPendingAdvanceWishes turns the pending advance wishes of one programme and semester into wishes
// where there is now a cohort to wish for. Called inside every transaction that releases a
// programme's demand or gives it a cohort; does nothing where the programme is not released.
func convertPendingAdvanceWishes(ctx context.Context, q *Queries, semesterID, programmeID uuid.UUID) error {
	if err := q.ConvertAdvanceWishes(ctx, ConvertAdvanceWishesParams{
		SemesterID: semesterID, ProgrammeID: programmeID,
	}); err != nil {
		return fmt.Errorf("cannot convert the advance wishes: %w", err)
	}
	return nil
}
