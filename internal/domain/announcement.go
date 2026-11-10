package domain

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
)

// Announcements: a mail from somebody who leads the planning to a group of people, sent from here
// and kept here. Who may write to whom is policy.MaySendAnnouncement; this file is the rest.
//
// Nobody subscribes to them and nobody unsubscribes: they are official post. Each recipient gets
// their own mail with nobody else in it, and replies go to the sender.

// MaxAnnouncementsPerDay bounds one sender. Ten is a busy day for a dean's office; a hundred is a
// script or a mistake, and the recipients would pay for either.
const MaxAnnouncementsPerDay = 10

var (
	// ErrAudienceNotAllowed is an audience the sender may not write to.
	ErrAudienceNotAllowed = errors.New("an diese Gruppe kannst Du keine Rundmail schicken")
	// ErrAudienceEmpty is an audience that reaches nobody.
	ErrAudienceEmpty = errors.New("diese Gruppe erreicht derzeit niemanden")
	// ErrAnnouncementInvalid is an empty or overlong subject or body, or an audience missing what
	// its kind needs.
	ErrAnnouncementInvalid = errors.New("Betreff (bis 200 Zeichen) und Text (bis 20 000 Zeichen) " + //nolint:staticcheck // ST1005: the sentence starts with a noun
		"müssen ausgefüllt sein")
	// ErrAnnouncementRateLimited is the MaxAnnouncementsPerDay bound.
	ErrAnnouncementRateLimited = errors.New("heute sind schon zehn Rundmails von Dir verschickt worden")
)

// AnnouncementAudience is an audience, resolved: what a screen and a mail say about it.
type AnnouncementAudience struct {
	Kind         policy.AnnouncementAudienceKind
	SubjectGroup *SubjectGroupRef
	Programme    *Programme
	SemesterCode string
	// SemesterID is the row the PROGRAMME audience reads, uuid.Nil when nothing has been decided
	// about that semester yet — and then it reaches nobody.
	SemesterID uuid.UUID
	// State is that semester's, for the wish filter the recipients are read through.
	State policy.SemesterState
}

// Policy is the audience as the rule needs it.
func (a AnnouncementAudience) Policy() policy.AnnouncementAudience {
	out := policy.AnnouncementAudience{Kind: a.Kind}
	if a.SubjectGroup != nil {
		out.SubjectGroupID = a.SubjectGroup.ID
	}
	if a.Programme != nil {
		out.ProgrammeID = a.Programme.ID
	}
	return out
}

// AnnouncementAudienceInput is an audience on the way in.
type AnnouncementAudienceInput struct {
	Kind           policy.AnnouncementAudienceKind
	SubjectGroupID uuid.UUID
	ProgrammeCode  string
	SemesterCode   string
}

// Announcement is one sent.
type Announcement struct {
	ID       uuid.UUID
	Sender   Person
	Audience AnnouncementAudience
	Subject  string
	Body     string
	// RecipientCount is how many it went to, or -1 for a reader who is not its sender.
	RecipientCount int
	SentAt         time.Time
}

// AnnouncementMail is what the renderer needs.
type AnnouncementMail struct {
	SenderName string
	SenderMail string
	Audience   AnnouncementAudience
	Subject    string
	Body       string
}

// AnnouncementPreview is an announcement as it would go out.
type AnnouncementPreview struct {
	Mail           Mail
	RecipientCount int
}

// NewAnnouncement is everything stored for one, in one transaction with its mail.
type NewAnnouncement struct {
	SenderID   uuid.UUID
	Audience   AnnouncementAudience
	Subject    string
	Body       string
	Recipients []uuid.UUID
	Mail       Mail
}

// AnnouncementStore is what the service needs from persistence.
type AnnouncementStore interface {
	// ResolveAudience reads what an input names. ErrAnnouncementInvalid when its kind lacks what
	// it needs, ErrSubjectGroupNotFound / ErrProgrammeNotFound when that does not exist.
	ResolveAudience(ctx context.Context, in AnnouncementAudienceInput) (AnnouncementAudience, error)
	// Recipients are the active accounts an audience reaches today. The wishers among a
	// PROGRAMME audience are read through wishes, the sender's wish filter, like every read of
	// that table.
	Recipients(ctx context.Context, a AnnouncementAudience, wishes policy.WishFilter) ([]uuid.UUID, error)
	// SentSince counts a sender's announcements.
	SentSince(ctx context.Context, senderID uuid.UUID, since time.Time) (int, error)
	// CreateAnnouncement stores one with its recipients and queues a mail to each.
	CreateAnnouncement(ctx context.Context, a NewAnnouncement) (*Announcement, error)
	// Announcements are those a person sent or received, newest first.
	Announcements(ctx context.Context, personID uuid.UUID) ([]Announcement, error)
	// AudienceCandidates are the subject groups and programmes there are, for the list of
	// audiences — filtered by the policy, not here.
	AudienceCandidates(ctx context.Context) ([]SubjectGroupRef, []Programme, error)
	// PlanningSemesterCode is the semester the faculty is planning, or "".
	PlanningSemesterCode(ctx context.Context) (string, error)
}

// AnnouncementService is the business logic of announcements.
type AnnouncementService struct {
	store    AnnouncementStore
	renderer MailRenderer
	now      func() time.Time
}

// NewAnnouncementService wires one up.
func NewAnnouncementService(s AnnouncementStore, r MailRenderer, now func() time.Time) *AnnouncementService {
	if now == nil {
		now = time.Now
	}
	return &AnnouncementService{store: s, renderer: r, now: now}
}

// Audiences are those the caller may write to. PROGRAMME audiences for semesterCode, or for the
// planning semester when it is empty — and none when nobody has said which that is.
func (s *AnnouncementService) Audiences(ctx context.Context, actor principal.Actor,
	semesterCode string) ([]AnnouncementAudience, error) {
	if !actor.Authenticated() {
		return nil, ErrNotAuthenticated
	}
	if semesterCode == "" {
		code, err := s.store.PlanningSemesterCode(ctx)
		if err != nil {
			return nil, err
		}
		semesterCode = code
	}
	groups, programmes, err := s.store.AudienceCandidates(ctx)
	if err != nil {
		return nil, err
	}

	var out []AnnouncementAudience
	for _, g := range groups {
		a := AnnouncementAudience{Kind: policy.AudienceSubjectGroup, SubjectGroup: &g}
		if policy.MaySendAnnouncement(actor, a.Policy()) {
			out = append(out, a)
		}
	}
	if semesterCode != "" {
		for _, p := range programmes {
			a := AnnouncementAudience{Kind: policy.AudienceProgramme, Programme: &p, SemesterCode: semesterCode}
			if policy.MaySendAnnouncement(actor, a.Policy()) {
				out = append(out, a)
			}
		}
	}
	for _, kind := range []policy.AnnouncementAudienceKind{
		policy.AudienceColleagues, policy.AudienceLeads, policy.AudienceAllAccounts,
	} {
		a := AnnouncementAudience{Kind: kind}
		if policy.MaySendAnnouncement(actor, a.Policy()) {
			out = append(out, a)
		}
	}
	return out, nil
}

// List is what the caller sent or received.
func (s *AnnouncementService) List(ctx context.Context, actor principal.Actor) ([]Announcement, error) {
	if !actor.Authenticated() {
		return nil, ErrNotAuthenticated
	}
	list, err := s.store.Announcements(ctx, actor.ID)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].Sender.ID != actor.ID {
			list[i].RecipientCount = -1
		}
	}
	return list, nil
}

// Preview renders one and counts its audience, without sending.
func (s *AnnouncementService) Preview(ctx context.Context, actor principal.Actor,
	in AnnouncementAudienceInput, subject, body string) (*AnnouncementPreview, error) {
	_, recipients, mail, err := s.prepare(ctx, actor, in, subject, body)
	if err != nil {
		return nil, err
	}
	return &AnnouncementPreview{Mail: mail, RecipientCount: len(recipients)}, nil
}

// Send sends one.
func (s *AnnouncementService) Send(ctx context.Context, actor principal.Actor,
	in AnnouncementAudienceInput, subject, body string) (*Announcement, error) {
	audience, recipients, mail, err := s.prepare(ctx, actor, in, subject, body)
	if err != nil {
		return nil, err
	}
	if len(recipients) == 0 {
		return nil, ErrAudienceEmpty
	}
	sent, err := s.store.SentSince(ctx, actor.ID, s.now().Add(-24*time.Hour))
	if err != nil {
		return nil, err
	}
	if sent >= MaxAnnouncementsPerDay {
		return nil, ErrAnnouncementRateLimited
	}
	return s.store.CreateAnnouncement(ctx, NewAnnouncement{
		SenderID:   actor.ID,
		Audience:   audience,
		Subject:    strings.TrimSpace(subject),
		Body:       strings.TrimSpace(body),
		Recipients: recipients,
		Mail:       mail,
	})
}

func (s *AnnouncementService) prepare(ctx context.Context, actor principal.Actor,
	in AnnouncementAudienceInput, subject, body string,
) (AnnouncementAudience, []uuid.UUID, Mail, error) {
	if !actor.Authenticated() {
		return AnnouncementAudience{}, nil, Mail{}, ErrNotAuthenticated
	}
	subject, body = strings.TrimSpace(subject), strings.TrimSpace(body)
	if subject == "" || utf8.RuneCountInString(subject) > 200 || strings.ContainsAny(subject, "\r\n") ||
		body == "" || utf8.RuneCountInString(body) > 20000 {
		return AnnouncementAudience{}, nil, Mail{}, ErrAnnouncementInvalid
	}
	if in.Kind == policy.AudienceProgramme && in.SemesterCode == "" {
		code, err := s.store.PlanningSemesterCode(ctx)
		if err != nil {
			return AnnouncementAudience{}, nil, Mail{}, err
		}
		in.SemesterCode = code
	}
	audience, err := s.store.ResolveAudience(ctx, in)
	if err != nil {
		return AnnouncementAudience{}, nil, Mail{}, err
	}
	if !policy.MaySendAnnouncement(actor, audience.Policy()) {
		return AnnouncementAudience{}, nil, Mail{}, ErrAudienceNotAllowed
	}
	recipients, err := s.store.Recipients(ctx, audience, policy.WishVisibility(actor, audience.State))
	if err != nil {
		return AnnouncementAudience{}, nil, Mail{}, err
	}
	mail, err := s.renderer.Announcement(AnnouncementMail{
		SenderName: actor.Name, SenderMail: actor.Mail,
		Audience: audience, Subject: subject, Body: body,
	})
	if err != nil {
		return AnnouncementAudience{}, nil, Mail{}, err
	}
	return audience, recipients, mail, nil
}
