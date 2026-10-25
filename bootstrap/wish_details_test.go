package bootstrap_test

import (
	"testing"

	"github.com/obcode/tallox.go/internal/graphqltest"
	"github.com/obcode/tallox.go/internal/testdata"
)

const wishDetailsQuery = `query($s: String!) {
	wishes(semester: $s) { person { mail } priority note createdAt updatedAt }
}`

type wishDetails struct {
	Wishes []struct {
		Person    struct{ Mail string }
		Priority  string
		Note      string
		CreatedAt *string
		UpdatedAt *string
	}
}

// Publication makes visible who wants what — and not the owner's note or when they registered,
// which is the first-come order the rule exists to make irrelevant. The owner keeps both through
// either door, the lead of the module's group keeps them in the browser.
func TestPublishedWishesKeepTheirNoteAndTimestampsToThoseWhoReadThemBefore(t *testing.T) {
	t.Parallel()

	f := wishHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Zwei, []string{"LECTURER"}},
		grants{testdata.Vier, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
	)
	f.leadGroup(t, testdata.Vier, f.group)
	var ignored struct{}
	graphqltest.New(f.handler).AsUser(testdata.Eins.Mail).MustQuery(t, setWishMutation,
		map[string]any{"p": f.instance, "prio": "IF_NEEDED", "note": "nur wenn es sonst niemand macht"}, &ignored)
	f.publish(t)

	graphqltest.EachDoor(t, f.handler, testdata.Zwei.Mail, testdata.Zwei.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var out wishDetails
			c.MustQuery(t, wishDetailsQuery, map[string]any{"s": f.semester}, &out)
			if len(out.Wishes) != 1 || out.Wishes[0].Person.Mail != testdata.Eins.Mail {
				t.Fatalf("a colleague reads %+v after publication, want Eins's wish", out.Wishes)
			}
			w := out.Wishes[0]
			if w.Note != "" || w.CreatedAt != nil || w.UpdatedAt != nil {
				t.Errorf("a colleague reads the note %q and the times %v / %v", w.Note, w.CreatedAt, w.UpdatedAt)
			}
			if w.Priority != "IF_NEEDED" {
				t.Errorf("the priority is %q; publication shows it", w.Priority)
			}
		})

	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var out wishDetails
			c.MustQuery(t, wishDetailsQuery, map[string]any{"s": f.semester}, &out)
			if len(out.Wishes) != 1 || out.Wishes[0].Note == "" || out.Wishes[0].CreatedAt == nil {
				t.Errorf("the owner reads their own wish as %+v", out.Wishes)
			}
		})

	var lead wishDetails
	graphqltest.New(f.handler).AsUser(testdata.Vier.Mail).
		MustQuery(t, wishDetailsQuery, map[string]any{"s": f.semester}, &lead)
	if len(lead.Wishes) != 1 || lead.Wishes[0].Note == "" || lead.Wishes[0].CreatedAt == nil {
		t.Errorf("the lead of the module's group reads %+v in the browser", lead.Wishes)
	}
	var leadByToken wishDetails
	graphqltest.New(f.handler).WithToken(testdata.Vier.Token).
		MustQuery(t, wishDetailsQuery, map[string]any{"s": f.semester}, &leadByToken)
	if len(leadByToken.Wishes) != 1 || leadByToken.Wishes[0].Note != "" {
		t.Errorf("the lead reads the note through a token: %+v", leadByToken.Wishes)
	}
}
