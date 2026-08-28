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

// Wishes is the persistence behind domain.WishService.
//
// The one thing worth reading in this file is wishFilterParams: every read here takes a
// policy.WishFilter and turns it into query parameters, so the confidentiality rule runs as a
// WHERE clause rather than over rows already read. That is not an optimisation. A predicate
// applied after the fact is one that exports, digests and the token path do not have, and a
// COUNT would skip entirely — which is why db/queries/wish.sql has no COUNT at all.
type Wishes struct {
	pool *pgxpool.Pool
}

// NewWishes wires one up.
func NewWishes(pool *pgxpool.Pool) *Wishes { return &Wishes{pool: pool} }

var _ domain.WishStore = (*Wishes)(nil)

// wishFilterParams turns the policy's filter into the four parameters every wish query takes.
//
// A function rather than four fields the caller fills in, because the parameters are meaningless
// apart and dangerous alone: a scope of "own_or_scoped" with the id lists left empty is a filter
// that silently reaches nothing, and "all" with an owner set reads as if it were restricted.
// Building them in one place is what keeps them consistent.
//
// The empty slices matter. `= ANY(NULL)` is NULL and therefore not true, which would be the right
// answer — but a nil slice reaching pgx as NULL rather than as an empty array is the kind of
// thing that is true of one driver version and not the next. An explicit empty array says what is
// meant.
func wishFilterParams(f policy.WishFilter) (scope string, owner uuid.UUID,
	programmes, groups []uuid.UUID) {
	programmes, groups = []uuid.UUID{}, []uuid.UUID{}

	switch f.Scope {
	case policy.WishScopeAll:
		return string(policy.WishScopeAll), uuid.Nil, programmes, groups
	case policy.WishScopeOwn:
		return string(policy.WishScopeOwn), f.OwnerID, programmes, groups
	case policy.WishScopeOwnOrScoped:
		if len(f.ProgrammeIDs) > 0 {
			programmes = f.ProgrammeIDs
		}
		if len(f.SubjectGroupIDs) > 0 {
			groups = f.SubjectGroupIDs
		}
		return string(policy.WishScopeOwnOrScoped), f.OwnerID, programmes, groups
	default:
		// WishScopeNone and anything this build does not know. The query matches no branch for an
		// unrecognised string, which is the same fail-closed reading WishFilter.Matches takes —
		// and "none" is spelled out rather than passed through so that a future scope value
		// cannot arrive here and accidentally mean something.
		return "none", uuid.Nil, programmes, groups
	}
}

// Wishes returns the wishes the filter allows — of one semester, or of every semester when the
// query names none.
func (w *Wishes) Wishes(ctx context.Context, q domain.WishQuery,
	filter policy.WishFilter) ([]domain.Wish, error) {
	scope, owner, programmes, groups := wishFilterParams(filter)

	params := WishesOfSemesterParams{
		Scope:           scope,
		OwnerID:         owner,
		ProgrammeIds:    programmes,
		SubjectGroupIds: groups,
	}

	if q.SemesterCode != "" {
		semester, err := New(w.pool).SemesterByCode(ctx, q.SemesterCode)
		if errors.Is(err, pgx.ErrNoRows) {
			// No row means nobody has decided anything about this semester, so it holds no
			// instances and therefore no wishes.
			return []domain.Wish{}, nil
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

	rows, err := New(w.pool).WishesOfSemester(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("cannot read the wishes: %w", err)
	}

	out := make([]domain.Wish, 0, len(rows))
	for _, row := range rows {
		out = append(out, wishFrom(wishRow(row)))
	}
	return out, nil
}

// WishByID returns one wish through the same filter, or (nil, nil).
//
// "Not there" and "not yours to see" are deliberately the same answer: the difference between
// them is the fact the whole rule protects.
func (w *Wishes) WishByID(ctx context.Context, id uuid.UUID,
	filter policy.WishFilter) (*domain.Wish, error) {
	scope, owner, programmes, groups := wishFilterParams(filter)

	row, err := New(w.pool).WishByID(ctx, WishByIDParams{
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
		return nil, fmt.Errorf("cannot read the wish: %w", err)
	}

	wish := wishFrom(wishRow(row))
	return &wish, nil
}

// SetWish registers or updates one person's own interest.
func (w *Wishes) SetWish(ctx context.Context, instanceID, personID uuid.UUID,
	priority domain.WishPriority, note string) (*domain.Wish, error) {
	level, ok := priority.Level()
	if !ok {
		return nil, domain.ErrWishPriorityInvalid
	}

	written, err := New(w.pool).UpsertWish(ctx, UpsertWishParams{
		CourseInstanceID: instanceID,
		PersonID:         personID,
		Priority:         level,
		Note:             note,
	})
	// The instance may have been withdrawn between the phase check and the write.
	if isForeignKeyViolation(err) {
		return nil, domain.ErrInstanceNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("cannot register the wish: %w", err)
	}

	// Read back through the owner's own filter, so that the answer is assembled by the same query
	// that renders a list — rather than by a second assembly of the same shape, which is a second
	// place for the two to disagree.
	wish, err := w.WishByID(ctx, written.ID, policy.WishFilter{
		Scope:   policy.WishScopeOwn,
		OwnerID: personID,
	})
	if err != nil {
		return nil, err
	}
	if wish == nil {
		return nil, domain.ErrWishNotFound
	}
	return wish, nil
}

// WithdrawWish removes one person's own wish.
func (w *Wishes) WithdrawWish(ctx context.Context, id, personID uuid.UUID) error {
	rows, err := New(w.pool).DeleteOwnWish(ctx, DeleteOwnWishParams{ID: id, PersonID: personID})
	if err != nil {
		return fmt.Errorf("cannot withdraw the wish: %w", err)
	}
	if rows == 0 {
		// Either it is gone or it was never this person's. The same answer for both, because
		// telling them apart would say whose it is.
		return domain.ErrWishNotFound
	}
	return nil
}

// WishWriteContext is what the write rule needs about an instance: its semester, and whether the
// subject group of its module is taking entries.
//
// One statement for both halves. They used to be one — the semester alone — and the window arrived
// on 2026-08-28 as the thing that actually ends a wish round; reading them apart would be two
// round trips and two chances to decide against a state that has since moved.
func (w *Wishes) WishWriteContext(ctx context.Context,
	instanceID uuid.UUID) (domain.WishWriteContext, error) {
	row, err := New(w.pool).WishWriteContext(ctx, instanceID)
	if errors.Is(err, pgx.ErrNoRows) {
		// The zero value answers Found() false. Not an error: an instance withdrawn between two
		// clicks is ordinary, and the service turns it into a refusal.
		return domain.WishWriteContext{}, nil
	}
	if err != nil {
		return domain.WishWriteContext{}, fmt.Errorf("cannot read the instance's context: %w", err)
	}
	return wishWriteContextFrom(row), nil
}

// wishRow is the shape both wish queries produce.
//
// sqlc emits one type per query; they are structurally identical because the SELECT lists are.
// Converting rather than copying field by field makes that a compile-time claim — the same
// arrangement teacherRow makes, and the same reason: two queries whose projections drift apart
// would be two shapes of the same record.
type wishRow struct {
	ID                uuid.UUID
	CourseInstanceID  uuid.UUID
	PersonID          uuid.UUID
	Priority          int16
	Note              string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	Track             string
	ProgrammeSemester *int32
	SemesterCode      string
	SemesterPhase     string
	TeachingHours     float64
	ProgrammeID       uuid.UUID
	ProgrammeCode     string
	ProgrammeTitle    string
	ModuleID          uuid.UUID
	ModuleName        string
	PersonMail        string
	PersonName        string
	PersonSortName    string
}

func wishFrom(row wishRow) domain.Wish {
	return domain.Wish{
		ID: row.ID,
		Person: domain.Person{
			ID:       row.PersonID,
			Mail:     row.PersonMail,
			Name:     domain.PlainName(row.PersonName, row.PersonSortName),
			SortName: row.PersonSortName,
			Active:   true,
		},
		Instance: domain.CourseInstance{
			ID: row.CourseInstanceID,
			// The parts are not in this projection, so the sum comes from the query. See the
			// field's own comment: exactly one of the two is ever the source.
			HoursFromQuery:    &row.TeachingHours,
			SemesterCode:      row.SemesterCode,
			SemesterPhase:     policy.Phase(row.SemesterPhase),
			Track:             row.Track,
			ProgrammeSemester: intOrNil(row.ProgrammeSemester),
			Module:            domain.Module{ID: row.ModuleID, Name: row.ModuleName},
			Programme: domain.Programme{
				ID: row.ProgrammeID, Code: row.ProgrammeCode, Title: row.ProgrammeTitle,
			},
		},
		Priority:  domain.WishPriorityFromLevel(row.Priority),
		Note:      row.Note,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}
}
