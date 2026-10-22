package graph

import (
	"context"
	"sync"

	"github.com/99designs/gqlgen/graphql"

	"github.com/obcode/tallox.go/internal/domain"
)

// degreeCacheKey is where an operation keeps the degrees it has read.
type degreeCacheKey struct{}

// degreeCache holds the programme degrees for one operation.
//
// `Programme.degree` is a field resolver over a lookup table rather than a column the store fills
// in, because a programme reaches the API from some twenty queries — a demand row, a wish, an
// assignment, a coverage host — and a field filled in by some of them would read as "not
// recorded" on the others, which is exactly the answer a filter would then trust. The table is
// some thirty rows; reading it once per operation that asks is cheaper than any of the joins.
type degreeCache struct {
	mu      sync.Mutex
	loaded  bool
	degrees map[string]domain.Degree
}

// CacheProgrammeDegrees gives every operation its own, empty degree cache.
//
// Wired in bootstrap with srv.AroundOperations. Per operation and not per process: the dean's
// office records a degree and the next request has to see it, on every replica.
func CacheProgrammeDegrees(ctx context.Context, next graphql.OperationHandler) graphql.ResponseHandler {
	return next(context.WithValue(ctx, degreeCacheKey{}, &degreeCache{}))
}

// programmeDegrees answers from the operation's cache, loading it on first use. Without a cache —
// a context this package did not prepare — it loads every time, which is slower and still right.
func programmeDegrees(ctx context.Context,
	load func(context.Context) (map[string]domain.Degree, error),
) (map[string]domain.Degree, error) {
	cache, _ := ctx.Value(degreeCacheKey{}).(*degreeCache)
	if cache == nil {
		return load(ctx)
	}

	cache.mu.Lock()
	defer cache.mu.Unlock()
	if !cache.loaded {
		degrees, err := load(ctx)
		if err != nil {
			return nil, err
		}
		cache.degrees, cache.loaded = degrees, true
	}
	return cache.degrees, nil
}

// forgetProgrammeDegrees drops what the operation has read, after a write that changed it.
func forgetProgrammeDegrees(ctx context.Context) {
	if cache, _ := ctx.Value(degreeCacheKey{}).(*degreeCache); cache != nil {
		cache.mu.Lock()
		cache.loaded, cache.degrees = false, nil
		cache.mu.Unlock()
	}
}
