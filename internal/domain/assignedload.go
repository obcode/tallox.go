package domain

import (
	"context"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
)

// AssignedLoad is how many hours a week one person already holds in a semester: in total, and by
// the subject group of the modules. Who may read it is policy.AssignedLoadVisibility — the one
// aggregate over assignments that a subject group lead reads before publication, and why.
type AssignedLoad struct {
	Person Person
	// Total is the sum of BySubjectGroup.
	Total float64
	// BySubjectGroup has one entry per subject group the person holds parts in, ordered by code,
	// and last one with a nil SubjectGroup for modules in no group.
	BySubjectGroup []AssignedLoadPart
}

// AssignedLoadPart is the share of one subject group.
type AssignedLoadPart struct {
	// SubjectGroup is nil for modules nobody has sorted into a group yet.
	SubjectGroup *SubjectGroupRef
	Hours        float64
}

// AssignedLoadStore is what the service needs from persistence. Part of AssignmentStore.
type AssignedLoadStore interface {
	// AssignedLoad sums the load of the people the filter allows, narrowed to one person unless
	// person is uuid.Nil. Somebody who holds nothing has no entry.
	AssignedLoad(ctx context.Context, semesterCode string, person uuid.UUID,
		filter policy.AssignedLoadFilter) ([]AssignedLoad, error)
}

// AssignedLoad lists the load of everybody this actor may see in a semester, or of one person.
//
// Not an error when the actor may see nobody's but their own, for the reason List gives: refused
// and empty would be an oracle.
func (s *AssignmentService) AssignedLoad(ctx context.Context, actor principal.Actor,
	semesterCode string, person uuid.UUID) ([]AssignedLoad, error) {
	if !actor.Authenticated() {
		return nil, ErrNotAuthenticated
	}
	if semesterCode == "" {
		return nil, ErrSemesterRequired
	}
	semester, err := s.semesters.ByCode(ctx, actor, semesterCode)
	if err != nil {
		return nil, err
	}
	return s.store.AssignedLoad(ctx, semester.Code, person,
		policy.AssignedLoadVisibility(actor, semester.State()))
}
