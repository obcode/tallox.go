package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/obcode/tallox.go/internal/domain"
)

// ConfirmAssignments records "passt so" over what the person holds in the semester right now.
// Reports false, and records nothing, when they hold nothing there — see the query.
func (a *Assignments) ConfirmAssignments(ctx context.Context, semesterID, personID uuid.UUID,
) (bool, error) {
	_, err := New(a.pool).ConfirmAssignments(ctx, ConfirmAssignmentsParams{
		SemesterID: semesterID,
		PersonID:   personID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("cannot record the confirmation: %w", err)
	}
	return true, nil
}

// ConfirmationRecords answers for exactly the persons named. It does not filter: the domain
// derives the ids from the assignments the caller may read, and asks about no one else.
func (a *Assignments) ConfirmationRecords(ctx context.Context, semesterID uuid.UUID,
	personIDs []uuid.UUID) ([]domain.ConfirmationRecord, error) {
	rows, err := New(a.pool).AssignmentConfirmationStates(ctx, AssignmentConfirmationStatesParams{
		SemesterID: semesterID,
		PersonIds:  personIDs,
	})
	if err != nil {
		return nil, fmt.Errorf("cannot read the confirmations: %w", err)
	}
	out := make([]domain.ConfirmationRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.ConfirmationRecord{
			PersonID:    row.PersonID,
			ConfirmedAt: nullableTime(row.ConfirmedAt),
			Current:     row.Current,
		})
	}
	return out, nil
}
