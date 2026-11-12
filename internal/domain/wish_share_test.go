package domain_test

import (
	"errors"
	"testing"

	"github.com/obcode/tallox.go/internal/domain"
)

func TestWishShare(t *testing.T) {
	t.Parallel()

	share := func(n int) *int { return &n }
	for _, c := range []struct {
		name    string
		share   *int
		hours   float64
		want    int
		refused bool
	}{
		{"no share is the whole cohort", nil, 4, 0, false},
		{"no share of a cohort without hours", nil, 0, 0, false},
		{"a share", share(2), 4, 2, false},
		{"one hour", share(1), 8, 1, false},
		{"all of it is the whole cohort", share(4), 4, 0, false},
		{"the hours rounded up are the whole cohort", share(3), 2.5, 0, false},
		{"below the hours with a half", share(2), 2.5, 2, false},
		{"zero", share(0), 4, 0, true},
		{"negative", share(-1), 4, 0, true},
		{"more than the cohort", share(5), 4, 0, true},
		{"more than the hours rounded up", share(4), 2.5, 0, true},
		{"a cohort without hours", share(1), 0, 0, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got, err := domain.WishShare(c.share, c.hours)
			if c.refused {
				if !errors.Is(err, domain.ErrWishShareOutOfRange) {
					t.Fatalf("got %d, %v; want WISH_SHARE_OUT_OF_RANGE", got, err)
				}
				return
			}
			if err != nil || got != c.want {
				t.Errorf("got %d, %v; want %d", got, err, c.want)
			}
		})
	}
}
