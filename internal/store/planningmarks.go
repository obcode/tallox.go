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

// DemandRelease counts the planned programmes of one semester and how many have released.
func (m *PlanningMarks) DemandRelease(ctx context.Context,
	semesterCode string) (domain.DemandRelease, error) {
	row, err := New(m.pool).DemandReleaseOfSemester(ctx, semesterCode)
	if err != nil {
		return domain.DemandRelease{}, fmt.Errorf("cannot count the demand releases: %w", err)
	}
	return domain.DemandRelease{Released: int(row.Released), Planned: int(row.Planned)}, nil
}

// AnnounceDemandComplete records or refreshes one announcement, and moves a semester still in
// DEMAND_PLANNING on to WISHES.
func (m *PlanningMarks) AnnounceDemandComplete(ctx context.Context, semesterCode, programme string,
	by uuid.UUID) (*domain.DemandCompletion, error) {
	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := New(tx)

	row, err := q.AnnounceDemandComplete(ctx, AnnounceDemandCompleteParams{
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

	// The release is what the advance wishes of this programme wait for: in the same transaction,
	// so that nobody reads a released programme whose advance wishes are still pending. Nothing of
	// it is reported — how many became wishes is the count before publication.
	if err := convertPendingAdvanceWishes(ctx, q, row.SemesterID, row.ProgrammeID); err != nil {
		return nil, err
	}

	// The first release moves the semester from DEMAND_PLANNING to WISHES (2026-11-10). Here and
	// not in a trigger, so that a fixture writing the mark directly leaves the phase alone; in this
	// transaction, so that nobody reads a released programme in a semester still showing the
	// demand planning. Compare-and-set: no row is "already further on", including a switch by the
	// dean's office a moment earlier, and is not an error. Every release while in DEMAND_PLANNING
	// advances — also after the dean's office has deliberately gone back — because the release is
	// the act that says the wishes have begun.
	if _, err := q.AdvanceToWishesOnRelease(ctx, row.SemesterID); err != nil {
		return nil, fmt.Errorf("cannot advance the semester to the wishes: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("cannot commit: %w", err)
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

// ReviewCompletions returns the review announcements of one semester.
func (m *PlanningMarks) ReviewCompletions(ctx context.Context,
	semesterCode string) ([]domain.ReviewCompletion, error) {
	rows, err := New(m.pool).ReviewCompletionsOfSemester(ctx, semesterCode)
	if err != nil {
		return nil, fmt.Errorf("cannot read the review announcements: %w", err)
	}

	out := make([]domain.ReviewCompletion, 0, len(rows))
	for _, row := range rows {
		completion := domain.ReviewCompletion{
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

// AnnounceReviewComplete records or refreshes one review announcement.
func (m *PlanningMarks) AnnounceReviewComplete(ctx context.Context, semesterCode, programme string,
	by uuid.UUID) (*domain.ReviewCompletion, error) {
	row, err := New(m.pool).AnnounceReviewComplete(ctx, AnnounceReviewCompleteParams{
		Semester:    semesterCode,
		Programme:   programme,
		CompletedBy: nullUUID(nonNilUUID(by)),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrProgrammeNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("cannot announce the review: %w", err)
	}

	list, err := m.ReviewCompletions(ctx, semesterCode)
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

// WithdrawReviewComplete removes one and reports whether there was one.
func (m *PlanningMarks) WithdrawReviewComplete(ctx context.Context,
	semesterCode, programme string) (bool, error) {
	rows, err := New(m.pool).WithdrawReviewComplete(ctx, WithdrawReviewCompleteParams{
		Semester:  semesterCode,
		Programme: programme,
	})
	if err != nil {
		return false, fmt.Errorf("cannot withdraw the review announcement: %w", err)
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
	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := New(tx)

	row, err := q.SetWishWindow(ctx, SetWishWindowParams{
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
	// An advance wish noted down after its programme's release waits for an open round before it
	// becomes wishes (ConvertAdvanceWishes). Opening the round is that moment, for every programme
	// of the semester, in this transaction — the same as a release or a new cohort. Nothing of it
	// is reported.
	if open {
		if err := convertPendingAdvanceWishes(ctx, q, row.SemesterID, uuid.Nil); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("cannot commit: %w", err)
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
		WindowOpen:      row.WishWindowOpen,
		WishesReleased:  row.WishesReleased,
		TaughtElsewhere: row.TaughtElsewhere,
		EnglishPossible: domain.EnglishPossible(domain.ModuleSource(row.ModuleSource),
			domain.Language(stringOrEmpty(row.ModuleTeachingLanguage)),
			domain.Language(stringOrEmpty(row.ModuleOtherLanguage))),
		TeachingHours: row.TeachingHours,
	}
	if row.WishesPublishedAt.Valid {
		out.Semester.WishesPublishedAt = row.WishesPublishedAt.Time
	}
	if row.SubjectGroupID.Valid {
		out.SubjectGroupID = row.SubjectGroupID.UUID
	}
	return out
}
