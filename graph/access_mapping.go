package graph

import (
	"github.com/google/uuid"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/obcode/tallox.go/graph/model"
	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
)

// What the access-log resolvers translate with. Separate from access.resolvers.go because
// gqlgen rewrites that file from the schema on every generate.

// mayReadAccessLog is the guard every read in this area runs first.
//
// In the resolver as well as behind @interactiveOnly, for the reason mayReadImport gives: the
// directive protects a field, this protects the rule, and the next surface to read this data —
// an export, a maintenance command — has to ask the same question.
func mayReadAccessLog(actor principal.Actor) error {
	if !policy.MayReadAccessLog(actor) {
		return &gqlerror.Error{
			Message:    policy.AccessLogReason,
			Extensions: map[string]any{"code": "FORBIDDEN"},
		}
	}
	return nil
}

// accessEntryModel reshapes one entry for the wire.
func accessEntryModel(e domain.AccessEntry) *model.AccessLogEntry {
	out := &model.AccessLogEntry{
		ID:           e.ID.String(),
		At:           e.At,
		Door:         model.AccessDoor(e.Door),
		Roles:        rolesOf(e.Roles),
		Fields:       e.Fields,
		Mutation:     e.Mutation,
		Outcome:      model.AccessOutcome(e.Outcome),
		NarrowedFrom: rolesOf(e.NarrowedFrom),
	}
	if e.ActorID != nil {
		id := e.ActorID.String()
		out.PersonID = &id
	}
	out.PersonName = optional(e.ActorName)
	out.Mail = optional(e.ActorMail)
	out.TokenID = optional(e.TokenID)
	out.Operation = optional(e.Operation)
	out.ErrorCode = optional(e.ErrorCode)
	if e.Duration > 0 {
		ms := int(e.Duration.Milliseconds())
		out.DurationMs = &ms
	}
	if e.SourceIP.IsValid() {
		out.SourceIP = optional(e.SourceIP.String())
	}
	// narrowedFrom is nullable and means "was not narrowed" when absent, so an empty slice has
	// to stay nil rather than become [].
	if len(out.NarrowedFrom) == 0 {
		out.NarrowedFrom = nil
	}
	return out
}

// accessSummaryModel reshapes one window's figures for the wire.
func accessSummaryModel(s domain.AccessSummary) *model.AccessSummary {
	out := &model.AccessSummary{
		From:  s.From,
		Until: s.Until,
		Counts: &model.AccessCounts{
			Total:              int(s.Counts.Total),
			Interactive:        int(s.Counts.Interactive),
			Token:              int(s.Counts.Token),
			Mutations:          int(s.Counts.Mutations),
			Errors:             int(s.Counts.Errors),
			RefusedAuth:        int(s.Counts.RefusedAuth),
			RefusedScope:       int(s.Counts.RefusedScope),
			RefusedInteractive: int(s.Counts.RefusedInteractive),
			People:             int(s.Counts.People),
		},
		Roles:     make([]*model.AccessRoleCount, 0, len(s.Roles)),
		Refused:   make([]*model.RefusedSignIn, 0, len(s.Refused)),
		Mutations: make([]*model.AccessMutationCount, 0, len(s.Mutations)),
	}
	for _, r := range s.Roles {
		out.Roles = append(out.Roles, &model.AccessRoleCount{
			Role:       policy.Role(r.Role),
			Operations: int(r.Operations),
		})
	}
	for _, r := range s.Refused {
		out.Refused = append(out.Refused, &model.RefusedSignIn{
			Mail:     r.Mail,
			TokenID:  r.TokenID,
			Reason:   r.Reason,
			Door:     model.AccessDoor(r.Door),
			Attempts: int(r.Attempts),
			LastAt:   r.LastAt,
		})
	}
	for _, m := range s.Mutations {
		out.Mutations = append(out.Mutations, &model.AccessMutationCount{
			Mail:   m.Mail,
			Field:  m.Field,
			Calls:  int(m.Calls),
			LastAt: m.LastAt,
		})
	}
	return out
}

// accessFilter turns the input into the domain filter.
//
// An unparseable person id is not an error: this is a filter, and the honest answer to "show me
// the entries of a person who cannot exist" is an empty page rather than a refusal that reads
// as though something went wrong with the log.
func accessFilter(in *model.AccessLogFilter, limit *int, before *string) domain.AccessFilter {
	filter := domain.AccessFilter{}
	if limit != nil {
		filter.Limit = *limit
	}
	if before != nil {
		if id, err := uuid.Parse(*before); err == nil {
			filter.Before = &id
		}
	}
	if in == nil {
		return filter
	}

	if in.PersonID != nil {
		if id, err := uuid.Parse(*in.PersonID); err == nil {
			filter.ActorID = &id
		} else {
			// A id that is not a uuid matches nobody, and saying so with a uuid that exists
			// nowhere is more honest than silently ignoring the filter and returning the log
			// of everybody.
			nobody := uuid.New()
			filter.ActorID = &nobody
		}
	}
	if in.Mail != nil {
		filter.Mail = *in.Mail
	}
	if in.Door != nil {
		filter.Door = domain.AccessDoor(*in.Door)
	}
	if in.OnlyRefused != nil {
		filter.OnlyRefused = *in.OnlyRefused
	}
	if in.OnlyMutations != nil {
		filter.OnlyMutations = *in.OnlyMutations
	}
	filter.From = in.From
	filter.Until = in.Until
	return filter
}

// rolesOf converts stored role strings into the schema's enum.
//
// Unknown values are dropped rather than passed through. A role string the policy does not know
// grants nothing (see policy.RolesOf), and a log that rendered it would be the one place in the
// system where such a string looks like a role.
func rolesOf(stored []string) []policy.Role {
	out := make([]policy.Role, 0, len(stored))
	for _, s := range stored {
		if role, ok := policy.ParseRole(s); ok {
			out = append(out, role)
		}
	}
	return out
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
