package graph

import (
	"errors"

	"github.com/obcode/tallox.go/graph/model"
	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/policy"
)

// The competence area, on the way out and on the way back. Nothing here filters — see
// wish_mapping.go for why that is the arrangement.

func competenceModel(c domain.Competence) *model.Competence {
	return &model.Competence{
		ID: c.ID.String(),
		Module: &model.CompetenceModule{
			ID:           c.Module.ID.String(),
			Name:         c.Module.Name,
			Compulsory:   c.Module.Compulsory,
			SubjectGroup: subjectGroupRefModel(c.Module.SubjectGroup),
		},
		Holder:               assigneeModel(c.Holder),
		Level:                c.Level,
		Note:                 c.Note,
		OutsideSubjectGroups: c.OutsideSubjectGroups,
		CreatedAt:            c.CreatedAt,
		UpdatedAt:            c.UpdatedAt,
	}
}

func competenceModels(list []domain.Competence) []*model.Competence {
	out := make([]*model.Competence, 0, len(list))
	for _, c := range list {
		out = append(out, competenceModel(c))
	}
	return out
}

func textOf(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// competenceError maps the refusals of this area to codes the interface branches on.
//
// COMPETENCE_NOT_FOUND covers "not there", "not yours" and "not a row you may remove" alike —
// telling them apart would confirm that somebody else's statement exists. The upsert never trips
// a uniqueness constraint, so there is no wording of that to get wrong.
func competenceError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, domain.ErrNotAuthenticated):
		return err
	case errors.Is(err, domain.ErrCompetenceNotFound):
		return refusal("COMPETENCE_NOT_FOUND", err.Error())
	case errors.Is(err, domain.ErrCompetenceOutsideGroups):
		return refusal("COMPETENCE_OUTSIDE_GROUPS", policy.CompetenceOutsideGroupsReason)
	case errors.Is(err, domain.ErrCompetenceForTeacherRefused):
		return refusal("COMPETENCE_FOR_TEACHER_REFUSED", policy.CompetenceForTeacherReason)
	case errors.Is(err, domain.ErrCompetenceTeacherHasAccount):
		return refusal("COMPETENCE_TEACHER_HAS_ACCOUNT", err.Error())
	case errors.Is(err, domain.ErrCompetenceModuleRetired):
		return refusal("COMPETENCE_MODULE_RETIRED", err.Error())
	case errors.Is(err, domain.ErrCompetenceLevelInvalid):
		return refusal("COMPETENCE_LEVEL_INVALID", err.Error())
	case errors.Is(err, domain.ErrCompetenceNoteTooLong):
		return refusal("COMPETENCE_NOTE_TOO_LONG", err.Error())
	case errors.Is(err, domain.ErrCompetenceGroupRefused):
		return refusal("COMPETENCE_GROUP_REFUSED", err.Error())
	case errors.Is(err, domain.ErrModuleNotFound):
		return refusal("MODULE_NOT_FOUND", err.Error())
	case errors.Is(err, domain.ErrAssigneeNotFound):
		return refusal("ASSIGNEE_NOT_FOUND", err.Error())
	}
	return err
}
