package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/obcode/tallox.go/internal/domain"
)

// Catalogue projects the cached examination-office payloads into the domain tables.
//
// It is the only thing that writes programme, spo, module and module_offering, and it never
// writes module_component — the split of a module into teachable units is the faculty's
// knowledge, not the source's, and an import that could overwrite it would make it unsafe to
// enter.
type Catalogue struct {
	pool *pgxpool.Pool
}

// NewCatalogue binds the projection to a pool.
//
// A pool rather than a DBTX, unlike most repositories here, because the projection manages its
// own transaction — and deliberately keeps its bookkeeping outside it. See Project.
func NewCatalogue(pool *pgxpool.Pool) *Catalogue { return &Catalogue{pool: pool} }

var _ domain.CatalogueStore = (*Catalogue)(nil)

// Project rebuilds the catalogue from whatever the cache currently holds.
//
// runID names the import run that triggered this, or is nil for a projection somebody asked for
// on its own — which is a thing to want, because the projection rules change and a changed rule
// has to be applicable to data already held without reaching into another institution's system.
//
// # Why the bookkeeping sits outside the transaction and the data inside it
//
// Two requirements that pull in opposite directions. A half-projected catalogue must never be
// observable, which wants one transaction around everything. And a projection that died must
// leave a trace, which a rollback would take with it — the failure this whole import guards
// against is not a wrong answer but a job that quietly stopped weeks ago while everything looks
// healthy.
//
// So the run row is written on the pool before the work and updated on the pool after it, while
// the statements that move data run in a single transaction between them. A crash leaves a row
// stuck in RUNNING, which is visible, and a catalogue untouched, which is correct.
func (c *Catalogue) Project(ctx context.Context, runID *uuid.UUID) (domain.CatalogueProjection, error) {
	started, err := New(c.pool).StartCatalogueProjection(ctx, nullUUID(runID))
	if err != nil {
		return domain.CatalogueProjection{}, fmt.Errorf("cannot start the projection: %w", err)
	}

	counts, notes, projectErr := c.project(ctx, started.ID)

	status := domain.ProjectionSucceeded
	var failure *string
	if projectErr != nil {
		status = domain.ProjectionFailed
		// The operator's sentence, not the caller's: this ends up on the import page, where
		// what is needed is what went wrong rather than a stack of wrapped verbs.
		message := projectErr.Error()
		failure = &message
	}

	finished, err := New(c.pool).FinishCatalogueProjection(ctx, FinishCatalogueProjectionParams{
		ID:                started.ID,
		Status:            string(status),
		ProgrammesWritten: int32(counts.Programmes),
		ModulesWritten:    int32(counts.Modules),
		OfferingsWritten:  int32(counts.Offerings),
		OfferingsRemoved:  int32(counts.OfferingsRemoved),
		Error:             failure,
	})
	if err != nil {
		return domain.CatalogueProjection{}, fmt.Errorf("cannot finish the projection: %w", err)
	}

	result := projectionFrom(finished)
	result.Notes = notes
	if projectErr != nil {
		return result, projectErr
	}
	return result, nil
}

// counts is what the four writing statements reported.
type counts struct {
	Programmes       int
	Modules          int
	Offerings        int
	OfferingsRemoved int
}

// project does the work in one transaction and returns what it did.
//
// The order is a dependency order and not a preference: an offering needs its module and its
// regulations, a set of regulations needs its programme, and a module needs its home programme.
func (c *Catalogue) project(ctx context.Context, projectionID uuid.UUID) (counts, []domain.CatalogueProjectionNote, error) {
	var done counts

	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return done, nil, fmt.Errorf("cannot begin: %w", err)
	}
	// Rollback after a successful commit is a no-op, so this needs no branching.
	defer func() { _ = tx.Rollback(ctx) }()

	q := New(tx)

	programmes, err := q.ProjectProgrammes(ctx)
	if err != nil {
		return done, nil, fmt.Errorf("cannot project the programmes: %w", err)
	}
	done.Programmes = int(programmes)

	if _, err := q.ProjectSpos(ctx); err != nil {
		return done, nil, fmt.Errorf("cannot project the regulations: %w", err)
	}

	frequencyPhrases, frequencyValues := domain.FrequencyPhraseMapping()
	courseTypePhrases, courseTypeValues := domain.CourseTypePhraseMapping()
	modules, err := q.ProjectModules(ctx, ProjectModulesParams{
		FrequencyPhrases:  frequencyPhrases,
		FrequencyValues:   frequencyValues,
		CourseTypePhrases: courseTypePhrases,
		CourseTypeValues:  courseTypeValues,
	})
	if err != nil {
		return done, nil, fmt.Errorf("cannot project the modules: %w", err)
	}
	done.Modules = int(modules)

	offerings, err := q.ProjectModuleOfferings(ctx)
	if err != nil {
		return done, nil, fmt.Errorf("cannot project the offerings: %w", err)
	}
	done.Offerings = int(offerings)

	removed, err := q.DeleteStaleModuleOfferings(ctx)
	if err != nil {
		return done, nil, fmt.Errorf("cannot remove the offerings the source dropped: %w", err)
	}
	done.OfferingsRemoved = int(removed)

	// The report is gathered inside the same transaction, against the same snapshot — so it
	// describes the projection that happened rather than the cache as it looked a moment later.
	notes, err := gatherNotes(ctx, q)
	if err != nil {
		return done, nil, err
	}
	for _, note := range notes {
		if err := q.RecordCatalogueProjectionNote(ctx, RecordCatalogueProjectionNoteParams{
			ProjectionID: projectionID,
			Code:         string(note.Code),
			Count:        int32(note.Count),
			Sample:       note.Sample,
		}); err != nil {
			return done, nil, fmt.Errorf("cannot record the note %s: %w", note.Code, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return done, nil, fmt.Errorf("cannot commit: %w", err)
	}
	return done, notes, nil
}

// noteRow is the shape all nine counting queries produce.
//
// sqlc emits a distinct type per query because each one selects from somewhere else, and every
// one of them is structurally this. Converting rather than copying field by field is what makes
// that identity a compile-time claim: the day a counting query grows a third column, the
// conversion stops compiling instead of quietly dropping it.
type noteRow struct {
	Count  int32
	Sample []string
}

func gatherNotes(ctx context.Context, q *Queries) ([]domain.CatalogueProjectionNote, error) {
	sources := []struct {
		code domain.ProjectionNoteCode
		read func() (noteRow, error)
	}{
		{domain.NoteModuleWithoutHomeProgramme, func() (noteRow, error) {
			r, err := q.CountModulesWithoutHomeProgramme(ctx)
			return noteRow(r), err
		}},
		{domain.NoteProgrammeWithoutRegulations, func() (noteRow, error) {
			r, err := q.CountProgrammesWithoutRegulations(ctx)
			return noteRow(r), err
		}},
		{domain.NoteModuleWithoutName, func() (noteRow, error) {
			r, err := q.CountModulesWithoutName(ctx)
			return noteRow(r), err
		}},
		{domain.NoteModuleInactive, func() (noteRow, error) {
			r, err := q.CountInactiveModules(ctx)
			return noteRow(r), err
		}},
		{domain.NoteAssociationWithUnknownRegulations, func() (noteRow, error) {
			r, err := q.CountAssociationsWithUnknownRegulations(ctx)
			return noteRow(r), err
		}},
		{domain.NoteFrequencyUnmapped, func() (noteRow, error) {
			r, err := q.CountUnmappedFrequencies(ctx)
			return noteRow(r), err
		}},
		{domain.NoteCourseTypeUnmapped, func() (noteRow, error) {
			// The phrase that legitimately maps to the default is passed in so it can be
			// excluded here rather than restated in SQL: the source really does write "je nach
			// Fach" for 23 modules, and reporting those every night would train people to
			// ignore the line.
			r, err := q.CountUnmappedCourseTypes(ctx, domain.DependsOnSubjectPhrase)
			return noteRow(r), err
		}},
		{domain.NoteMinSemesterConflict, func() (noteRow, error) {
			r, err := q.CountMinSemesterConflicts(ctx)
			return noteRow(r), err
		}},
		{domain.NoteDutyConflict, func() (noteRow, error) {
			r, err := q.CountDutyConflicts(ctx)
			return noteRow(r), err
		}},
	}

	notes := make([]domain.CatalogueProjectionNote, 0, len(sources)+1)
	for _, source := range sources {
		row, err := source.read()
		if err != nil {
			return nil, fmt.Errorf("cannot count %s: %w", source.code, err)
		}
		// A note saying "nothing happened" is noise in a report whose value is that every line
		// means something.
		if row.Count == 0 {
			continue
		}
		notes = append(notes, domain.CatalogueProjectionNote{
			Code:   source.code,
			Count:  int(row.Count),
			Sample: row.Sample,
		})
	}

	// The malformed codes come from a query returning the codes themselves rather than a count,
	// because there is nothing else to say about them and the codes are the useful part.
	malformed, err := q.MalformedProgrammeCodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot count the malformed programme codes: %w", err)
	}
	if len(malformed) > 0 {
		notes = append(notes, domain.CatalogueProjectionNote{
			Code:   domain.NoteProgrammeCodeMalformed,
			Count:  len(malformed),
			Sample: malformed,
		})
	}

	return notes, nil
}

// LatestProjections returns the recent projection runs, newest first, each with its report.
func (c *Catalogue) LatestProjections(ctx context.Context, limit int) ([]domain.CatalogueProjection, error) {
	q := New(c.pool)

	rows, err := q.LatestCatalogueProjections(ctx, int32(limit))
	if err != nil {
		return nil, fmt.Errorf("cannot read the projections: %w", err)
	}

	out := make([]domain.CatalogueProjection, 0, len(rows))
	for _, row := range rows {
		projection := projectionFrom(row)
		notes, err := q.CatalogueProjectionNotes(ctx, row.ID)
		if err != nil {
			return nil, fmt.Errorf("cannot read the report of %s: %w", row.ID, err)
		}
		for _, note := range notes {
			code, ok := domain.ParseProjectionNoteCode(note.Code)
			if !ok {
				// A row written by a newer version. Skipping it is the safe reading: the
				// constraint keeps the set closed, so this means the binary is older than the
				// database, which rolling back an image does.
				continue
			}
			projection.Notes = append(projection.Notes, domain.CatalogueProjectionNote{
				Code:   code,
				Count:  int(note.Count),
				Sample: note.Sample,
			})
		}
		out = append(out, projection)
	}
	return out, nil
}

func projectionFrom(row ZpaCatalogueProjection) domain.CatalogueProjection {
	projection := domain.CatalogueProjection{
		ID:                row.ID,
		StartedAt:         row.StartedAt,
		Status:            domain.ProjectionStatus(row.Status),
		ProgrammesWritten: int(row.ProgrammesWritten),
		ModulesWritten:    int(row.ModulesWritten),
		OfferingsWritten:  int(row.OfferingsWritten),
		OfferingsRemoved:  int(row.OfferingsRemoved),
		Error:             row.Error,
	}
	if row.RunID.Valid {
		id := row.RunID.UUID
		projection.RunID = &id
	}
	if row.FinishedAt.Valid {
		at := row.FinishedAt.Time
		projection.FinishedAt = &at
	}
	return projection
}

func nullUUID(id *uuid.UUID) uuid.NullUUID {
	if id == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *id, Valid: true}
}
