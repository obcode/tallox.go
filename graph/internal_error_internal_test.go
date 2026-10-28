package graph

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// TestAnUncodedErrorIsReplacedByINTERNAL is the backstop in one sentence: whatever reaches
// the presenter without a code leaves as the generic sentence, path intact.
func TestAnUncodedErrorIsReplacedByINTERNAL(t *testing.T) {
	t.Parallel()

	raw := fmt.Errorf("cannot register the wish: %w",
		errors.New(`ERROR: insert or update on table "wish" violates foreign key constraint "wish_course_instance_id_fkey" (SQLSTATE 23503)`))

	ctx := graphql.WithPathContext(context.Background(), graphql.NewPathWithField("setWish"))
	got := PresentError(ctx, raw)

	if got.Message != internalMessage {
		t.Errorf("message = %q, want the generic sentence", got.Message)
	}
	if code, _ := got.Extensions["code"].(string); code != "INTERNAL" {
		t.Errorf("code = %q, want INTERNAL", code)
	}
	for _, leak := range []string{"SQLSTATE", "wish", "constraint", "ERROR"} {
		if strings.Contains(got.Message, leak) {
			t.Errorf("the presented message carries %q: %s", leak, got.Message)
		}
	}
	if got.Path.String() != "setWish" {
		t.Errorf("path = %q, want setWish kept so the client knows which field", got.Path)
	}
}

// TestACodedRefusalPassesThePresenterUnchanged: the presenter replaces nobody's sentence.
func TestACodedRefusalPassesThePresenterUnchanged(t *testing.T) {
	t.Parallel()

	in := refusal("WISH_NOT_FOUND", "Diesen Wunsch gibt es nicht.")
	got := PresentError(context.Background(), in)

	if got.Message != "Diesen Wunsch gibt es nicht." {
		t.Errorf("message = %q, want the area's own sentence", got.Message)
	}
	if code, _ := got.Extensions["code"].(string); code != "WISH_NOT_FOUND" {
		t.Errorf("code = %q, want WISH_NOT_FOUND", code)
	}
}

// TestGqlgensOwnCodesPassThePresenter: validation and parse failures carry gqlgen's codes and
// are the caller's to read — a presenter that hid them would make a typo look like an outage.
func TestGqlgensOwnCodesPassThePresenter(t *testing.T) {
	t.Parallel()

	in := &gqlerror.Error{
		Message:    `Cannot query field "nope" on type "Query".`,
		Extensions: map[string]any{"code": "GRAPHQL_VALIDATION_FAILED"},
		Locations:  []gqlerror.Location{{Line: 1, Column: 9}},
	}
	got := PresentError(context.Background(), in)
	if got.Message != in.Message || len(got.Locations) != 1 {
		t.Errorf("validation failure was rewritten: %+v", got)
	}
}

func TestNilPresentsAsNil(t *testing.T) {
	t.Parallel()
	if got := PresentError(context.Background(), nil); got != nil {
		t.Errorf("nil presented as %+v", got)
	}
}

// TestInternalRefusalNamesNothing pins the shape every default arm now produces.
func TestInternalRefusalNamesNothing(t *testing.T) {
	t.Parallel()

	err := internalRefusal("wish", errors.New("ERROR: relation \"wish\" does not exist (SQLSTATE 42P01)"))
	if !coded(err) {
		t.Fatal("internalRefusal returned an uncoded error")
	}
	if strings.Contains(err.Error(), "SQLSTATE") || strings.Contains(err.Error(), "relation") {
		t.Errorf("the refusal carries the database's words: %s", err)
	}
}
