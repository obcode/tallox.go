package bootstrap_test

import (
	"strings"
	"testing"

	"github.com/obcode/tallox.go/internal/graphqltest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// Subscriptions and announcements through the API, through all three doors. Subscriptions are
// one's own and open on every door; announcements are sent only from a browser.

const subscribeMutation = `mutation($in: NotificationSubscriptionInput!) {
	subscribeNotification(input: $in) { id kind delivery targetSet programme { code } }
}`

const mySubscriptionsQuery = `{ myNotificationSubscriptions { id kind targetSet } }`

const sendAnnouncementMutation = `mutation($a: AnnouncementAudienceInput!, $s: String!, $b: String!) {
	sendAnnouncement(audience: $a, subject: $s, body: $b) { id subject recipientCount sender { mail } }
}`

const previewAnnouncementQuery = `query($a: AnnouncementAudienceInput!, $s: String!, $b: String!) {
	previewAnnouncement(audience: $a, subject: $s, body: $b) { subject recipientCount html }
}`

const announcementsQuery = `{ announcements { subject bodyHtml recipientCount sender { mail } } }`

func TestSubscriptionsAreOwnThroughEveryDoor(t *testing.T) {
	t.Parallel()

	f := assignmentHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Zwei, []string{"LECTURER"}},
	)
	doors := map[string]*graphqltest.Client{
		"browser":    graphqltest.New(f.handler).AsUser(testdata.Eins.Mail),
		"token":      graphqltest.New(f.handler).WithToken(testdata.Eins.Token),
		"full reach": graphqltest.New(f.handler).WithToken(fullReachToken(t, f.schema, testdata.Eins)),
	}
	kinds := map[string]string{"browser": "DEMAND_RELEASED", "token": "WISHES_PUBLISHED",
		"full reach": "PHASE_ADVANCED"}
	for door, c := range doors {
		var out struct {
			SubscribeNotification struct{ Kind, Delivery string }
		}
		c.MustQuery(t, subscribeMutation, map[string]any{"in": map[string]any{"kind": kinds[door]}}, &out)
		if out.SubscribeNotification.Kind != kinds[door] || out.SubscribeNotification.Delivery != "IMMEDIATE" {
			t.Errorf("%s: subscribing answered %+v", door, out.SubscribeNotification)
		}
	}

	var mine struct{ MyNotificationSubscriptions []struct{ Kind string } }
	graphqltest.New(f.handler).AsUser(testdata.Eins.Mail).MustQuery(t, mySubscriptionsQuery, nil, &mine)
	if len(mine.MyNotificationSubscriptions) != 3 {
		t.Errorf("Eins has %d subscriptions, want the three made through the three doors",
			len(mine.MyNotificationSubscriptions))
	}

	var theirs struct{ MyNotificationSubscriptions []struct{ Kind string } }
	graphqltest.New(f.handler).AsUser(testdata.Zwei.Mail).MustQuery(t, mySubscriptionsQuery, nil, &theirs)
	if len(theirs.MyNotificationSubscriptions) != 0 {
		t.Errorf("Zwei reads %d subscriptions, want none", len(theirs.MyNotificationSubscriptions))
	}
}

func TestASubscriptionThatCannotMatchIsRefused(t *testing.T) {
	t.Parallel()

	f := assignmentHandler(t, grants{testdata.Eins, []string{"LECTURER"}})
	c := graphqltest.New(f.handler).AsUser(testdata.Eins.Mail)
	for _, in := range []map[string]any{
		{"kind": "DEMAND_RELEASED", "targetSet": "MY_COMPETENCES"},
		{"kind": "MODULE_OFFERED", "programme": f.programme, "targetSet": "MY_WISHES"},
		{"kind": "MODULE_OFFERED", "programme": "NOPE"},
		{"kind": "MODULE_OFFERED", "semester": "2027-XX"},
	} {
		resp := c.Do(t, subscribeMutation, map[string]any{"in": in})
		if !hasCode(resp, "SUBSCRIPTION_INVALID") {
			t.Errorf("%v answered %v, want SUBSCRIPTION_INVALID", in, resp.Messages())
		}
	}
}

func TestRecommendedSubscriptionsAreOnlyMadeWhenAsked(t *testing.T) {
	t.Parallel()

	f := assignmentHandler(t, grants{testdata.Eins, []string{"LECTURER"}})
	c := graphqltest.New(f.handler).AsUser(testdata.Eins.Mail)

	var before struct{ MyNotificationSubscriptions []struct{ Kind string } }
	c.MustQuery(t, mySubscriptionsQuery, nil, &before)
	if len(before.MyNotificationSubscriptions) != 0 {
		t.Fatalf("an account starts with %d subscriptions, want none", len(before.MyNotificationSubscriptions))
	}

	var out struct {
		SubscribeRecommendedNotifications []struct{ Kind, TargetSet string }
	}
	c.MustQuery(t, `mutation { subscribeRecommendedNotifications { kind targetSet } }`, nil, &out)
	if len(out.SubscribeRecommendedNotifications) == 0 {
		t.Fatal("the recommended set is empty")
	}
	for _, s := range out.SubscribeRecommendedNotifications {
		if !strings.HasPrefix(s.TargetSet, "MY_") || s.TargetSet == "MY_PROGRAMMES" {
			t.Errorf("a lecturer was given %+v", s)
		}
	}
}

// TestAnnouncementsAreSentOnlyFromABrowser: the dean's office writes to the colleagues through its
// browser session; neither of its tokens can, not even to preview.
func TestAnnouncementsAreSentOnlyFromABrowser(t *testing.T) {
	t.Parallel()

	f := assignmentHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Vier, []string{"DEANS_OFFICE"}},
	)
	vars := map[string]any{"a": map[string]any{"kind": "COLLEAGUES"}, "s": "Hallo", "b": "Ein **Text**."}

	for door, c := range map[string]*graphqltest.Client{
		"token":      graphqltest.New(f.handler).WithToken(testdata.Vier.Token),
		"full reach": graphqltest.New(f.handler).WithToken(fullReachToken(t, f.schema, testdata.Vier)),
	} {
		for _, op := range []string{sendAnnouncementMutation, previewAnnouncementQuery} {
			if resp := c.Do(t, op, vars); !hasCode(resp, "INTERACTIVE_ONLY") {
				t.Errorf("%s: answered %v, want INTERACTIVE_ONLY", door, resp.Messages())
			}
		}
	}

	browser := graphqltest.New(f.handler).AsUser(testdata.Vier.Mail)
	var preview struct {
		PreviewAnnouncement struct {
			Subject        string
			RecipientCount int
			HTML           string
		}
	}
	browser.MustQuery(t, previewAnnouncementQuery, vars, &preview)
	if preview.PreviewAnnouncement.RecipientCount != 2 ||
		!strings.Contains(preview.PreviewAnnouncement.HTML, "<strong>Text</strong>") {
		t.Errorf("the preview is %+v", preview.PreviewAnnouncement)
	}

	var sent struct {
		SendAnnouncement struct {
			Subject        string
			RecipientCount *int
		}
	}
	browser.MustQuery(t, sendAnnouncementMutation, vars, &sent)
	if sent.SendAnnouncement.RecipientCount == nil || *sent.SendAnnouncement.RecipientCount != 2 {
		t.Errorf("sending answered %+v", sent.SendAnnouncement)
	}

	// The recipient reads it in the archive, through any door, without the count.
	graphqltest.EachDoor(t, f.handler, testdata.Eins.Mail, testdata.Eins.Token,
		func(t *testing.T, c *graphqltest.Client) {
			var out struct {
				Announcements []struct {
					Subject        string
					BodyHTML       string `json:"bodyHtml"`
					RecipientCount *int
				}
			}
			c.MustQuery(t, announcementsQuery, nil, &out)
			if len(out.Announcements) != 1 || out.Announcements[0].RecipientCount != nil {
				t.Fatalf("Eins reads %+v", out.Announcements)
			}
			// Rendered like the mail, and without its inline styles.
			if got := out.Announcements[0].BodyHTML; got != "<p>Ein <strong>Text</strong>.</p>" {
				t.Errorf("bodyHtml = %q", got)
			}
		})
}

// TestAnAudienceOneMayNotWriteToIsRefusedWithoutDetail: a lecturer cannot write to a programme,
// and the refusal says nothing about who would have received it.
func TestAnAudienceOneMayNotWriteToIsRefusedWithoutDetail(t *testing.T) {
	t.Parallel()

	f := assignmentHandler(t,
		grants{testdata.Eins, []string{"LECTURER"}},
		grants{testdata.Zwei, []string{"LECTURER"}},
		grants{testdata.Drei, []string{"LECTURER", "SUBJECT_GROUP_LEAD"}},
	)
	f.leadGroup(t, testdata.Drei, f.group)
	f.fill(t, testdata.Drei, f.lecture, testdata.Zwei)

	c := graphqltest.New(f.handler).AsUser(testdata.Eins.Mail)
	for _, audience := range []map[string]any{
		{"kind": "PROGRAMME", "programme": f.programme, "semester": f.semester},
		{"kind": "SUBJECT_GROUP", "subjectGroupId": f.group.String()},
		{"kind": "COLLEAGUES"}, {"kind": "LEADS"}, {"kind": "ALL_ACCOUNTS"},
	} {
		for _, op := range []string{sendAnnouncementMutation, previewAnnouncementQuery} {
			resp := c.Do(t, op, map[string]any{"a": audience, "s": "x", "b": "y"})
			if !hasCode(resp, "AUDIENCE_NOT_ALLOWED") {
				t.Errorf("%v answered %v, want AUDIENCE_NOT_ALLOWED", audience, resp.Messages())
			}
			for _, msg := range resp.Messages() {
				graphqltest.AssertNoLeak(t, msg, append(graphqltest.DatabaseNoise(),
					testdata.Mails(testdata.Others(testdata.Eins))...)...)
			}
		}
	}

	// The subject group lead writes to their group — which has no members yet: refused as empty.
	resp := graphqltest.New(f.handler).AsUser(testdata.Drei.Mail).Do(t, sendAnnouncementMutation,
		map[string]any{"a": map[string]any{"kind": "SUBJECT_GROUP", "subjectGroupId": f.group.String()},
			"s": "x", "b": "y"})
	if !hasCode(resp, "AUDIENCE_EMPTY") {
		t.Errorf("an empty group answered %v, want AUDIENCE_EMPTY", resp.Messages())
	}
}
