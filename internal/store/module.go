package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/obcode/tallox.go/internal/domain"
)

// Catalogue reading, and the one write that belongs to it.
//
// The shape of this file is set by a decision made once: a list of modules costs a fixed number
// of statements rather than one per row. Four for a filtered list — modules, programmes,
// components, offerings — stitched in Go. The alternative is a resolver per relation and a
// query per module, which at 506 modules is 500 round trips to render one screen.
//
// A loader framework would solve the same problem generically and is not worth it here: the
// whole catalogue is under two thousand rows, and "load what this screen needs, then join it"
// is a technique a reader can follow without learning anything first.

// Modules is the persistence behind domain.CatalogueService.
type Modules struct {
	pool *pgxpool.Pool
}

// NewModules binds the catalogue queries to a pool.
//
// A pool rather than a DBTX: replacing a module's split is a statement about a set of rows and
// takes a transaction of its own.
func NewModules(pool *pgxpool.Pool) *Modules { return &Modules{pool: pool} }

var _ domain.CatalogueReader = (*Modules)(nil)

// Programmes lists every study programme with its versions of the regulations.
func (m *Modules) Programmes(ctx context.Context) ([]domain.Programme, error) {
	q := New(m.pool)

	rows, err := q.ListProgrammes(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot read the programmes: %w", err)
	}

	spos, err := q.ListSpos(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot read the regulations: %w", err)
	}

	byProgramme := make(map[uuid.UUID][]domain.Spo, len(rows))
	for _, s := range spos {
		byProgramme[s.ProgrammeID] = append(byProgramme[s.ProgrammeID], domain.Spo{
			ID:        s.ID,
			Version:   int(s.Version),
			ValidFrom: dateOrZero(s.ValidFrom),
			PrimussID: stringOrEmpty(s.PrimussID),
		})
	}

	out := make([]domain.Programme, 0, len(rows))
	for _, row := range rows {
		programme := domain.Programme{
			ID: row.ID, Code: row.Code, Title: row.Title, Active: row.Active,
		}
		// The programme is filled in on each of its own versions, so that a caller walking from
		// a programme to a version and back does not find a hole.
		for _, spo := range byProgramme[row.ID] {
			spo.Programme = programme
			programme.Spos = append(programme.Spos, spo)
		}
		out = append(out, programme)
	}
	return out, nil
}

// ProgrammesByID resolves a handful of programmes by id, without their regulations.
func (m *Modules) ProgrammesByID(ctx context.Context, ids []uuid.UUID) ([]domain.Programme, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	rows, err := New(m.pool).ProgrammesByIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("cannot read the programmes: %w", err)
	}

	out := make([]domain.Programme, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.Programme{
			ID: row.ID, Code: row.Code, Title: row.Title, Active: row.Active,
		})
	}
	return out, nil
}

// ProgrammeByCode returns one programme with its regulations, or (nil, nil).
func (m *Modules) ProgrammeByCode(ctx context.Context, code string) (*domain.Programme, error) {
	// Through the list rather than a query of its own: there are twenty programmes, the list is
	// already assembled with its regulations attached, and a second assembly of the same shape
	// is a second place for the two to disagree.
	all, err := m.Programmes(ctx)
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].Code == code {
			return &all[i], nil
		}
	}
	return nil, nil
}

// Teachers lists the people who teach.
func (m *Modules) Teachers(ctx context.Context, filter domain.TeacherFilter) ([]domain.Teacher, error) {
	params := ListTeachersParams{IncludeInactive: filter.IncludeInactive}
	if filter.Search != "" {
		params.Search = &filter.Search
	}

	rows, err := New(m.pool).ListTeachers(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("cannot read the teachers: %w", err)
	}

	out := make([]domain.Teacher, 0, len(rows))
	for _, row := range rows {
		out = append(out, teacherFrom(teacherRow(row)))
	}
	return out, nil
}

// teacherRow is the shape both teacher queries produce.
//
// sqlc emits one type per query; they are structurally identical because the SELECT lists are.
// Converting rather than copying field by field makes that a compile-time claim.
type teacherRow struct {
	ID                   uuid.UUID
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
	IsUser               bool
}

func teacherFrom(row teacherRow) domain.Teacher {
	return domain.Teacher{
		ID:                   row.ID,
		Name:                 row.FullName,
		SortName:             row.ShortName,
		Mail:                 row.Mail,
		IsProfessor:          row.IsProfessor,
		IsLecturerOnContract: row.IsLecturerOnContract,
		IsHonoraryProfessor:  row.IsHonoraryProfessor,
		IsStaff:              row.IsStaff,
		Active:               row.Active,
		Faculty:              stringOrEmpty(row.Faculty),
		LastSemester:         stringOrEmpty(row.LastSemester),
		IsUser:               row.IsUser,
	}
}

// Modules lists the catalogue, filtered, with each module's split and offerings attached.
func (m *Modules) Modules(ctx context.Context, filter domain.ModuleFilter) ([]domain.Module, error) {
	q := New(m.pool)

	params := ListModulesParams{
		IncludeInactive:   filter.IncludeInactive,
		WithoutComponents: filter.WithoutComponents,
		// An empty frequency list means "every frequency", carried as its own flag rather than
		// as an empty array: `= ANY('{}')` is false for every row, so the natural reading of an
		// empty filter would return nothing at all.
		AnyFrequency: len(filter.Frequency) == 0,
		Frequencies:  make([]string, 0, len(filter.Frequency)),
	}
	for _, f := range filter.Frequency {
		params.Frequencies = append(params.Frequencies, string(f))
	}
	if filter.Programme != "" {
		params.Programme = &filter.Programme
	}
	if filter.Spo != uuid.Nil {
		params.Spo = uuid.NullUUID{UUID: filter.Spo, Valid: true}
	}
	if filter.Duty != "" {
		duty := string(filter.Duty)
		params.Duty = &duty
	}
	if filter.Search != "" {
		params.Search = &filter.Search
	}
	if filter.Responsible != uuid.Nil {
		params.Responsible = uuid.NullUUID{UUID: filter.Responsible, Valid: true}
	}

	rows, err := q.ListModules(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("cannot read the modules: %w", err)
	}
	if len(rows) == 0 {
		return []domain.Module{}, nil
	}

	modules := make([]domain.Module, 0, len(rows))
	ids := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		modules = append(modules, moduleFrom(row))
		ids = append(ids, row.ID)
	}

	if err := m.attach(ctx, q, modules, ids); err != nil {
		return nil, err
	}
	return modules, nil
}

// ModuleByID returns one module with everything attached, or (nil, nil).
func (m *Modules) ModuleByID(ctx context.Context, id uuid.UUID) (*domain.Module, error) {
	q := New(m.pool)

	row, err := q.ModuleByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read the module: %w", err)
	}

	modules := []domain.Module{moduleFrom(ListModulesRow(row))}
	if err := m.attach(ctx, q, modules, []uuid.UUID{id}); err != nil {
		return nil, err
	}
	return &modules[0], nil
}

// ModulesByID returns a handful of modules with everything attached, keyed by nothing — the
// caller matches them up.
//
// For the demand, whose instances name tens of modules out of 506. Filtering the list query
// would read the catalogue to answer a question about twenty rows, and a Module assembled
// differently here than there would be a Module whose empty split means something else — and an
// empty split is what stops an instance being declared.
func (m *Modules) ModulesByID(ctx context.Context, ids []uuid.UUID) ([]domain.Module, error) {
	if len(ids) == 0 {
		return []domain.Module{}, nil
	}

	q := New(m.pool)

	rows, err := q.ModulesByIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("cannot read the modules: %w", err)
	}

	modules := make([]domain.Module, 0, len(rows))
	found := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		modules = append(modules, moduleFrom(ListModulesRow(row)))
		found = append(found, row.ID)
	}
	if err := m.attach(ctx, q, modules, found); err != nil {
		return nil, err
	}
	return modules, nil
}

// attach fills in the home programme, the split and the offerings of a set of modules.
//
// Three statements regardless of how many modules there are, which is the whole point.
func (m *Modules) attach(ctx context.Context, q *Queries, modules []domain.Module, ids []uuid.UUID) error {
	programmes, err := m.Programmes(ctx)
	if err != nil {
		return err
	}
	byID := make(map[uuid.UUID]domain.Programme, len(programmes))
	for _, p := range programmes {
		// Without its own versions attached: a module's home programme is being named here, not
		// described, and carrying 29 regulations into every one of 500 rows is a lot of bytes
		// for a field nobody asked for.
		byID[p.ID] = domain.Programme{ID: p.ID, Code: p.Code, Title: p.Title, Active: p.Active}
	}

	// The people the listed modules name as responsible, in one statement. About eighty distinct
	// people across the whole catalogue, so a query per module would be a great many round trips
	// for a handful of rows.
	responsibleIDs := make([]uuid.UUID, 0, len(modules))
	for i := range modules {
		if modules[i].Responsible != nil && !slices.Contains(responsibleIDs, modules[i].Responsible.ID) {
			responsibleIDs = append(responsibleIDs, modules[i].Responsible.ID)
		}
	}
	teachersByID := make(map[uuid.UUID]domain.Teacher, len(responsibleIDs))
	if len(responsibleIDs) > 0 {
		rows, err := q.TeachersByID(ctx, responsibleIDs)
		if err != nil {
			return fmt.Errorf("cannot read the responsible teachers: %w", err)
		}
		for _, row := range rows {
			teachersByID[row.ID] = teacherFrom(teacherRow(row))
		}
	}

	components, err := q.ModuleComponentsFor(ctx, ids)
	if err != nil {
		return fmt.Errorf("cannot read the module components: %w", err)
	}
	componentsByModule := make(map[uuid.UUID][]domain.ModuleComponent, len(ids))
	for _, c := range components {
		componentsByModule[c.ModuleID] = append(componentsByModule[c.ModuleID], domain.ModuleComponent{
			ID:            c.ID,
			Kind:          domain.InstancePartKind(c.Kind),
			TeachingHours: numericFloat(c.TeachingHours),
			Position:      int(c.Position),
		})
	}

	offerings, err := q.ModuleOfferingsFor(ctx, ids)
	if err != nil {
		return fmt.Errorf("cannot read the module offerings: %w", err)
	}
	offeringsByModule := make(map[uuid.UUID][]domain.ModuleOffering, len(ids))
	for _, o := range offerings {
		offeringsByModule[o.ModuleID] = append(offeringsByModule[o.ModuleID], domain.ModuleOffering{
			ID: o.ID,
			Spo: domain.Spo{
				ID:        o.SpoID,
				Version:   int(o.SpoVersion),
				ValidFrom: dateOrZero(o.SpoValidFrom),
				PrimussID: stringOrEmpty(o.SpoPrimussID),
				Programme: domain.Programme{
					ID:     o.ProgrammeID,
					Code:   o.ProgrammeCode,
					Title:  o.ProgrammeTitle,
					Active: o.ProgrammeActive,
				},
			},
			IsDuty:               o.IsDuty,
			ModuleCodes:          o.ModuleCodes,
			Focuses:              o.Focuses,
			MinProgrammeSemester: intOrNil(o.MinProgrammeSemester),
		})
	}

	for i := range modules {
		modules[i].HomeProgramme = byID[modules[i].HomeProgramme.ID]
		modules[i].Components = componentsByModule[modules[i].ID]
		modules[i].Offerings = offeringsByModule[modules[i].ID]
		if modules[i].Responsible != nil {
			if teacher, ok := teachersByID[modules[i].Responsible.ID]; ok {
				modules[i].Responsible = &teacher
			} else {
				// The row is gone between the two statements. Rare and harmless, and a module
				// with no responsible person is a state the schema already allows.
				modules[i].Responsible = nil
			}
		}
	}
	return nil
}

// SetModuleComponents replaces a module's split.
//
// In a transaction, because delete-then-insert has a moment in between where the module has no
// split at all — and a module with no split is one an instance cannot be declared for. Somebody
// reading the catalogue during that moment would see a module become undeclarable and then not.
//
// The guard belongs here in the same sense the last-administrator guard does: it is a statement
// about a set of rows, and a version of it in a service layer passes its unit test while the
// shipped code races.
func (m *Modules) SetModuleComponents(
	ctx context.Context, moduleID uuid.UUID, components []domain.ModuleComponent, by uuid.UUID,
) (*domain.Module, error) {
	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot begin: %w", err)
	}
	// Rollback after a successful commit is a no-op, so this needs no branching.
	defer func() { _ = tx.Rollback(ctx) }()

	q := New(tx)

	if err := q.ReplaceModuleComponents(ctx, moduleID); err != nil {
		return nil, fmt.Errorf("cannot clear the split: %w", err)
	}
	for _, c := range components {
		hours, err := numericFrom(c.TeachingHours)
		if err != nil {
			return nil, err
		}
		if err := q.InsertModuleComponent(ctx, InsertModuleComponentParams{
			ModuleID:      moduleID,
			Kind:          string(c.Kind),
			TeachingHours: hours,
			Position:      int32(c.Position),
			CreatedBy:     nullUUID(nonNilUUID(by)),
		}); err != nil {
			return nil, fmt.Errorf("cannot record a part of the split: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("cannot commit: %w", err)
	}
	return m.ModuleByID(ctx, moduleID)
}

func moduleFrom(row ListModulesRow) domain.Module {
	module := domain.Module{
		ID:                  row.ID,
		Name:                row.Name,
		HomeProgramme:       domain.Programme{ID: row.HomeProgrammeID},
		CourseType:          domain.CourseType(row.CourseType),
		Frequency:           domain.Frequency(row.Frequency),
		ContactHoursPerWeek: intOrNil(row.ContactHoursPerWeek),
		Credits:             intOrNil(row.Credits),
		Active:              row.Active,
		Official:            row.Official,
		ZpaID:               row.ZpaModuleRef,
	}
	if row.ResponsibleTeacherID.Valid {
		// Only the id here; attach() fills in the rest in one statement for the whole list.
		module.Responsible = &domain.Teacher{ID: row.ResponsibleTeacherID.UUID}
	}
	if row.RetiredAt.Valid {
		at := row.RetiredAt.Time
		module.RetiredAt = &at
	}
	return module
}

func dateOrZero(d pgtype.Date) time.Time {
	if !d.Valid {
		return time.Time{}
	}
	return d.Time
}

func stringOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func intOrNil(v *int32) *int {
	if v == nil {
		return nil
	}
	n := int(*v)
	return &n
}

// numericFloat reads a numeric(4,2) as a float.
//
// Hours are a small decimal — two, two and a half, four — and float64 represents every value the
// constraint permits exactly enough to add up. The column is numeric rather than float precisely
// so the database is not the place that rounds.
func numericFloat(n pgtype.Numeric) float64 {
	if !n.Valid {
		return 0
	}
	f, err := n.Float64Value()
	if err != nil || !f.Valid {
		return 0
	}
	return f.Float64
}

func numericFrom(f float64) (pgtype.Numeric, error) {
	var n pgtype.Numeric
	if err := n.Scan(fmt.Sprintf("%.2f", f)); err != nil {
		return n, fmt.Errorf("cannot represent %v as hours: %w", f, err)
	}
	return n, nil
}

// nonNilUUID turns the anonymous id into an absent one.
//
// created_by records who stated the split, and the nil uuid is what an unauthenticated caller
// carries — writing it would be a foreign key to a person who cannot exist.
func nonNilUUID(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}
