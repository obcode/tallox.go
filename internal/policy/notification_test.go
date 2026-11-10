package policy_test

import (
	"strings"
	"testing"

	"github.com/obcode/tallox.go/internal/golden"
	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
	"github.com/obcode/tallox.go/internal/testdata"
)

// TestAnnouncementMatrix renders who may write an announcement to which audience, through which
// door. Re-record with: go test ./internal/policy/ -update-golden — and then read the diff.
func TestAnnouncementMatrix(t *testing.T) {
	t.Parallel()

	golden.Assert(t, "announcement_matrix", renderAnnouncementMatrix())
}

func renderAnnouncementMatrix() string {
	var b strings.Builder
	b.WriteString(announcementPreamble)

	header := announcementRow("Role", "Door", "Scoped to",
		"group 1", "group 2", "prog. 1", "prog. 2", "colleagues", "leads", "all accounts")
	b.WriteString(header)
	b.WriteString(rule(header))

	previous := ""
	for _, c := range callers() {
		if previous != "" && c.label != previous {
			b.WriteString("\n")
		}
		previous = c.label

		scoped := "—"
		if c.scoped != "" {
			scoped = c.scoped
		}
		may := func(a policy.AnnouncementAudience) string {
			return answer(policy.MaySendAnnouncement(c.actor, a))
		}
		b.WriteString(announcementRow(c.label, c.door, scoped,
			may(policy.AnnouncementAudience{Kind: policy.AudienceSubjectGroup, SubjectGroupID: groupOne}),
			may(policy.AnnouncementAudience{Kind: policy.AudienceSubjectGroup, SubjectGroupID: groupTwo}),
			may(policy.AnnouncementAudience{Kind: policy.AudienceProgramme, ProgrammeID: programmeOne}),
			may(policy.AnnouncementAudience{Kind: policy.AudienceProgramme, ProgrammeID: programmeTwo}),
			may(policy.AnnouncementAudience{Kind: policy.AudienceColleagues}),
			may(policy.AnnouncementAudience{Kind: policy.AudienceLeads}),
			may(policy.AnnouncementAudience{Kind: policy.AudienceAllAccounts}),
		))
	}
	return b.String()
}

// TestAnnouncementSenderReadsTheProgrammesRows is the argument for the PROGRAMME audience, as a
// property: whoever may write to a programme's teachers may already read, in that same session,
// every unpublished wish and assignment that audience is made of. The recipient count tells them
// nothing new.
func TestAnnouncementSenderReadsTheProgrammesRows(t *testing.T) {
	t.Parallel()

	wish := policy.Wish{OwnerID: testdata.Zwei.ID(), ProgrammeID: programmeOne, SubjectGroupID: groupTwo}
	assignment := policy.Assignment{AssigneeID: testdata.Zwei.ID(), ProgrammeID: programmeOne,
		SubjectGroupID: groupTwo}
	audience := policy.AnnouncementAudience{Kind: policy.AudienceProgramme, ProgrammeID: programmeOne}

	for _, c := range callers() {
		if !policy.MaySendAnnouncement(c.actor, audience) {
			continue
		}
		if !policy.CanSeeWish(c.actor, unpublished, wish) {
			t.Errorf("%s %s %s may write to programme 1 but not read its unpublished wishes",
				c.label, c.door, c.scoped)
		}
		if !policy.CanSeeAssignment(c.actor, unpublished, assignment) {
			t.Errorf("%s %s %s may write to programme 1 but not read its unpublished assignments",
				c.label, c.door, c.scoped)
		}
	}
}

// TestNoTokenSendsAnnouncements holds the second line of the browser-only rule: even a full-reach
// token of the dean's office and of an administrator writes to nobody.
func TestNoTokenSendsAnnouncements(t *testing.T) {
	t.Parallel()

	for _, fullReach := range []bool{false, true} {
		actor := testdata.Vier.Actor(principal.KindToken,
			string(policy.RoleDeansOffice), string(policy.RoleAdmin))
		actor.FullReach = fullReach
		for _, kind := range policy.AnnouncementAudienceKinds() {
			audience := policy.AnnouncementAudience{Kind: kind, SubjectGroupID: groupOne,
				ProgrammeID: programmeOne}
			if policy.MaySendAnnouncement(actor, audience) {
				t.Errorf("a token (full reach %v) may write to %s", fullReach, kind)
			}
		}
	}
}

var announcementWidths = []int{20, 12, 12, 8, 8, 8, 8, 11, 6}

func announcementRow(cells ...string) string {
	var b strings.Builder
	for i, c := range cells {
		if i == len(cells)-1 {
			b.WriteString(c)
			break
		}
		b.WriteString(pad(c, announcementWidths[i]))
	}
	b.WriteString("\n")
	return b.String()
}

const announcementPreamble = `Announcements — who may write to which audience
===============================================

Generated from internal/policy (TestAnnouncementMatrix). Do not edit by hand:

    go test ./internal/policy/ -update-golden

Only in a browser session: no token sends an announcement, not even one with full reach. A
subject group or a programme is written to by its own lead and by the dean's office; the whole
faculty by the dean's office; the leads by anybody who leads; every account by an administrator.
Whoever may write to a programme reads the wishes and assignments its audience is made of
(TestAnnouncementSenderReadsTheProgrammesRows).

`
