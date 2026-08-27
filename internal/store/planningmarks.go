package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/policy"
)

// PlanningMarks is the persistence behind domain.PlanningMarkService: the two things that decide
// when the planning is open, at the grain the planning happens in.
//
// No visibility filter anywhere in this file, deliberately. Both marks are facts about the process
// rather than about people — which programmes have settled their demand, which subjects are taking
// entries — and a colleague who cannot see them gets a tool that refuses writes without saying
// why. What is confidential is what the marks are *about*: the wishes and the assignments, and
// those have their own rules.
type PlanningMarks struct {
	pool *pgxpool.Pool
}

// NewPlanningMarks wires one up.
func NewPlanningMarks(pool *pgxpool.Pool) *PlanningMarks { return &PlanningMarks{pool: pool} }

var _ domain.PlanningMarkStore = (*PlanningMarks)(nil)

// DemandCompletions returns the announcements of one semester.
func (m *PlanningMarks) DemandCompletions(ctx context.Context,
	semesterCode string) ([]domain.DemandCompletion, error) {
	rows, err := New(m.pool).DemandCompletionsOfSemester(ctx, semesterCode)
	if err != nil {
		return nil, fmt.Errorf("cannot read the demand announcements: %w", err)
	}

	out := make([]domain.DemandCompletion, 0, len(rows))
	for _, row := range rows {
		completion := domain.DemandCompletion{
			SemesterCode: semesterCode,
			Programme: domain.Programme{
				ID:    row.ProgrammeID,
				Code:  row.ProgrammeCode,
				Title: row.ProgrammeTitle,
			},
			CompletedAt: row.CompletedAt,
		}
		if row.CompletedBy.Valid {
			completion.CompletedBy = row.CompletedBy.UUID
		}
		out = append(out, completion)
	}
	return out, nil
}

// AnnounceDemandComplete records or refreshes one announcement.
func (m *PlanningMarks) AnnounceDemandComplete(ctx context.Context, semesterCode, programme string,
	by uuid.UUID) (*domain.DemandCompletion, error) {
	row, err := New(m.pool).AnnounceDemandComplete(ctx, AnnounceDemandCompleteParams{
		Semester:    semesterCode,
		Programme:   programme,
		CompletedBy: nullUUID(nonNilUUID(by)),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// The SELECT that feeds the INSERT found no semester or no programme. Either way there is
		// nothing to announce about.
		return nil, domain.ErrProgrammeNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("cannot announce the demand: %w", err)
	}

	// Read back through the list query rather than assembling an answer here: one shape for this
	// record, built by the statement that also renders it on a screen.
	list, err := m.DemandCompletions(ctx, semesterCode)
	if err != nil {
		return nil, err
	}
	for _, completion := range list {
		if completion.Programme.ID == row.ProgrammeID {
			return &completion, nil
		}
	}
	return nil, domain.ErrProgrammeNotFound
}

// WithdrawDemandComplete removes one and reports whether there was one.
func (m *PlanningMarks) WithdrawDemandComplete(ctx context.Context,
	semesterCode, programme string) (bool, error) {
	rows, err := New(m.pool).WithdrawDemandComplete(ctx, WithdrawDemandCompleteParams{
		Semester:  semesterCode,
		Programme: programme,
	})
	if err != nil {
		return false, fmt.Errorf("cannot withdraw the announcement: %w", err)
	}
	return rows > 0, nil
}

// WishWindows returns the subject groups somebody has decided something about.
//
// Not every group: an absent row is open, so this is the list of exceptions. A caller that wants
// the state of one group reads "open" when it is not in here.
func (m *PlanningMarks) WishWindows(ctx context.Context,
	semesterCode string) ([]domain.WishWindow, error) {
	rows, err := New(m.pool).WishWindowsOfSemester(ctx, semesterCode)
	if err != nil {
		return nil, fmt.Errorf("cannot read the wish windows: %w", err)
	}

	out := make([]domain.WishWindow, 0, len(rows))
	for _, row := range rows {
		window := domain.WishWindow{
			SemesterCode:     semesterCode,
			SubjectGroupID:   row.SubjectGroupID,
			SubjectGroupCode: row.SubjectGroupCode,
			SubjectGroupName: row.SubjectGroupName,
			Open:             row.Open,
			ChangedAt:        row.ChangedAt,
		}
		if row.ChangedBy.Valid {
			window.ChangedBy = row.ChangedBy.UUID
		}
		out = append(out, window)
	}
	return out, nil
}

// SetWishWindow opens or shuts one subject group's wish round.
func (m *PlanningMarks) SetWishWindow(ctx context.Context, semesterCode string,
	subjectGroupID uuid.UUID, open bool, by uuid.UUID) (*domain.WishWindow, error) {
	_, err := New(m.pool).SetWishWindow(ctx, SetWishWindowParams{
		Semester:       semesterCode,
		SubjectGroupID: subjectGroupID,
		Open:           open,
		ChangedBy:      nullUUID(nonNilUUID(by)),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// The SELECT that feeds the INSERT found no semester. Unreachable through the service,
		// which records the row first — and kept, because "unreachable" is a claim about two
		// functions agreeing.
		return nil, domain.ErrSubjectGroupNotFound
	}
	if isForeignKeyViolation(err) {
		return nil, domain.ErrSubjectGroupNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("cannot switch the wish window: %w", err)
	}

	list, err := m.WishWindows(ctx, semesterCode)
	if err != nil {
		return nil, err
	}
	for _, window := range list {
		if window.SubjectGroupID == subjectGroupID {
			return &window, nil
		}
	}
	return nil, domain.ErrSubjectGroupNotFound
}

// ProgrammeIDByCode resolves a code to the id the policy asks about.
func (m *PlanningMarks) ProgrammeIDByCode(ctx context.Context, code string) (uuid.UUID, error) {
	id, err := New(m.pool).ProgrammeIDByCode(ctx, code)
	if errors.Is(err, pgx.ErrNoRows) {
		// Nil rather than an error: "this faculty has no such programme" is something the service
		// turns into a refusal, and it knows which one.
		return uuid.Nil, nil
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("cannot resolve the programme: %w", err)
	}
	return id, nil
}

// wishWriteContextFrom reshapes the row both wish-context queries produce.
func wishWriteContextFrom(row WishWriteContextRow) domain.WishWriteContext {
	out := domain.WishWriteContext{
		Semester: domain.Semester{
			ID:    row.ID,
			Code:  row.Code,
			Phase: policy.Phase(row.Phase),
		},
		WindowOpen: row.WishWindowOpen,
	}
	if row.WishesPublishedAt.Valid {
		out.Semester.WishesPublishedAt = row.WishesPublishedAt.Time
	}
	if row.SubjectGroupID.Valid {
		out.SubjectGroupID = row.SubjectGroupID.UUID
	}
	return out
}
