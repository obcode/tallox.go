package graph

import (
	"errors"

	"github.com/obcode/tallox.go/graph/model"
	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
)

// The two planning marks, on the way out and on the way back.
//
// Nothing here filters, and unlike its neighbours that is not a discipline but a fact about the
// data: which programmes have settled their demand and which subjects are taking entries is public
// to anybody with an account. What is confidential is what the marks are about.

func demandCompletionModel(c domain.DemandCompletion) *model.DemandCompletion {
	return &model.DemandCompletion{
		Semester:    c.SemesterCode,
		Programme:   programmeModel(c.Programme),
		CompletedAt: c.CompletedAt,
	}
}

func demandCompletionModels(list []domain.DemandCompletion) []*model.DemandCompletion {
	out := make([]*model.DemandCompletion, 0, len(list))
	for _, c := range list {
		out = append(out, demandCompletionModel(c))
	}
	return out
}

func wishWindowModel(w domain.WishWindow) *model.WishWindow {
	return &model.WishWindow{
		Semester: w.SemesterCode,
		SubjectGroup: &model.SubjectGroupRef{
			ID:   w.SubjectGroupID.String(),
			Code: w.SubjectGroupCode,
			Name: w.SubjectGroupName,
			// The row exists, so the group does; whether it is retired is not carried by this
			// query and defaults to the ordinary case rather than to a claim.
			Active: true,
		},
		Open:      w.Open,
		ChangedAt: w.ChangedAt,
	}
}

func wishWindowModels(list []domain.WishWindow) []*model.WishWindow {
	out := make([]*model.WishWindow, 0, len(list))
	for _, w := range list {
		out = append(out, wishWindowModel(w))
	}
	return out
}

// planningMarkError maps this area's refusals to codes the interface branches on.
//
// Takes the actor, like demandUserFacing does and for the same reason: "you lead no programme yet"
// and "you lead a different one" are the same refusal from the service and two different people to
// go and see. Which of the two it is cannot be read off the error.
func planningMarkError(actor principal.Actor, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, domain.ErrNotAuthenticated):
		return err
	case errors.Is(err, domain.ErrNotYourProgramme):
		if policy.HoldsProgrammeLeadWithoutScope(actor) {
			return refusal("PROGRAMME_SCOPE_MISSING", policy.ProgrammeScopeMissingReason)
		}
		return refusal("NOT_YOUR_PROGRAMME", policy.DemandAnnouncementReason)
	case errors.Is(err, domain.ErrNotYourSubjectGroup):
		if policy.HoldsSubjectGroupLeadWithoutScope(actor) {
			return refusal("SUBJECT_GROUP_SCOPE_MISSING", policy.SubjectGroupScopeMissingReason)
		}
		return refusal("NOT_YOUR_SUBJECT_GROUP", policy.WishWindowSwitchReason)
	case errors.Is(err, domain.ErrProgrammeNotFound):
		return refusal("PROGRAMME_NOT_FOUND", err.Error())
	case errors.Is(err, domain.ErrSubjectGroupNotFound):
		return refusal("SUBJECT_GROUP_NOT_FOUND", err.Error())
	case semesterRefusal(err) != nil:
		return semesterRefusal(err)
	}
	return err
}
