package graph

import (
	"errors"
	"testing"

	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/principal"
)

// TestTheSemesterRefusalsReadTheSameEverywhere pins two things one bug had broken at once.
//
// Every area names a semester — the semester workflow, the demand, the wishes — so all three can
// meet the same two refusals about a semester *code*. Two of the three used to pass
// `domain.Err….Error()` straight through, and those strings are English: this repository writes
// everything in English except what a person reads. It reached a German page as "this semester is
// too far away to plan".
//
// So: one meaning per code whichever field produced it, and never the internal text.
func TestTheSemesterRefusalsReadTheSameEverywhere(t *testing.T) {
	t.Parallel()

	mappers := map[string]func(error) error{
		"semester": semesterUserFacing,
		"demand":   func(err error) error { return demandUserFacing(principal.Actor{}, err) },
		"wish":     wishError,
	}

	for _, tc := range []struct {
		name     string
		err      error
		wantCode string
	}{
		{"out of range", domain.ErrSemesterOutOfRange, "SEMESTER_OUT_OF_RANGE"},
		{"malformed code", domain.ErrSemesterCodeInvalid, "SEMESTER_CODE_INVALID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			seen := map[string]string{}
			for area, mapper := range mappers {
				var gql *gqlerror.Error
				if !errors.As(mapper(tc.err), &gql) {
					t.Fatalf("%s did not turn %v into a refusal", area, tc.err)
				}

				if code, _ := gql.Extensions["code"].(string); code != tc.wantCode {
					t.Errorf("%s answered with the code %q, want %q", area, code, tc.wantCode)
				}
				if gql.Message == tc.err.Error() {
					t.Errorf("%s shows the internal error text: %q. That string is English and "+
						"is written for a log; what reaches a screen is the German sentence.",
						area, gql.Message)
				}
				seen[gql.Message] = area
			}

			if len(seen) != 1 {
				t.Errorf("the areas answer %d different sentences for %s: %v.\n"+
					"One meaning per code is what lets the interface branch on the code at all.",
					len(seen), tc.wantCode, seen)
			}
		})
	}
}
