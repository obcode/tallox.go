package graph

import (
	"errors"

	"github.com/obcode/tallox.go/graph/model"
	"github.com/obcode/tallox.go/internal/domain"
)

// The wish area, on the way out and on the way back.
//
// Nothing here filters. What a caller sees was decided by a WHERE clause before these rows
// existed in memory, which is the whole arrangement: a mapping layer that dropped rows would be a
// second implementation of the confidentiality rule, and the exports and digests that will not
// pass through here would not have it.
//
// What this file does have to get right is the refusals. Two of them say less than they could on
// purpose, and both are marked where they are built.

func wishModel(w domain.Wish) *model.Wish {
	return &model.Wish{
		ID:        w.ID.String(),
		Person:    personModel(w.Person),
		Instance:  courseInstanceModel(w.Instance),
		Priority:  w.Priority,
		Note:      w.Note,
		CreatedAt: w.CreatedAt,
		UpdatedAt: w.UpdatedAt,
	}
}

func wishModels(wishes []domain.Wish) []*model.Wish {
	out := make([]*model.Wish, 0, len(wishes))
	for _, w := range wishes {
		out = append(out, wishModel(w))
	}
	return out
}

// wishError maps the refusals of this area to codes the interface branches on.
//
// The German sentence is the half that gets reworded after a support question; the code is the
// stable half of the contract, and src/lib/server/graphqlError.ts keeps an allowlist of the ones
// whose wording it passes through.
//
// Two of these are deliberately uninformative:
//
//   - WISH_NOT_FOUND covers "there is no such wish" and "it is not yours". Telling them apart
//     would say whose it is, which is the fact the whole area protects.
//   - WISH_ALREADY_SET can say so in plain words, and that is a consequence of a decision rather
//     than of this table: with only-self entry, the only person who can trip the uniqueness
//     constraint is the owner, about their own row. If proxy entry is ever allowed, this has to
//     become generic in the same commit.
func wishError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, domain.ErrNotAuthenticated):
		return err
	case errors.Is(err, domain.ErrWishPhaseClosed):
		return refusal("WISH_PHASE_CLOSED", err.Error())
	case errors.Is(err, domain.ErrWishNotFound):
		return refusal("WISH_NOT_FOUND", err.Error())
	case errors.Is(err, domain.ErrWishNoteTooLong):
		return refusal("WISH_NOTE_TOO_LONG", err.Error())
	case errors.Is(err, domain.ErrWishPriorityInvalid):
		return refusal("WISH_PRIORITY_INVALID", err.Error())
	case errors.Is(err, domain.ErrInstanceNotFound):
		// Withdrawn between the screen being rendered and the form being sent, which is the
		// ordinary race here — the same code the demand area uses for it, because it is the same
		// fact about the same row.
		return refusal("INSTANCE_NOT_FOUND", err.Error())
	case semesterRefusal(err) != nil:
		// Shared with the semester workflow and the demand: one meaning per code, and a German
		// sentence rather than the English one domain.Err… carries for the log.
		return semesterRefusal(err)
	}
	// Anything else keeps its own shape rather than being dressed up with a code that would then
	// mean nothing. The database noise a driver error carries never reaches here: internal/store
	// wraps it, and graphqltest.AssertNoLeak is what says so.
	return err
}
