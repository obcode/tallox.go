// Package mail turns what the dispatcher and the announcements have to say into mails, and hands
// them to an SMTP server.
//
// The German in this package is the exception the repository rule allows for what a person reads:
// a mail is read like the GUI is, and translating it in tallox.gui is not possible because the GUI
// never sees it. Keep it to the same register as the GUI — "Du", gender-fair without gender
// characters ("Kolleginnen und Kollegen", "alle") — and keep the facts to what every account may
// read: a mail is an export (see the notifications migration).
package mail

import (
	"bytes"
	"embed"
	"fmt"
	htmltemplate "html/template"
	"net/url"
	"slices"
	"strings"
	texttemplate "text/template"
	"time"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/policy"
)

//go:embed templates/*.tmpl
var templates embed.FS

var (
	htmlTemplate = htmltemplate.Must(htmltemplate.ParseFS(templates, "templates/mail.html.tmpl"))
	textTemplate = texttemplate.Must(texttemplate.ParseFS(templates, "templates/mail.txt.tmpl"))
)

// Renderer renders mails with links into one installation.
type Renderer struct {
	base string
}

// NewRenderer wires one up. baseURL is where the GUI is reached, without a trailing slash.
func NewRenderer(baseURL string) *Renderer {
	return &Renderer{base: strings.TrimRight(baseURL, "/")}
}

var _ domain.MailRenderer = (*Renderer)(nil)

type view struct {
	Label     string
	Title     string
	Preheader string
	Accent    string
	Greeting  string
	Intro     string
	Body      htmltemplate.HTML
	BodyText  string
	Sections  []section
	Button    *link
	Footer    []string
	Manage    *link
}

// Underline is the plain-text heading rule.
func (v view) Underline() string {
	return strings.Repeat("=", len([]rune(v.Title)))
}

type section struct {
	Heading string
	Items   []item
}

type item struct {
	Text string
	Meta string
	Link string
}

type link struct {
	Label string
	URL   string
}

const (
	accentNotification = "#0f766e"
	accentAnnouncement = "#1d4ed8"
)

// Digest renders one person's notifications.
func (r *Renderer) Digest(d domain.Digest) (domain.Mail, error) {
	v := view{
		Label:    "Benachrichtigung",
		Accent:   accentNotification,
		Greeting: greeting(d.PersonName),
		Manage:   &link{Label: "Benachrichtigungen verwalten", URL: r.url("/konto/benachrichtigungen")},
	}

	byKind := map[domain.NotificationKind][]domain.DigestItem{}
	var reasons []string
	for _, it := range d.Items {
		byKind[it.Event.Kind] = append(byKind[it.Event.Kind], it)
		if reason := reasonText(it.Reason); reason != "" && !slices.Contains(reasons, reason) {
			reasons = append(reasons, reason)
		}
	}
	for _, kind := range domain.NotificationKinds() {
		items := byKind[kind]
		if len(items) == 0 {
			continue
		}
		s := section{Heading: heading(kind, len(items))}
		for _, it := range items {
			s.Items = append(s.Items, r.eventItem(it.Event))
		}
		v.Sections = append(v.Sections, s)
	}

	if len(d.Items) == 1 {
		v.Title = r.singleTitle(d.Items[0].Event)
		v.Intro = "das ist in Tallox jetzt offiziell:"
		if s := v.Sections[0]; len(s.Items) == 1 && s.Items[0].Link != "" {
			v.Button = &link{Label: buttonLabel(d.Items[0].Event.Kind), URL: s.Items[0].Link}
		}
	} else {
		v.Title = "Neues aus der Einsatzplanung"
		v.Intro = fmt.Sprintf("in Tallox ist offiziell geworden, was Du abonniert hast — %d Neuigkeiten:",
			len(d.Items))
	}
	v.Preheader = v.Title

	if len(reasons) > 0 {
		v.Footer = append(v.Footer, "Du bekommst diese Mail, weil Du "+joinGerman(reasons)+" abonniert hast.")
	}
	v.Footer = append(v.Footer, "Gemeldet wird nur, was offiziell ist — eine Freigabe, eine Wunschrunde, "+
		"eine Veröffentlichung. Wer sich wofür interessiert, steht in keiner Benachrichtigung.")

	subject := v.Title
	if len(d.Items) > 1 {
		subject = fmt.Sprintf("Neues aus der Einsatzplanung (%d)", len(d.Items))
	}
	return r.render(domain.MailNotification, "", "[Tallox] "+subject, v)
}

// Announcement renders an announcement.
func (r *Renderer) Announcement(a domain.AnnouncementMail) (domain.Mail, error) {
	v := view{
		Label:     "Rundmail",
		Accent:    accentAnnouncement,
		Title:     a.Subject,
		Preheader: "Rundmail von " + a.SenderName + " " + audienceText(a.Audience),
		Body:      htmltemplate.HTML(MarkdownHTML(a.Body)), //nolint:gosec // MarkdownHTML escapes everything it does not produce
		BodyText:  strings.TrimSpace(a.Body),
		Footer: []string{
			"Rundmail von " + a.SenderName + " " + audienceText(a.Audience) + ", verschickt über Tallox. " +
				"Antworten gehen direkt an " + a.SenderName + ".",
			"Rundmails lassen sich nicht abbestellen: Sie kommen von den Leitungen der Planung.",
		},
		Manage: &link{Label: "Alle Rundmails in Tallox", URL: r.url("/mitteilungen")},
	}
	return r.render(domain.MailAnnouncement, a.SenderMail, "[Tallox] "+a.Subject, v)
}

func (r *Renderer) render(category domain.MailCategory, replyTo, subject string, v view) (domain.Mail, error) {
	var h, t bytes.Buffer
	if err := htmlTemplate.Execute(&h, v); err != nil {
		return domain.Mail{}, fmt.Errorf("cannot render the HTML part: %w", err)
	}
	if err := textTemplate.Execute(&t, v); err != nil {
		return domain.Mail{}, fmt.Errorf("cannot render the text part: %w", err)
	}
	return domain.Mail{Category: category, ReplyTo: replyTo, Subject: subject,
		HTML: h.String(), Text: tidy(t.String())}, nil
}

func (r *Renderer) url(path string) string { return r.base + path }

// eventItem is one line: what happened, where, and the link to it.
func (r *Renderer) eventItem(e domain.NotificationEvent) item {
	sem := SemesterLabel(e.SemesterCode)
	switch e.Kind {
	case domain.NotifyModuleOffered, domain.NotifyModuleWithdrawn:
		return item{Text: moduleName(e), Meta: joinDot(cohort(e), sem), Link: r.moduleURL(e)}
	case domain.NotifyDemandReleased:
		return item{Text: programmeName(e), Meta: sem + " · Wünsche lassen sich jetzt eintragen",
			Link: r.url("/wuensche")}
	case domain.NotifyReviewCompleted:
		return item{Text: programmeName(e), Meta: sem + " · Review abgeschlossen", Link: r.url("/statistik")}
	case domain.NotifyWishWindowChanged:
		state := "geöffnet — Wünsche lassen sich eintragen und ändern"
		if e.Detail == "SHUT" {
			state = "geschlossen — die Fachgruppe nimmt derzeit keine Wünsche an"
		}
		return item{Text: subjectGroupName(e), Meta: sem + " · Wunschrunde " + state, Link: r.url("/wuensche")}
	case domain.NotifyWishesPublished:
		return item{Text: "Wünsche " + sem, Meta: "Ab jetzt sehen alle, wer sich wofür eingetragen hat.",
			Link: r.url("/wuensche/kollegium")}
	case domain.NotifyAssignmentsPublished:
		return item{Text: "Zuteilungen " + sem, Meta: "Deine Zuteilungen und die aller anderen sind jetzt sichtbar.",
			Link: r.url("/konto/zuteilungen")}
	case domain.NotifyConfirmationDue:
		if due, ok := strings.CutPrefix(e.Detail, "REMINDER:"); ok {
			return item{Text: "Bitte bestätige Deine Zuteilungen für " + sem,
				Meta: "bis " + DateLabel(due) + " — Deine Bestätigung fehlt noch oder ist veraltet.",
				Link: r.url("/konto/zuteilungen")}
		}
		return item{Text: "Zuteilungen " + sem + " bestätigen",
			Meta: "Das Dekanat bittet um Bestätigung bis " + DateLabel(e.Detail) + ".",
			Link: r.url("/konto/zuteilungen")}
	case domain.NotifyDesiredHoursFrozen:
		return item{Text: "Wunsch-SWS " + sem + " eingefroren",
			Meta: "Ändern kann sie ab jetzt nur noch das Dekanat.", Link: r.url("/wuensche")}
	case domain.NotifyDesiredHoursPublished:
		return item{Text: "Wunsch-SWS " + sem + " veröffentlicht",
			Meta: "Die Wunsch-SWS im Kollegium sind jetzt für alle sichtbar.", Link: r.url("/wuensche/kollegium")}
	case domain.NotifyPhaseAdvanced:
		return item{Text: sem + ": " + PhaseLabel(e.Detail), Link: r.url("/ablauf")}
	case domain.NotifyAdvanceWishResolved:
		if e.Detail == "CONVERTED" {
			return item{Text: moduleName(e), Meta: joinDot(programmeCode(e), sem) +
				" · Deine Vormerkung ist jetzt ein Wunsch.", Link: r.url("/wuensche")}
		}
		return item{Text: moduleName(e), Meta: joinDot(programmeCode(e), sem) +
			" · nach aktuellem Stand dort nicht angeboten.", Link: r.url("/wuensche/vormerkungen")}
	}
	return item{Text: string(e.Kind)}
}

func (r *Renderer) moduleURL(e domain.NotificationEvent) string {
	if e.Module == nil {
		return r.url("/module")
	}
	return r.url("/module/" + url.PathEscape(e.Module.ID.String()))
}

// singleTitle is the headline of a mail about one event: the event itself, as a sentence.
func (r *Renderer) singleTitle(e domain.NotificationEvent) string {
	sem := SemesterLabel(e.SemesterCode)
	switch e.Kind {
	case domain.NotifyModuleOffered:
		return moduleName(e) + " wird im " + sem + " angeboten"
	case domain.NotifyModuleWithdrawn:
		return moduleName(e) + " wird im " + sem + " doch nicht angeboten"
	case domain.NotifyDemandReleased:
		return programmeCode(e) + " hat den Bedarf für das " + sem + " freigegeben"
	case domain.NotifyReviewCompleted:
		return programmeCode(e) + " hat das Review für das " + sem + " abgeschlossen"
	case domain.NotifyWishWindowChanged:
		if e.Detail == "SHUT" {
			return "Wunschrunde " + subjectGroupName(e) + " geschlossen"
		}
		return "Wunschrunde " + subjectGroupName(e) + " geöffnet"
	case domain.NotifyWishesPublished:
		return "Die Wünsche für das " + sem + " sind veröffentlicht"
	case domain.NotifyAssignmentsPublished:
		return "Die Zuteilungen für das " + sem + " sind veröffentlicht"
	case domain.NotifyConfirmationDue:
		if strings.HasPrefix(e.Detail, "REMINDER:") {
			return "Erinnerung: Zuteilungen für das " + sem + " bestätigen"
		}
		return "Zuteilungen für das " + sem + " bitte bis " + DateLabel(e.Detail) + " bestätigen"
	case domain.NotifyDesiredHoursFrozen:
		return "Die Wunsch-SWS für das " + sem + " sind eingefroren"
	case domain.NotifyDesiredHoursPublished:
		return "Die Wunsch-SWS für das " + sem + " sind veröffentlicht"
	case domain.NotifyPhaseAdvanced:
		return "Das " + sem + " ist jetzt in der Phase „" + PhaseLabel(e.Detail) + "“"
	case domain.NotifyAdvanceWishResolved:
		if e.Detail == "CONVERTED" {
			return "Deine Vormerkung " + moduleName(e) + " ist jetzt ein Wunsch"
		}
		return moduleName(e) + " wird im " + sem + " in " + programmeCode(e) + " nicht angeboten"
	}
	return "Neues aus der Einsatzplanung"
}

func heading(kind domain.NotificationKind, n int) string {
	switch kind {
	case domain.NotifyModuleOffered:
		return "Wird angeboten"
	case domain.NotifyModuleWithdrawn:
		return "Wird doch nicht angeboten"
	case domain.NotifyDemandReleased:
		return "Bedarf freigegeben"
	case domain.NotifyReviewCompleted:
		return "Review abgeschlossen"
	case domain.NotifyWishWindowChanged:
		if n == 1 {
			return "Wunschrunde"
		}
		return "Wunschrunden"
	case domain.NotifyWishesPublished, domain.NotifyAssignmentsPublished,
		domain.NotifyDesiredHoursFrozen, domain.NotifyDesiredHoursPublished:
		return "Veröffentlicht und festgelegt"
	case domain.NotifyConfirmationDue:
		return "Bestätigung der Zuteilungen"
	case domain.NotifyPhaseAdvanced:
		return "Neue Phase"
	case domain.NotifyAdvanceWishResolved:
		if n == 1 {
			return "Deine Vormerkung"
		}
		return "Deine Vormerkungen"
	}
	return string(kind)
}

func buttonLabel(kind domain.NotificationKind) string {
	switch kind {
	case domain.NotifyModuleOffered, domain.NotifyModuleWithdrawn:
		return "Zum Modul"
	case domain.NotifyDemandReleased, domain.NotifyWishWindowChanged, domain.NotifyAdvanceWishResolved:
		return "Zu den Wünschen"
	case domain.NotifyAssignmentsPublished, domain.NotifyConfirmationDue:
		return "Zu meinen Zuteilungen"
	case domain.NotifyPhaseAdvanced:
		return "Zum Ablauf"
	}
	return "In Tallox öffnen"
}

// KindLabel is how a screen and a mail name a kind of event.
func KindLabel(kind domain.NotificationKind) string {
	switch kind {
	case domain.NotifyModuleOffered:
		return "„wird angeboten“"
	case domain.NotifyModuleWithdrawn:
		return "„wird doch nicht angeboten“"
	case domain.NotifyDemandReleased:
		return "„Bedarf freigegeben“"
	case domain.NotifyWishWindowChanged:
		return "„Wunschrunde geöffnet oder geschlossen“"
	case domain.NotifyWishesPublished:
		return "„Wünsche veröffentlicht“"
	case domain.NotifyAssignmentsPublished:
		return "„Zuteilungen veröffentlicht“"
	case domain.NotifyConfirmationDue:
		return "„Bestätigung der Zuteilungen“"
	case domain.NotifyDesiredHoursFrozen:
		return "„Wunsch-SWS eingefroren“"
	case domain.NotifyDesiredHoursPublished:
		return "„Wunsch-SWS veröffentlicht“"
	case domain.NotifyPhaseAdvanced:
		return "„neue Phase“"
	case domain.NotifyReviewCompleted:
		return "„Review abgeschlossen“"
	case domain.NotifyAdvanceWishResolved:
		return "„Vormerkung entschieden“"
	}
	return string(kind)
}

func targetSetLabel(set domain.NotificationTargetSet) string {
	switch set {
	case domain.TargetMyAdvanceWishes:
		return "für Deine Vormerkungen"
	case domain.TargetMyWishes:
		return "für Deine Wünsche"
	case domain.TargetMyAssignments:
		return "für Deine Zuteilungen"
	case domain.TargetMyCompetences:
		return "für Deine Kompetenzen"
	case domain.TargetMySubjectGroups:
		return "für Deine Fachgruppen"
	case domain.TargetMyProgrammes:
		return "für Deine Studiengänge"
	}
	return ""
}

// reasonText is one subscription, as the footer names it: „wird angeboten“ für Softwareentwicklung II.
func reasonText(r domain.SubscriptionReason) string {
	if r.Kind == "" {
		return ""
	}
	parts := []string{KindLabel(r.Kind)}
	switch {
	case r.ModuleName != "":
		parts = append(parts, "für "+r.ModuleName)
	case r.ProgrammeCode != "":
		parts = append(parts, "für "+r.ProgrammeCode)
	case r.SubjectGroupName != "":
		parts = append(parts, "für die Fachgruppe "+r.SubjectGroupName)
	case r.TargetSet != "":
		parts = append(parts, targetSetLabel(r.TargetSet))
	}
	if r.SemesterCode != "" {
		parts = append(parts, "im "+SemesterLabel(r.SemesterCode))
	}
	return strings.Join(parts, " ")
}

func audienceText(a domain.AnnouncementAudience) string {
	switch a.Kind {
	case policy.AudienceSubjectGroup:
		if a.SubjectGroup != nil {
			return "an die Fachgruppe " + a.SubjectGroup.Name
		}
	case policy.AudienceProgramme:
		if a.Programme != nil {
			return "an alle, die im " + SemesterLabel(a.SemesterCode) + " in " + a.Programme.Code +
				" lehren oder sich dafür eingetragen haben"
		}
	case policy.AudienceColleagues:
		return "an das Kollegium"
	case policy.AudienceLeads:
		return "an alle Fachgruppen- und Studiengangsleitungen und das Dekanat"
	case policy.AudienceAllAccounts:
		return "an alle mit einem Tallox-Konto"
	}
	return ""
}

func greeting(name string) string {
	if name = strings.TrimSpace(name); name == "" {
		return "Hallo,"
	}
	return "Hallo " + name + ","
}

func moduleName(e domain.NotificationEvent) string {
	if e.Module == nil || e.Module.Name == "" {
		return "Ein Modul"
	}
	return e.Module.Name
}

func programmeCode(e domain.NotificationEvent) string {
	if e.Programme == nil {
		return ""
	}
	return e.Programme.Code
}

func programmeName(e domain.NotificationEvent) string {
	if e.Programme == nil {
		return ""
	}
	if e.Programme.Title == "" {
		return e.Programme.Code
	}
	return e.Programme.Code + " · " + e.Programme.Title
}

func subjectGroupName(e domain.NotificationEvent) string {
	if e.SubjectGroup == nil {
		return ""
	}
	return e.SubjectGroup.Name
}

// cohort is the label a colleague reads: IF + track, or the programme alone. The programme
// semester is not stored on the event, so the label stays programme and track — IF A.
func cohort(e domain.NotificationEvent) string {
	code := programmeCode(e)
	if e.Detail == "" {
		return code
	}
	return code + " Zug " + e.Detail
}

// SemesterLabel renders 2026-WS as „WiSe 2026/27“ and 2027-SS as „SoSe 2027“.
func SemesterLabel(code string) string {
	if len(code) != 7 || code[4] != '-' {
		return code
	}
	year := code[:4]
	switch code[5:] {
	case "SS":
		return "SoSe " + year
	case "WS":
		var y int
		if _, err := fmt.Sscanf(year, "%d", &y); err != nil {
			return code
		}
		return fmt.Sprintf("WiSe %d/%02d", y, (y+1)%100)
	}
	return code
}

// PhaseLabel is the faculty's name for a phase.
func PhaseLabel(phase string) string {
	switch phase {
	case string(policy.PhaseDemandPlanning):
		return "Bedarfsplanung"
	case string(policy.PhaseWishes):
		return "Wunschphase"
	case string(policy.PhaseAssignment):
		return "Zuteilung"
	case string(policy.PhaseReview):
		return "Studiengangs-Review"
	case string(policy.PhaseFinal):
		return "abgeschlossen"
	}
	return phase
}

// DateLabel renders 2026-12-04 as 04.12.2026.
func DateLabel(iso string) string {
	d, err := time.Parse("2006-01-02", iso)
	if err != nil {
		return iso
	}
	return d.Format("02.01.2006")
}

func joinDot(parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " · ")
}

// joinGerman joins „a“, „a und b“, „a, b und c“.
func joinGerman(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " und " + parts[len(parts)-1]
}

// tidy collapses the blank lines the text template leaves behind.
func tidy(s string) string {
	lines := strings.Split(s, "\n")
	var out []string
	blank := 0
	for _, l := range lines {
		l = strings.TrimRight(l, " ")
		if l == "" {
			blank++
			if blank > 1 {
				continue
			}
		} else {
			blank = 0
		}
		out = append(out, l)
	}
	return strings.TrimSpace(strings.Join(out, "\n")) + "\n"
}
