package graph

import (
	"errors"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/graph/model"
	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
)

// The assignment area, on the way out and on the way back.
//
// Nothing here filters, the same way nothing in wish_mapping.go does. What a caller sees was
// decided by a WHERE clause before these rows existed in memory; a mapping layer that dropped rows
// would be a second implementation of the rule, and the exports that will not pass through here
// would not have it.
//
// Its own file rather than lines in assignment.resolvers.go, because gqlgen rewrites that one
// from the schema on every generate.

func assignmentModel(a domain.Assignment) *model.Assignment {
	out := &model.Assignment{
		ID:        a.ID.String(),
		Part:      instancePartModel(a.Part),
		Instance:  courseInstanceModel(a.Instance),
		Assignee:  assigneeModel(a.Assignee),
		Note:      a.Note,
		CreatedAt: a.CreatedAt,
		UpdatedAt: a.UpdatedAt,
	}
	if a.AssignedBy != uuid.Nil {
		by := a.AssignedBy.String()
		out.AssignedByID = &by
	}
	return out
}

// assigneeModel renders the two ways of naming somebody as one shape.
//
// The name and the address come from the store already coalesced, so this does not choose between
// the two columns — which is the point: which table somebody is in is a fact about accounts, and
// a screen that had to branch on it would eventually branch differently in two places.
func assigneeModel(a domain.Assignee) *model.Assignee {
	out := &model.Assignee{Name: a.Name}
	if a.Mail != "" {
		mail := a.Mail
		out.Mail = &mail
	}
	if a.PersonID != uuid.Nil {
		id := a.PersonID.String()
		out.PersonID = &id
	}
	if a.TeacherID != uuid.Nil {
		id := a.TeacherID.String()
		out.TeacherID = &id
	}
	return out
}

func assignmentModels(list []domain.Assignment) []*model.Assignment {
	out := make([]*model.Assignment, 0, len(list))
	for _, a := range list {
		out = append(out, assignmentModel(a))
	}
	return out
}

// assignmentError maps the refusals of this area to codes the interface branches on.
//
// The German sentence is the half that gets reworded after a support question; the code is the
// stable half of the contract, and src/lib/server/graphqlError.ts keeps an allowlist of the ones
// whose wording it passes through.
//
// One of these is deliberately uninformative and one deliberately is not:
//
//   - ASSIGNMENT_NOT_FOUND covers "there is no such assignment" and "that one is not yours to
//     see". Telling them apart would answer, for anybody holding an id, the question the
//     confidentiality rule refuses.
//   - PART_ALREADY_ASSIGNED says plainly that somebody holds the part, and that is safe because
//     of who can reach it: only a caller who may write here, and anybody who may write here may
//     read what they collided with. bootstrap.TestPartAlreadyAssignedTellsNobodySomethingNew
//     asserts that rather than trusting it — and if the write rule ever widens, that test is what
//     turns red.
func assignmentError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, domain.ErrNotAuthenticated):
		return refusal("UNAUTHENTICATED", "Nicht angemeldet.")
	case errors.Is(err, domain.ErrFullReachRequired):
		return refusal("FULL_REACH_REQUIRED", policy.FullReachReason)
	case errors.Is(err, domain.ErrAssignmentPhaseClosed):
		return refusal("ASSIGNMENT_PHASE_CLOSED", err.Error())
	case errors.Is(err, domain.ErrNotYourSubject):
		return refusal("NOT_YOUR_SUBJECT", err.Error())
	case errors.Is(err, domain.ErrAssignmentNotFound):
		return refusal("ASSIGNMENT_NOT_FOUND", err.Error())
	case errors.Is(err, domain.ErrPartAlreadyAssigned):
		return refusal("PART_ALREADY_ASSIGNED", err.Error())
	case errors.Is(err, domain.ErrAssignmentMovedOn):
		return refusal("ASSIGNMENT_MOVED_ON", err.Error())
	case errors.Is(err, domain.ErrAssigneeInvalid):
		return refusal("ASSIGNEE_INVALID", err.Error())
	case errors.Is(err, domain.ErrAssigneeNotFound):
		return refusal("ASSIGNEE_NOT_FOUND", err.Error())
	case errors.Is(err, domain.ErrAssignmentNoteTooLong):
		return refusal("ASSIGNMENT_NOTE_TOO_LONG", err.Error())
	case errors.Is(err, domain.ErrPartTaughtElsewhere):
		return refusal("PART_TAUGHT_ELSEWHERE", err.Error())
	case errors.Is(err, domain.ErrPartNotFound):
		// Removed between the screen being rendered and the form being sent, which is the ordinary
		// race here — the same code the demand area uses, because it is the same fact about the
		// same row.
		return refusal("PART_NOT_FOUND", err.Error())
	case semesterRefusal(err) != nil:
		// Shared with the semester workflow, the demand and the wishes: one meaning per code.
		return semesterRefusal(err)
	case coded(err):
		return err
	}
	// Anything else is ours, not the caller's: the generic sentence, and the truth in the log.
	return internalRefusal("assignment", err)
}

// partArrangementError maps the refusals of dividing a part and of handing it to another faculty.
//
// The assignment's sentinels for permission, because it is the assignment's rule — the same split
// setCourseInstanceFormat makes — and the demand's for the shape of the part, because those are
// the same facts about the same row that changeInstancePart reports.
func partArrangementError(actor principal.Actor, err error) error {
	switch {
	case errors.Is(err, domain.ErrNotYourSubject), errors.Is(err, domain.ErrAssignmentPhaseClosed),
		errors.Is(err, domain.ErrPartTaughtElsewhere):
		return assignmentError(err)
	case errors.Is(err, domain.ErrPartNotDivided):
		return refusal("PART_NOT_DIVIDED", err.Error())
	case errors.Is(err, domain.ErrTaughtByTooLong):
		return refusal("TAUGHT_BY_TOO_LONG", err.Error())
	}
	return demandUserFacing(actor, err)
}

func assignedLoadModel(l domain.AssignedLoad) *model.AssignedLoad {
	shares := make([]*model.AssignedLoadShare, 0, len(l.BySubjectGroup))
	for _, part := range l.BySubjectGroup {
		shares = append(shares, &model.AssignedLoadShare{
			SubjectGroup: subjectGroupRefModel(part.SubjectGroup),
			Hours:        part.Hours,
		})
	}
	return &model.AssignedLoad{
		Person:         personModel(l.Person),
		Total:          l.Total,
		BySubjectGroup: shares,
	}
}
