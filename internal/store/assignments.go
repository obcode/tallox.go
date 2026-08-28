package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/policy"
)

// Assignments is the persistence behind domain.AssignmentService.
//
// Two things in this file carry weight. assignmentFilterParams turns a policy.AssignmentFilter
// into query parameters, so the confidentiality rule runs as a WHERE clause rather than over rows
// already read — the same arrangement wishes.go makes, and for the same reason: a predicate
// applied after the fact is one that exports and the token path do not have, and a COUNT would
// skip entirely.
//
// The other is that both writes are conditional. Filling a part inserts only while nobody holds
// it; replacing one updates only while the named assignment is still the one there. Neither is a
// check followed by a write, because two roles may write this row and a check-then-write is
// correct in a unit test and a race in the meeting where both of them decide.
type Assignments struct {
	pool *pgxpool.Pool
}

// NewAssignments wires one up.
func NewAssignments(pool *pgxpool.Pool) *Assignments { return &Assignments{pool: pool} }

var _ domain.AssignmentStore = (*Assignments)(nil)

// assignmentFilterParams turns the policy's filter into the four parameters every read takes.
//
// A function rather than four fields the caller fills in, because the parameters are meaningless
// apart and dangerous alone: "own_or_scoped" with the id lists left empty is a filter that
// silently reaches nothing, and "all" with an assignee set reads as if it were restricted.
//
// The empty slices matter, for the reason wishFilterParams gives: `= ANY(NULL)` is NULL and
// therefore not true, which is the right answer — but a nil slice arriving as NULL rather than as
// an empty array is the kind of thing that holds for one driver version and not the next.
func assignmentFilterParams(f policy.AssignmentFilter) (scope string, assignee uuid.UUID,
	programmes, groups []uuid.UUID) {
	programmes, groups = []uuid.UUID{}, []uuid.UUID{}

	switch f.Scope {
	case policy.AssignmentReadScopeAll:
		return string(policy.AssignmentReadScopeAll), uuid.Nil, programmes, groups
	case policy.AssignmentReadScopeOwn:
		return string(policy.AssignmentReadScopeOwn), f.AssigneeID, programmes, groups
	case policy.AssignmentReadScopeOwnOrScoped:
		if len(f.ProgrammeIDs) > 0 {
			programmes = f.ProgrammeIDs
		}
		if len(f.SubjectGroupIDs) > 0 {
			groups = f.SubjectGroupIDs
		}
		return string(policy.AssignmentReadScopeOwnOrScoped), f.AssigneeID, programmes, groups
	default:
		// AssignmentReadScopeNone and anything this build does not know. The query matches no
		// branch for an unrecognised string, the same fail-closed reading AssignmentFilter.Matches
		// takes — and "none" is spelled out rather than passed through, so that a future scope
		// value cannot arrive here and accidentally mean something.
		return "none", uuid.Nil, programmes, groups
	}
}

// Assignments returns what the filter allows — of one semester, or of every semester when the
// query names none.
func (a *Assignments) Assignments(ctx context.Context, q domain.AssignmentQuery,
	filter policy.AssignmentFilter) ([]domain.Assignment, error) {
	scope, assignee, programmes, groups := assignmentFilterParams(filter)

	params := AssignmentsOfSemesterParams{
		Scope:           scope,
		AssigneeID:      assignee,
		ProgrammeIds:    programmes,
		SubjectGroupIds: groups,
	}

	if q.SemesterCode != "" {
		semester, err := New(a.pool).SemesterByCode(ctx, q.SemesterCode)
		if errors.Is(err, pgx.ErrNoRows) {
			// No row means nobody has decided anything about this semester, so it holds no
			// instances and therefore nothing is assigned in it.
			return []domain.Assignment{}, nil
		}
		if err != nil {
			return nil, fmt.Errorf("cannot read the semester: %w", err)
		}
		params.SemesterID = uuid.NullUUID{UUID: semester.ID, Valid: true}
	}

	if q.Programme != "" {
		params.Programme = &q.Programme
	}
	if q.Module != uuid.Nil {
		params.Module = uuid.NullUUID{UUID: q.Module, Valid: true}
	}
	if q.Instance != uuid.Nil {
		params.Instance = uuid.NullUUID{UUID: q.Instance, Valid: true}
	}
	if q.Person != uuid.Nil {
		params.Person = uuid.NullUUID{UUID: q.Person, Valid: true}
	}

	rows, err := New(a.pool).AssignmentsOfSemester(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("cannot read the assignments: %w", err)
	}

	out := make([]domain.Assignment, 0, len(rows))
	for _, row := range rows {
		out = append(out, assignmentFrom(assignmentRow(row)))
	}
	return out, nil
}

// AssignmentByID returns one through the same filter, or (nil, nil).
//
// "Not there" and "not yours to see" are deliberately the same answer, the same way they are for
// a wish: the difference between them is a fact the rule protects.
func (a *Assignments) AssignmentByID(ctx context.Context, id uuid.UUID,
	filter policy.AssignmentFilter) (*domain.Assignment, error) {
	scope, assignee, programmes, groups := assignmentFilterParams(filter)

	row, err := New(a.pool).AssignmentByID(ctx, AssignmentByIDParams{
		ID:              id,
		Scope:           scope,
		AssigneeID:      assignee,
		ProgrammeIds:    programmes,
		SubjectGroupIds: groups,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read the assignment: %w", err)
	}

	found := assignmentFrom(assignmentRow(row))
	return &found, nil
}

// PartWriteContext reads what the write rule needs about a part.
func (a *Assignments) PartWriteContext(ctx context.Context,
	partID uuid.UUID) (domain.PartWriteContext, error) {
	row, err := New(a.pool).PartWriteContext(ctx, partID)
	if errors.Is(err, pgx.ErrNoRows) {
		// The zero value answers Found() false. Not an error: "this part is gone" is something the
		// service turns into a refusal, and a part withdrawn between two clicks is ordinary.
		return domain.PartWriteContext{}, nil
	}
	if err != nil {
		return domain.PartWriteContext{}, fmt.Errorf("cannot read the part: %w", err)
	}
	return writeContextFrom(row), nil
}

// AssignmentWriteContext is the same, reached from an assignment.
func (a *Assignments) AssignmentWriteContext(ctx context.Context,
	id uuid.UUID) (domain.PartWriteContext, error) {
	row, err := New(a.pool).AssignmentWriteContextByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.PartWriteContext{}, nil
	}
	if err != nil {
		return domain.PartWriteContext{}, fmt.Errorf("cannot read the assignment: %w", err)
	}
	// Field by field rather than a conversion, because the two rows differ in exactly one place
	// and the difference is meaningful: reached from an assignment, the assignment is there by
	// construction, so sqlc types that column as non-nullable. Restating it as a valid NullUUID is
	// what makes the two paths produce the same context.
	return writeContextFrom(PartWriteContextRow{
		InstancePartID:         row.InstancePartID,
		CourseInstanceID:       row.CourseInstanceID,
		SemesterCode:           row.SemesterCode,
		SemesterPhase:          row.SemesterPhase,
		AssignmentsPublishedAt: row.AssignmentsPublishedAt,
		ProgrammeID:            row.ProgrammeID,
		SubjectGroupID:         row.SubjectGroupID,
		AssignmentID:           uuid.NullUUID{UUID: row.AssignmentID, Valid: true},
	}), nil
}

// FillPart puts somebody on a part nobody holds.
func (a *Assignments) FillPart(ctx context.Context, partID uuid.UUID, who domain.Assignee,
	note string, by uuid.UUID) (uuid.UUID, error) {
	id, err := New(a.pool).FillInstancePart(ctx, FillInstancePartParams{
		InstancePartID: partID,
		PersonID:       nullUUID(nonNilUUID(who.PersonID)),
		TeacherID:      nullUUID(nonNilUUID(who.TeacherID)),
		Note:           note,
		AssignedBy:     nullUUID(nonNilUUID(by)),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// ON CONFLICT DO NOTHING returned nothing: somebody holds this part. The caller believed
		// it was free, so this is a refusal and not a silent overwrite.
		return uuid.Nil, domain.ErrPartAlreadyAssigned
	}
	// The part may have been removed between the phase check and the write.
	if isForeignKeyViolation(err) {
		return uuid.Nil, domain.ErrPartNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("cannot fill the part: %w", err)
	}
	return id, nil
}

// ReplaceAssignment hands a part to somebody else, while the named assignment is still there.
func (a *Assignments) ReplaceAssignment(ctx context.Context, partID, replacing uuid.UUID,
	who domain.Assignee, note string, by uuid.UUID) (uuid.UUID, error) {
	id, err := New(a.pool).ReplaceAssignment(ctx, ReplaceAssignmentParams{
		InstancePartID: partID,
		Replacing:      replacing,
		PersonID:       nullUUID(nonNilUUID(who.PersonID)),
		TeacherID:      nullUUID(nonNilUUID(who.TeacherID)),
		Note:           note,
		AssignedBy:     nullUUID(nonNilUUID(by)),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// The compare-and-set lost: the assignment the caller was looking at is not the one on
		// this part now. Somebody else decided in between, and they are told rather than
		// overwriting a decision they never saw.
		return uuid.Nil, domain.ErrAssignmentMovedOn
	}
	if isForeignKeyViolation(err) {
		return uuid.Nil, domain.ErrAssigneeNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("cannot replace the assignment: %w", err)
	}
	return id, nil
}

// ClearAssignment removes one and reports whether there was one.
func (a *Assignments) ClearAssignment(ctx context.Context, id uuid.UUID) (bool, error) {
	rows, err := New(a.pool).ClearAssignment(ctx, id)
	if err != nil {
		return false, fmt.Errorf("cannot clear the assignment: %w", err)
	}
	return rows > 0, nil
}

// AccountOfTeacher returns the account belonging to a teacher, or uuid.Nil when there is none.
func (a *Assignments) AccountOfTeacher(ctx context.Context, teacherID uuid.UUID) (uuid.UUID, error) {
	id, err := New(a.pool).PersonIDByTeacherID(ctx, teacherID)
	if errors.Is(err, pgx.ErrNoRows) {
		// No account, which is an ordinary state and the whole reason teacher_id exists.
		return uuid.Nil, nil
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("cannot resolve the teacher's account: %w", err)
	}
	return id, nil
}

// TeacherExists reports whether a teacher may be given teaching.
func (a *Assignments) TeacherExists(ctx context.Context, teacherID uuid.UUID) (bool, error) {
	ok, err := New(a.pool).AssignableTeacherExists(ctx, teacherID)
	if err != nil {
		return false, fmt.Errorf("cannot look up the teacher: %w", err)
	}
	return ok, nil
}

// PersonExists reports the same for an account.
func (a *Assignments) PersonExists(ctx context.Context, personID uuid.UUID) (bool, error) {
	ok, err := New(a.pool).AssignablePersonExists(ctx, personID)
	if err != nil {
		return false, fmt.Errorf("cannot look up the person: %w", err)
	}
	return ok, nil
}

// writeContextFrom reshapes the row the two context queries produce.
//
// One shape for both, so that "may I write here" is answered from the same fields whether the
// caller named a part or an assignment. The one column that differs is handled at the call site
// above.
func writeContextFrom(row PartWriteContextRow) domain.PartWriteContext {
	out := domain.PartWriteContext{
		InstancePartID:   row.InstancePartID,
		CourseInstanceID: row.CourseInstanceID,
		SemesterCode:     row.SemesterCode,
		Semester: policy.SemesterState{
			Phase:                  policy.Phase(row.SemesterPhase),
			AssignmentsPublishedAt: nullableTime(row.AssignmentsPublishedAt),
		},
		ProgrammeID: row.ProgrammeID,
	}
	if row.SubjectGroupID.Valid {
		out.SubjectGroupID = row.SubjectGroupID.UUID
	}
	if row.AssignmentID.Valid {
		out.AssignmentID = row.AssignmentID.UUID
	}
	return out
}

// assignmentRow is the shape both assignment queries produce.
//
// sqlc emits one type per query; they are structurally identical because the SELECT lists are.
// Converting rather than copying field by field makes that a compile-time claim: two queries whose
// projections drift apart would be two shapes of the same record, and only one of them tested.
type assignmentRow struct {
	ID                  uuid.UUID
	InstancePartID      uuid.UUID
	PersonID            uuid.NullUUID
	TeacherID           uuid.NullUUID
	Note                string
	AssignedBy          uuid.NullUUID
	CreatedAt           time.Time
	UpdatedAt           time.Time
	PartKind            string
	PartPosition        int32
	PartTeachingHours   pgtype.Numeric
	ServesSiblingTracks bool
	CourseInstanceID    uuid.UUID
	Track               string
	ProgrammeSemester   *int32
	SemesterCode        string
	SemesterPhase       string
	ProgrammeID         uuid.UUID
	ProgrammeCode       string
	ProgrammeTitle      string
	ModuleID            uuid.UUID
	ModuleName          string
	AssigneeName        string
	AssigneeMail        string
	AssigneeSortName    string
}

// assignmentFrom builds the domain record.
//
// The instance is carried in full because an assignment is unreadable without it, and its Parts
// are deliberately left empty: this query renders one row per assignment, and joining out the
// parts would turn each of them into one row per part. What the screen needs of the part is on
// the assignment itself.
func assignmentFrom(row assignmentRow) domain.Assignment {
	out := domain.Assignment{
		ID: row.ID,
		Part: domain.InstancePart{
			ID:                 row.InstancePartID,
			Kind:               domain.InstancePartKind(row.PartKind),
			Position:           int(row.PartPosition),
			TeachingHours:      numericFloatOrNil(row.PartTeachingHours),
			SharedAcrossTracks: row.ServesSiblingTracks,
		},
		Instance: domain.CourseInstance{
			ID:                row.CourseInstanceID,
			SemesterCode:      row.SemesterCode,
			SemesterPhase:     policy.Phase(row.SemesterPhase),
			Track:             row.Track,
			ProgrammeSemester: intOrNil(row.ProgrammeSemester),
			Module: domain.Module{
				ID:   row.ModuleID,
				Name: row.ModuleName,
			},
			Programme: domain.Programme{
				ID:    row.ProgrammeID,
				Code:  row.ProgrammeCode,
				Title: row.ProgrammeTitle,
			},
		},
		Assignee: domain.Assignee{
			Name:     domain.PlainName(row.AssigneeName, row.AssigneeSortName),
			Mail:     row.AssigneeMail,
			SortName: row.AssigneeSortName,
		},
		Note:      row.Note,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}
	if row.PersonID.Valid {
		out.Assignee.PersonID = row.PersonID.UUID
	}
	if row.TeacherID.Valid {
		out.Assignee.TeacherID = row.TeacherID.UUID
	}
	if row.AssignedBy.Valid {
		out.AssignedBy = row.AssignedBy.UUID
	}
	return out
}
