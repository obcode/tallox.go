package bootstrap_test

import (
	"strings"
	"testing"

	"github.com/obcode/tallox.go/internal/graphqltest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// The wishes of a semester are published once every subject group with something planned in it
// has closed its round — before that, an entry made in an open round would be visible the moment it
// is made. The refusal names the group, so that the dean's office knows whom to ask. A group with
// nothing planned does not hold it up.
func TestWishesArePublishedOnlyOnceEveryRoundIsClosed(t *testing.T) {
	t.Parallel()

	f := wishHandler(t, grants{testdata.Fuenf, []string{"LECTURER", "DEANS_OFFICE"}})
	deans := graphqltest.New(f.handler).AsUser(testdata.Fuenf.Mail)
	const publish = `mutation($s: String!) { publishWishes(code: $s) { wishesPublishedAt } }`
	const window = `mutation($s: String!, $g: ID!, $open: Boolean!) {
		setWishWindow(semester: $s, subjectGroupId: $g, open: $open) { open }
	}`

	resp := deans.Do(t, publish, map[string]any{"s": f.semester})
	if !resp.Failed() {
		t.Fatalf("published with the MATHE round open: %s", resp.Body)
	}
	if got := resp.Errors[0].Extensions["code"]; got != "WISH_WINDOWS_OPEN" {
		t.Fatalf("refused with %v, want WISH_WINDOWS_OPEN", got)
	}
	if msg := resp.Errors[0].Message; !strings.Contains(msg, "Mathematik") || strings.Contains(msg, "Softwarefächer") {
		t.Errorf("the refusal names %q; want Mathematik, and not the group with nothing planned", msg)
	}

	// Closed, then opened again, is open.
	var ignored struct{}
	deans.MustQuery(t, window, map[string]any{"s": f.semester, "g": f.group.String(), "open": false}, &ignored)
	deans.MustQuery(t, window, map[string]any{"s": f.semester, "g": f.group.String(), "open": true}, &ignored)
	mustRefuseWith(t, deans, publish, map[string]any{"s": f.semester}, "WISH_WINDOWS_OPEN")

	deans.MustQuery(t, window, map[string]any{"s": f.semester, "g": f.group.String(), "open": false}, &ignored)
	var out struct {
		PublishWishes struct{ WishesPublishedAt *string }
	}
	deans.MustQuery(t, publish, map[string]any{"s": f.semester}, &out)
	if out.PublishWishes.WishesPublishedAt == nil {
		t.Fatal("publishing with every round closed left wishesPublishedAt null")
	}

	// Published is a fact: reopening a round does not turn a repeated call into a refusal.
	deans.MustQuery(t, window, map[string]any{"s": f.semester, "g": f.group.String(), "open": true}, &ignored)
	deans.MustQuery(t, publish, map[string]any{"s": f.semester}, &out)
}
