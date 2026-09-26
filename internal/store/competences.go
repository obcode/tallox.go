package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/policy"
)

// Competences is the persistence behind domain.CompetenceService.
//
// Every read of other people's statements takes a policy.CompetenceFilter and turns it into query
// parameters, as wishes.go does, so the rule runs as a WHERE clause. The three aggregates are the
// exception db/queries/competence.sql explains.
type Competences struct {
	pool *pgxpool.Pool
}

// NewCompetences wires one up.
func NewCompetences(pool *pgxpool.Pool) *Competences { return &Competences{pool: pool} }

var _ domain.CompetenceStore = (*Competences)(nil)

// competenceFilterParams turns the policy's filter into the four parameters every read takes.
// Same translation as wishFilterParams, for the same reasons.
func competenceFilterParams(f policy.CompetenceFilter) (scope string, owner uuid.UUID,
	programmes, groups []uuid.UUID) {
	programmes, groups = []uuid.UUID{}, []uuid.UUID{}

	switch f.Scope {
	case policy.CompetenceScopeAll:
		return string(policy.CompetenceScopeAll), uuid.Nil, programmes, groups
	case policy.CompetenceScopeOwn:
		return string(policy.CompetenceScopeOwn), f.OwnerID, programmes, groups
	case policy.CompetenceScopeOwnOrScoped:
		if len(f.ProgrammeIDs) > 0 {
			programmes = f.ProgrammeIDs
		}
		if len(f.SubjectGroupIDs) > 0 {
			groups = f.SubjectGroupIDs
		}
		return string(policy.CompetenceScopeOwnOrScoped), f.OwnerID, programmes, groups
	default:
		return "none", uuid.Nil, programmes, groups
	}
}

// Competences returns the statements the filter allows, narrowed by the query.
func (c *Competences) Competences(ctx context.Context, q domain.CompetenceQuery,
	filter policy.CompetenceFilter) ([]domain.Competence, error) {
	scope, owner, programmes, groups := competenceFilterParams(filter)

	rows, err := New(c.pool).Competences(ctx, CompetencesParams{
		Module:          narrowBy(q.Module),
		SubjectGroup:    narrowBy(q.SubjectGroup),
		Person:          narrowBy(q.Person),
		Teacher:         narrowBy(q.Teacher),
		Scope:           scope,
		OwnerID:         owner,
		ProgrammeIds:    programmes,
		SubjectGroupIds: groups,
	})
	if err != nil {
		return nil, fmt.Errorf("cannot read the competences: %w", err)
	}

	out := make([]domain.Competence, 0, len(rows))
	for _, row := range rows {
		out = append(out, competenceFrom(competenceRow(row)))
	}
	return out, nil
}

// CompetenceByID returns one statement through the same filter, or (nil, nil).
func (c *Competences) CompetenceByID(ctx context.Context, id uuid.UUID,
	filter policy.CompetenceFilter) (*domain.Competence, error) {
	scope, owner, programmes, groups := competenceFilterParams(filter)

	row, err := New(c.pool).CompetenceByID(ctx, CompetenceByIDParams{
		ID:              id,
		Scope:           scope,
		OwnerID:         owner,
		ProgrammeIds:    programmes,
		SubjectGroupIds: groups,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read the competence: %w", err)
	}
	competence := competenceFrom(competenceRow(row))
	return &competence, nil
}

// ModuleContext is what the write rule needs about a module.
func (c *Competences) ModuleContext(ctx context.Context,
	moduleID, personID uuid.UUID) (domain.CompetenceModuleContext, error) {
	row, err := New(c.pool).CompetenceModuleContext(ctx, CompetenceModuleContextParams{
		PersonID: personID,
		ModuleID: moduleID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.CompetenceModuleContext{}, nil
	}
	if err != nil {
		return domain.CompetenceModuleContext{}, fmt.Errorf("cannot read the module: %w", err)
	}
	return domain.CompetenceModuleContext{
		Found:          true,
		Open:           row.Open,
		SubjectGroupID: row.SubjectGroupID.UUID,
		CallerIsMember: row.IsMember,
	}, nil
}

// SetOwn writes one's own statement.
func (c *Competences) SetOwn(ctx context.Context, moduleID, personID uuid.UUID,
	level domain.CompetenceLevel, note string) (uuid.UUID, error) {
	id, err := New(c.pool).UpsertOwnCompetence(ctx, UpsertOwnCompetenceParams{
		ModuleID: moduleID,
		PersonID: personID,
		Level:    string(level),
		Note:     note,
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("cannot record the competence: %w", err)
	}
	return id, nil
}

// SetForTeacher writes a statement about a teacher without an account.
func (c *Competences) SetForTeacher(ctx context.Context, moduleID, teacherID, enteredBy uuid.UUID,
	level domain.CompetenceLevel, note string) (uuid.UUID, error) {
	id, err := New(c.pool).UpsertTeacherCompetence(ctx, UpsertTeacherCompetenceParams{
		ModuleID:  moduleID,
		TeacherID: teacherID,
		Level:     string(level),
		Note:      note,
		EnteredBy: enteredBy,
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("cannot record the competence: %w", err)
	}
	return id, nil
}

// WithdrawOwn removes one's own statement. Not there and not yours are the same answer.
func (c *Competences) WithdrawOwn(ctx context.Context, id, personID uuid.UUID) error {
	rows, err := New(c.pool).DeleteOwnCompetence(ctx, DeleteOwnCompetenceParams{ID: id, PersonID: personID})
	if err != nil {
		return fmt.Errorf("cannot withdraw the competence: %w", err)
	}
	if rows == 0 {
		return domain.ErrCompetenceNotFound
	}
	return nil
}

// TeacherRowGroup is the subject group of a teacher row's module.
func (c *Competences) TeacherRowGroup(ctx context.Context, id uuid.UUID) (uuid.UUID, bool, error) {
	row, err := New(c.pool).TeacherCompetenceContext(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("cannot read the competence: %w", err)
	}
	return row.SubjectGroupID.UUID, true, nil
}

// WithdrawForTeacher removes a teacher row.
func (c *Competences) WithdrawForTeacher(ctx context.Context, id uuid.UUID) error {
	rows, err := New(c.pool).DeleteTeacherCompetence(ctx, id)
	if err != nil {
		return fmt.Errorf("cannot withdraw the competence: %w", err)
	}
	if rows == 0 {
		return domain.ErrCompetenceNotFound
	}
	return nil
}

// TeacherExists reports whether a teacher may be named — the same question the assignment asks.
func (c *Competences) TeacherExists(ctx context.Context, teacherID uuid.UUID) (bool, error) {
	return NewAssignments(c.pool).TeacherExists(ctx, teacherID)
}

// AccountOfTeacher is the account belonging to a teacher, or uuid.Nil.
func (c *Competences) AccountOfTeacher(ctx context.Context, teacherID uuid.UUID) (uuid.UUID, error) {
	return NewAssignments(c.pool).AccountOfTeacher(ctx, teacherID)
}

// OwnGroupStatus counts the caller's own compulsory CAN_TEACH statements per subject group.
func (c *Competences) OwnGroupStatus(ctx context.Context,
	personID uuid.UUID) ([]domain.CompetenceGroupStatus, error) {
	rows, err := New(c.pool).OwnCompulsoryCounts(ctx, personID)
	if err != nil {
		return nil, fmt.Errorf("cannot count the competences: %w", err)
	}
	out := make([]domain.CompetenceGroupStatus, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.CompetenceGroupStatus{
			SubjectGroup:       domain.SubjectGroupRef{ID: row.ID, Code: row.Code, Name: row.Name, Active: true},
			CanTeachCompulsory: int(row.CanTeachCompulsory),
		})
	}
	return out, nil
}

// MemberStatus counts every member's compulsory CAN_TEACH statements in one subject group.
func (c *Competences) MemberStatus(ctx context.Context,
	subjectGroupID uuid.UUID) ([]domain.MemberCompetenceStatus, error) {
	rows, err := New(c.pool).MemberCompulsoryCounts(ctx, subjectGroupID)
	if err != nil {
		return nil, fmt.Errorf("cannot count the competences: %w", err)
	}
	out := make([]domain.MemberCompetenceStatus, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.MemberCompetenceStatus{
			Person: domain.Person{
				ID:       row.ID,
				Mail:     row.Mail,
				Name:     domain.PlainName(row.Name, row.SortName),
				SortName: row.SortName,
				Active:   true,
			},
			CanTeachCompulsory: int(row.CanTeachCompulsory),
		})
	}
	return out, nil
}

// ModulesWithoutCompetence lists the modules of one group nobody can teach.
func (c *Competences) ModulesWithoutCompetence(ctx context.Context,
	subjectGroupID uuid.UUID) ([]domain.ModuleWithoutCompetence, error) {
	rows, err := New(c.pool).ModulesWithoutCompetence(ctx, subjectGroupID)
	if err != nil {
		return nil, fmt.Errorf("cannot read the modules: %w", err)
	}
	out := make([]domain.ModuleWithoutCompetence, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.ModuleWithoutCompetence{ID: row.ID, Name: row.Name, Compulsory: row.Compulsory})
	}
	return out, nil
}

// competenceRow is the shape both competence queries produce — converted rather than copied, so
// that the two projections drifting apart is a compile error.
type competenceRow struct {
	ID                   uuid.UUID
	ModuleID             uuid.UUID
	PersonID             uuid.NullUUID
	TeacherID            uuid.NullUUID
	Level                string
	Note                 string
	CreatedAt            time.Time
	UpdatedAt            time.Time
	ModuleName           string
	Compulsory           bool
	SubjectGroupID       uuid.NullUUID
	SubjectGroupCode     *string
	SubjectGroupName     *string
	HolderName           string
	HolderMail           string
	HolderSortName       string
	OutsideSubjectGroups bool
}

func competenceFrom(row competenceRow) domain.Competence {
	module := domain.CompetenceModule{ID: row.ModuleID, Name: row.ModuleName, Compulsory: row.Compulsory}
	if row.SubjectGroupID.Valid {
		ref := domain.SubjectGroupRef{ID: row.SubjectGroupID.UUID, Active: true}
		if row.SubjectGroupCode != nil {
			ref.Code = *row.SubjectGroupCode
		}
		if row.SubjectGroupName != nil {
			ref.Name = *row.SubjectGroupName
		}
		module.SubjectGroup = &ref
	}

	return domain.Competence{
		ID:     row.ID,
		Module: module,
		Holder: domain.Assignee{
			PersonID:  row.PersonID.UUID,
			TeacherID: row.TeacherID.UUID,
			Name:      domain.PlainName(row.HolderName, row.HolderSortName),
			Mail:      row.HolderMail,
			SortName:  row.HolderSortName,
		},
		Level:                domain.CompetenceLevel(row.Level),
		Note:                 row.Note,
		OutsideSubjectGroups: row.OutsideSubjectGroups,
		CreatedAt:            row.CreatedAt,
		UpdatedAt:            row.UpdatedAt,
	}
}

// narrowBy turns an optional narrowing — uuid.Nil for none — into a nullable query argument.
func narrowBy(id uuid.UUID) uuid.NullUUID {
	return uuid.NullUUID{UUID: id, Valid: id != uuid.Nil}
}
