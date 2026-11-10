package mail_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/golden"
	"github.com/obcode/tallox.go/internal/mail"
)

func TestMarkdownCarriesNoMarkupOfItsOwn(t *testing.T) {
	t.Parallel()

	for _, body := range []string{
		`<script>alert(1)</script>`,
		`<img src="https://x.example/track.gif">`,
		`[klick](javascript:alert(1))`,
		`[klick](https://x.example/" onmouseover="alert(1))`,
		`<a href="https://x.example">x</a>`,
	} {
		got := mail.MarkdownHTML(body)
		for _, forbidden := range []string{"<script", "<img", `href="javascript`, `onmouseover="`, `<a href="https://x.example">x`} {
			if strings.Contains(got, forbidden) {
				t.Errorf("MarkdownHTML(%q) = %q, contains %q", body, got, forbidden)
			}
		}
	}
}

func TestMarkdownRendersWhatAnAnnouncementUses(t *testing.T) {
	t.Parallel()

	got := mail.MarkdownHTML("Hallo **alle**,\n\n- eins\n- zwei\n\n1. erst\n2. dann\n\n" +
		"Mehr unter https://tallox.example.org/ablauf. Oder [hier](https://tallox.example.org).")
	for _, want := range []string{
		"<strong>alle</strong>",
		"<ul", "<li style=\"margin:0 0 6px 0;\">eins</li>",
		"<ol", "<li style=\"margin:0 0 6px 0;\">dann</li>",
		`<a href="https://tallox.example.org/ablauf" `, `</a>.`,
		`<a href="https://tallox.example.org" style="color:#1d4ed8;text-decoration:underline;">hier</a>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
}

// TestMailRenderings keeps every mail's wording a reviewable diff.
// Re-record with: go test ./internal/mail/ -update-golden — and then read the diff.
func TestMailRenderings(t *testing.T) {
	t.Parallel()

	for name, m := range mail.PreviewMails(mail.NewRenderer("https://tallox.example.org")) {
		golden.Assert(t, "mail_"+name, "Subject: "+m.Subject+"\nReply-To: "+m.ReplyTo+"\n\n"+m.Text)
		if !strings.Contains(m.HTML, "<!DOCTYPE html>") || !strings.Contains(m.HTML, `lang="de"`) {
			t.Errorf("%s: the HTML part is not a document", name)
		}
	}
}

// TestNotificationsNameNoOtherPerson is the export rule on the rendered mail: a digest names its
// recipient and the facts of the process, and nobody else.
func TestNotificationsNameNoOtherPerson(t *testing.T) {
	t.Parallel()

	mails := mail.PreviewMails(mail.NewRenderer("https://tallox.example.org"))
	for _, name := range []string{"notification-single", "notification-digest"} {
		m := mails[name]
		for _, forbidden := range []string{"Zwei", "Drei", "Vier", "@example.org"} {
			if strings.Contains(m.HTML, forbidden) || strings.Contains(m.Text, forbidden) {
				t.Errorf("%s mentions %q", name, forbidden)
			}
		}
	}
}

func TestSMTPMessageHeaders(t *testing.T) {
	t.Parallel()

	smtp := mail.NewSMTP(mail.SMTPConfig{From: "noreply@example.org", BaseURL: "https://tallox.example.org"})
	msg, err := smtp.Message(domain.OutgoingMail{
		To: "prof.eins@example.org", ToName: "Eins Beispiel", Category: domain.MailAnnouncement,
		ReplyTo: "prof.vier@example.org", Subject: "[Tallox] Hallo", HTML: "<p>Hallo</p>", Text: "Hallo",
	})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := msg.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	raw := buf.String()
	for _, want := range []string{
		"From: <noreply@example.org>", "prof.eins@example.org", "Reply-To: <prof.vier@example.org>",
		"List-Id: Tallox <tallox.example.org>", "Precedence: bulk", "multipart/alternative",
		"text/plain", "text/html",
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("missing %q in the message", want)
		}
	}
}

func TestSMTPDryRunRedirectsEveryMail(t *testing.T) {
	t.Parallel()

	smtp := mail.NewSMTP(mail.SMTPConfig{From: "noreply@example.org", TestMail: "test@example.org"})
	msg, err := smtp.Message(domain.OutgoingMail{To: "prof.eins@example.org", Category: domain.MailNotification,
		Subject: "x", HTML: "x", Text: "x"})
	if err != nil {
		t.Fatal(err)
	}
	to := msg.GetToString()
	if len(to) != 1 || !strings.Contains(to[0], "test@example.org") {
		t.Errorf("dry run sends to %v", to)
	}
}
