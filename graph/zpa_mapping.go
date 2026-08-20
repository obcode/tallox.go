package graph

import (
	"errors"
	"strconv"

	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/obcode/tallox.go/graph/model"
	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
	"github.com/obcode/tallox.go/internal/store"
)

// What the import resolvers translate with. Separate from zpa.resolvers.go because gqlgen
// rewrites that file from the schema on every generate and moves anything it did not put there
// to the end under a warning banner — a helper left in it survives until the next generate.

// zpaRunModel reshapes a run for the wire.
func zpaRunModel(run domain.ZPASyncRun) *model.ZpaSyncRun {
	out := &model.ZpaSyncRun{
		ID:          run.ID.String(),
		Trigger:     run.Trigger,
		StartedAt:   run.StartedAt,
		Status:      run.Status,
		Fetched:     run.Fetched,
		Appeared:    run.Appeared,
		Changed:     run.Changed,
		Disappeared: run.Disappeared,
		Kinds:       make([]*model.ZpaSyncRunKind, 0, len(run.Kinds)),
	}
	if run.StartedByName != "" {
		name := run.StartedByName
		out.StartedBy = &name
	}
	if run.FinishedAt != nil {
		finished := *run.FinishedAt
		out.FinishedAt = &finished
	}
	if run.Error != "" {
		message := run.Error
		out.Error = &message
	}
	for _, kind := range run.Kinds {
		entry := &model.ZpaSyncRunKind{Kind: kind.Kind, Status: kind.Status, Fetched: kind.Fetched}
		if kind.Error != "" {
			message := kind.Error
			entry.Error = &message
		}
		out.Kinds = append(out.Kinds, entry)
	}
	return out
}

// zpaChangeModel reshapes one line of a run's report.
func zpaChangeModel(change domain.ZPAChange) *model.ZpaChange {
	out := &model.ZpaChange{
		ID:   change.ID.String(),
		Kind: change.Kind,
		// A string, not an Int. It is another database's key, it is never arithmetic here, and
		// GraphQL's Int is 32 bits — which is fine today and is not a promise worth making
		// about somebody else's sequence.
		ZpaID:       strconv.FormatInt(change.ZpaID, 10),
		Change:      change.Change,
		ChangedKeys: change.ChangedKeys,
		DetectedAt:  change.DetectedAt,
	}
	if change.Label != "" {
		label := change.Label
		out.Label = &label
	}
	if out.ChangedKeys == nil {
		// The schema types this non-null, and an appearance legitimately names no keys.
		out.ChangedKeys = []string{}
	}
	return out
}

// zpaUserFacing decides what a caller is told about a failed import operation.
//
// The code is what a client branches on and the German sentence is the part that gets reworded
// after a support question. Matching prose across the repository boundary breaks the first time
// somebody improves a message.
func zpaUserFacing(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, domain.ErrZPANotConfigured):
		return &gqlerror.Error{
			Message:    domain.ErrZPANotConfigured.Error(),
			Extensions: map[string]any{"code": "ZPA_NOT_CONFIGURED"},
		}
	case errors.Is(err, domain.ErrZPASyncedRecently):
		return &gqlerror.Error{
			Message:    domain.ErrZPASyncedRecently.Error(),
			Extensions: map[string]any{"code": "ZPA_SYNCED_RECENTLY"},
		}
	case errors.Is(err, store.ErrZPASyncLocked), errors.Is(err, domain.ErrZPASyncRunning):
		return &gqlerror.Error{
			Message:    domain.ErrZPASyncRunning.Error(),
			Extensions: map[string]any{"code": "ZPA_SYNC_RUNNING"},
		}
	default:
		// Anything else is generic on purpose. The error underneath may name a table, a
		// constraint or a host, and none of that belongs in an answer.
		return &gqlerror.Error{
			Message:    "Der ZPA-Import konnte nicht ausgeführt werden.",
			Extensions: map[string]any{"code": "INTERNAL"},
		}
	}
}

// zpaForbidden is the single refusal for every field in this area.
func zpaForbidden() error {
	return &gqlerror.Error{
		Message:    policy.ZPAImportReason,
		Extensions: map[string]any{"code": "FORBIDDEN"},
	}
}

// mayReadImport is the guard every read in this area runs first.
//
// In the resolver as well as behind @interactiveOnly, because the directive protects a field
// and this protects the rule: the next surface that reads this data — an export, a maintenance
// command — has to ask the same question, and a rule that exists only as a schema annotation is
// one the next surface does not have.
func mayReadImport(actor principal.Actor) error {
	if !policy.MayReadZPAImport(actor) {
		return zpaForbidden()
	}
	return nil
}

// projectionModel reshapes one rebuild of the catalogue for the wire.
func projectionModel(p domain.CatalogueProjection) *model.ZpaCatalogueProjection {
	out := &model.ZpaCatalogueProjection{
		ID:                p.ID.String(),
		StartedAt:         p.StartedAt,
		FinishedAt:        p.FinishedAt,
		Status:            p.Status,
		ProgrammesWritten: p.ProgrammesWritten,
		ModulesWritten:    p.ModulesWritten,
		OfferingsWritten:  p.OfferingsWritten,
		OfferingsRemoved:  p.OfferingsRemoved,
		Error:             p.Error,
		Notes:             make([]*model.ZpaProjectionNote, 0, len(p.Notes)),
	}
	if p.RunID != nil {
		id := p.RunID.String()
		out.RunID = &id
	}
	for _, note := range p.Notes {
		out.Notes = append(out.Notes, &model.ZpaProjectionNote{
			Finding: note.Code,
			Count:   note.Count,
			// Never nil on the wire: a list field that is sometimes null and sometimes empty
			// makes every client handle two shapes of nothing.
			Sample: append([]string{}, note.Sample...),
		})
	}
	return out
}
