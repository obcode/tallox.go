package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/policy"
)

// The demand: reading it in a fixed number of statements, and writing it in transactions.
//
// Same arrangement as the catalogue next door — load the rows, load their relations, stitch in
// Go — and for the same measured reason. What is different here is that most of the operations
// write, and that nearly every one of them is a statement about more than one row: declaring an
// instance writes the instance and the parts made from the module's split, sharing a lecture
// across cohorts writes one flag and deletes the siblings' own lectures, and copying a semester
// writes all of it or none of it.
//
// So the transaction boundary is the point of this file, and it is here rather than in a service
// for the same reason the last-administrator guard is: a version of these rules one layer up
// passes its unit test and races in production.

// Demand is the persistence behind domain.DemandService.
type Demand struct {
	pool *pgxpool.Pool
	// modules attaches the catalogue entry to each instance. The Module a demand screen shows
	// has to be the Module the catalogue shows, assembled by the same code: an empty split means
	// "nobody has stated how the hours divide", which is the precondition of this entire
	// feature, and a second assembly that forgot to load the components would report every
	// module as undeclarable.
	modules *Modules
}

// NewDemand binds the demand queries to a pool and the catalogue that fills in the modules.
func NewDemand(pool *pgxpool.Pool, modules *Modules) *Demand {
	return &Demand{pool: pool, modules: modules}
}

var _ domain.DemandStore = (*Demand)(nil)

// instanceRow is the shape all three instance queries produce.
//
// sqlc emits one type per query and they are structurally identical because the SELECT lists
// are. Converting rather than copying field by field makes that a compile-time claim: adding a
// column to one of them and not the others stops the build here instead of reading as null
// forever.
type instanceRow struct {
	ID                     uuid.UUID
	SemesterID             uuid.UUID
	ModuleID               uuid.UUID
	ProgrammeID            uuid.UUID
	Track                  string
	ProgrammeSemester      *int32
	Note                   string
	Delivery               string
	TaughtInEnglish        *bool
	ModuleTeachingLanguage *string
	Cie                    bool
	Topic                  *string
	CreatedAt              time.Time
	UpdatedAt              time.Time
	SemesterCode           string
	SemesterPhase          string
	ProgrammeCode          string
	ProgrammeTitle         string
	ProgrammeActive        bool

	// The coverage link, read from the guest's side. All of these are null on an ordinary
	// instance, which is almost every row.
	CoveredByInstanceID        uuid.NullUUID
	CoveredRequestedAt         pgtype.Timestamptz
	CoveredAcceptedAt          pgtype.Timestamptz
	CoveredByProgrammeCode     *string
	CoveredByProgrammeTitle    *string
	CoveredByTrack             *string
	CoveredByProgrammeSemester *int32
}

func instanceFrom(row instanceRow) domain.CourseInstance {
	return domain.CourseInstance{
		ID:            row.ID,
		SemesterCode:  row.SemesterCode,
		SemesterPhase: policy.Phase(row.SemesterPhase),
		// Filled in by attach. Carrying the id in the otherwise empty struct is what lets the
		// stitching find its module without a parallel slice of ids.
		Module:    domain.Module{ID: row.ModuleID},
		Programme: domain.Programme{ID: row.ProgrammeID, Code: row.ProgrammeCode, Title: row.ProgrammeTitle, Active: row.ProgrammeActive},
		Track:     row.Track,
		Topic:     stringOrEmpty(row.Topic),

		ProgrammeSemester: intOrNil(row.ProgrammeSemester),
		Note:              row.Note,
		Delivery:          domain.Delivery(row.Delivery),
		EnglishOverride:   row.TaughtInEnglish,
		CatalogueLanguage: domain.Language(stringOrEmpty(row.ModuleTeachingLanguage)),
		CiE:               row.Cie,
		CoveredBy:         coverageFrom(row),
		CreatedAt:         row.CreatedAt,
		UpdatedAt:         row.UpdatedAt,
	}
}

// coverageFrom builds the guest's side of the link out of the columns the instance queries carry.
//
// The host is filled in as far as one row reaches — its programme, its cohort — and no further.
// Loading the host properly would be a second query per instance for a fact the screen renders as
// one sentence, and the parts it holds already arrive through BorrowedInstancePartsFor.
func coverageFrom(row instanceRow) *domain.InstanceCoverage {
	if !row.CoveredByInstanceID.Valid || !row.CoveredRequestedAt.Valid {
		return nil
	}

	host := domain.CourseInstance{
		ID:                row.CoveredByInstanceID.UUID,
		Track:             stringOrEmpty(row.CoveredByTrack),
		ProgrammeSemester: intOrNil(row.CoveredByProgrammeSemester),
		Programme: domain.Programme{
			Code:  stringOrEmpty(row.CoveredByProgrammeCode),
			Title: stringOrEmpty(row.CoveredByProgrammeTitle),
		},
	}

	coverage := &domain.InstanceCoverage{
		Instance:    host,
		RequestedAt: row.CoveredRequestedAt.Time,
	}
	if row.CoveredAcceptedAt.Valid {
		accepted := row.CoveredAcceptedAt.Time
		coverage.AcceptedAt = &accepted
	}
	return coverage
}

func partFrom(id uuid.UUID, kind string, position int32, hours pgtype.Numeric, shared bool,
	dividedFrom uuid.NullUUID, taughtBy *string,
) domain.InstancePart {
	part := domain.InstancePart{
		ID:                 id,
		Kind:               domain.InstancePartKind(kind),
		Position:           int(position),
		TeachingHours:      numericFloatOrNil(hours),
		SharedAcrossTracks: shared,
		TaughtBy:           stringOrEmpty(taughtBy),
	}
	if dividedFrom.Valid {
		part.DividedFrom = dividedFrom.UUID
	}
	return part
}

// CourseInstances lists the demand of a semester.
func (d *Demand) CourseInstances(ctx context.Context, filter domain.DemandFilter) ([]domain.CourseInstance, error) {
	q := New(d.pool)

	params := ListCourseInstancesParams{Semester: filter.SemesterCode}
	if filter.Programme != "" {
		params.Programme = &filter.Programme
	}
	if filter.Module != uuid.Nil {
		params.Module = uuid.NullUUID{UUID: filter.Module, Valid: true}
	}
	params.ForeignHome = filter.ForeignHome
	params.Degree = degreeParam(filter.Degree)
	params.AllProgrammes = filter.Reach.All
	params.ProgrammeIds = filter.Reach.IDs
	if params.ProgrammeIds == nil {
		params.ProgrammeIds = []uuid.UUID{}
	}

	rows, err := q.ListCourseInstances(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("cannot read the demand: %w", err)
	}
	if len(rows) == 0 {
		return []domain.CourseInstance{}, nil
	}

	instances := make([]domain.CourseInstance, 0, len(rows))
	ids := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		instances = append(instances, instanceFrom(instanceRow(row)))
		ids = append(ids, row.ID)
	}

	if err := d.attach(ctx, q, instances, ids); err != nil {
		return nil, err
	}
	return instances, nil
}

// TeachingLoad is the semester's teaching by the programme that holds it, one entry per programme.
//
// The query answers one row per (programme of the instance, home of the module); this folds them.
// A programme with nothing declared comes back as one row with no home, and becomes an entry with
// no shares rather than a share of zero hours.
func (d *Demand) TeachingLoad(ctx context.Context, semesterCode string) ([]domain.TeachingLoad, error) {
	rows, err := New(d.pool).TeachingLoadByProgramme(ctx, semesterCode)
	if err != nil {
		return nil, fmt.Errorf("cannot read the teaching load: %w", err)
	}

	loads := []domain.TeachingLoad{}
	for _, row := range rows {
		if len(loads) == 0 || loads[len(loads)-1].Programme.ID != row.ProgrammeID {
			loads = append(loads, domain.TeachingLoad{
				Programme: domain.Programme{
					ID:             row.ProgrammeID,
					Code:           row.ProgrammeCode,
					Title:          row.ProgrammeTitle,
					Active:         row.ProgrammeActive,
					PlanningStatus: domain.ProgrammeStatus(row.ProgrammePlanningStatus),
				},
				Shares: []domain.TeachingLoadShare{},
			})
		}
		if !row.HomeID.Valid {
			continue
		}

		load := &loads[len(loads)-1]
		load.Shares = append(load.Shares, domain.TeachingLoadShare{
			HomeProgramme: domain.Programme{
				ID:             row.HomeID.UUID,
				Code:           stringOrEmpty(row.HomeCode),
				Title:          stringOrEmpty(row.HomeTitle),
				Active:         row.HomeActive != nil && *row.HomeActive,
				PlanningStatus: domain.ProgrammeStatus(stringOrEmpty(row.HomePlanningStatus)),
			},
			TeachingHours:     row.Hours,
			Instances:         int(row.Instances),
			FwpInstances:      int(row.FwpInstances),
			FwpHours:          row.FwpHours,
			PartsWithoutHours: int(row.PartsWithoutHours),
			ExternalHours:     row.ExternalHours,
		})
	}
	return loads, nil
}

// ProgrammeQuotas lists the quotas entered for one study year.
func (d *Demand) ProgrammeQuotas(ctx context.Context, studyYear int) ([]domain.ProgrammeQuota, error) {
	rows, err := New(d.pool).ProgrammeQuotasOfYear(ctx, int32(studyYear)) //nolint:gosec // bounded by the service
	if err != nil {
		return nil, fmt.Errorf("cannot read the programme quotas: %w", err)
	}

	out := make([]domain.ProgrammeQuota, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.ProgrammeQuota{
			Programme: domain.Programme{
				ID:             row.ProgrammeID,
				Code:           row.ProgrammeCode,
				Title:          row.ProgrammeTitle,
				Active:         row.ProgrammeActive,
				PlanningStatus: domain.ProgrammeStatus(row.ProgrammePlanningStatus),
			},
			StudyYear:     int(row.StudyYear),
			TeachingHours: row.TeachingHours,
			SetAt:         row.SetAt,
			SetBy:         row.SetBy.UUID,
		})
	}
	return out, nil
}

// SetProgrammeQuota enters or corrects one quota, and returns (nil, nil) for an unknown code.
//
// The programme comes back from a second read rather than from the upsert: the statement's
// RETURNING can only see the row it wrote, and the caller wants the programme with it.
func (d *Demand) SetProgrammeQuota(ctx context.Context, programme string, studyYear int,
	hours float64, by uuid.UUID,
) (*domain.ProgrammeQuota, error) {
	q := New(d.pool)

	params := SetProgrammeQuotaParams{
		StudyYear:     int32(studyYear), //nolint:gosec // bounded by the service
		TeachingHours: hours,
		Programme:     programme,
	}
	if by != uuid.Nil {
		params.SetBy = uuid.NullUUID{UUID: by, Valid: true}
	}
	if _, err := q.SetProgrammeQuota(ctx, params); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("cannot set the programme quota: %w", err)
	}

	quotas, err := d.ProgrammeQuotas(ctx, studyYear)
	if err != nil {
		return nil, err
	}
	for _, quota := range quotas {
		if quota.Programme.Code == programme {
			return &quota, nil
		}
	}
	return nil, nil
}

// ClearProgrammeQuota takes one back. Nothing to take back is not an error.
func (d *Demand) ClearProgrammeQuota(ctx context.Context, programme string, studyYear int) error {
	_, err := New(d.pool).ClearProgrammeQuota(ctx, ClearProgrammeQuotaParams{
		StudyYear: int32(studyYear), //nolint:gosec // bounded by the service
		Programme: programme,
	})
	if err != nil {
		return fmt.Errorf("cannot clear the programme quota: %w", err)
	}
	return nil
}

// CourseInstanceByID returns one instance with everything attached, or (nil, nil).
func (d *Demand) CourseInstanceByID(ctx context.Context, id uuid.UUID) (*domain.CourseInstance, error) {
	q := New(d.pool)

	row, err := q.CourseInstanceByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read the instance: %w", err)
	}
	return d.one(ctx, q, instanceRow(row))
}

// CourseInstanceByPartID returns the instance a part belongs to, or (nil, nil).
func (d *Demand) CourseInstanceByPartID(ctx context.Context, partID uuid.UUID) (*domain.CourseInstance, error) {
	q := New(d.pool)

	row, err := q.CourseInstanceByPartID(ctx, partID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read the instance of this part: %w", err)
	}
	return d.one(ctx, q, instanceRow(row))
}

func (d *Demand) one(ctx context.Context, q *Queries, row instanceRow) (*domain.CourseInstance, error) {
	instances := []domain.CourseInstance{instanceFrom(row)}
	if err := d.attach(ctx, q, instances, []uuid.UUID{row.ID}); err != nil {
		return nil, err
	}
	return &instances[0], nil
}

// attach fills in the parts, the borrowed parts, the covered demands, who else offers the module
// separately, and the catalogue entry of a set of instances.
//
// Five statements plus the catalogue's own, regardless of how many instances there are.
func (d *Demand) attach(ctx context.Context, q *Queries, instances []domain.CourseInstance, ids []uuid.UUID) error {
	parts, err := q.InstancePartsFor(ctx, ids)
	if err != nil {
		return fmt.Errorf("cannot read the instance parts: %w", err)
	}
	partsByInstance := make(map[uuid.UUID][]domain.InstancePart, len(ids))
	for _, p := range parts {
		part := partFrom(p.ID, p.Kind, p.Position, p.TeachingHours, p.ServesSiblingTracks,
			p.DividedFromID, p.TaughtBy)
		partsByInstance[p.CourseInstanceID] = append(partsByInstance[p.CourseInstanceID], part)
	}

	borrowed, err := q.BorrowedInstancePartsFor(ctx, ids)
	if err != nil {
		return fmt.Errorf("cannot read the parts held for these cohorts: %w", err)
	}
	borrowedByInstance := make(map[uuid.UUID][]domain.BorrowedPart, len(ids))
	for _, b := range borrowed {
		part := partFrom(b.ID, b.Kind, b.Position, b.TeachingHours, b.ServesSiblingTracks,
			b.DividedFromID, b.TaughtBy)
		borrowedByInstance[b.ForInstanceID] = append(borrowedByInstance[b.ForInstanceID],
			domain.BorrowedPart{
				Part:          part,
				FromTrack:     b.FromTrack,
				FromProgramme: stringOrEmpty(b.FromProgrammeCode),
			})
	}

	// The host's side of the coverage link. Unconditional, like the parts: a host read without
	// its guests renders as an ordinary cohort, and the whole point is that it is not one.
	covering, err := q.CoveringInstancesFor(ctx, ids)
	if err != nil {
		return fmt.Errorf("cannot read the demands these cohorts cover: %w", err)
	}
	coversByInstance := make(map[uuid.UUID][]domain.InstanceCoverage, len(ids))
	for _, c := range covering {
		coverage := domain.InstanceCoverage{
			Instance: domain.CourseInstance{
				ID:                c.ID,
				Track:             c.Track,
				ProgrammeSemester: intOrNil(c.ProgrammeSemester),
				Programme: domain.Programme{
					ID:    c.ProgrammeID,
					Code:  c.ProgrammeCode,
					Title: c.ProgrammeTitle,
				},
			},
			RequestedAt: c.CoveredRequestedAt.Time,
		}
		if c.CoveredAcceptedAt.Valid {
			accepted := c.CoveredAcceptedAt.Time
			coverage.AcceptedAt = &accepted
		}
		coversByInstance[c.ForInstanceID.UUID] = append(coversByInstance[c.ForInstanceID.UUID], coverage)
	}

	// The same offering, declared by somebody else and held there. One statement for the whole
	// list, like the two above it — and the only thing attach computes about a row rather than
	// reading off it.
	separate, err := q.SeparatelyPlannedInstancesFor(ctx, ids)
	if err != nil {
		return fmt.Errorf("cannot read who else offers these modules: %w", err)
	}
	separateByInstance := make(map[uuid.UUID][]domain.CourseInstance, len(ids))
	for _, s := range separate {
		separateByInstance[s.ForInstanceID] = append(separateByInstance[s.ForInstanceID],
			domain.CourseInstance{
				ID:                s.ID,
				Track:             s.Track,
				ProgrammeSemester: intOrNil(s.ProgrammeSemester),
				Programme: domain.Programme{
					ID: s.ProgrammeID, Code: s.ProgrammeCode, Title: s.ProgrammeTitle,
				},
			})
	}

	moduleIDs := make([]uuid.UUID, 0, len(instances))
	for i := range instances {
		if !slices.Contains(moduleIDs, instances[i].Module.ID) {
			moduleIDs = append(moduleIDs, instances[i].Module.ID)
		}
	}
	modules, err := d.modules.ModulesByID(ctx, moduleIDs)
	if err != nil {
		return err
	}
	modulesByID := make(map[uuid.UUID]domain.Module, len(modules))
	for _, m := range modules {
		modulesByID[m.ID] = m
	}

	for i := range instances {
		instances[i].Parts = partsByInstance[instances[i].ID]
		instances[i].BorrowedParts = borrowedByInstance[instances[i].ID]
		instances[i].Covers = coversByInstance[instances[i].ID]
		instances[i].AlsoPlannedSeparately = separateByInstance[instances[i].ID]
		// A module that vanished between the two statements is not a state the schema allows —
		// course_instance references it ON DELETE RESTRICT and modules are never deleted — so
		// the zero Module here would be a bug rather than a case, and it keeps its id so that
		// one is recognisable.
		if module, ok := modulesByID[instances[i].Module.ID]; ok {
			instances[i].Module = module
		}
	}
	return nil
}

// The write half.
//
// Every one of these ends with a re-read through CourseInstanceByID rather than assembling the
// answer from what it just wrote. The alternative is a second opinion about what an instance
// looks like — one built by the writer, one by the reader — and the two would drift on the first
// field somebody adds to a query without adding it to the other.

// statedComponents is what an instance's parts are made from: the split somebody confirmed.
//
// Read inside the caller's transaction, because "this module has no split" and "this module had
// no split a moment ago" are different statements and only the first one is true when it is read
// in the transaction that writes the parts.
//
// No fall back to the proposal any more — see domain.Module.Plannable for why. A module without a
// confirmed split is ErrModuleNotDecomposed, which says what to repair.
func statedComponents(ctx context.Context, q *Queries, moduleID uuid.UUID) ([]domain.ModuleComponent, error) {
	rows, err := q.ModuleComponentsFor(ctx, []uuid.UUID{moduleID})
	if err != nil {
		return nil, fmt.Errorf("cannot read the module's split: %w", err)
	}
	if len(rows) > 0 {
		out := make([]domain.ModuleComponent, 0, len(rows))
		for _, c := range rows {
			out = append(out, domain.ModuleComponent{
				ID:            c.ID,
				Kind:          domain.InstancePartKind(c.Kind),
				TeachingHours: numericFloat(c.TeachingHours),
				Position:      int(c.Position),
			})
		}
		return out, nil
	}

	if _, err := q.ModuleByID(ctx, moduleID); errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrModuleNotFound
	} else if err != nil {
		return nil, fmt.Errorf("cannot read the module: %w", err)
	}
	return nil, domain.ErrModuleNotDecomposed
}

// partsFromSplit writes an instance's parts from a module's split.
//
// One part per kind, in the order the kinds first appear. A kind the split names again — a lecture
// of 2 + 2 — becomes a piece of that part (divided_from_id) rather than a second lecture: the
// same arrangement divideInstancePart makes, stated once for every instance of the module. So the
// group count, the hours and the shared lecture all read it as one unit.
//
// skip leaves out kinds a sibling cohort already holds for this one. Positions start at 0, and a
// piece takes the position of its part.
func partsFromSplit(ctx context.Context, q *Queries, instanceID uuid.UUID,
	components []domain.ModuleComponent, skip map[string]bool,
) ([]InstancePartsForRow, error) {
	var out []InstancePartsForRow
	roots := map[string]InstancePartsForRow{}
	position := int32(0)
	for _, c := range components {
		kind := string(c.Kind)
		if skip[kind] {
			continue
		}
		// What a lecturer is credited with starts as what the split says and is editable
		// afterwards — the two are different quantities that merely begin equal.
		hours, err := numericFrom(c.TeachingHours)
		if err != nil {
			return nil, err
		}
		row := InstancePartsForRow{
			CourseInstanceID: instanceID,
			Kind:             kind,
			Position:         position,
			TeachingHours:    hours,
		}
		if root, ok := roots[kind]; ok {
			row.Position = root.Position
			row.DividedFromID = uuid.NullUUID{UUID: root.ID, Valid: true}
		}
		id, err := q.InsertInstancePiece(ctx, InsertInstancePieceParams{
			CourseInstanceID: instanceID,
			Kind:             kind,
			Position:         row.Position,
			TeachingHours:    hours,
			DividedFromID:    row.DividedFromID,
		})
		if err != nil {
			return nil, err
		}
		row.ID = id
		if !row.DividedFromID.Valid {
			roots[kind] = row
			position++
		}
		out = append(out, row)
	}
	return out, nil
}

// CreateCourseInstance declares an instance and makes its parts from the module's split.
//
// The precondition is checked inside the transaction, not before it: "this module has no split"
// and "this module had no split a moment ago" are different statements, and only the first one
// is true when it is read in the same transaction that writes the parts.
//
// One part per unit of the split, and no multiplicity. The ordinary case is that a cohort has
// its own lecture and its own laboratories, so that is what a new instance holds; a second
// laboratory group is one click afterwards, and sharing a lecture with the sibling cohort is a
// deliberate act rather than a default nobody chose.
func (d *Demand) CreateCourseInstance(ctx context.Context, spec domain.NewCourseInstance) (*domain.CourseInstance, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := New(tx)

	components, err := statedComponents(ctx, q, spec.ModuleID)
	if err != nil {
		return nil, err
	}

	programmeSemester := int32OrNil(spec.ProgrammeSemester)
	if programmeSemester == nil {
		seeded, err := q.SeedProgrammeSemester(ctx, SeedProgrammeSemesterParams{
			ModuleID:    spec.ModuleID,
			ProgrammeID: spec.ProgrammeID,
		})
		if err != nil {
			return nil, fmt.Errorf("cannot read the cohort year from the regulations: %w", err)
		}
		// Zero is the query saying the regulations do not state one, which the column's own
		// CHECK (1 to 12) makes unambiguous.
		if seeded > 0 {
			programmeSemester = &seeded
		}
	}

	id, err := q.InsertCourseInstance(ctx, InsertCourseInstanceParams{
		SemesterID:        spec.SemesterID,
		ModuleID:          spec.ModuleID,
		ProgrammeID:       spec.ProgrammeID,
		Track:             spec.Track,
		ProgrammeSemester: programmeSemester,
		Note:              spec.Note,
		Delivery:          string(domain.DeliveryWeekly),
		CreatedBy:         nullUUID(nonNilUUID(spec.CreatedBy)),
	})
	if isUniqueViolation(err) {
		return nil, domain.ErrTrackTaken
	}
	if err != nil {
		return nil, fmt.Errorf("cannot declare the instance: %w", err)
	}

	// Held with another programme's event where there is one, and then it holds nothing itself.
	//
	// The parts are skipped rather than created and deleted again: a covered cohort holds nothing
	// by construction, and building them first would mean this path could fail on an assignment
	// that cannot exist yet.
	coupled, err := coupleIfHostExists(ctx, q, id,
		spec.SemesterID, spec.ModuleID, spec.ProgrammeID, spec.CreatedBy)
	if err != nil {
		return nil, err
	}
	if coupled {
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("cannot commit: %w", err)
		}
		return d.CourseInstanceByID(ctx, id)
	}

	if _, err := partsFromSplit(ctx, q, id, components, nil); err != nil {
		return nil, fmt.Errorf("cannot create a part of the instance: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("cannot commit: %w", err)
	}
	return d.CourseInstanceByID(ctx, id)
}

// DuplicateCourseInstance copies an instance to another parallel cohort.
//
// Two things it deliberately does not copy. A part that is already held for the sibling cohorts
// is not copied, because it already serves the new one — copying it would be the same lecture
// twice, and its hours would be counted twice with it. And the cohort year is copied rather than
// re-seeded: the source is a decision somebody made, and re-reading the regulations could
// produce a sibling that sits in a different year than the cohort it is a sibling of.
//
// sourceTrack renames the source in the same transaction. Splitting a single cohort into two is
// one act — IF1 becomes IF1A and IF1B — and doing it as two mutations leaves a moment in which
// the two rows do not look like siblings, which is exactly the moment somebody's screen renders.
func (d *Demand) DuplicateCourseInstance(ctx context.Context, id uuid.UUID, track, sourceTrack string,
	by uuid.UUID,
) (*domain.CourseInstance, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := New(tx)

	source, err := q.CourseInstanceByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrInstanceNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read the instance: %w", err)
	}

	// A covered cohort has no parts to copy and its coverage is not copied either: whether a
	// second cohort of this programme is also held by the other one is that programme's decision,
	// not a side effect of pressing "duplicate". Copying it as it stands would produce a cohort
	// with no teaching and no explanation, which is the row this whole mechanism exists to
	// prevent.
	if source.CoveredAcceptedAt.Valid {
		return nil, domain.ErrInstanceCovered
	}

	parts, err := q.InstancePartsFor(ctx, []uuid.UUID{id})
	if err != nil {
		return nil, fmt.Errorf("cannot read the instance parts: %w", err)
	}

	if sourceTrack != "" && sourceTrack != source.Track {
		if err := q.UpdateCourseInstance(ctx, UpdateCourseInstanceParams{
			ID:                id,
			Track:             sourceTrack,
			ProgrammeSemester: source.ProgrammeSemester,
		}); isUniqueViolation(err) {
			return nil, domain.ErrTrackTaken
		} else if err != nil {
			return nil, fmt.Errorf("cannot rename the source cohort: %w", err)
		}
	}

	newID, err := q.InsertCourseInstance(ctx, InsertCourseInstanceParams{
		SemesterID:        source.SemesterID,
		ModuleID:          source.ModuleID,
		ProgrammeID:       source.ProgrammeID,
		Track:             track,
		ProgrammeSemester: source.ProgrammeSemester,
		Note:              source.Note,
		// The second cohort is taught the way the first one is until somebody says otherwise —
		// the stored override, not the effective value, so the catalogue keeps its say.
		Delivery:        source.Delivery,
		TaughtInEnglish: source.TaughtInEnglish,
		Cie:             source.Cie,
		// The topic is not duplicated: a second cohort of an umbrella seminar is a second
		// seminar, and carrying the first one's title would list the same seminar twice.
		CreatedBy: nullUUID(nonNilUUID(by)),
	})
	if isUniqueViolation(err) {
		return nil, domain.ErrTrackTaken
	}
	if err != nil {
		return nil, fmt.Errorf("cannot declare the second cohort: %w", err)
	}

	// The shared parts stay with the source: they already serve the new cohort as a sibling.
	// Their pieces are shared too, and go with them.
	var own []InstancePartsForRow
	for _, p := range parts {
		if !p.ServesSiblingTracks {
			own = append(own, p)
		}
	}
	if _, err := copyParts(ctx, q, own, newID, true); err != nil {
		return nil, fmt.Errorf("cannot copy a part of the instance: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("cannot commit: %w", err)
	}
	return d.CourseInstanceByID(ctx, newID)
}

// UpdateCourseInstance writes the cohort and the cohort year.
func (d *Demand) UpdateCourseInstance(ctx context.Context, id uuid.UUID, track string,
	programmeSemester *int,
) (*domain.CourseInstance, error) {
	err := New(d.pool).UpdateCourseInstance(ctx, UpdateCourseInstanceParams{
		ID:                id,
		Track:             track,
		ProgrammeSemester: int32OrNil(programmeSemester),
	})
	if isUniqueViolation(err) {
		return nil, domain.ErrTrackTaken
	}
	if err != nil {
		return nil, fmt.Errorf("cannot change the instance: %w", err)
	}

	instance, err := d.CourseInstanceByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if instance == nil {
		return nil, domain.ErrInstanceNotFound
	}
	return instance, nil
}

// SetCourseInstanceTopic writes which seminar a cohort is. nil takes the topic away.
func (d *Demand) SetCourseInstanceTopic(ctx context.Context, id uuid.UUID, topic *string,
) (*domain.CourseInstance, error) {
	if err := New(d.pool).UpdateCourseInstanceTopic(ctx, UpdateCourseInstanceTopicParams{
		ID:    id,
		Topic: topic,
	}); err != nil {
		return nil, fmt.Errorf("cannot set the topic of the instance: %w", err)
	}

	instance, err := d.CourseInstanceByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if instance == nil {
		return nil, domain.ErrInstanceNotFound
	}
	return instance, nil
}

// SetCourseInstanceCiE writes whether a cohort is listed as a course in English.
func (d *Demand) SetCourseInstanceCiE(ctx context.Context, id uuid.UUID, cie bool) (*domain.CourseInstance, error) {
	if err := New(d.pool).UpdateCourseInstanceCiE(ctx, UpdateCourseInstanceCiEParams{
		ID:  id,
		Cie: cie,
	}); err != nil {
		return nil, fmt.Errorf("cannot set whether the instance is listed as CiE: %w", err)
	}

	instance, err := d.CourseInstanceByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if instance == nil {
		return nil, domain.ErrInstanceNotFound
	}
	return instance, nil
}

// SetCourseInstanceFormat writes how a cohort is taught.
//
// Whether the language is stored at all is decided by the statement, against the module's row
// in the same snapshot: a value that matches the catalogue is the catalogue's, not a decision.
func (d *Demand) SetCourseInstanceFormat(ctx context.Context, id uuid.UUID,
	delivery domain.Delivery, englishOverride *bool,
) (*domain.CourseInstance, error) {
	if err := New(d.pool).UpdateCourseInstanceFormat(ctx, UpdateCourseInstanceFormatParams{
		ID:              id,
		Delivery:        string(delivery),
		TaughtInEnglish: englishOverride,
	}); err != nil {
		return nil, fmt.Errorf("cannot set how the instance is taught: %w", err)
	}

	instance, err := d.CourseInstanceByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if instance == nil {
		return nil, domain.ErrInstanceNotFound
	}
	return instance, nil
}

// DeleteCourseInstance withdraws an instance, unless something hangs off it.
//
// The refusal is a foreign key, mapped here without being read, and it stays opaque where
// removing a part does not. Two different things can hang off an instance now — a wish on the
// instance itself, an assignment on one of its parts — and telling the caller which would be
// telling them something they may not be entitled to. "This instance has three wishes" is the
// confidential fact with the names taken out, and this is where it would leak.
//
// A part has exactly one kind of thing pointing at it, so DeleteInstancePart can afford to name
// it. The asymmetry is deliberate and each half is argued where it is.
func (d *Demand) DeleteCourseInstance(ctx context.Context, id uuid.UUID) error {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("cannot begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := New(tx)

	// Whatever this cohort was holding for other programmes is handed to one of them first, so
	// that the withdrawal can go ahead at all. A refusal here would leave a stale row in one
	// programme holding another programme's lead hostage.
	if _, err := promoteCoverageSuccessor(ctx, q, id); err != nil {
		return err
	}

	// And whatever interest was registered in it moves to a sibling cohort, if the module stays
	// offered in this programme. See handOverWishes for why this is not a refusal any more.
	if err := handOverWishes(ctx, q, id); err != nil {
		return err
	}

	rows, err := q.DeleteCourseInstance(ctx, id)
	if isForeignKeyViolation(err) {
		// Something still points at it — a wish on the last cohort of a module, an assignment on
		// one of its parts — and that refusal stays opaque. Naming it would be the wish oracle
		// db/queries/wish.sql exists to prevent: "this instance has 3 wishes" is the confidential
		// fact with the names taken out. The handovers above roll back with it.
		return domain.ErrInstanceInUse
	}
	if err != nil {
		return fmt.Errorf("cannot withdraw the instance: %w", err)
	}
	if rows == 0 {
		return domain.ErrInstanceNotFound
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("cannot commit: %w", err)
	}
	return nil
}

// AddInstancePart appends a part to an instance.
func (d *Demand) AddInstancePart(ctx context.Context, instanceID uuid.UUID,
	kind domain.InstancePartKind, hours *float64,
) (*domain.CourseInstance, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := New(tx)

	// A covered cohort holds no teaching of its own; another programme holds it. Adding a part
	// here would be the joint event counted a second time, in the programme that does not hold
	// it, and it would look exactly like ordinary demand.
	if err := refuseIfCovered(ctx, q, instanceID); err != nil {
		return nil, err
	}

	position, err := q.NextInstancePartPosition(ctx, instanceID)
	if err != nil {
		return nil, fmt.Errorf("cannot find the end of the list: %w", err)
	}

	numeric, err := numericOrNull(hours)
	if err != nil {
		return nil, err
	}

	if _, err := q.InsertInstancePart(ctx, InsertInstancePartParams{
		CourseInstanceID:    instanceID,
		Kind:                string(kind),
		Position:            position,
		TeachingHours:       numeric,
		ServesSiblingTracks: false,
	}); isForeignKeyViolation(err) {
		return nil, domain.ErrInstanceNotFound
	} else if err != nil {
		return nil, fmt.Errorf("cannot add the part: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("cannot commit: %w", err)
	}
	return d.CourseInstanceByID(ctx, instanceID)
}

// UpdateInstancePart writes a part's kind and hours.
func (d *Demand) UpdateInstancePart(ctx context.Context, partID uuid.UUID,
	kind domain.InstancePartKind, hours *float64,
) (*domain.CourseInstance, error) {
	numeric, err := numericOrNull(hours)
	if err != nil {
		return nil, err
	}

	if err := New(d.pool).UpdateInstancePart(ctx, UpdateInstancePartParams{
		ID:            partID,
		Kind:          string(kind),
		TeachingHours: numeric,
	}); isForeignKeyViolation(err) {
		// The only foreign key a part's own columns take part in is the one tying a piece to the
		// part it was cut from, by kind: a laboratory cut in two cannot become a lecture on one
		// side only.
		return nil, domain.ErrPartDivided
	} else if err != nil {
		return nil, fmt.Errorf("cannot change the part: %w", err)
	}

	instance, err := d.CourseInstanceByPartID(ctx, partID)
	if err != nil {
		return nil, err
	}
	if instance == nil {
		return nil, domain.ErrPartNotFound
	}
	return instance, nil
}

// DeleteInstancePart removes a part, unless something hangs off it.
func (d *Demand) DeleteInstancePart(ctx context.Context, partID uuid.UUID) (*domain.CourseInstance, error) {
	// Read the instance first: after the delete there is no part to find it through, and
	// answering with the instance is what lets one screen refresh from one round trip.
	instance, err := d.CourseInstanceByPartID(ctx, partID)
	if err != nil {
		return nil, err
	}
	if instance == nil {
		return nil, domain.ErrPartNotFound
	}

	q := New(d.pool)
	// A part with pieces cut off it is refused before the delete rather than read off its foreign
	// key violation, because that violation is indistinguishable from an assignment's without
	// naming the constraint — see isForeignKeyViolation for why that is not done.
	hasPieces, err := q.InstancePartHasPieces(ctx, partID)
	if err != nil {
		return nil, fmt.Errorf("cannot check for pieces of the part: %w", err)
	}
	if hasPieces {
		return nil, domain.ErrPartDivided
	}

	rows, err := q.DeleteInstancePart(ctx, partID)
	if isForeignKeyViolation(err) {
		// A part is pointed at by exactly one thing — an assignment — so unlike the instance
		// above, this refusal can name what hangs off it without choosing between candidates.
		// That is not a leak: only somebody who may write the demand of this programme reaches
		// here, and they may read its assignments. bootstrap.TestPartAssignedTellsNobody-
		// SomethingNew asserts it, and turns red the day either half of that changes.
		return nil, domain.ErrPartAssigned
	}
	if err != nil {
		return nil, fmt.Errorf("cannot remove the part: %w", err)
	}
	if rows == 0 {
		return nil, domain.ErrPartNotFound
	}
	return d.CourseInstanceByID(ctx, instance.ID)
}

// Dividing a part, and handing one to another faculty.
//
// Three writes on one row that each have to exclude an assignment arriving at the same moment, so
// each takes LockInstancePart first. FillInstancePart takes FOR SHARE on the same row, which is
// what makes the lock mean something: whichever comes second sees what the first one did.

// DivideInstancePart cuts hours off a part into a new piece of the same kind.
//
// The hours come off the part named, and the piece names the undivided part — the part itself, or
// the one a piece was cut from — so a lecture of 4 cut into 2 and then 1 is one part and two pieces
// beside it, not a chain. The arithmetic is done by PostgreSQL in numeric, so the instance's
// figure is the same afterwards to the cent.
func (d *Demand) DivideInstancePart(ctx context.Context, partID uuid.UUID, hours float64,
) (*domain.CourseInstance, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := New(tx)

	part, err := q.LockInstancePart(ctx, partID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrPartNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read the part: %w", err)
	}
	// Another faculty's part is not ours to cut. Dividing it would make two parts nobody here
	// teaches, which is the same arrangement with more rows.
	if part.TaughtBy != nil {
		return nil, domain.ErrPartTaughtElsewhere
	}
	current := numericFloatOrNil(part.TeachingHours)
	if current == nil || hours >= *current {
		return nil, domain.ErrPartInvalid
	}

	cut, err := numericFrom(hours)
	if err != nil {
		return nil, err
	}
	// numericFrom rounds to the cent; what is left has to be strictly positive after that too.
	if cutFloat := numericFloatOrNil(cut); cutFloat == nil || *cutFloat <= 0 || *cutFloat >= *current {
		return nil, domain.ErrPartInvalid
	}
	negative, err := numericFrom(-hours)
	if err != nil {
		return nil, err
	}

	root := part.ID
	if part.DividedFromID.Valid {
		root = part.DividedFromID.UUID
	}

	if err := q.AddInstancePartHours(ctx, AddInstancePartHoursParams{ID: part.ID, Delta: negative}); err != nil {
		return nil, fmt.Errorf("cannot take the hours off the part: %w", err)
	}
	if _, err := q.InsertInstancePiece(ctx, InsertInstancePieceParams{
		CourseInstanceID:    part.CourseInstanceID,
		Kind:                part.Kind,
		Position:            part.Position,
		ServesSiblingTracks: part.ServesSiblingTracks,
		TeachingHours:       cut,
		DividedFromID:       uuid.NullUUID{UUID: root, Valid: true},
	}); err != nil {
		return nil, fmt.Errorf("cannot cut the piece off: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("cannot commit: %w", err)
	}
	return d.CourseInstanceByID(ctx, part.CourseInstanceID)
}

// RejoinInstancePart gives a piece's hours back to the part it was cut from, and removes it.
//
// Refused while somebody holds the piece: that would take teaching away from a person, which is
// clearing an assignment and is done as one.
func (d *Demand) RejoinInstancePart(ctx context.Context, partID uuid.UUID) (*domain.CourseInstance, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := New(tx)

	piece, err := q.LockInstancePart(ctx, partID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrPartNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read the part: %w", err)
	}
	if !piece.DividedFromID.Valid {
		return nil, domain.ErrPartNotDivided
	}
	assigned, err := q.InstancePartIsAssigned(ctx, piece.ID)
	if err != nil {
		return nil, fmt.Errorf("cannot check whether the piece is staffed: %w", err)
	}
	if assigned {
		return nil, domain.ErrPartAssigned
	}

	if _, err := q.LockInstancePart(ctx, piece.DividedFromID.UUID); err != nil {
		return nil, fmt.Errorf("cannot read the part the piece was cut from: %w", err)
	}

	if piece.TeachingHours.Valid {
		if err := q.AddInstancePartHours(ctx, AddInstancePartHoursParams{
			ID:    piece.DividedFromID.UUID,
			Delta: piece.TeachingHours,
		}); hasSQLState(err, "23514") {
			// The undivided part was corrected upwards since, and the two together would exceed
			// what a part may credit anybody with.
			return nil, domain.ErrPartInvalid
		} else if err != nil {
			return nil, fmt.Errorf("cannot give the hours back: %w", err)
		}
	}

	if _, err := q.DeleteInstancePart(ctx, piece.ID); isForeignKeyViolation(err) {
		return nil, domain.ErrPartAssigned
	} else if err != nil {
		return nil, fmt.Errorf("cannot remove the piece: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("cannot commit: %w", err)
	}
	return d.CourseInstanceByID(ctx, piece.CourseInstanceID)
}

// SetInstancePartTaughtBy says that another faculty teaches a part, or with "" that we do again.
//
// Refused while somebody of ours holds it. Handing a staffed part away would leave an assignment
// on a part that cannot be staffed, and the person on it would find out from their own list.
func (d *Demand) SetInstancePartTaughtBy(ctx context.Context, partID uuid.UUID, faculty string,
) (*domain.CourseInstance, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := New(tx)

	part, err := q.LockInstancePart(ctx, partID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrPartNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read the part: %w", err)
	}

	var taughtBy *string
	if faculty != "" {
		assigned, err := q.InstancePartIsAssigned(ctx, part.ID)
		if err != nil {
			return nil, fmt.Errorf("cannot check whether the part is staffed: %w", err)
		}
		if assigned {
			return nil, domain.ErrPartAssigned
		}
		taughtBy = &faculty
	}

	if err := q.SetInstancePartTaughtBy(ctx, SetInstancePartTaughtByParams{
		ID:       part.ID,
		TaughtBy: taughtBy,
	}); hasSQLState(err, "23514") {
		return nil, domain.ErrTaughtByTooLong
	} else if err != nil {
		return nil, fmt.Errorf("cannot say who teaches the part: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("cannot commit: %w", err)
	}
	return d.CourseInstanceByID(ctx, part.CourseInstanceID)
}

// ShareInstancePartAcrossTracks makes one part serve the sibling cohorts as well.
//
// The case: the lecture for IF3A and IF3B is given once, by one person, and its two hours count
// once. What makes it a transaction is that it is two facts — this part is shared, and the
// siblings no longer hold their own — and a database in which only the first is written has a
// cohort attending two lectures.
//
// The siblings' parts are matched by kind, because that is what "the lecture" means to the
// person doing it. One of them that something already hangs off refuses to go, and then nothing
// happens at all.
func (d *Demand) ShareInstancePartAcrossTracks(ctx context.Context, partID uuid.UUID) (*domain.CourseInstance, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := New(tx)

	instanceID, kind, _, _, err := partContext(ctx, q, partID)
	if err != nil {
		return nil, err
	}

	siblings, err := q.SiblingInstanceIDs(ctx, instanceID)
	if err != nil {
		return nil, fmt.Errorf("cannot read the sibling cohorts: %w", err)
	}
	if len(siblings) == 0 {
		return nil, domain.ErrNoSiblingTracks
	}

	// Merging deletes the siblings' parts of this kind and flags only this one, so a division or
	// another faculty's part on either side would be lost or left half-shared. Both are exceptions
	// of an exception; undoing them first is the honest repair.
	involved, err := q.PartsOfKindAreDividedOrElsewhere(ctx, PartsOfKindAreDividedOrElsewhereParams{
		InstanceIds: append([]uuid.UUID{instanceID}, siblings...),
		Kind:        kind,
	})
	if err != nil {
		return nil, fmt.Errorf("cannot check the parts to merge: %w", err)
	}
	if involved {
		return nil, domain.ErrPartDivided
	}

	if err := q.SetInstancePartShared(ctx, SetInstancePartSharedParams{
		ID:                  partID,
		ServesSiblingTracks: true,
	}); err != nil {
		return nil, fmt.Errorf("cannot share the part: %w", err)
	}

	if _, err := q.DeleteInstancePartsOfKind(ctx, DeleteInstancePartsOfKindParams{
		InstanceIds: siblings,
		Kind:        kind,
	}); isForeignKeyViolation(err) {
		// Merging two cohorts' lectures into one removes the sibling's part, and a sibling part
		// that is staffed is somebody's teaching. Same refusal as removing one directly.
		return nil, domain.ErrPartAssigned
	} else if err != nil {
		return nil, fmt.Errorf("cannot remove the siblings' own parts: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("cannot commit: %w", err)
	}
	return d.CourseInstanceByID(ctx, instanceID)
}

// SplitInstancePartAcrossTracks undoes the sharing: every cohort holds its own again.
//
// The inverse has to exist and has to be as easy, because sharing is a judgement that gets
// revised — the person who was going to give both lectures is on sabbatical. A cohort that
// already has a part of that kind is left alone rather than given a second one: the two
// operations are not symmetric in what they can assume, and the safe direction is not to
// duplicate teaching.
func (d *Demand) SplitInstancePartAcrossTracks(ctx context.Context, partID uuid.UUID) (*domain.CourseInstance, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := New(tx)

	instanceID, kind, hours, shared, err := partContext(ctx, q, partID)
	if err != nil {
		return nil, err
	}
	if !shared {
		return nil, domain.ErrNotSharedAcrossTracks
	}

	// A shared lecture that was divided afterwards is several rows that each serve every cohort.
	// Handing each cohort its own copy of one of them would give it a fraction of a lecture.
	divided, err := q.PartsOfKindAreDividedOrElsewhere(ctx, PartsOfKindAreDividedOrElsewhereParams{
		InstanceIds: []uuid.UUID{instanceID},
		Kind:        kind,
	})
	if err != nil {
		return nil, fmt.Errorf("cannot check the part: %w", err)
	}
	if divided {
		return nil, domain.ErrPartDivided
	}

	siblings, err := q.SiblingInstanceIDs(ctx, instanceID)
	if err != nil {
		return nil, fmt.Errorf("cannot read the sibling cohorts: %w", err)
	}

	if err := q.SetInstancePartShared(ctx, SetInstancePartSharedParams{
		ID:                  partID,
		ServesSiblingTracks: false,
	}); err != nil {
		return nil, fmt.Errorf("cannot stop sharing the part: %w", err)
	}

	for _, sibling := range siblings {
		// A sibling whose demand another programme holds gets nothing back. It holds no teaching
		// at all by construction, and handing it a lecture here would give it parts of its own —
		// the one cardinality no foreign key can refuse and the reason refuseIfCovered exists.
		// The joint event would then be counted twice, in a figure that looks right.
		//
		// Theoretical while coupling was arranged by hand; ordinary now that declaring beside
		// another programme couples on the spot.
		if err := refuseIfCovered(ctx, q, sibling); errors.Is(err, domain.ErrInstanceCovered) {
			continue
		} else if err != nil {
			return nil, err
		}

		exists, err := q.InstancePartsOfKindExist(ctx, InstancePartsOfKindExistParams{
			CourseInstanceID: sibling,
			Kind:             kind,
		})
		if err != nil {
			return nil, fmt.Errorf("cannot check the sibling cohort: %w", err)
		}
		if exists {
			continue
		}

		position, err := q.NextInstancePartPosition(ctx, sibling)
		if err != nil {
			return nil, fmt.Errorf("cannot find the end of the sibling's list: %w", err)
		}
		if _, err := q.InsertInstancePart(ctx, InsertInstancePartParams{
			CourseInstanceID:    sibling,
			Kind:                kind,
			Position:            position,
			TeachingHours:       hours,
			ServesSiblingTracks: false,
		}); err != nil {
			return nil, fmt.Errorf("cannot give the sibling its own part: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("cannot commit: %w", err)
	}
	return d.CourseInstanceByID(ctx, instanceID)
}

// Coverage: one programme's demand met by another programme's event.
//
// Three writes, and the shape is the one ShareInstancePartAcrossTracks already has one level
// down — ask, agree, undo — because it is the same problem at the next grain up. What differs is
// that the two sides are two *programmes*, so the agreement is a decision by somebody the asking
// lead has no permission over, and it is therefore a stored fact rather than an argument.

// lockCoveragePair takes both rows in id order.
//
// Id order rather than guest-then-host: two simultaneous handshakes between the same pair, each
// locking the row it was asked about first, is the textbook deadlock. The foreign key would
// refuse the impossible outcome anyway; the lock is what makes the refusal a refusal instead of a
// serialisation error somebody has to interpret.
func lockCoveragePair(ctx context.Context, q *Queries, a, b uuid.UUID) error {
	first, second := a, b
	if first.String() > second.String() {
		first, second = second, first
	}
	for _, id := range []uuid.UUID{first, second} {
		if _, err := q.LockInstanceForCoverage(ctx, id); errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrInstanceNotFound
		} else if err != nil {
			return fmt.Errorf("cannot lock an instance: %w", err)
		}
	}
	return nil
}

// refuseIfCovered stops a write that would give a covered cohort teaching of its own.
//
// Accepted coverage only. A cohort whose request nobody has answered still holds its own parts and
// is still an ordinary instance in every way — the whole point of the two-sided handshake is that
// asking changes nothing until somebody agrees.
func refuseIfCovered(ctx context.Context, q *Queries, instanceID uuid.UUID) error {
	ctx1, err := q.CoverageContextByInstanceID(ctx, instanceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrInstanceNotFound
	}
	if err != nil {
		return fmt.Errorf("cannot read the instance: %w", err)
	}
	if ctx1.CoveredAcceptedAt.Valid {
		return domain.ErrInstanceCovered
	}
	return nil
}

// handOverWishes moves the wishes on a withdrawing cohort to a sibling cohort of the same module,
// so that two cohorts can be merged into one after the wish round has run.
//
// # WHY THE WISHES MOVE INSTEAD OF REFUSING THE WITHDRAWAL
//
// A wish points at a cohort, and the foreign key used to refuse withdrawing one somebody had
// wished for. That held the demand still from the first wish onwards, while the faculty plans
// iteratively: cohorts are added and merged after the wish round, once it is clear who teaches
// what (2026-10-23, from a programme lead's report). What somebody registered interest in is
// "Softwareentwicklung II in this programme", and a merged cohort is still that offer — so the
// interest moves with it rather than being refused or deleted.
//
// Somebody who wished for both cohorts keeps one wish: the stronger priority, both notes. A module
// withdrawn altogether has no sibling to move to, and the foreign key refuses as before — that is
// taking an offer away, not reshaping it.
//
// Reports nothing, and that is the confidentiality rule rather than an omission: "2 wishes moved"
// is the count before publication with the names taken out. The withdrawal now succeeds whether
// or not anybody wished for the cohort, which removes the oracle the refusal used to be instead
// of adding a new one.
func handOverWishes(ctx context.Context, q *Queries, instanceID uuid.UUID) error {
	target, err := q.WishHandoverTarget(ctx, instanceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("cannot find the cohort to hand the wishes to: %w", err)
	}

	if err := q.MergeCollidingWishNotes(ctx, MergeCollidingWishNotesParams{
		FromInstance: instanceID, ToInstance: target,
	}); err != nil {
		return fmt.Errorf("cannot merge the notes of a double wish: %w", err)
	}
	if err := q.DeleteWeakerCollidingWishes(ctx, DeleteWeakerCollidingWishesParams{
		FromInstance: instanceID, ToInstance: target,
	}); err != nil {
		return fmt.Errorf("cannot merge a double wish: %w", err)
	}
	if err := q.MoveWishesToInstance(ctx, MoveWishesToInstanceParams{
		FromInstance: instanceID, ToInstance: target,
	}); err != nil {
		return fmt.Errorf("cannot hand the wishes over: %w", err)
	}
	return nil
}

// promoteCoverageSuccessor hands a withdrawing cohort's teaching to one of the cohorts it holds it
// for, so that the withdrawal can go ahead.
//
// # WHY A WITHDRAWAL PROMOTES INSTEAD OF BEING REFUSED
//
// This one is not justified by staying inside the writer's scope; it plainly does not. Parts
// appear in a programme the withdrawing lead does not lead, and an assignment moves with them.
// It is justified by being the only outcome that loses nothing.
//
// A holder's withdrawal has four possible answers and three of them destroy a fact:
//
//   - Refuse it. That was the old answer, and it leaves one programme's stale row holding another
//     programme's lead hostage: the guest cannot fix it because the row is not theirs.
//   - Take the guests with it. That deletes another programme's declaration outright.
//   - Release the guests to rebuild from the module's split. That loses the group counts, loses
//     the assignment, and turns one event into several.
//   - Hand it to the longest-standing guest. The event survives with its groups and whoever holds
//     it, both declarations survive, the remaining guests keep attending the same teaching, and
//     the faculty's hours do not move by an hour.
//
// The consent that is missing is the successor's, and it is bought back the way the coupling's is:
// it may release at once, and then holds what it would have held had it never been coupled. What
// is *not* bought back is the group count — it arrives as the withdrawing programme planned it,
// and the plan report says so.
//
// Returns the successor for the report, or nil where there was nothing to promote.
func promoteCoverageSuccessor(ctx context.Context, q *Queries, hostID uuid.UUID) (
	*domain.CourseInstance, error,
) {
	if _, err := q.LockCoverageGroup(ctx, hostID); err != nil {
		return nil, fmt.Errorf("cannot lock the cohorts: %w", err)
	}

	successor, err := q.CoverageSuccessorFor(ctx, uuid.NullUUID{UUID: hostID, Valid: true})
	if errors.Is(err, pgx.ErrNoRows) {
		// Nobody's demand hangs off it; an ordinary withdrawal.
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot find the cohort to hand the teaching to: %w", err)
	}

	// First, give this programme's own sibling cohorts back what this cohort was holding for them.
	//
	// serves_sibling_tracks means "held for the other cohorts of my programme". Moved to another
	// programme it would go on saying that there — the withdrawing programme's siblings would
	// silently lose the lecture they were attending, and the successor's would gain one they never
	// had. Only inserts, so this cannot meet a RESTRICT.
	if err := unshareBeforeHandover(ctx, q, hostID); err != nil {
		return nil, err
	}

	// The successor stops being a guest before anything points at it: the foreign key reads its
	// is_covered, which is generated from covered_by_instance_id.
	if _, err := q.ReleaseInstanceCoverage(ctx, successor.ID); err != nil {
		return nil, fmt.Errorf("cannot release the cohort taking over: %w", err)
	}

	if _, err := q.MoveInstancePartsTo(ctx, MoveInstancePartsToParams{
		FromInstance: hostID,
		ToInstance:   successor.ID,
	}); err != nil {
		return nil, fmt.Errorf("cannot hand the teaching over: %w", err)
	}

	if _, err := q.RepointGuestsTo(ctx, RepointGuestsToParams{
		FromInstance: hostID,
		ToInstance:   successor.ID,
	}); err != nil {
		return nil, fmt.Errorf("cannot point the other cohorts at the new holder: %w", err)
	}

	return &domain.CourseInstance{
		ID:    successor.ID,
		Track: successor.Track,
		Programme: domain.Programme{
			ID: successor.ProgrammeID, Code: successor.ProgrammeCode, Title: successor.ProgrammeTitle,
		},
	}, nil
}

// unshareBeforeHandover gives a withdrawing cohort's own siblings back whatever it was holding for
// them, and clears the flag so the rows mean nothing once they belong to another programme.
//
// The body of SplitInstancePartAcrossTracks, minus its bookkeeping: a covered sibling is skipped
// for the reason it is skipped there too — it holds nothing by construction.
func unshareBeforeHandover(ctx context.Context, q *Queries, hostID uuid.UUID) error {
	shared, err := q.SharedPartsOf(ctx, hostID)
	if err != nil {
		return fmt.Errorf("cannot read what this cohort holds for its siblings: %w", err)
	}
	if len(shared) == 0 {
		return nil
	}

	siblings, err := q.SiblingInstanceIDs(ctx, hostID)
	if err != nil {
		return fmt.Errorf("cannot read the sibling cohorts: %w", err)
	}

	for _, part := range shared {
		for _, sibling := range siblings {
			if err := refuseIfCovered(ctx, q, sibling); errors.Is(err, domain.ErrInstanceCovered) {
				continue
			} else if err != nil {
				return err
			}

			exists, err := q.InstancePartsOfKindExist(ctx, InstancePartsOfKindExistParams{
				CourseInstanceID: sibling,
				Kind:             part.Kind,
			})
			if err != nil {
				return fmt.Errorf("cannot check the sibling cohort: %w", err)
			}
			if exists {
				continue
			}

			position, err := q.NextInstancePartPosition(ctx, sibling)
			if err != nil {
				return fmt.Errorf("cannot find the end of the sibling's list: %w", err)
			}
			if _, err := q.InsertInstancePart(ctx, InsertInstancePartParams{
				CourseInstanceID:    sibling,
				Kind:                part.Kind,
				Position:            position,
				TeachingHours:       part.TeachingHours,
				ServesSiblingTracks: false,
			}); err != nil {
				return fmt.Errorf("cannot give the sibling its own part back: %w", err)
			}
		}

	}

	// Every shared row at once, the pieces of a divided lecture included.
	if err := q.UnsharePartsOfInstance(ctx, hostID); err != nil {
		return fmt.Errorf("cannot stop sharing the parts: %w", err)
	}
	return nil
}

// coupleIfHostExists holds a freshly declared cohort with another programme's event, where there
// is one to hold it with.
//
// The rule the faculty stated: a cohort declared beside another programme's declaration of the
// same module is held with it from the moment it is declared. Planning separately is what somebody
// chooses afterwards, with one click.
//
// # WHY THIS IS NOT FOLDED INTO THE INSERT
//
// Called from exactly two places — declaring one instance and planning a screenful of them — and
// deliberately not from InsertCourseInstance itself. CopyDemand inserts through the same statement
// and must not couple: a copy reproduces the arrangement of the semester it copies from, and
// inventing a coupling because some other programme happens to have declared the module in the
// target semester would be the copy making a decision nobody pressed a button for.
//
// # WHY IT CAN DECLINE
//
// Two ways, and both end with the cohort holding its own teaching rather than with an error:
//
//   - The programme already has a covered cohort of this module. A covered cohort is that
//     programme's whole participation, so the second one holds its own — and the partial unique
//     index would otherwise refuse the write as a raw unique violation out of a save.
//   - The host vanished between the read and the write. FOR KEY SHARE with LIMIT 1 returns *no*
//     row under EvalPlanQual when the chosen one no longer qualifies, rather than the next
//     candidate, so the caller runs this inside a savepoint and carries on.
func coupleIfHostExists(ctx context.Context, q *Queries, instanceID uuid.UUID,
	semesterID, moduleID, programmeID, by uuid.UUID,
) (bool, error) {
	taken, err := q.CoveredCohortExistsFor(ctx, CoveredCohortExistsForParams{
		SemesterID:  semesterID,
		ModuleID:    moduleID,
		ProgrammeID: programmeID,
	})
	if err != nil {
		return false, fmt.Errorf("cannot check for a covered cohort: %w", err)
	}
	if taken {
		return false, nil
	}

	host, err := q.CoverageHostFor(ctx, CoverageHostForParams{
		SemesterID:  semesterID,
		ModuleID:    moduleID,
		ProgrammeID: programmeID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("cannot look for a holding cohort: %w", err)
	}

	rows, err := q.CoupleInstanceCoverage(ctx, CoupleInstanceCoverageParams{
		ID:     instanceID,
		HostID: host.ID,
		By:     nullUUID(nonNilUUID(by)),
	})
	if err != nil {
		return false, fmt.Errorf("cannot hold the cohort with the other programme's event: %w", err)
	}
	return rows > 0, nil
}

// RequestInstanceCoverage asks that this instance's demand be met by another programme's event.
//
// Written by the guest's lead, and it changes nothing about the instance it points at — which is
// what keeps the write inside the writer's own programme. The host's lead answers with
// AcceptInstanceCoverage, and until they do, nothing is borrowed and nothing is deleted.
//
// The schema decides whether the host is a legitimate target: same semester, same module, another
// programme, not itself covered, all four in one composite foreign key. The checks below exist to
// turn its refusal into a sentence that names the repair — the key is what makes them true, and
// they cannot come to disagree with it because they are read from the same row it points at.
func (d *Demand) RequestInstanceCoverage(ctx context.Context, guestID, hostID, by uuid.UUID) (
	*domain.CourseInstance, error,
) {
	if guestID == hostID {
		return nil, domain.ErrCoverageSelf
	}

	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := New(tx)

	if err := lockCoveragePair(ctx, q, guestID, hostID); err != nil {
		return nil, err
	}

	guest, err := q.CoverageContextByInstanceID(ctx, guestID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrInstanceNotFound
	} else if err != nil {
		return nil, fmt.Errorf("cannot read the instance: %w", err)
	}
	if guest.HostID.Valid {
		return nil, domain.ErrCoverageAlreadySet
	}

	host, err := q.CoverageContextByInstanceID(ctx, hostID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrInstanceNotFound
	} else if err != nil {
		return nil, fmt.Errorf("cannot read the covering instance: %w", err)
	}
	switch {
	case host.SemesterID != guest.SemesterID || host.ModuleID != guest.ModuleID:
		return nil, domain.ErrCoverageModuleMismatch
	case host.GuestProgrammeID == guest.GuestProgrammeID:
		return nil, domain.ErrCoverageSameProgramme
	case host.HostID.Valid:
		return nil, domain.ErrCoverageWouldChain
	}

	rows, err := q.RequestInstanceCoverage(ctx, RequestInstanceCoverageParams{
		ID:          guestID,
		HostID:      hostID,
		RequestedBy: nullUUID(nonNilUUID(by)),
	})
	if err != nil {
		return nil, fmt.Errorf("cannot record the request: %w", err)
	}
	if rows == 0 {
		return nil, domain.ErrCoverageAlreadySet
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("cannot commit: %w", err)
	}
	return d.CourseInstanceByID(ctx, guestID)
}

// AcceptInstanceCoverage agrees to hold this event for the asking programme as well.
//
// The exact counterpart of ShareInstancePartAcrossTracks, and the argument is the same sentence
// one level up: it is two facts — this demand is covered, and the asking cohort no longer holds
// its own teaching — and a database in which only the first is written counts one lecture twice.
//
// The guest's parts go by instance rather than by kind, because coverage is of the whole
// offering: what the guest keeps is nothing. A part that is already staffed refuses to go, and
// then nothing happens at all — assignment references instance_part ON DELETE RESTRICT, which is
// the same refusal removing a part directly meets.
//
// Wishes on the guest are untouched, and that is not an oversight. A wish points at the instance,
// the instance survives, and somebody's registered interest in teaching this module for their own
// programme does not stop being true because the event is now held jointly. Where it is decided
// — the assignment of the host's part — is where the two programmes' wishes are read together.
func (d *Demand) AcceptInstanceCoverage(ctx context.Context, guestID, by uuid.UUID) (
	*domain.CourseInstance, error,
) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := New(tx)

	guest, err := q.CoverageContextByInstanceID(ctx, guestID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrInstanceNotFound
	} else if err != nil {
		return nil, fmt.Errorf("cannot read the instance: %w", err)
	}
	if !guest.HostID.Valid {
		return nil, domain.ErrCoverageNotRequested
	}
	if guest.CoveredAcceptedAt.Valid {
		return nil, domain.ErrCoverageAlreadyAccepted
	}

	if err := lockCoveragePair(ctx, q, guestID, guest.HostID.UUID); err != nil {
		return nil, err
	}

	rows, err := q.AcceptInstanceCoverage(ctx, AcceptInstanceCoverageParams{
		ID:         guestID,
		HostID:     guest.HostID.UUID,
		AcceptedBy: nullUUID(nonNilUUID(by)),
	})
	if err != nil {
		return nil, fmt.Errorf("cannot record the agreement: %w", err)
	}
	if rows == 0 {
		// The guest's lead pointed somewhere else, or somebody else agreed, between the read
		// above and here. Answering the request that is there now would be agreeing to something
		// this caller never saw.
		return nil, domain.ErrCoverageNotRequested
	}

	if _, err := q.DeleteInstancePartsOfInstance(ctx, guestID); isForeignKeyViolation(err) {
		return nil, domain.ErrPartAssigned
	} else if err != nil {
		return nil, fmt.Errorf("cannot remove the covered cohort's own parts: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("cannot commit: %w", err)
	}
	return d.CourseInstanceByID(ctx, guestID)
}

// ReleaseInstanceCoverage ends it: a request withdrawn, a request declined, or an agreement
// revised.
//
// One operation for all three because they are one state — the demand is simply not covered — and
// three would be three places to get the permission wrong. Which side may call it is the service's
// question, not this one's.
//
// Where the coverage had been accepted the guest gets its teaching back, built from the module's
// split the same way declaring an instance builds it. The inverse has to be as easy as the
// agreement, for the reason SplitInstancePartAcrossTracks gives: sharing is a judgement that gets
// revised, and the person who was going to hold both is on sabbatical.
//
// What does not come back is the number of laboratory *groups*. The split states one unit per
// kind, and the multiplicity was a planning decision that went with the parts. Inventing three
// groups because there were three before would be inventing teaching; the lead adds them back, and
// the interface says so.
func (d *Demand) ReleaseInstanceCoverage(ctx context.Context, guestID uuid.UUID) (
	*domain.CourseInstance, error,
) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := New(tx)

	guest, err := q.CoverageContextByInstanceID(ctx, guestID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrInstanceNotFound
	} else if err != nil {
		return nil, fmt.Errorf("cannot read the instance: %w", err)
	}
	if !guest.HostID.Valid {
		return nil, domain.ErrCoverageNotRequested
	}
	wasAccepted := guest.CoveredAcceptedAt.Valid

	if _, err := q.ReleaseInstanceCoverage(ctx, guestID); err != nil {
		return nil, fmt.Errorf("cannot end the coverage: %w", err)
	}

	if wasAccepted {
		components, err := statedComponents(ctx, q, guest.ModuleID)
		if err != nil {
			return nil, err
		}
		if _, err := partsFromSplit(ctx, q, guestID, components, nil); err != nil {
			return nil, fmt.Errorf("cannot give the cohort its teaching back: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("cannot commit: %w", err)
	}
	return d.CourseInstanceByID(ctx, guestID)
}

// HostCandidates lists the instances that could cover this one.
//
// The foreign key's four conditions as a list, so a picker offers exactly what the schema would
// accept rather than a menu with entries that fail on click.
func (d *Demand) HostCandidates(ctx context.Context, guestID uuid.UUID) ([]domain.CourseInstance, error) {
	rows, err := New(d.pool).HostCandidatesFor(ctx, guestID)
	if err != nil {
		return nil, fmt.Errorf("cannot read the possible covering instances: %w", err)
	}

	out := make([]domain.CourseInstance, 0, len(rows))
	for _, r := range rows {
		out = append(out, domain.CourseInstance{
			ID:                r.ID,
			Track:             r.Track,
			ProgrammeSemester: intOrNil(r.ProgrammeSemester),
			Programme: domain.Programme{
				ID: r.ProgrammeID, Code: r.ProgrammeCode, Title: r.ProgrammeTitle,
			},
		})
	}
	return out, nil
}

// partContext reads the one part a part-level operation is about.
//
// Through the parts of its instance rather than by id, because the instance is what the caller
// needs anyway and the list is three rows long.
func partContext(ctx context.Context, q *Queries, partID uuid.UUID) (
	instanceID uuid.UUID, kind string, hours pgtype.Numeric, shared bool, err error,
) {
	instance, err := q.CourseInstanceByPartID(ctx, partID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, "", pgtype.Numeric{}, false, domain.ErrPartNotFound
	}
	if err != nil {
		return uuid.Nil, "", pgtype.Numeric{}, false, fmt.Errorf("cannot read the instance of this part: %w", err)
	}

	parts, err := q.InstancePartsFor(ctx, []uuid.UUID{instance.ID})
	if err != nil {
		return uuid.Nil, "", pgtype.Numeric{}, false, fmt.Errorf("cannot read the instance parts: %w", err)
	}
	for _, p := range parts {
		if p.ID == partID {
			return instance.ID, p.Kind, p.TeachingHours, p.ServesSiblingTracks, nil
		}
	}
	return uuid.Nil, "", pgtype.Numeric{}, false, domain.ErrPartNotFound
}

// copyParts writes parts into another instance, pieces and all.
//
// Two passes, because a piece names the undivided part by id and the copy's id is only known once
// it is written. A piece whose undivided part is not among parts is left out with it.
//
// renumber gives the undivided parts the positions 0, 1, 2 … — a duplication starts its own list —
// where a copy keeps them as they were. Either way a piece takes the position of its part.
func copyParts(ctx context.Context, q *Queries, parts []InstancePartsForRow, to uuid.UUID,
	renumber bool,
) (int, error) {
	created := 0
	copied := make(map[uuid.UUID]uuid.UUID, len(parts))
	positions := make(map[uuid.UUID]int32, len(parts))
	next := int32(0)
	for _, p := range parts {
		if p.DividedFromID.Valid {
			continue
		}
		position := p.Position
		if renumber {
			position = next
			next++
		}
		id, err := q.InsertInstancePiece(ctx, InsertInstancePieceParams{
			CourseInstanceID:    to,
			Kind:                p.Kind,
			Position:            position,
			ServesSiblingTracks: p.ServesSiblingTracks,
			TeachingHours:       p.TeachingHours,
			TaughtBy:            p.TaughtBy,
		})
		if err != nil {
			return created, err
		}
		copied[p.ID] = id
		positions[p.ID] = position
		created++
	}
	for _, p := range parts {
		if !p.DividedFromID.Valid {
			continue
		}
		root, ok := copied[p.DividedFromID.UUID]
		if !ok {
			continue
		}
		if _, err := q.InsertInstancePiece(ctx, InsertInstancePieceParams{
			CourseInstanceID:    to,
			Kind:                p.Kind,
			Position:            positions[p.DividedFromID.UUID],
			ServesSiblingTracks: p.ServesSiblingTracks,
			TeachingHours:       p.TeachingHours,
			DividedFromID:       uuid.NullUUID{UUID: root, Valid: true},
			TaughtBy:            p.TaughtBy,
		}); err != nil {
			return created, err
		}
		created++
	}
	return created, nil
}

// CopyDemand declares in one semester what another one holds for the same programme.
//
// One transaction for the whole copy. A half-copied semester is worse than an uncopied one,
// because the person looking at it cannot tell which half is missing — and the obvious repair,
// pressing the button again, would then be indistinguishable from the mistake.
//
// What it carries over is the previous semester's *instances and their parts*, not the modules'
// splits. That is the difference between this and declaring each instance by hand, and it is the
// reason it is worth having: the number of laboratory groups is a planning decision somebody
// made last year, and it is exactly what nobody wants to enter again.
//
// Instances already declared in the target are left untouched, counted, and reported. A copy
// that overwrote them would undo work in the semester it is copying into, which is the one thing
// it must never do.
func (d *Demand) CopyDemand(ctx context.Context, from, to domain.Semester, programmeID, by uuid.UUID) (domain.CopyCounts, error) {
	var counts domain.CopyCounts

	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return counts, fmt.Errorf("cannot begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := New(tx)

	sources, err := q.CourseInstancesOfProgramme(ctx, CourseInstancesOfProgrammeParams{
		SemesterID:  from.ID,
		ProgrammeID: programmeID,
	})
	if err != nil {
		return counts, fmt.Errorf("cannot read the demand of %s: %w", from.Code, err)
	}
	if len(sources) == 0 {
		return counts, nil
	}

	sourceIDs := make([]uuid.UUID, 0, len(sources))
	for _, s := range sources {
		sourceIDs = append(sourceIDs, s.ID)
	}
	parts, err := q.InstancePartsFor(ctx, sourceIDs)
	if err != nil {
		return counts, fmt.Errorf("cannot read the parts to copy: %w", err)
	}
	partsByInstance := make(map[uuid.UUID][]InstancePartsForRow, len(sourceIDs))
	for _, p := range parts {
		partsByInstance[p.CourseInstanceID] = append(partsByInstance[p.CourseInstanceID], p)
	}

	// Where a source cohort's demand was covered, remembered by the covering cohort's *identity*
	// rather than its id: the id belongs to the semester being copied from.
	carry, err := q.CoverageToCarryForward(ctx, sourceIDs)
	if err != nil {
		return counts, fmt.Errorf("cannot read the coverage to carry forward: %w", err)
	}
	carryBySource := make(map[uuid.UUID]CoverageToCarryForwardRow, len(carry))
	for _, c := range carry {
		carryBySource[c.ForInstanceID] = c
	}

	// The copies that want covering, resolved after every instance exists: the covering cohort may
	// itself be created by this same copy, and asking before it is there would find nothing.
	type pendingCoverage struct {
		guestID    uuid.UUID
		hostModule uuid.UUID
		hostProgID uuid.UUID
		hostTrack  string
	}
	var pending []pendingCoverage

	for _, source := range sources {
		newID, err := q.InsertCourseInstanceIfAbsent(ctx, InsertCourseInstanceIfAbsentParams{
			SemesterID:        to.ID,
			ModuleID:          source.ModuleID,
			ProgrammeID:       programmeID,
			Track:             source.Track,
			ProgrammeSemester: source.ProgrammeSemester,
			Note:              source.Note,
			Delivery:          source.Delivery,
			TaughtInEnglish:   source.TaughtInEnglish,
			Cie:               source.Cie,
			// The seminar a cohort was last year is the best guess for what it is this year;
			// a topic that changed is one edit, a topic that vanished is fourteen.
			Topic:     source.Topic,
			CreatedBy: nullUUID(nonNilUUID(by)),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			// DO NOTHING returns no row: this cohort is already declared in the target, and
			// what is there stays exactly as it is.
			counts.Skipped++
			continue
		}
		if err != nil {
			return counts, fmt.Errorf("cannot declare the copied instance: %w", err)
		}
		counts.Created++

		if c, ok := carryBySource[source.ID]; ok {
			pending = append(pending, pendingCoverage{
				guestID:    newID,
				hostModule: source.ModuleID,
				hostProgID: c.HostProgrammeID,
				hostTrack:  c.HostTrack,
			})
		}

		// Sharing is copied, unlike in a duplication: the sibling cohort is being copied too, so
		// the lecture that was held once for both is held once for both again. Dropping the flag
		// here would silently double the faculty's hours in every semester anybody copies.
		//
		// Divisions and another faculty's parts come along as well: an arrangement with another
		// faculty is usually next year's arrangement too, and dropping it would put its hours
		// back into this faculty's load without anybody having decided that.
		created, err := copyParts(ctx, q, partsByInstance[source.ID], newID, false)
		if err != nil {
			return counts, fmt.Errorf("cannot copy a part: %w", err)
		}
		counts.PartsCreated += created
	}

	// Ask again, never agree again.
	//
	// The other programme's lead agreed to hold the event in *that* semester. Carrying the
	// agreement forward would be a decision nobody made, in a programme this caller may not even
	// write. Carrying the request forward is what keeps the copied cohort from silently growing
	// its own teaching back.
	for _, p := range pending {
		hostID, err := q.InstanceByIdentity(ctx, InstanceByIdentityParams{
			SemesterID:  to.ID,
			ModuleID:    p.hostModule,
			ProgrammeID: p.hostProgID,
			Track:       p.hostTrack,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			// The other programme has not declared this module in the target semester yet. The
			// copied cohort arrives with no parts, and the count is what says so — building parts
			// from the module's split instead would invent teaching at the press of a button.
			counts.CoverageNotPossible++
			continue
		}
		if err != nil {
			return counts, fmt.Errorf("cannot find the covering instance in %s: %w", to.Code, err)
		}

		rows, err := q.RequestInstanceCoverage(ctx, RequestInstanceCoverageParams{
			ID:          p.guestID,
			HostID:      hostID,
			RequestedBy: nullUUID(nonNilUUID(by)),
		})
		if err != nil {
			return counts, fmt.Errorf("cannot carry the coverage forward: %w", err)
		}
		if rows > 0 {
			counts.CoverageRequested++
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return counts, fmt.Errorf("cannot commit: %w", err)
	}
	return counts, nil
}

// numericFloatOrNil reads a nullable numeric(4,2) as a pointer.
//
// The hours of a part are nullable because an instance can be declared before the detail is
// settled — the demand deadline comes before the detail does. Zero would be a different
// statement: a part that credits nobody with anything.
func numericFloatOrNil(n pgtype.Numeric) *float64 {
	if !n.Valid {
		return nil
	}
	f := numericFloat(n)
	return &f
}

// numericOrNull is the way back.
func numericOrNull(f *float64) (pgtype.Numeric, error) {
	if f == nil {
		return pgtype.Numeric{}, nil
	}
	return numericFrom(*f)
}

func int32OrNil(v *int) *int32 {
	if v == nil {
		return nil
	}
	n := int32(*v)
	return &n
}

// isUniqueViolation reports whether err is PostgreSQL refusing a duplicate key.
//
// By SQLSTATE rather than by message: the message is localised and reworded between server
// versions, and the class of error is what the caller branches on.
func isUniqueViolation(err error) bool { return hasSQLState(err, "23505") }

// isForeignKeyViolation reports whether err is PostgreSQL refusing to orphan a row.
//
// This is what "something still hangs off it" is, and it is deliberately the only thing the
// caller learns. Reading which constraint fired would name the table — and the first table to
// point at an instance part is the wish table.
//
// **Two SQLSTATEs, and the second one is not a belt-and-braces addition.** 23503 is a plain
// foreign key violation; 23001 is what an ON DELETE RESTRICT raises, and RESTRICT is what the
// wish table uses. Until the wish table existed nothing in this schema was RESTRICT on a path
// anything deleted, so 23503 alone was enough and looked complete — and the day it stopped being
// enough, the failure was not a missed refusal but a leak: the driver's message names the
// constraint, `wish_instance_part_id_fkey`, so a programme lead trying to withdraw an instance
// would have been told in so many words that somebody wants it.
//
// The distinction is worth knowing rather than papering over: NO ACTION defers the check to the
// end of the statement and raises 23503, RESTRICT checks immediately and raises 23001. Both mean
// the same thing to a caller here.
func isForeignKeyViolation(err error) bool {
	return hasSQLState(err, "23503") || hasSQLState(err, "23001")
}

func hasSQLState(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}

// PlanDemand reconciles a whole screen of demand against what is stored.
//
// # Why one call and not forty
//
// The faculty plans a semester as a table — one row per module, a tick, the cohorts, the groups.
// Written as one mutation per tick it would be forty round trips that can half-succeed, and the
// person watching would have no way to tell a refusal from a lost connection. Written as this, it
// is one transaction and one report.
//
// # What it may touch
//
// Only the modules named in entries. That is the property the interface's filters rest on: a
// screen showing the compulsory modules of the third semester must not withdraw the electives it
// is not showing, and the safest way to guarantee that is to make silence mean nothing at all.
//
// # The order, and why it is that order
//
// Withdraw, then rename, then create. Cohort letters are unique per module, so a rename into a
// letter that is about to be freed only works if the freeing happens first — and turning two
// cohorts back into one is exactly that case.
//
// Renaming rather than withdraw-and-create is what keeps IF1 from losing its parts when a second
// cohort appears beside it: the existing instance becomes IF1A, which is the same act
// duplicateCourseInstance performs with sourceTrack.
//
// # dryRun
//
// Runs everything and rolls back. The preview is therefore not a second computation that might
// disagree with the write — it is the write, not kept.
func (d *Demand) PlanDemand(ctx context.Context, semesterCode string, programmeID uuid.UUID,
	entries []domain.DemandEntry, by uuid.UUID, dryRun bool,
) (domain.DemandPlan, error) {
	plan := domain.DemandPlan{DryRun: dryRun}

	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return plan, fmt.Errorf("cannot begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := New(tx)

	// The semester row, inside this transaction and therefore inside the rollback.
	//
	// Nobody creates a semester; the row is the record of the first decision taken about one, and
	// declaring demand is such a decision. A dry run is not — and because the row is written here
	// rather than beforehand, "a preview records nothing" is a property of the transaction rather
	// than a case somebody has to remember. It was a case, it was forgotten, and planning the
	// first thing in an untouched semester failed on a foreign key.
	semester, err := q.EnsureSemester(ctx, semesterCode)
	if err != nil {
		return plan, fmt.Errorf("cannot record the semester: %w", err)
	}
	semesterID := semester.ID

	held, err := heldInstances(ctx, q, semesterID, programmeID)
	if err != nil {
		return plan, err
	}

	for _, entry := range entries {
		if err := d.planModule(ctx, tx, &plan, planContext{
			semesterID:  semesterID,
			programmeID: programmeID,
			by:          by,
			entry:       entry,
			held:        held[entry.ModuleID],
		}); err != nil {
			return plan, err
		}
	}

	if dryRun {
		// Deliberately not committed. Everything above ran against the real rows, including the
		// refusals a foreign key produced, and none of it is kept.
		if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			return plan, fmt.Errorf("cannot roll back the dry run: %w", err)
		}
	} else if err := tx.Commit(ctx); err != nil {
		return plan, fmt.Errorf("cannot commit: %w", err)
	}

	if err := d.nameModules(ctx, &plan); err != nil {
		return plan, err
	}
	return plan, nil
}

// planContext is what planning one row needs, gathered so the signature stays readable.
type planContext struct {
	semesterID  uuid.UUID
	programmeID uuid.UUID
	by          uuid.UUID
	entry       domain.DemandEntry
	held        []*heldInstance
}

// heldInstance is one cohort as it currently stands, with the parts it holds.
type heldInstance struct {
	id                uuid.UUID
	moduleID          uuid.UUID
	track             string
	programmeSemester *int32
	note              string
	delivery          string
	taughtInEnglish   *bool
	cie               bool
	parts             []InstancePartsForRow
	// covered is accepted coverage: another programme holds this cohort's teaching, so it has no
	// parts and must not be given any. Planning reports it rather than skipping it silently —
	// somebody moved a stepper and is owed an answer.
	covered bool
}

// heldInstances reads the demand of one programme in one semester, with the parts.
func heldInstances(ctx context.Context, q *Queries, semesterID, programmeID uuid.UUID) (map[uuid.UUID][]*heldInstance, error) {
	byModule := map[uuid.UUID][]*heldInstance{}

	rows, err := q.CourseInstancesOfProgramme(ctx, CourseInstancesOfProgrammeParams{
		SemesterID:  semesterID,
		ProgrammeID: programmeID,
	})
	if err != nil {
		return nil, fmt.Errorf("cannot read the demand: %w", err)
	}
	if len(rows) == 0 {
		return byModule, nil
	}

	ids := make([]uuid.UUID, 0, len(rows))
	byID := make(map[uuid.UUID]*heldInstance, len(rows))
	for _, row := range rows {
		instance := &heldInstance{
			id:                row.ID,
			moduleID:          row.ModuleID,
			track:             row.Track,
			programmeSemester: row.ProgrammeSemester,
			note:              row.Note,
			delivery:          row.Delivery,
			taughtInEnglish:   row.TaughtInEnglish,
			cie:               row.Cie,
			covered:           row.IsCovered,
		}
		ids = append(ids, row.ID)
		byID[row.ID] = instance
		byModule[row.ModuleID] = append(byModule[row.ModuleID], instance)
	}

	parts, err := q.InstancePartsFor(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("cannot read the instance parts: %w", err)
	}
	for _, p := range parts {
		if instance, ok := byID[p.CourseInstanceID]; ok {
			instance.parts = append(instance.parts, p)
		}
	}
	return byModule, nil
}

// planModule reconciles the cohorts of one module.
func (d *Demand) planModule(ctx context.Context, tx pgx.Tx, plan *domain.DemandPlan, pc planContext) error {
	q := New(tx)
	wanted := pc.entry.Tracks
	held := pc.held

	// Nothing wanted and nothing held is the ordinary case for most rows of a screen: a module
	// that is not offered and was not offered. It costs no query at all.
	if len(wanted) == 0 && len(held) == 0 {
		return nil
	}

	matched := make([]*heldInstance, len(wanted))
	used := make([]bool, len(held))

	// The letters that already agree keep their instance, whatever else moves.
	for w := range wanted {
		for h := range held {
			if !used[h] && held[h].track == wanted[w].Track {
				matched[w] = held[h]
				used[h] = true
				break
			}
		}
	}

	var spare []*heldInstance
	for h := range held {
		if !used[h] {
			spare = append(spare, held[h])
		}
	}
	var open []int
	for w := range wanted {
		if matched[w] == nil {
			open = append(open, w)
		}
	}

	// Withdraw first: a rename into a letter that is being freed needs the freeing to have
	// happened. From the back, so that the cohort that goes is the last one.
	for len(spare) > len(open) {
		last := spare[len(spare)-1]
		spare = spare[:len(spare)-1]

		if err := d.withdraw(ctx, tx, plan, last, len(wanted) > 0); err != nil {
			return err
		}
	}

	// Then rename what is left over into the letters still open.
	for i, instance := range spare {
		w := open[i]
		if err := d.rename(ctx, tx, plan, instance, wanted[w]); err != nil {
			return err
		}
		matched[w] = instance
	}
	open = open[len(spare):]

	// Then create what is still missing.
	shared := sharedKindsOf(held)
	fresh := map[uuid.UUID]bool{}
	for _, w := range open {
		instance, err := d.createForPlan(ctx, tx, plan, pc, wanted[w], shared)
		if err != nil {
			return err
		}
		if instance != nil {
			fresh[instance.id] = true
		}
		matched[w] = instance
	}

	// And finally bring every cohort to the number of groups the row asks for.
	for w, instance := range matched {
		if instance == nil {
			continue
		}
		// A cohort that was just declared reports itself as created and nothing else. Its groups
		// are part of declaring it, and "2 declared, 1 changed" for two acts is a summary that
		// makes somebody count on their fingers.
		if err := d.adjustGroups(ctx, tx, plan, instance, wanted[w].Groups, fresh[instance.id]); err != nil {
			return err
		}
		if err := d.applyProgrammeSemester(ctx, q, instance, pc.entry.ProgrammeSemester); err != nil {
			return err
		}
		if err := d.applyNote(ctx, q, instance, pc.entry.Note); err != nil {
			return err
		}
	}
	return nil
}

// sharedKindsOf collects the kinds a sibling cohort already holds for everybody.
//
// A new cohort of a module whose lecture is already shared must not get a lecture of its own: the
// shared one serves it, and a second would be the same teaching counted twice. Same rule as
// duplicateCourseInstance's, applied where a cohort is created rather than copied.
func sharedKindsOf(held []*heldInstance) map[string]bool {
	kinds := map[string]bool{}
	for _, instance := range held {
		// A covered cohort holds nothing of its own, so it shares nothing with anybody. Reading
		// its (empty) parts would be harmless today and wrong the moment anything else changes.
		if instance.covered {
			continue
		}
		for _, p := range instance.parts {
			if p.ServesSiblingTracks {
				kinds[p.Kind] = true
			}
		}
	}
	return kinds
}

// withdraw takes one cohort away inside a savepoint.
//
// keepOffered says the row still wants the module, with fewer cohorts: a merge rather than taking
// the offer away. Only then do the cohort's wishes move to a sibling — when the whole module goes,
// the foreign key refuses the last cohort as it always did.
func (d *Demand) withdraw(ctx context.Context, tx pgx.Tx, plan *domain.DemandPlan, instance *heldInstance,
	keepOffered bool,
) error {
	// A savepoint — pgx opens one for a transaction begun inside a transaction — because a
	// foreign key that refuses this one withdrawal would otherwise abort the whole transaction,
	// and the rest of the screen with it. One cohort that is spoken for must cost exactly that
	// cohort.
	sub, err := tx.Begin(ctx)
	if err != nil {
		return fmt.Errorf("cannot open a savepoint: %w", err)
	}

	// The same handover the single withdrawal does, inside the savepoint this one already opens —
	// so that a cohort somebody has registered interest in costs its own row and not the screen.
	promoted, err := promoteCoverageSuccessor(ctx, New(sub), instance.id)
	if err != nil {
		_ = sub.Rollback(ctx)
		return err
	}

	if keepOffered {
		if err := handOverWishes(ctx, New(sub), instance.id); err != nil {
			_ = sub.Rollback(ctx)
			return err
		}
	}

	rows, err := New(sub).DeleteCourseInstance(ctx, instance.id)
	switch {
	case isForeignKeyViolation(err):
		_ = sub.Rollback(ctx)
		plan.Refused = append(plan.Refused, domain.DemandRefusal{
			ModuleID: instance.moduleID,
			Track:    instance.track,
			Code:     "INSTANCE_IN_USE",
			Reason:   domain.ErrInstanceInUse.Error(),
		})
		return nil
	case err != nil:
		_ = sub.Rollback(ctx)
		return fmt.Errorf("cannot withdraw the instance: %w", err)
	case rows == 0:
		_ = sub.Rollback(ctx)
		return nil
	}

	if err := sub.Commit(ctx); err != nil {
		return fmt.Errorf("cannot release the savepoint: %w", err)
	}
	plan.Withdrawn = append(plan.Withdrawn, domain.DemandChange{
		ModuleID: instance.moduleID,
		Track:    instance.track,
	})
	if promoted != nil {
		// Said out loud, because it happened in a programme this caller does not lead. A save that
		// hands another programme's cohort four hours of teaching and whoever holds it, silently,
		// is a save nobody can check.
		plan.Promoted = append(plan.Promoted, domain.DemandChange{
			ModuleID: instance.moduleID,
			Track:    promoted.Track,
			Programme: &domain.Programme{
				ID:    promoted.Programme.ID,
				Code:  promoted.Programme.Code,
				Title: promoted.Programme.Title,
			},
		})
	}
	return nil
}

func (d *Demand) rename(ctx context.Context, tx pgx.Tx, plan *domain.DemandPlan,
	instance *heldInstance, want domain.DemandTrack,
) error {
	if instance.track == want.Track {
		return nil
	}

	sub, err := tx.Begin(ctx)
	if err != nil {
		return fmt.Errorf("cannot open a savepoint: %w", err)
	}

	err = New(sub).UpdateCourseInstance(ctx, UpdateCourseInstanceParams{
		ID:                instance.id,
		Track:             want.Track,
		ProgrammeSemester: instance.programmeSemester,
	})
	if err != nil {
		_ = sub.Rollback(ctx)
		if isUniqueViolation(err) {
			plan.Refused = append(plan.Refused, domain.DemandRefusal{
				ModuleID: instance.moduleID,
				Track:    want.Track,
				Code:     "TRACK_TAKEN",
				Reason:   domain.ErrTrackTaken.Error(),
			})
			return nil
		}
		return fmt.Errorf("cannot rename the cohort: %w", err)
	}
	if err := sub.Commit(ctx); err != nil {
		return fmt.Errorf("cannot release the savepoint: %w", err)
	}

	before := instance.track
	plan.Changed = append(plan.Changed, domain.DemandChange{
		ModuleID:    instance.moduleID,
		Track:       want.Track,
		TrackBefore: &before,
	})
	instance.track = want.Track
	return nil
}

func (d *Demand) createForPlan(ctx context.Context, tx pgx.Tx, plan *domain.DemandPlan,
	pc planContext, want domain.DemandTrack, shared map[string]bool,
) (*heldInstance, error) {
	q := New(tx)

	components, err := statedComponents(ctx, q, pc.entry.ModuleID)
	if err != nil {
		// Both of these cost the row and not the screen. A module that vanished between the load
		// and the save is somebody else's deletion, not this caller's mistake — and it must not
		// take the other fourteen rows down with it.
		for _, known := range []struct {
			sentinel error
			code     string
		}{
			{domain.ErrModuleNotDecomposed, "MODULE_NOT_DECOMPOSED"},
			{domain.ErrModuleNotFound, "MODULE_NOT_FOUND"},
		} {
			if errors.Is(err, known.sentinel) {
				plan.Refused = append(plan.Refused, domain.DemandRefusal{
					ModuleID: pc.entry.ModuleID,
					Track:    want.Track,
					Code:     known.code,
					Reason:   known.sentinel.Error(),
				})
				return nil, nil
			}
		}
		return nil, err
	}

	programmeSemester := int32OrNil(pc.entry.ProgrammeSemester)
	if programmeSemester == nil {
		seeded, err := q.SeedProgrammeSemester(ctx, SeedProgrammeSemesterParams{
			ModuleID:    pc.entry.ModuleID,
			ProgrammeID: pc.programmeID,
		})
		if err != nil {
			return nil, fmt.Errorf("cannot read the cohort year from the regulations: %w", err)
		}
		if seeded > 0 {
			programmeSemester = &seeded
		}
	}

	// The row's note, so a cohort added to a row does not arrive as the one without one: what
	// the entry states, else what a sibling cohort already carries.
	note := ""
	if pc.entry.Note != nil {
		note = *pc.entry.Note
	} else if len(pc.held) > 0 {
		note = pc.held[0].note
	}
	// How the row is taught, for the same reason: a cohort added to a row of block weeks is
	// not the one that quietly runs weekly. Taken from a sibling, since a plan states neither.
	delivery, taughtInEnglish, cie := string(domain.DeliveryWeekly), (*bool)(nil), false
	if len(pc.held) > 0 {
		delivery, taughtInEnglish, cie = pc.held[0].delivery, pc.held[0].taughtInEnglish, pc.held[0].cie
	}

	id, err := q.InsertCourseInstance(ctx, InsertCourseInstanceParams{
		SemesterID:        pc.semesterID,
		ModuleID:          pc.entry.ModuleID,
		ProgrammeID:       pc.programmeID,
		Track:             want.Track,
		ProgrammeSemester: programmeSemester,
		Note:              note,
		Delivery:          delivery,
		TaughtInEnglish:   taughtInEnglish,
		Cie:               cie,
		CreatedBy:         nullUUID(nonNilUUID(pc.by)),
	})
	if isUniqueViolation(err) {
		plan.Refused = append(plan.Refused, domain.DemandRefusal{
			ModuleID: pc.entry.ModuleID,
			Track:    want.Track,
			Code:     "TRACK_TAKEN",
			Reason:   domain.ErrTrackTaken.Error(),
		})
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot declare the instance: %w", err)
	}

	instance := &heldInstance{
		id:                id,
		moduleID:          pc.entry.ModuleID,
		track:             want.Track,
		programmeSemester: programmeSemester,
		note:              note,
		delivery:          delivery,
		taughtInEnglish:   taughtInEnglish,
		cie:               cie,
	}

	// Held with another programme's event where there is one — the case the whole automatic half
	// of this exists for. It holds no parts then, so the loop below is skipped rather than run and
	// undone, and adjustGroups is told not to ask about groups it has no say over.
	//
	// In a savepoint: the host is read under FOR KEY SHARE, and a host that stops qualifying
	// between the read and the write yields no row rather than the next candidate. That costs this
	// cohort its coupling, never the screen.
	sub, err := tx.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot open a savepoint: %w", err)
	}
	coupled, err := coupleIfHostExists(ctx, New(sub), id,
		pc.semesterID, pc.entry.ModuleID, pc.programmeID, pc.by)
	switch {
	case err != nil:
		_ = sub.Rollback(ctx)
		return nil, err
	case coupled:
		if err := sub.Commit(ctx); err != nil {
			return nil, fmt.Errorf("cannot release the savepoint: %w", err)
		}
		instance.covered = true
		plan.Coupled = append(plan.Coupled, domain.DemandChange{
			ModuleID: pc.entry.ModuleID,
			Track:    want.Track,
		})
		return instance, nil
	default:
		if err := sub.Commit(ctx); err != nil {
			return nil, fmt.Errorf("cannot release the savepoint: %w", err)
		}
	}

	// A kind a sibling already holds for everybody is left out.
	parts, err := partsFromSplit(ctx, q, id, components, shared)
	if err != nil {
		return nil, fmt.Errorf("cannot create a part of the instance: %w", err)
	}
	instance.parts = append(instance.parts, parts...)

	plan.Created = append(plan.Created, domain.DemandChange{
		ModuleID: pc.entry.ModuleID,
		Track:    want.Track,
	})
	return instance, nil
}

// adjustGroups brings one cohort to the number of parallel groups the row asks for.
//
// Groups are parts of the practical kind — the laboratory of a lecture-plus-laboratory module,
// the exercise of a lecture-plus-exercise one. A module that is nothing but a lecture has no such
// kind, and the figure is then without effect: parallel lectures are not what anybody means by
// "groups" here.
func (d *Demand) adjustGroups(ctx context.Context, tx pgx.Tx, plan *domain.DemandPlan,
	instance *heldInstance, want int, justCreated bool,
) error {
	q := New(tx)

	// A covered cohort holds no teaching: another programme does. Its group count is that
	// programme's to set, and inserting groups here would be the joint event's laboratories
	// counted twice — the plausible-looking wrong number this model is arranged to prevent.
	//
	// Reported rather than skipped. Somebody moved a stepper and is owed an answer; a silent
	// no-op reads as a save that did not stick.
	//
	// Except where the cohort was declared coupled by this very save. Then nothing was refused:
	// the row is reported as coupled where it was created, and saying so twice — once as made,
	// once as impossible — would be one save telling somebody both.
	if instance.covered {
		if justCreated {
			return nil
		}
		plan.Refused = append(plan.Refused, domain.DemandRefusal{
			ModuleID: instance.moduleID,
			Track:    instance.track,
			Code:     "INSTANCE_COVERED",
			Reason:   domain.ErrInstanceCovered.Error(),
		})
		return nil
	}

	components, err := statedComponents(ctx, q, instance.moduleID)
	if err != nil {
		// A module with nothing to make parts from was already reported where it was created;
		// an existing cohort of one simply keeps what it has.
		if errors.Is(err, domain.ErrModuleNotDecomposed) {
			return nil
		}
		return err
	}

	kind, hours, ok := domain.PracticalKindOf(components)
	if !ok {
		return nil
	}

	// A piece is not a group: a laboratory cut in two between us and another faculty is still
	// one laboratory group, and counting the piece would add a group on the next save.
	pieced := map[uuid.UUID]bool{}
	var groups []InstancePartsForRow
	for _, p := range instance.parts {
		if p.DividedFromID.Valid {
			pieced[p.DividedFromID.UUID] = true
			continue
		}
		if p.Kind == string(kind) && !p.ServesSiblingTracks {
			groups = append(groups, p)
		}
	}
	before := len(groups)
	if before == want {
		return nil
	}

	for len(groups) < want {
		numeric, err := numericFrom(hours)
		if err != nil {
			return err
		}
		position, err := q.NextInstancePartPosition(ctx, instance.id)
		if err != nil {
			return fmt.Errorf("cannot find the end of the list: %w", err)
		}
		partID, err := q.InsertInstancePart(ctx, InsertInstancePartParams{
			CourseInstanceID:    instance.id,
			Kind:                string(kind),
			Position:            position,
			TeachingHours:       numeric,
			ServesSiblingTracks: false,
		})
		if err != nil {
			return fmt.Errorf("cannot add a group: %w", err)
		}
		added := InstancePartsForRow{
			ID: partID, CourseInstanceID: instance.id, Kind: string(kind),
			Position: position, TeachingHours: numeric,
		}
		groups = append(groups, added)
		instance.parts = append(instance.parts, added)
	}

	for len(groups) > want {
		last := groups[len(groups)-1]

		// A group that was divided carries an arrangement — with a colleague, or with another
		// faculty — that removing it would drop without anybody deciding to. Reported, like a
		// staffed one.
		if pieced[last.ID] {
			plan.Refused = append(plan.Refused, domain.DemandRefusal{
				ModuleID: instance.moduleID,
				Track:    instance.track,
				Code:     "PART_DIVIDED",
				Reason:   domain.ErrPartDivided.Error(),
			})
			break
		}

		// Savepoint again, and for the same reason: a group somebody is already assigned to costs
		// that group and not the screen.
		//
		// It used to say "somebody has already registered interest in", which stopped being true
		// with migration 16 — a wish points at the instance, so nothing pointed at a part at all
		// and this branch was unreachable. Migration 17 makes it live again, for assignments.
		sub, err := tx.Begin(ctx)
		if err != nil {
			return fmt.Errorf("cannot open a savepoint: %w", err)
		}
		_, err = New(sub).DeleteInstancePart(ctx, last.ID)
		if err != nil {
			_ = sub.Rollback(ctx)
			if isForeignKeyViolation(err) {
				plan.Refused = append(plan.Refused, domain.DemandRefusal{
					ModuleID: instance.moduleID,
					Track:    instance.track,
					Code:     "PART_ASSIGNED",
					Reason:   domain.ErrPartAssigned.Error(),
				})
				break
			}
			return fmt.Errorf("cannot remove a group: %w", err)
		}
		if err := sub.Commit(ctx); err != nil {
			return fmt.Errorf("cannot release the savepoint: %w", err)
		}
		groups = groups[:len(groups)-1]
	}

	if after := len(groups); after != before && !justCreated {
		from, to := before, after
		plan.Changed = append(plan.Changed, domain.DemandChange{
			ModuleID:     instance.moduleID,
			Track:        instance.track,
			GroupsBefore: &from,
			GroupsAfter:  &to,
		})
	}
	return nil
}

// applyProgrammeSemester writes the cohort year where the row states one.
//
// Not reported as a change of its own: it is a property of the row the person is looking at
// rather than something that happens to the plan, and a summary line about it would be noise in
// front of the two that matter.
func (d *Demand) applyProgrammeSemester(ctx context.Context, q *Queries, instance *heldInstance, year *int) error {
	if year == nil {
		return nil
	}
	wanted := int32OrNil(year)
	if instance.programmeSemester != nil && *instance.programmeSemester == *wanted {
		return nil
	}

	if err := q.UpdateCourseInstance(ctx, UpdateCourseInstanceParams{
		ID:                instance.id,
		Track:             instance.track,
		ProgrammeSemester: wanted,
	}); err != nil {
		return fmt.Errorf("cannot set the cohort year: %w", err)
	}
	instance.programmeSemester = wanted
	return nil
}

// applyNote writes the note where the row states one, the same way as the cohort year: nil
// leaves what is there, and the empty string clears it.
//
// Not reported as a change either, and for the same reason — a sentence beside the row is not
// something that happens to the plan.
func (d *Demand) applyNote(ctx context.Context, q *Queries, instance *heldInstance, note *string) error {
	if note == nil || instance.note == *note {
		return nil
	}
	if err := q.UpdateCourseInstanceNote(ctx, UpdateCourseInstanceNoteParams{
		ID:   instance.id,
		Note: *note,
	}); err != nil {
		return fmt.Errorf("cannot set the note: %w", err)
	}
	instance.note = *note
	return nil
}

// nameModules fills in the module names the report shows.
//
// After the transaction and in one statement: the report is read by a person, and a list of uuids
// is not something anybody can check against what they just did.
func (d *Demand) nameModules(ctx context.Context, plan *domain.DemandPlan) error {
	ids := make([]uuid.UUID, 0,
		len(plan.Created)+len(plan.Withdrawn)+len(plan.Changed)+
			len(plan.Coupled)+len(plan.Promoted)+len(plan.Refused))
	collect := func(id uuid.UUID) {
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	for _, c := range plan.Created {
		collect(c.ModuleID)
	}
	for _, c := range plan.Withdrawn {
		collect(c.ModuleID)
	}
	for _, c := range plan.Changed {
		collect(c.ModuleID)
	}
	for _, c := range plan.Coupled {
		collect(c.ModuleID)
	}
	for _, c := range plan.Promoted {
		collect(c.ModuleID)
	}
	for _, r := range plan.Refused {
		collect(r.ModuleID)
	}
	if len(ids) == 0 {
		return nil
	}

	modules, err := d.modules.ModulesByID(ctx, ids)
	if err != nil {
		return err
	}
	names := make(map[uuid.UUID]string, len(modules))
	for _, m := range modules {
		names[m.ID] = m.Name
	}

	for i := range plan.Created {
		plan.Created[i].ModuleName = names[plan.Created[i].ModuleID]
	}
	for i := range plan.Withdrawn {
		plan.Withdrawn[i].ModuleName = names[plan.Withdrawn[i].ModuleID]
	}
	for i := range plan.Changed {
		plan.Changed[i].ModuleName = names[plan.Changed[i].ModuleID]
	}
	for i := range plan.Coupled {
		plan.Coupled[i].ModuleName = names[plan.Coupled[i].ModuleID]
	}
	for i := range plan.Promoted {
		plan.Promoted[i].ModuleName = names[plan.Promoted[i].ModuleID]
	}
	for i := range plan.Refused {
		plan.Refused[i].ModuleName = names[plan.Refused[i].ModuleID]
	}
	return nil
}
