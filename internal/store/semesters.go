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

// Semesters is the persistence behind domain.SemesterService.
type Semesters struct {
	q *Queries
	// pool is here for the one operation that needs two statements to be one decision: moving
	// the planning mark. Everything else on this type is a single statement, which is why the
	// queries are held separately rather than opened from the pool each time.
	pool *pgxpool.Pool
}

// NewSemesters binds the semester queries to a pool.
func NewSemesters(pool *pgxpool.Pool) *Semesters { return &Semesters{q: New(pool), pool: pool} }

var _ domain.SemesterStore = (*Semesters)(nil)

// semesterFrom reshapes a generated row into the domain type.
//
// The phase becomes a policy.Phase here and nowhere else. It is not validated on the way
// through: a phase this build does not know is a fact about the row, and the service is the
// layer that decides what to do about it. Swallowing it here — substituting a default —
// would land on DEMAND_PLANNING, which is the most permissive phase for writes.
func semesterFrom(row Semester) domain.Semester {
	return domain.Semester{
		ID:                row.ID,
		Code:              row.Code,
		Phase:             policy.Phase(row.Phase),
		IsPlanning:        row.IsPlanningSemester,
		WishesPublishedAt: nullableTime(row.WishesPublishedAt),
		CreatedAt:         row.CreatedAt,
		UpdatedAt:         row.UpdatedAt,
	}
}

// EnsureSemester returns the row for a code, creating it on the first decision about it.
//
// The uniqueness of the code is what makes this safe when two people arrive together, and it
// is doing that work inside a single statement — see the query. A check-then-insert in Go
// would pass its unit test and lose the race in the meeting where two members of the dean's
// office switch the same semester at the same moment.
//
// A duplicate is therefore not an error and there is no SQLSTATE 23505 to map here — the
// caller wanted the row for this semester and gets it, whoever inserted it. The habit of
// mapping that code rather than forwarding it still has to be in place for the wish workflow,
// where the driver's verbatim text would reveal that a colleague has already registered
// interest; this is simply no longer a place that can raise it.
func (s *Semesters) EnsureSemester(ctx context.Context, code string) (domain.Semester, error) {
	row, err := s.q.EnsureSemester(ctx, code)
	if err != nil {
		return domain.Semester{}, fmt.Errorf("cannot record semester: %w", err)
	}
	return semesterFrom(row), nil
}

// SemesterByCode returns the row for a code, or a zero Semester when there is none.
//
// No row is not an error and not a missing thing: it is a semester nobody has decided anything
// about, which is an ordinary and by far the most common state. The domain turns it into the
// defaults an untouched semester has, in one place, so that this layer does not have to hold
// an opinion about what DEMAND_PLANNING means.
func (s *Semesters) SemesterByCode(ctx context.Context, code string) (domain.Semester, error) {
	row, err := s.q.SemesterByCode(ctx, code)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Semester{}, nil
		}
		return domain.Semester{}, fmt.Errorf("cannot read semester: %w", err)
	}
	return semesterFrom(row), nil
}

// Semesters lists the recorded ones, newest first.
func (s *Semesters) Semesters(ctx context.Context) ([]domain.Semester, error) {
	rows, err := s.q.Semesters(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot list semesters: %w", err)
	}

	out := make([]domain.Semester, 0, len(rows))
	for _, row := range rows {
		out = append(out, semesterFrom(row))
	}
	return out, nil
}

// AdvanceSemesterPhase moves a semester, but only if it is still where the caller thinks.
//
// No rows back means the phase changed under the caller, and it can mean nothing else: the
// caller ensured the row moments ago and there is no way to delete one. So the answer is
// domain.ErrPhaseMovedOn, whose sentence asks for a reload — which is the useful instruction,
// because the page in front of whoever clicked is simply out of date.
func (s *Semesters) AdvanceSemesterPhase(ctx context.Context, id uuid.UUID,
	from, to policy.Phase,
) (domain.Semester, error) {
	row, err := s.q.AdvanceSemesterPhase(ctx, AdvanceSemesterPhaseParams{
		ID:      id,
		Phase:   string(to),
		Phase_2: string(from),
	})
	if err == nil {
		return semesterFrom(row), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.Semester{}, fmt.Errorf("cannot switch phase: %w", err)
	}

	return domain.Semester{}, domain.ErrPhaseMovedOn
}

// PublishSemesterWishes ends the confidentiality window. Idempotent — see the query.
func (s *Semesters) PublishSemesterWishes(ctx context.Context, id uuid.UUID,
) (domain.Semester, error) {
	row, err := s.q.PublishSemesterWishes(ctx, id)
	if err != nil {
		return domain.Semester{}, fmt.Errorf("cannot publish wishes: %w", err)
	}
	return semesterFrom(row), nil
}

// PlanningSemester returns the semester the faculty is planning, or a zero Semester when
// nobody has said.
//
// No row is not an error, for the same reason SemesterByCode says so: it is a state the system
// really has — a fresh installation, or one whose planning mark was rolled back with the
// column — and the service turns it into the fallback the list uses.
func (s *Semesters) PlanningSemester(ctx context.Context) (domain.Semester, error) {
	row, err := s.q.PlanningSemester(ctx)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Semester{}, nil
		}
		return domain.Semester{}, fmt.Errorf("cannot read the planning semester: %w", err)
	}
	return semesterFrom(row), nil
}

// SetPlanningSemester moves the mark onto this semester, in one transaction.
//
// Two statements, and both halves are needed: a database in which two semesters carry the mark
// is one the unique index refuses to be in, so writing only the second half would fail rather
// than corrupt — but it would fail with a constraint violation, which says nothing anybody can
// act on. Clearing first turns that into ordinary serialisation: the UPDATE takes a row lock on
// whichever semester currently carries the mark, and two people deciding at the same moment
// take turns instead of colliding. The second one wins, which is what a decision taken on
// purpose should do.
func (s *Semesters) SetPlanningSemester(ctx context.Context, id, by uuid.UUID,
) (domain.Semester, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Semester{}, fmt.Errorf("cannot begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := New(tx)

	if err := q.ClearPlanningSemester(ctx, id); err != nil {
		return domain.Semester{}, fmt.Errorf("cannot clear the planning semester: %w", err)
	}

	row, err := q.MarkPlanningSemester(ctx, MarkPlanningSemesterParams{
		ID:    id,
		SetBy: nullUUID(nonNilUUID(by)),
	})
	if err != nil {
		return domain.Semester{}, fmt.Errorf("cannot set the planning semester: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.Semester{}, fmt.Errorf("cannot commit: %w", err)
	}
	return semesterFrom(row), nil
}
