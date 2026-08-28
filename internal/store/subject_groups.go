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

// SubjectGroups is the store half of the faculty's own grouping of modules and people.
type SubjectGroups struct {
	pool *pgxpool.Pool
}

// NewSubjectGroups wires one up.
func NewSubjectGroups(pool *pgxpool.Pool) *SubjectGroups {
	return &SubjectGroups{pool: pool}
}

// SubjectGroups lists the groups with their leads, members and module counts.
//
// Three statements for the whole screen and none of them per row: the groups, then the leads of
// all of them, then the members of all of them. The alternative — a lead list per group — is the
// shape that looks fine on ten rows and is discovered on a hundred.
func (s *SubjectGroups) SubjectGroups(ctx context.Context,
	includeInactive bool) ([]domain.SubjectGroup, error) {
	rows, err := New(s.pool).SubjectGroups(ctx, includeInactive)
	if err != nil {
		return nil, fmt.Errorf("cannot read the subject groups: %w", err)
	}

	groups := make([]domain.SubjectGroup, 0, len(rows))
	for _, row := range rows {
		groups = append(groups, subjectGroupFrom(row.ID, row.Code, row.Name, row.Active,
			row.ModuleCount, row.CreatedAt, row.UpdatedAt))
	}
	return s.withPeople(ctx, groups)
}

// SubjectGroupByID returns one group, or (nil, nil) — the convention this repository uses for
// "not found" everywhere.
func (s *SubjectGroups) SubjectGroupByID(ctx context.Context,
	id uuid.UUID) (*domain.SubjectGroup, error) {
	row, err := New(s.pool).SubjectGroupByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read the subject group: %w", err)
	}

	group := subjectGroupFrom(row.ID, row.Code, row.Name, row.Active, row.ModuleCount,
		row.CreatedAt, row.UpdatedAt)
	filled, err := s.withPeople(ctx, []domain.SubjectGroup{group})
	if err != nil {
		return nil, err
	}
	return &filled[0], nil
}

// SubjectGroupsOfPerson is one person's memberships.
func (s *SubjectGroups) SubjectGroupsOfPerson(ctx context.Context,
	personID uuid.UUID) ([]domain.SubjectGroup, error) {
	rows, err := New(s.pool).SubjectGroupsOfPerson(ctx, personID)
	if err != nil {
		return nil, fmt.Errorf("cannot read the subject groups of the person: %w", err)
	}

	// Filled in like every other read of a group, rather than left half-populated. A type whose
	// fields are sometimes there and sometimes not is one every caller has to know the
	// provenance of, and the saving is two statements over a handful of rows.
	groups := make([]domain.SubjectGroup, 0, len(rows))
	for _, row := range rows {
		groups = append(groups, subjectGroupFrom(row.ID, row.Code, row.Name, row.Active,
			row.ModuleCount, row.CreatedAt, row.UpdatedAt))
	}
	return s.withPeople(ctx, groups)
}

// CreateSubjectGroup adds one.
func (s *SubjectGroups) CreateSubjectGroup(ctx context.Context,
	code, name string) (*domain.SubjectGroup, error) {
	row, err := New(s.pool).CreateSubjectGroup(ctx, CreateSubjectGroupParams{
		Code: code,
		Name: name,
	})
	if isUniqueViolation(err) {
		return nil, fmt.Errorf("%w: %q", domain.ErrSubjectGroupCodeTaken, code)
	}
	if err != nil {
		return nil, fmt.Errorf("cannot create the subject group: %w", err)
	}

	group := subjectGroupFrom(row.ID, row.Code, row.Name, row.Active, row.ModuleCount,
		row.CreatedAt, row.UpdatedAt)
	return &group, nil
}

// RenameSubjectGroup writes the name.
func (s *SubjectGroups) RenameSubjectGroup(ctx context.Context, id uuid.UUID,
	name string) (*domain.SubjectGroup, error) {
	row, err := New(s.pool).RenameSubjectGroup(ctx, RenameSubjectGroupParams{ID: id, Name: name})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrSubjectGroupNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("cannot rename the subject group: %w", err)
	}

	group := subjectGroupFrom(row.ID, row.Code, row.Name, row.Active, row.ModuleCount,
		row.CreatedAt, row.UpdatedAt)
	return &group, nil
}

// SetSubjectGroupActive retires a group or brings it back.
func (s *SubjectGroups) SetSubjectGroupActive(ctx context.Context, id uuid.UUID,
	active bool) (*domain.SubjectGroup, error) {
	row, err := New(s.pool).SetSubjectGroupActive(ctx, SetSubjectGroupActiveParams{
		ID:     id,
		Active: active,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrSubjectGroupNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("cannot retire the subject group: %w", err)
	}

	group := subjectGroupFrom(row.ID, row.Code, row.Name, row.Active, row.ModuleCount,
		row.CreatedAt, row.UpdatedAt)
	return &group, nil
}

// SetModulesSubjectGroup assigns a batch of modules to one group, or clears their assignment.
//
// Returns how many rows it wrote. "Nothing happened" and "it failed" are indistinguishable to
// the person who pressed the button otherwise — the same argument CopyDemandReport makes for
// reporting its zeroes.
func (s *SubjectGroups) SetModulesSubjectGroup(ctx context.Context, moduleIDs []uuid.UUID,
	group uuid.NullUUID, assignedBy uuid.UUID) (int, error) {
	if !group.Valid {
		written, err := New(s.pool).ClearModulesSubjectGroup(ctx, moduleIDs)
		if err != nil {
			return 0, fmt.Errorf("cannot clear the subject group of the modules: %w", err)
		}
		return int(written), nil
	}

	written, err := New(s.pool).AssignModulesToSubjectGroup(ctx, AssignModulesToSubjectGroupParams{
		SubjectGroupID: group.UUID,
		AssignedBy:     nullUUID(nonNilUUID(assignedBy)),
		ModuleIds:      moduleIDs,
	})
	// The foreign key refuses a group that does not exist, and a module that does not either.
	// Reported as the group, because the module ids come from a list the screen just rendered
	// and the group is the thing somebody chose.
	if isForeignKeyViolation(err) {
		return 0, domain.ErrSubjectGroupNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("cannot assign the modules: %w", err)
	}
	return int(written), nil
}

// SetSubjectGroupMembers replaces the members of one group.
//
// In a transaction, for the reason SetPersonProgrammes gives: delete-then-insert has a moment in
// between in which the group has nobody in it, and somebody's wish screen landing in that moment
// would be filtered to nothing for a reason that was never true.
func (s *SubjectGroups) SetSubjectGroupMembers(ctx context.Context, groupID uuid.UUID,
	people []uuid.UUID, grantedBy uuid.UUID) error {
	return s.inTx(ctx, func(q *Queries) error {
		if err := q.ClearSubjectGroupMembers(ctx, groupID); err != nil {
			return fmt.Errorf("cannot clear the members: %w", err)
		}
		for _, person := range people {
			err := q.AddSubjectGroupMembership(ctx, AddSubjectGroupMembershipParams{
				PersonID:       person,
				SubjectGroupID: groupID,
				GrantedBy:      nullUUID(nonNilUUID(grantedBy)),
			})
			// The group or the person may not exist. Reported as the group, because the person
			// ids come from a list the screen just rendered and the group is what somebody
			// navigated to.
			if isForeignKeyViolation(err) {
				return domain.ErrSubjectGroupNotFound
			}
			if err != nil {
				return fmt.Errorf("cannot add a member: %w", err)
			}
		}
		return nil
	})
}

// SetSubjectGroupLeads replaces the people leading one group.
//
// A transaction for the same reason as the memberships, and with a sharper edge: leading nothing
// has a meaning here. A lead with no group may do nothing at all, deliberately — so a request
// landing between the delete and the insert would be refused for a reason that was never true,
// and the sentence it would be refused with names a repair the administrator was in the middle
// of performing.
func (s *SubjectGroups) SetSubjectGroupLeads(ctx context.Context, groupID uuid.UUID,
	people []uuid.UUID, grantedBy uuid.UUID) error {
	return s.inTx(ctx, func(q *Queries) error {
		if err := q.ClearSubjectGroupLeads(ctx, groupID); err != nil {
			return fmt.Errorf("cannot clear the leads: %w", err)
		}
		for _, person := range people {
			err := q.AddSubjectGroupLead(ctx, AddSubjectGroupLeadParams{
				PersonID:       person,
				SubjectGroupID: groupID,
				GrantedBy:      nullUUID(nonNilUUID(grantedBy)),
			})
			// Two constraints can refuse here and they mean different things: the group may not
			// exist, or the person may not hold SUBJECT_GROUP_LEAD. The service checks the role
			// first so that the ordinary case gets its own sentence; what reaches this point is
			// the race, and the safe report for a race is the one that does not claim to know
			// which half lost.
			if isForeignKeyViolation(err) {
				return domain.ErrNotASubjectGroupLead
			}
			if err != nil {
				return fmt.Errorf("cannot add a lead: %w", err)
			}
		}
		return nil
	})
}

// SetSubjectGroupsOfPerson replaces one person's memberships.
//
// In a transaction, for the reason the group-side write gives: delete-then-insert has a moment in
// between in which the person is in no group at all, and their own wish screen landing in that
// moment would be filtered to nothing for a reason that was never true.
func (s *SubjectGroups) SetSubjectGroupsOfPerson(ctx context.Context, personID uuid.UUID,
	groups []uuid.UUID, grantedBy uuid.UUID) error {
	return s.inTx(ctx, func(q *Queries) error {
		if err := q.ClearSubjectGroupsOfPerson(ctx, personID); err != nil {
			return fmt.Errorf("cannot clear the memberships: %w", err)
		}
		for _, group := range groups {
			err := q.AddSubjectGroupMembership(ctx, AddSubjectGroupMembershipParams{
				PersonID:       personID,
				SubjectGroupID: group,
				GrantedBy:      nullUUID(nonNilUUID(grantedBy)),
			})
			if isForeignKeyViolation(err) {
				return domain.ErrSubjectGroupNotFound
			}
			if err != nil {
				return fmt.Errorf("cannot add a membership: %w", err)
			}
		}
		return nil
	})
}

// ModulesOfSubjectGroup is what a group holds.
func (s *SubjectGroups) ModulesOfSubjectGroup(ctx context.Context,
	groupID uuid.UUID) ([]domain.ModuleRef, error) {
	rows, err := New(s.pool).ModulesOfSubjectGroup(ctx, groupID)
	if err != nil {
		return nil, fmt.Errorf("cannot read the modules of the subject group: %w", err)
	}

	out := make([]domain.ModuleRef, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.ModuleRef{
			ID: row.ID, Name: row.Name, HomeProgrammeCode: row.HomeProgrammeCode,
		})
	}
	return out, nil
}

// ModulesWithoutSubjectGroup is October's work list as a number.
func (s *SubjectGroups) ModulesWithoutSubjectGroup(ctx context.Context) (int, error) {
	n, err := New(s.pool).ModulesWithoutSubjectGroup(ctx)
	if err != nil {
		return 0, fmt.Errorf("cannot count the modules without a subject group: %w", err)
	}
	return int(n), nil
}

// SubjectGroupsWithoutLead is the other half of it: "keine Fachgruppe ohne Person, die sich ihrer
// annimmt", as a number rather than as a constraint.
func (s *SubjectGroups) SubjectGroupsWithoutLead(ctx context.Context) (int, error) {
	n, err := New(s.pool).SubjectGroupsWithoutLead(ctx)
	if err != nil {
		return 0, fmt.Errorf("cannot count the subject groups without a lead: %w", err)
	}
	return int(n), nil
}

// withPeople fills in the leads and the members of a set of groups, in two statements.
func (s *SubjectGroups) withPeople(ctx context.Context,
	groups []domain.SubjectGroup) ([]domain.SubjectGroup, error) {
	if len(groups) == 0 {
		return groups, nil
	}

	ids := make([]uuid.UUID, 0, len(groups))
	for _, g := range groups {
		ids = append(ids, g.ID)
	}

	q := New(s.pool)

	leads, err := q.SubjectGroupLeadsFor(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("cannot read the subject group leads: %w", err)
	}
	members, err := q.SubjectGroupMembersFor(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("cannot read the subject group members: %w", err)
	}

	byLead := make(map[uuid.UUID][]domain.Person, len(groups))
	for _, row := range leads {
		byLead[row.SubjectGroupID] = append(byLead[row.SubjectGroupID], domain.Person{
			ID: row.ID, Mail: row.Mail, SortName: row.SortName,
			Name:   domain.PlainName(row.Name, row.SortName),
			Active: row.Active,
			// The role is not read back: everybody in this list holds SUBJECT_GROUP_LEAD by
			// construction, since the query joins through person_role to find them.
			Roles: []policy.Role{policy.RoleSubjectGroupLead},
		})
	}
	byMember := make(map[uuid.UUID][]domain.Person, len(groups))
	for _, row := range members {
		byMember[row.SubjectGroupID] = append(byMember[row.SubjectGroupID], domain.Person{
			ID: row.ID, Mail: row.Mail, SortName: row.SortName,
			Name:   domain.PlainName(row.Name, row.SortName),
			Active: row.Active,
		})
	}

	for i := range groups {
		groups[i].Leads = byLead[groups[i].ID]
		groups[i].Members = byMember[groups[i].ID]
	}
	return groups, nil
}

// inTx runs fn in a transaction. Rollback after a successful commit is a no-op, so this needs no
// branching.
func (s *SubjectGroups) inTx(ctx context.Context, fn func(*Queries) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("cannot begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(New(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("cannot commit: %w", err)
	}
	return nil
}

// subjectGroupFrom is the one place the generated row shapes become the domain type. The four
// queries returning a group all return the same columns, and this keeps them agreeing about what
// those columns mean.
func subjectGroupFrom(id uuid.UUID, code, name string, active bool, moduleCount int32,
	created, updated time.Time) domain.SubjectGroup {
	return domain.SubjectGroup{
		ID:          id,
		Code:        code,
		Name:        name,
		Active:      active,
		ModuleCount: int(moduleCount),
		CreatedAt:   created,
		UpdatedAt:   updated,
	}
}
