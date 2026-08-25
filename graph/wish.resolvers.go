package graph

// Wishes.
//
// Thin, like every resolver here: parse, delegate, map. The filtering happened in the database
// before any of this ran — see internal/store/wishes.go — so there is nothing in this file that
// decides who sees what, and that is the arrangement rather than an omission.

import (
	"context"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/graph/model"
	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/principal"
)

// SetWish is the resolver for the setWish field.
func (r *mutationResolver) SetWish(ctx context.Context, instancePartID string, priority domain.WishPriority, note *string) (*model.Wish, error) {
	partID, err := parseID(instancePartID)
	if err != nil {
		return nil, err
	}

	text := ""
	if note != nil {
		text = *note
	}

	// The owner is the actor and comes from nowhere else. There is no argument for whose wish
	// this is, and adding one would be the whole decision reversed.
	wish, err := r.Wishes.Set(ctx, principal.From(ctx), partID, priority, text)
	if err != nil {
		return nil, wishError(err)
	}
	return wishModel(*wish), nil
}

// WithdrawWish is the resolver for the withdrawWish field.
func (r *mutationResolver) WithdrawWish(ctx context.Context, id string) (string, error) {
	wishID, err := parseID(id)
	if err != nil {
		return "", err
	}
	if err := r.Wishes.Withdraw(ctx, principal.From(ctx), wishID); err != nil {
		return "", wishError(err)
	}
	return id, nil
}

// MyWishes is the resolver for the myWishes field.
func (r *queryResolver) MyWishes(ctx context.Context, semester string) ([]*model.Wish, error) {
	wishes, err := r.Resolver.Wishes.Mine(ctx, principal.From(ctx), semester)
	if err != nil {
		return nil, wishError(err)
	}
	return wishModels(wishes), nil
}

// Wishes is the resolver for the wishes field.
func (r *queryResolver) Wishes(ctx context.Context, semester string, programme *string, module *string, part *string, person *string) ([]*model.Wish, error) {
	query := domain.WishQuery{SemesterCode: semester}
	if programme != nil {
		query.Programme = *programme
	}

	for _, arg := range []struct {
		raw  *string
		into *uuid.UUID
	}{
		{module, &query.Module},
		{part, &query.Part},
		{person, &query.Person},
	} {
		if arg.raw == nil {
			continue
		}
		id, err := parseID(*arg.raw)
		if err != nil {
			return nil, err
		}
		*arg.into = id
	}

	wishes, err := r.Resolver.Wishes.List(ctx, principal.From(ctx), query)
	if err != nil {
		return nil, wishError(err)
	}
	return wishModels(wishes), nil
}
