package store

import (
	"context"
	"fmt"
	"math"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/policy"
)

// Carrying a corrected split over to existing instances — see domain.DemandService.ApplyModuleSplit
// for what and why. This is the how, and it is one transaction for the same reason planDemand is:
// the dry run is the write rolled back, so the preview and the save cannot be two computations.

// splitTarget is a module's split in the shape an instance is compared against: per kind, the
// hours of the part and then of each piece, and the kinds in the order the split names them.
type splitTarget struct {
	kinds []string
	hours map[string][]float64
}

func targetOf(components []domain.ModuleComponent) splitTarget {
	t := splitTarget{hours: map[string][]float64{}}
	for _, c := range components {
		kind := string(c.Kind)
		if _, seen := t.hours[kind]; !seen {
			t.kinds = append(t.kinds, kind)
		}
		t.hours[kind] = append(t.hours[kind], c.TeachingHours)
	}
	return t
}

// partGroup is one undivided part and the pieces cut from it — one group, as the demand counts.
type partGroup struct {
	root   InstancePartsForRow
	pieces []InstancePartsForRow
}

// groupsByKind arranges an instance's parts as the split is arranged: by kind, in order, with the
// pieces behind their part.
func groupsByKind(parts []InstancePartsForRow) (map[string][]*partGroup, []string) {
	roots := map[uuid.UUID]*partGroup{}
	byKind := map[string][]*partGroup{}
	var kinds []string
	for _, p := range parts {
		if p.DividedFromID.Valid {
			continue
		}
		g := &partGroup{root: p}
		roots[p.ID] = g
		if _, seen := byKind[p.Kind]; !seen {
			kinds = append(kinds, p.Kind)
		}
		byKind[p.Kind] = append(byKind[p.Kind], g)
	}
	for _, p := range parts {
		if g, ok := roots[p.DividedFromID.UUID]; p.DividedFromID.Valid && ok {
			g.pieces = append(g.pieces, p)
		}
	}
	return byKind, kinds
}

func sumHours(parts []InstancePartsForRow) float64 {
	total := 0.0
	for _, p := range parts {
		total += numericFloat(p.TeachingHours)
	}
	return total
}

// sameHours compares a stored figure with the split's. A part with no hours stated is never the
// same: the split always states some.
func sameHours(stored InstancePartsForRow, want float64) bool {
	got := numericFloatOrNil(stored.TeachingHours)
	return got != nil && math.Abs(*got-want) < 1e-9
}

// splitOutcome is what happened to one instance, before the instances are read for the report.
type splitOutcome struct {
	id       uuid.UUID
	before   float64
	changed  bool
	refusals []splitRefusal
}

type splitRefusal struct {
	kind string
	err  error
	code string
}

func (o *splitOutcome) refuse(kind, code string, err error) {
	for _, r := range o.refusals {
		if r.kind == kind && r.code == code {
			return
		}
	}
	o.refusals = append(o.refusals, splitRefusal{kind: kind, code: code, err: err})
}

// ApplyModuleSplit rewrites the parts of the module's instances to follow its split.
func (d *Demand) ApplyModuleSplit(ctx context.Context, moduleID uuid.UUID,
	mayRemove func(domain.SplitInstanceScope) bool, dryRun bool,
) (domain.SplitApplication, error) {
	result := domain.SplitApplication{DryRun: dryRun}

	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return result, fmt.Errorf("cannot begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := New(tx)

	// No split, nothing to carry over — and ErrModuleNotDecomposed says what to do instead.
	components, err := statedComponents(ctx, q, moduleID)
	if err != nil {
		return result, err
	}
	target := targetOf(components)

	rows, err := q.InstancesForSplit(ctx, moduleID)
	if err != nil {
		return result, fmt.Errorf("cannot read the instances of the module: %w", err)
	}
	if len(rows) == 0 {
		return result, nil
	}
	ids := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}

	parts, err := q.InstancePartsFor(ctx, ids)
	if err != nil {
		return result, fmt.Errorf("cannot read the parts: %w", err)
	}
	partsOf := map[uuid.UUID][]InstancePartsForRow{}
	for _, p := range parts {
		partsOf[p.CourseInstanceID] = append(partsOf[p.CourseInstanceID], p)
	}

	// What a sibling cohort holds for this one is not this one's to add: a lecture the A cohort
	// holds for A and B must not turn into a second lecture in B.
	borrowed, err := q.BorrowedInstancePartsFor(ctx, ids)
	if err != nil {
		return result, fmt.Errorf("cannot read the shared parts: %w", err)
	}
	heldBySibling := map[uuid.UUID]map[string]bool{}
	for _, b := range borrowed {
		if b.FromProgrammeCode != nil {
			continue
		}
		if heldBySibling[b.ForInstanceID] == nil {
			heldBySibling[b.ForInstanceID] = map[string]bool{}
		}
		heldBySibling[b.ForInstanceID][b.Kind] = true
	}

	outcomes := make([]*splitOutcome, 0, len(rows))
	for _, r := range rows {
		scope := domain.SplitInstanceScope{
			ProgrammeID: r.ProgrammeID,
			Semester: policy.SemesterState{
				Phase:                  policy.Phase(r.SemesterPhase),
				WishesPublishedAt:      nullableTime(r.WishesPublishedAt),
				AssignmentsPublishedAt: nullableTime(r.AssignmentsPublishedAt),
			},
		}
		o := &splitOutcome{id: r.ID, before: sumHours(partsOf[r.ID])}
		if err := alignInstance(ctx, tx, o, partsOf[r.ID], target, components,
			heldBySibling[r.ID], mayRemove(scope)); err != nil {
			return result, err
		}
		outcomes = append(outcomes, o)
	}

	var changedIDs []uuid.UUID
	for _, o := range outcomes {
		if o.changed {
			changedIDs = append(changedIDs, o.id)
		}
	}
	after := map[uuid.UUID]float64{}
	if len(changedIDs) > 0 {
		rewritten, err := q.InstancePartsFor(ctx, changedIDs)
		if err != nil {
			return result, fmt.Errorf("cannot read the rewritten parts: %w", err)
		}
		byInstance := map[uuid.UUID][]InstancePartsForRow{}
		for _, p := range rewritten {
			byInstance[p.CourseInstanceID] = append(byInstance[p.CourseInstanceID], p)
		}
		for _, id := range changedIDs {
			after[id] = sumHours(byInstance[id])
		}
	}

	if !dryRun {
		if err := tx.Commit(ctx); err != nil {
			return result, fmt.Errorf("cannot commit: %w", err)
		}
	} else if err := tx.Rollback(ctx); err != nil {
		return result, fmt.Errorf("cannot roll back the dry run: %w", err)
	}

	// The instances are read after the transaction, so the report shows what is there now: the
	// rewritten parts after a save, the untouched ones after a dry run.
	for _, o := range outcomes {
		if !o.changed && len(o.refusals) == 0 {
			result.Unchanged++
			continue
		}
		instance, err := d.CourseInstanceByID(ctx, o.id)
		if err != nil {
			return result, err
		}
		if instance == nil {
			continue
		}
		if o.changed {
			result.Changed = append(result.Changed, domain.SplitChange{
				Instance: *instance, HoursBefore: o.before, HoursAfter: after[o.id],
			})
		}
		for _, r := range o.refusals {
			result.Refused = append(result.Refused, domain.SplitRefusal{
				Instance: *instance,
				Kind:     domain.InstancePartKind(r.kind),
				Code:     r.code,
				Reason:   r.err.Error(),
			})
		}
	}
	return result, nil
}

// alignInstance brings one instance's parts in line with the split.
//
// Three passes, in this order: the kinds the split still names get its hours, the kinds it no
// longer names are removed, and the kinds it names and the instance lacks are added — last,
// because how many groups of a new practical kind to add is how many of the old one went.
func alignInstance(ctx context.Context, tx pgx.Tx, o *splitOutcome, parts []InstancePartsForRow,
	target splitTarget, components []domain.ModuleComponent, heldBySibling map[string]bool,
	mayRemove bool,
) error {
	q := New(tx)
	byKind, kinds := groupsByKind(parts)

	for _, kind := range kinds {
		want, named := target.hours[kind]
		if !named {
			continue
		}
		for _, g := range byKind[kind] {
			switch {
			case len(g.pieces) == len(want)-1:
				// The same arrangement: only the figures can differ.
				rows := append([]InstancePartsForRow{g.root}, g.pieces...)
				for i, row := range rows {
					if sameHours(row, want[i]) {
						continue
					}
					if err := setPartHours(ctx, q, row, want[i]); err != nil {
						return err
					}
					o.changed = true
				}
			case len(g.pieces) == 0:
				// The split now names the kind more than once: the part is divided, the way
				// partsFromSplit divides it for a new instance. Nothing is removed, so nobody
				// holding the part loses it — they keep the first share.
				if !sameHours(g.root, want[0]) {
					if err := setPartHours(ctx, q, g.root, want[0]); err != nil {
						return err
					}
				}
				for _, h := range want[1:] {
					if err := insertGroupRow(ctx, q, g.root.CourseInstanceID, kind, g.root.Position,
						h, g.root.ServesSiblingTracks, &g.root.ID); err != nil {
						return err
					}
				}
				o.changed = true
			default:
				// Divided otherwise than the split says. Joining or re-cutting would drop an
				// arrangement somebody made — with a colleague, or with another faculty.
				o.refuse(kind, "PART_DIVIDED", domain.ErrPartDivided)
			}
		}
	}

	removed := map[string]int{}
	for _, kind := range kinds {
		if _, named := target.hours[kind]; named {
			continue
		}
		if !mayRemove {
			o.refuse(kind, "SPLIT_LEFT_TO_PROGRAMME", domain.ErrSplitLeftToProgramme)
			continue
		}
		for _, g := range byKind[kind] {
			if len(g.pieces) > 0 || g.root.TaughtBy != nil {
				o.refuse(kind, "PART_DIVIDED", domain.ErrPartDivided)
				continue
			}
			// A savepoint, so that a held part costs that part and not the instance.
			sub, err := tx.Begin(ctx)
			if err != nil {
				return fmt.Errorf("cannot open a savepoint: %w", err)
			}
			if _, err := New(sub).DeleteInstancePart(ctx, g.root.ID); err != nil {
				_ = sub.Rollback(ctx)
				if isForeignKeyViolation(err) {
					o.refuse(kind, "PART_ASSIGNED", domain.ErrPartAssigned)
					continue
				}
				return fmt.Errorf("cannot remove a part: %w", err)
			}
			if err := sub.Commit(ctx); err != nil {
				return fmt.Errorf("cannot release the savepoint: %w", err)
			}
			removed[kind]++
			o.changed = true
		}
	}

	practical, _, hasPractical := domain.PracticalKindOf(components)
	for _, kind := range target.kinds {
		if len(byKind[kind]) > 0 || heldBySibling[kind] {
			continue
		}
		count := 1
		if hasPractical && kind == string(practical) {
			if replaced, ok := replacedPractical(byKind, kinds, target); ok {
				// As many groups as went: the group count is a planning decision, and a group
				// that could not go stays and is reported rather than doubled.
				count = removed[replaced]
			}
		}
		for range count {
			if err := insertGroup(ctx, q, o, kind, target.hours[kind]); err != nil {
				return err
			}
		}
	}

	if o.changed {
		if err := q.RenumberInstanceParts(ctx, o.id); err != nil {
			return fmt.Errorf("cannot renumber the parts: %w", err)
		}
	}
	return nil
}

// replacedPractical is the practical kind a changed split replaces: of the kinds it no longer
// names, other than a lecture, the one the instance has most groups of. A laboratory that became
// an exercise keeps its three groups.
func replacedPractical(byKind map[string][]*partGroup, kinds []string, target splitTarget) (string, bool) {
	best, most := "", 0
	for _, kind := range kinds {
		if _, named := target.hours[kind]; named || kind == string(domain.PartKindLecture) {
			continue
		}
		if n := len(byKind[kind]); n > most {
			best, most = kind, n
		}
	}
	return best, most > 0
}

// insertGroup adds one group of a kind: its part, and a piece for every further entry of the kind.
func insertGroup(ctx context.Context, q *Queries, o *splitOutcome, kind string, hours []float64) error {
	position, err := q.NextInstancePartPosition(ctx, o.id)
	if err != nil {
		return fmt.Errorf("cannot find the end of the list: %w", err)
	}
	numeric, err := numericFrom(hours[0])
	if err != nil {
		return err
	}
	rootID, err := q.InsertInstancePart(ctx, InsertInstancePartParams{
		CourseInstanceID:    o.id,
		Kind:                kind,
		Position:            position,
		TeachingHours:       numeric,
		ServesSiblingTracks: false,
	})
	if err != nil {
		return fmt.Errorf("cannot add a part: %w", err)
	}
	for _, h := range hours[1:] {
		if err := insertGroupRow(ctx, q, o.id, kind, position, h, false, &rootID); err != nil {
			return err
		}
	}
	o.changed = true
	return nil
}

// insertGroupRow inserts a piece of a part. A piece of a shared part is shared too.
func insertGroupRow(ctx context.Context, q *Queries, instanceID uuid.UUID, kind string,
	position int32, hours float64, shared bool, root *uuid.UUID,
) error {
	numeric, err := numericFrom(hours)
	if err != nil {
		return err
	}
	if _, err := q.InsertInstancePiece(ctx, InsertInstancePieceParams{
		CourseInstanceID:    instanceID,
		Kind:                kind,
		Position:            position,
		TeachingHours:       numeric,
		ServesSiblingTracks: shared,
		DividedFromID:       uuid.NullUUID{UUID: *root, Valid: true},
	}); err != nil {
		return fmt.Errorf("cannot add a piece: %w", err)
	}
	return nil
}

func setPartHours(ctx context.Context, q *Queries, part InstancePartsForRow, hours float64) error {
	numeric, err := numericFrom(hours)
	if err != nil {
		return err
	}
	if err := q.UpdateInstancePart(ctx, UpdateInstancePartParams{
		ID:            part.ID,
		Kind:          part.Kind,
		TeachingHours: numeric,
	}); err != nil {
		return fmt.Errorf("cannot change the hours of a part: %w", err)
	}
	return nil
}
