package graph

import (
	"errors"

	"github.com/google/uuid"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/obcode/tallox.go/graph/model"
	"github.com/obcode/tallox.go/internal/domain"
)

// The subject group area, on the way out and on the way back.
//
// Nothing here is confidential — a subject group is who works on what, and the counts are over
// catalogue rows. The one thing this file has to get right is that the refusals name what is
// missing rather than saying "not allowed", because the two states an administrator meets here
// have different repairs: a code that is taken, and a person who does not hold the role.

func subjectGroupModel(g domain.SubjectGroup) *model.SubjectGroup {
	return &model.SubjectGroup{
		ID:          g.ID.String(),
		Code:        g.Code,
		Name:        g.Name,
		Active:      g.Active,
		Leads:       personModels(g.Leads),
		Members:     personModels(g.Members),
		ModuleCount: g.ModuleCount,
	}
}

func subjectGroupModels(groups []domain.SubjectGroup) []*model.SubjectGroup {
	out := make([]*model.SubjectGroup, 0, len(groups))
	for _, g := range groups {
		out = append(out, subjectGroupModel(g))
	}
	return out
}

// subjectGroupError maps the refusals this area produces to codes the interface branches on.
//
// The German sentence is the half that gets reworded after a support question; the code is the
// stable half of the contract. Anything unrecognised is passed through as an ordinary error
// rather than dressed up with a code that would then mean nothing.
func subjectGroupError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, domain.ErrNotAdministrator):
		return refusal("NOT_ADMINISTRATOR", err.Error())
	case errors.Is(err, domain.ErrSubjectGroupNotFound):
		return refusal("SUBJECT_GROUP_NOT_FOUND", err.Error())
	case errors.Is(err, domain.ErrSubjectGroupCodeTaken):
		return refusal("SUBJECT_GROUP_CODE_TAKEN", err.Error())
	case errors.Is(err, domain.ErrSubjectGroupCodeInvalid):
		return refusal("SUBJECT_GROUP_CODE_INVALID", err.Error())
	case errors.Is(err, domain.ErrSubjectGroupNameBlank):
		return refusal("SUBJECT_GROUP_NAME_BLANK", err.Error())
	case errors.Is(err, domain.ErrNotASubjectGroupLead):
		// The repair is a role grant, not a different group, so it gets its own code — an
		// administrator who reads "that group does not exist" would go looking for the group.
		return refusal("NOT_A_SUBJECT_GROUP_LEAD", err.Error())
	case errors.Is(err, domain.ErrNotAuthenticated):
		return err
	}

	var gql *gqlerror.Error
	if errors.As(err, &gql) {
		return err
	}
	return err
}

// parseIDs turns a list of identifiers into uuids, refusing the whole list on the first one that
// is not.
//
// All or nothing rather than skipping the bad ones: these lists are "the set afterwards", so
// dropping an unparsable entry would silently write a smaller set than the caller asked for —
// and on the list of leads, a smaller set is a revocation nobody performed.
func parseIDs(raw []string) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0, len(raw))
	for _, r := range raw {
		id, err := parseID(r)
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
}

// errorsIsSubjectGroupNotFound is the one refusal in this area that is rendered as an empty
// answer rather than as an error.
func errorsIsSubjectGroupNotFound(err error) bool {
	return errors.Is(err, domain.ErrSubjectGroupNotFound)
}
