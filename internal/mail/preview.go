package mail

import (
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/policy"
)

// PreviewMails is one of each kind of mail, from invented data — the cast of internal/testdata,
// never anybody real. For -mail-preview, and for the golden test that turns every change of
// wording into a reviewable diff.
func PreviewMails(r *Renderer) map[string]domain.Mail {
	module := &domain.ModuleRef{ID: uuid.MustParse("aaaaaaaa-0000-4000-8000-000000000001"),
		Name: "Softwareentwicklung II", HomeProgrammeCode: "IF"}
	other := &domain.ModuleRef{ID: uuid.MustParse("aaaaaaaa-0000-4000-8000-000000000002"),
		Name: "Verteilte Systeme", HomeProgrammeCode: "IF"}
	programme := &domain.Programme{Code: "IF", Title: "Informatik"}
	group := &domain.SubjectGroupRef{Name: "Softwaretechnik", Code: "SWE"}
	at := time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)

	single := domain.Digest{PersonName: "Eins Beispiel", Items: []domain.DigestItem{{
		Event: domain.NotificationEvent{Kind: domain.NotifyModuleOffered, SemesterCode: "2027-SS",
			Module: module, Programme: programme, Detail: "A", OccurredAt: at},
		Reason: domain.SubscriptionReason{Kind: domain.NotifyModuleOffered, ModuleName: module.Name},
	}}}
	digest := domain.Digest{PersonName: "Eins Beispiel", Items: []domain.DigestItem{
		{Event: domain.NotificationEvent{Kind: domain.NotifyDemandReleased, SemesterCode: "2027-SS",
			Programme: programme, OccurredAt: at},
			Reason: domain.SubscriptionReason{Kind: domain.NotifyDemandReleased, ProgrammeCode: "IF"}},
		{Event: domain.NotificationEvent{Kind: domain.NotifyModuleOffered, SemesterCode: "2027-SS",
			Module: module, Programme: programme, Detail: "A", OccurredAt: at},
			Reason: domain.SubscriptionReason{Kind: domain.NotifyModuleOffered, TargetSet: domain.TargetMyCompetences}},
		{Event: domain.NotificationEvent{Kind: domain.NotifyModuleOffered, SemesterCode: "2027-SS",
			Module: other, Programme: programme, OccurredAt: at},
			Reason: domain.SubscriptionReason{Kind: domain.NotifyModuleOffered, TargetSet: domain.TargetMyCompetences}},
		{Event: domain.NotificationEvent{Kind: domain.NotifyWishWindowChanged, SemesterCode: "2027-SS",
			SubjectGroup: group, Detail: "OPEN", OccurredAt: at},
			Reason: domain.SubscriptionReason{Kind: domain.NotifyWishWindowChanged, TargetSet: domain.TargetMySubjectGroups}},
		{Event: domain.NotificationEvent{Kind: domain.NotifyAdvanceWishResolved, SemesterCode: "2027-SS",
			Module: other, Programme: programme, Detail: "NOT_OFFERED", OccurredAt: at},
			Reason: domain.SubscriptionReason{Kind: domain.NotifyAdvanceWishResolved, TargetSet: domain.TargetMyAdvanceWishes}},
		{Event: domain.NotificationEvent{Kind: domain.NotifyConfirmationDue, SemesterCode: "2026-WS",
			Detail: "REMINDER:2026-12-04", OccurredAt: at},
			Reason: domain.SubscriptionReason{Kind: domain.NotifyConfirmationDue, TargetSet: domain.TargetMyAssignments}},
	}}
	announcement := domain.AnnouncementMail{
		SenderName: "Vier Beispiel", SenderMail: "prof.vier@example.org",
		Audience: domain.AnnouncementAudience{Kind: policy.AudienceSubjectGroup, SubjectGroup: group},
		Subject:  "Wunschrunde Sommersemester",
		Body: "Liebe Kolleginnen und Kollegen,\n\ndie Wunschrunde für das **Sommersemester 2027** ist offen " +
			"bis Freitag. Bitte tragt Eure Wünsche ein:\n\n- Vorlesungen und Praktika\n- auch FWPs\n\n" +
			"Details stehen unter https://tallox.example.org/ablauf.\n\nViele Grüße",
	}

	out := map[string]domain.Mail{}
	add := func(name string, m domain.Mail, err error) {
		if err != nil {
			// The data is fixed and the templates are parsed at init: a failure here is a broken
			// template, which every test run would show first.
			panic(fmt.Sprintf("cannot render preview %s: %v", name, err))
		}
		out[name] = m
	}
	m, err := r.Digest(single)
	add("notification-single", m, err)
	m, err = r.Digest(digest)
	add("notification-digest", m, err)
	m, err = r.Announcement(announcement)
	add("announcement", m, err)
	return out
}
