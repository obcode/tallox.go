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

// People is the persistence behind domain.PeopleService.
//
// It takes a pool rather than a DBTX, unlike Tokens and Directory, because two of its
// operations are not single statements: removing an administrator has to lock, check and
// write in one transaction, and a type that could be handed a DBTX would be a type that
// cannot begin one. Making that visible in the constructor is better than discovering it at
// the call site.
type People struct {
	pool *pgxpool.Pool
}

// NewPeople binds user administration to a pool.
func NewPeople(pool *pgxpool.Pool) *People { return &People{pool: pool} }

var _ domain.PeopleStore = (*People)(nil)

// ListPeople returns the people this installation knows.
func (p *People) ListPeople(ctx context.Context, search string,
	includeInactive bool) ([]domain.Person, error) {
	var searchArg *string
	if search != "" {
		searchArg = &search
	}

	rows, err := New(p.pool).ListPeople(ctx, ListPeopleParams{
		Search:          searchArg,
		IncludeInactive: includeInactive,
	})
	if err != nil {
		return nil, fmt.Errorf("cannot list people: %w", err)
	}

	people := make([]domain.Person, 0, len(rows))
	for _, row := range rows {
		people = append(people, domain.Person{
			ID:       row.ID,
			Mail:     row.Mail,
			Name:     domain.PlainName(row.Name, row.SortName),
			SortName: row.SortName,
			Active:   row.Active,
			Roles:    knownRoles(row.Roles),
		})
	}

	// One statement for the whole list rather than one per row: the administration screen shows
	// which programmes each lead is assigned to, and a query per person would make that screen
	// cost a round trip per colleague.
	if err := p.attachProgrammes(ctx, pointersTo(people)); err != nil {
		return nil, err
	}
	return people, nil
}

// pointersTo addresses the elements of a slice, so that a value slice can be filled in by
// something that writes through pointers.
func pointersTo(people []domain.Person) []*domain.Person {
	out := make([]*domain.Person, len(people))
	for i := range people {
		out[i] = &people[i]
	}
	return out
}

// attachProgrammes fills in the study programmes each person's leadership applies to.
//
// Pointers rather than a value slice, because two of the four callers hold one person and a
// third holds people reached through a teacher — writing back by index would mean a different
// copy-out dance at each of them, and one of those dances would eventually be wrong.
func (p *People) attachProgrammes(ctx context.Context, people []*domain.Person) error {
	ids := make([]uuid.UUID, 0, len(people))
	for _, person := range people {
		if person != nil {
			ids = append(ids, person.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}

	rows, err := New(p.pool).ProgrammeScopesFor(ctx, ids)
	if err != nil {
		return fmt.Errorf("cannot read the programme assignments: %w", err)
	}

	byPerson := make(map[uuid.UUID][]domain.Programme, len(ids))
	for _, row := range rows {
		byPerson[row.PersonID] = append(byPerson[row.PersonID], domain.Programme{
			ID:     row.ProgrammeID,
			Code:   row.Code,
			Title:  row.Title,
			Active: row.Active,
		})
	}

	for _, person := range people {
		if person != nil {
			person.Programmes = byPerson[person.ID]
		}
	}
	return nil
}

// TeacherAccounts lists everybody the examination office publishes, with the account they have
// here.
//
// The join is on the mail address, done on every read rather than stored, so that somebody
// admitted this morning is connected now rather than after the next import — the reason there is
// no teacher.person_id column at all.
func (p *People) TeacherAccounts(ctx context.Context) ([]domain.TeacherAccount, error) {
	rows, err := New(p.pool).ListTeacherAccounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot list the teacher accounts: %w", err)
	}

	accounts := make([]domain.TeacherAccount, 0, len(rows))
	for _, row := range rows {
		accounts = append(accounts, teacherAccountFrom(teacherAccountRow(row)))
	}

	// One statement for the whole list, like ListPeople: the screen shows which programmes each
	// lead is assigned to, and a query per row would cost a round trip per colleague.
	people := make([]*domain.Person, 0, len(accounts))
	for i := range accounts {
		if accounts[i].Person != nil {
			people = append(people, accounts[i].Person)
		}
	}
	if err := p.attachProgrammes(ctx, people); err != nil {
		return nil, err
	}
	return accounts, nil
}

// TeacherAccountByID resolves one teacher and their account. "Not found" is (nil, nil).
func (p *People) TeacherAccountByID(ctx context.Context,
	teacherID uuid.UUID) (*domain.TeacherAccount, error) {
	row, err := New(p.pool).TeacherAccountByID(ctx, teacherID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read the teacher account: %w", err)
	}

	account := teacherAccountFrom(teacherAccountRow(row))
	if account.Person != nil {
		if err := p.attachProgrammes(ctx, []*domain.Person{account.Person}); err != nil {
			return nil, err
		}
	}
	return &account, nil
}

// teacherAccountRow is the shape both teacher-account queries produce.
//
// sqlc emits one type per query and they are structurally identical because the SELECT lists
// are, the same trick teacherRow plays in module.go. The teacher half is then handed to
// teacherFrom rather than copied again — one description of a teacher row, in one place.
type teacherAccountRow struct {
	TeacherID            uuid.UUID
	Mail                 string
	FullName             string
	ShortName            string
	IsProfessor          bool
	IsLecturerOnContract bool
	IsHonoraryProfessor  bool
	IsStaff              bool
	Active               bool
	Faculty              *string
	LastSemester         *string
	PersonID             uuid.NullUUID
	PersonMail           string
	PersonName           string
	PersonActive         bool
	Roles                []string
}

func teacherAccountFrom(row teacherAccountRow) domain.TeacherAccount {
	account := domain.TeacherAccount{
		Teacher: teacherFrom(teacherRow{
			ID:                   row.TeacherID,
			Mail:                 row.Mail,
			FullName:             row.FullName,
			ShortName:            row.ShortName,
			IsProfessor:          row.IsProfessor,
			IsLecturerOnContract: row.IsLecturerOnContract,
			IsHonoraryProfessor:  row.IsHonoraryProfessor,
			IsStaff:              row.IsStaff,
			Active:               row.Active,
			Faculty:              row.Faculty,
			LastSemester:         row.LastSemester,
			// Whether they may sign in, which is what the field means everywhere else. The
			// account below says more, and says it to the one screen that needs more.
			IsUser: row.PersonID.Valid && row.PersonActive,
		}),
	}
	if row.PersonID.Valid {
		account.Person = &domain.Person{
			ID:   row.PersonID.UUID,
			Mail: row.PersonMail,
			// The teacher's surname-first spelling, because it is the same colleague: an
			// account and the row it was made from must not read as two people.
			Name:   domain.PlainName(row.PersonName, row.ShortName),
			Active: row.PersonActive,
			Roles:  knownRoles(row.Roles),
		}
	}
	return account
}

// PersonByID resolves one person. "Not found" is (nil, nil), the convention throughout this
// repository.
func (p *People) PersonByID(ctx context.Context, id uuid.UUID) (*domain.Person, error) {
	row, err := New(p.pool).PersonByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read person: %w", err)
	}
	person := &domain.Person{
		ID:       row.ID,
		Mail:     row.Mail,
		Name:     domain.PlainName(row.Name, row.SortName),
		SortName: row.SortName,
		Active:   row.Active,
		Roles:    knownRoles(row.Roles),
	}
	if err := p.attachProgrammes(ctx, []*domain.Person{person}); err != nil {
		return nil, err
	}
	return person, nil
}

// PersonByMail resolves one person by the address the proxy asserts.
func (p *People) PersonByMail(ctx context.Context, mail string) (*domain.Person, error) {
	row, err := New(p.pool).PersonByMail(ctx, mail)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read person by mail: %w", err)
	}
	person := &domain.Person{
		ID:     row.ID,
		Mail:   row.Mail,
		Name:   row.Name,
		Active: row.Active,
		Roles:  knownRoles(row.Roles),
	}
	if err := p.attachProgrammes(ctx, []*domain.Person{person}); err != nil {
		return nil, err
	}
	return person, nil
}

// CreatePerson adds somebody, with no roles.
func (p *People) CreatePerson(ctx context.Context, mail, name string) (*domain.Person, error) {
	row, err := New(p.pool).CreatePerson(ctx, CreatePersonParams{
		ID:   uuid.New(),
		Mail: mail,
		Name: name,
	})
	if err != nil {
		return nil, fmt.Errorf("cannot create person: %w", err)
	}
	return &domain.Person{
		ID:     row.ID,
		Mail:   row.Mail,
		Name:   row.Name,
		Active: row.Active,
		Roles:  []policy.Role{},
	}, nil
}

// AdmitTeacher gives one of the people the examination office publishes an account here, and
// the role that says what the act means.
//
// One transaction, for three reasons of decreasing size. The first settles it: EnsurePerson is
// an upsert, so a second click — or a second administrator on the same screen — is a no-op
// rather than a unique violation the caller would meet as an unexplainable internal error. The
// second is that a created account with no grant can sign in and see nothing, which reads as a
// permissions bug and is not one. The third is that the three statements are one decision.
//
// The role is a parameter and not a constant here: which role admission carries is a decision,
// and it belongs where it can be read next to its reason, in internal/domain.
//
// No administrator lock, unlike SetPersonActive. This only ever adds an active person and a
// grant, and both directions of that guard are about removal.
func (p *People) AdmitTeacher(ctx context.Context, teacherID uuid.UUID, role policy.Role,
	grantedBy uuid.UUID) (*domain.TeacherAccount, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot begin: %w", err)
	}
	// Rollback after a successful commit is a no-op, so this needs no branching.
	defer func() { _ = tx.Rollback(ctx) }()

	q := New(tx)

	row, err := q.TeacherAccountByID(ctx, teacherID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNoSuchTeacher
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read the teacher: %w", err)
	}
	if row.Mail == "" {
		return nil, domain.ErrTeacherHasNoMail
	}

	person, err := q.EnsurePerson(ctx, EnsurePersonParams{
		ID: uuid.New(),
		// The address as the examination office publishes it, lower-cased by the cache. mail is
		// citext on both sides, so an account created from either list is the same row.
		Mail: row.Mail,
		// Only written when the row is created — EnsurePerson says so. Somebody renamed here, or
		// by a later import, is not renamed back by re-admitting them.
		//
		// The plain spelling, so that the row this account is stored as reads the same as the
		// row it was made from. `me` is the one place that shows person.name without a surname-
		// first spelling beside it to derive from, and an account created titled would be the
		// one titled name left on an otherwise untitled screen.
		Name: domain.PlainName(row.FullName, row.ShortName),
	})
	if err != nil {
		return nil, fmt.Errorf("cannot create the account: %w", err)
	}

	// Re-admitting somebody who was deactivated is the other half of the switch, and it is the
	// same statement: this person may sign in.
	if err := q.SetPersonActive(ctx, SetPersonActiveParams{ID: person.ID, Active: true}); err != nil {
		return nil, fmt.Errorf("cannot activate the account: %w", err)
	}

	if err := q.GrantRole(ctx, GrantRoleParams{
		PersonID:  person.ID,
		Role:      string(role),
		GrantedBy: uuid.NullUUID{UUID: grantedBy, Valid: grantedBy != uuid.Nil},
		// No expiry. A grant with a date on it is for stepping over a threshold and having the
		// database step back; this one says somebody teaches here.
	}); err != nil {
		return nil, fmt.Errorf("cannot grant %s: %w", role, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("cannot commit: %w", err)
	}

	return p.TeacherAccountByID(ctx, teacherID)
}

// SetPersonName renames somebody.
func (p *People) SetPersonName(ctx context.Context, id uuid.UUID, name string) error {
	if err := New(p.pool).SetPersonName(ctx, SetPersonNameParams{ID: id, Name: name}); err != nil {
		return fmt.Errorf("cannot rename person: %w", err)
	}
	return nil
}

// SetPersonActive activates or deactivates somebody, refusing to deactivate the last
// administrator.
//
// Deactivating is a permission change even though it writes the person table: an inactive
// person fails authentication on both doors, so deactivating the only administrator is
// exactly as final as revoking the only ADMIN grant. Both paths therefore take the same lock
// and ask the same question.
func (p *People) SetPersonActive(ctx context.Context, id uuid.UUID, active bool) error {
	if active {
		// Activating can only ever add an administrator. No guard, no transaction.
		if err := New(p.pool).SetPersonActive(ctx, SetPersonActiveParams{
			ID: id, Active: true,
		}); err != nil {
			return fmt.Errorf("cannot activate person: %w", err)
		}
		return nil
	}

	return p.guardingAdmins(ctx, id, func(q *Queries) error {
		if err := q.SetPersonActive(ctx, SetPersonActiveParams{ID: id, Active: false}); err != nil {
			return fmt.Errorf("cannot deactivate person: %w", err)
		}
		return nil
	})
}

// GrantRole grants a role, optionally with an expiry.
//
// No guard: a grant can only ever add somebody who may administer the installation.
func (p *People) GrantRole(ctx context.Context, personID uuid.UUID, role policy.Role,
	grantedBy uuid.UUID, expiresAt time.Time) error {
	err := New(p.pool).GrantRole(ctx, GrantRoleParams{
		PersonID:  personID,
		Role:      string(role),
		GrantedBy: uuid.NullUUID{UUID: grantedBy, Valid: grantedBy != uuid.Nil},
		ExpiresAt: pgtype.Timestamptz{Time: expiresAt, Valid: !expiresAt.IsZero()},
	})
	if err != nil {
		return fmt.Errorf("cannot grant role: %w", err)
	}
	return nil
}

// RevokeRole withdraws a role, refusing to withdraw the last ADMIN grant.
func (p *People) RevokeRole(ctx context.Context, personID uuid.UUID, role policy.Role) error {
	if role != policy.RoleAdmin {
		if err := New(p.pool).RevokeRole(ctx, RevokeRoleParams{
			PersonID: personID, Role: string(role),
		}); err != nil {
			return fmt.Errorf("cannot revoke role: %w", err)
		}
		return nil
	}

	return p.guardingAdmins(ctx, personID, func(q *Queries) error {
		if err := q.RevokeRole(ctx, RevokeRoleParams{
			PersonID: personID, Role: string(policy.RoleAdmin),
		}); err != nil {
			return fmt.Errorf("cannot revoke ADMIN: %w", err)
		}
		return nil
	})
}

// RoleGrants returns one person's grants, expired ones included.
func (p *People) RoleGrants(ctx context.Context, personID uuid.UUID) ([]domain.RoleGrant, error) {
	rows, err := New(p.pool).RoleGrantsByPerson(ctx, personID)
	if err != nil {
		return nil, fmt.Errorf("cannot read role grants: %w", err)
	}

	grants := make([]domain.RoleGrant, 0, len(rows))
	for _, row := range rows {
		role, known := policy.ParseRole(row.Role)
		if !known {
			// A grant the policy cannot interpret grants nothing, and showing it in the
			// administration screen as if it did would be the one misleading thing this
			// list can do. It is still in the table, and the drift test in this package is
			// what makes it visible.
			continue
		}
		var grantedBy uuid.UUID
		if row.GrantedBy.Valid {
			grantedBy = row.GrantedBy.UUID
		}
		grants = append(grants, domain.RoleGrant{
			Role:      role,
			GrantedAt: row.GrantedAt,
			GrantedBy: grantedBy,
			ExpiresAt: nullableTime(row.ExpiresAt),
		})
	}
	return grants, nil
}

// guardingAdmins runs write inside a transaction that has first established that it does not
// remove the last administrator.
//
// The order is the whole point: lock, then read, then write. Reading before locking answers a
// question about a state another transaction is allowed to change in the meantime, which is
// exactly how two administrators remove each other simultaneously and both succeed.
//
// One condition serves both callers, because it is the same condition. Revoking somebody's
// ADMIN and deactivating them are both fatal in precisely the case where that person is
// currently an active administrator and there is no other one — and in every other case both
// are harmless. A person who does not hold ADMIN, or who is already inactive, is not the
// thing standing between this installation and nobody being able to get in.
func (p *People) guardingAdmins(ctx context.Context, personID uuid.UUID,
	write func(q *Queries) error) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("cannot begin the transaction: %w", err)
	}
	// Rollback after a successful commit is a no-op, so this needs no branching.
	defer func() { _ = tx.Rollback(ctx) }()

	q := New(tx)

	if err := q.LockAdminGrants(ctx); err != nil {
		return fmt.Errorf("cannot lock the administrator grants: %w", err)
	}

	others, err := q.CountOtherActiveAdmins(ctx, personID)
	if err != nil {
		return fmt.Errorf("cannot count the other administrators: %w", err)
	}
	if others == 0 {
		person, err := q.PersonByID(ctx, personID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			// Nobody to protect. The write below will be a no-op too.
		case err != nil:
			return fmt.Errorf("cannot read person: %w", err)
		case person.Active && hasRole(person.Roles, string(policy.RoleAdmin)):
			return domain.ErrLastAdmin
		}
	}

	if err := write(q); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("cannot commit: %w", err)
	}
	return nil
}

func hasRole(roles []string, want string) bool {
	for _, r := range roles {
		if r == want {
			return true
		}
	}
	return false
}

// knownRoles drops grants internal/policy does not recognise, exactly as policy.RolesOf does
// on the authentication path.
//
// The same rule in both places, and it has to be: a role that grants nothing when a request is
// judged must not appear in the administration screen as though it granted something. That
// would be an interface telling an administrator they have configured access they have not.
func knownRoles(raw []string) []policy.Role {
	out := make([]policy.Role, 0, len(raw))
	for _, s := range raw {
		if r, ok := policy.ParseRole(s); ok {
			out = append(out, r)
		}
	}
	return out
}

// SetPersonProgrammes replaces the study programmes one person's leadership applies to.
//
// In a transaction, because delete-then-insert has a moment in between where the person leads
// nothing — and leading nothing has a meaning here: it is the state in which a programme lead
// may plan nothing at all. Somebody's request landing in that moment would be refused for a
// reason that was never true.
//
// The codes are resolved to programmes here rather than by the caller, so that an unknown one is
// a refusal with a name in it rather than a foreign key violation.
func (p *People) SetPersonProgrammes(ctx context.Context, personID uuid.UUID, codes []string,
	grantedBy uuid.UUID) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("cannot begin: %w", err)
	}
	// Rollback after a successful commit is a no-op, so this needs no branching.
	defer func() { _ = tx.Rollback(ctx) }()

	q := New(tx)

	programmes := make([]uuid.UUID, 0, len(codes))
	for _, code := range codes {
		row, err := q.ProgrammeByCode(ctx, code)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: %q", domain.ErrUnknownProgramme, code)
		}
		if err != nil {
			return fmt.Errorf("cannot read the programme %q: %w", code, err)
		}
		// Leading a programme this faculty does not plan is not a grant that could ever be used:
		// every write against such a programme is refused for the programme's sake, whoever
		// holds what. Refused here rather than in the service because this is the one place the
		// row is read, and a second lookup would be a second answer to the same question.
		if !domain.ProgrammeStatus(row.PlanningStatus).Planned() {
			return fmt.Errorf("%w: %q", domain.ErrProgrammeNotPlanned, code)
		}
		programmes = append(programmes, row.ID)
	}

	if err := q.UnassignProgrammes(ctx, UnassignProgrammesParams{
		PersonID: personID,
		Role:     string(policy.RoleProgrammeLead),
	}); err != nil {
		return fmt.Errorf("cannot clear the programme assignments: %w", err)
	}

	for _, programme := range programmes {
		if err := q.AssignProgramme(ctx, AssignProgrammeParams{
			PersonID:    personID,
			Role:        string(policy.RoleProgrammeLead),
			ProgrammeID: programme,
			GrantedBy:   nullUUID(nonNilUUID(grantedBy)),
		}); err != nil {
			// The foreign key to person_role is what refuses an assignment to somebody who does
			// not hold the role. The service checks for it first so that the ordinary case gets
			// a sentence; this is the race the constraint closes.
			return fmt.Errorf("cannot assign a programme: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("cannot commit: %w", err)
	}
	return nil
}
