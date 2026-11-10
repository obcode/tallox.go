package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/policy"
)

// Notifications is the persistence behind subscriptions, the dispatcher and announcements.
//
// Every statement that decides who is told lives in db/queries/notifications.sql and
// announcements.sql, as SQL: "a target set is resolved against the subscriber's own rows" is a
// WHERE clause, and a version of it in Go would pass its unit test against a fake while the shipped
// query told somebody about another person's wish.
type Notifications struct {
	pool *pgxpool.Pool
}

// NewNotifications wires one up.
func NewNotifications(pool *pgxpool.Pool) *Notifications { return &Notifications{pool: pool} }

var (
	_ domain.NotificationStore = (*Notifications)(nil)
	_ domain.DispatchStore     = (*Notifications)(nil)
	_ domain.AnnouncementStore = (*Notifications)(nil)
)

// --- subscriptions ---------------------------------------------------------------------------

// Subscriptions lists one person's, newest first.
func (n *Notifications) Subscriptions(ctx context.Context, personID uuid.UUID,
) ([]domain.NotificationSubscription, error) {
	rows, err := New(n.pool).NotificationSubscriptionsOf(ctx, personID)
	if err != nil {
		return nil, fmt.Errorf("cannot read the subscriptions: %w", err)
	}
	out := make([]domain.NotificationSubscription, 0, len(rows))
	for _, r := range rows {
		s := domain.NotificationSubscription{
			ID:           r.ID,
			PersonID:     r.PersonID,
			Kind:         domain.NotificationKind(r.Kind),
			SemesterCode: deref(r.SemesterCode),
			TargetSet:    domain.NotificationTargetSet(deref(r.TargetSet)),
			Delivery:     domain.NotificationDelivery(r.Delivery),
			CreatedAt:    r.CreatedAt,
		}
		if r.ModuleID.Valid {
			s.Module = &domain.ModuleRef{ID: r.ModuleID.UUID, Name: deref(r.ModuleName),
				HomeProgrammeCode: deref(r.ModuleProgrammeCode)}
		}
		if r.ProgrammeID.Valid {
			s.Programme = &domain.Programme{ID: r.ProgrammeID.UUID, Code: deref(r.ProgrammeCode),
				Title: deref(r.ProgrammeTitle), Active: true}
		}
		if r.SubjectGroupID.Valid {
			s.SubjectGroup = &domain.SubjectGroupRef{ID: r.SubjectGroupID.UUID,
				Code: deref(r.SubjectGroupCode), Name: deref(r.SubjectGroupName),
				Active: r.SubjectGroupActive != nil && *r.SubjectGroupActive}
		}
		out = append(out, s)
	}
	return out, nil
}

// Subscribe stores one, or changes the delivery of the identical one already there.
func (n *Notifications) Subscribe(ctx context.Context, s domain.NewNotificationSubscription,
) (*domain.NotificationSubscription, error) {
	id, err := New(n.pool).SubscribeNotification(ctx, SubscribeNotificationParams{
		PersonID:       s.PersonID,
		Kind:           string(s.Kind),
		SemesterCode:   nonEmpty(s.SemesterCode),
		ModuleID:       optionalUUID(s.ModuleID),
		ProgrammeID:    optionalUUID(s.ProgrammeID),
		SubjectGroupID: optionalUUID(s.SubjectGroupID),
		TargetSet:      nonEmpty(string(s.TargetSet)),
		Delivery:       string(s.Delivery),
	})
	if err != nil {
		return nil, fmt.Errorf("cannot subscribe: %w", err)
	}
	return n.subscription(ctx, s.PersonID, id)
}

// SetDelivery changes one of the person's.
func (n *Notifications) SetDelivery(ctx context.Context, personID, id uuid.UUID,
	d domain.NotificationDelivery) (*domain.NotificationSubscription, error) {
	rows, err := New(n.pool).SetNotificationDelivery(ctx, SetNotificationDeliveryParams{
		Delivery: string(d), ID: id, PersonID: personID,
	})
	if err != nil {
		return nil, fmt.Errorf("cannot change the delivery: %w", err)
	}
	if rows == 0 {
		return nil, nil
	}
	return n.subscription(ctx, personID, id)
}

func (n *Notifications) subscription(ctx context.Context, personID, id uuid.UUID,
) (*domain.NotificationSubscription, error) {
	list, err := n.Subscriptions(ctx, personID)
	if err != nil {
		return nil, err
	}
	for _, s := range list {
		if s.ID == id {
			return &s, nil
		}
	}
	return nil, nil
}

// Unsubscribe removes one of the person's.
func (n *Notifications) Unsubscribe(ctx context.Context, personID, id uuid.UUID) (bool, error) {
	rows, err := New(n.pool).Unsubscribe(ctx, UnsubscribeParams{ID: id, PersonID: personID})
	if err != nil {
		return false, fmt.Errorf("cannot unsubscribe: %w", err)
	}
	return rows > 0, nil
}

// SubscriptionCount is how many the person has.
func (n *Notifications) SubscriptionCount(ctx context.Context, personID uuid.UUID) (int, error) {
	c, err := New(n.pool).NotificationSubscriptionCount(ctx, personID)
	if err != nil {
		return 0, fmt.Errorf("cannot count the subscriptions: %w", err)
	}
	return int(c), nil
}

// Settings reads the person's settings; none stored is the defaults.
func (n *Notifications) Settings(ctx context.Context, personID uuid.UUID) (domain.NotificationSettings, error) {
	d, err := New(n.pool).NotificationSettingsOf(ctx, personID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.NotificationSettings{}, nil
	}
	if err != nil {
		return domain.NotificationSettings{}, fmt.Errorf("cannot read the settings: %w", err)
	}
	return domain.NotificationSettings{PausedUntil: dateOrZero(d)}, nil
}

// SetPausedUntil writes the pause.
func (n *Notifications) SetPausedUntil(ctx context.Context, personID uuid.UUID, until time.Time,
) (domain.NotificationSettings, error) {
	if err := New(n.pool).SetNotificationPausedUntil(ctx, SetNotificationPausedUntilParams{
		PersonID: personID, PausedUntil: pgtype.Date{Time: until, Valid: !until.IsZero()},
	}); err != nil {
		return domain.NotificationSettings{}, fmt.Errorf("cannot pause: %w", err)
	}
	return n.Settings(ctx, personID)
}

// ModuleExists resolves a module target.
func (n *Notifications) ModuleExists(ctx context.Context, id uuid.UUID) (bool, error) {
	ok, err := New(n.pool).ModuleExists(ctx, id)
	if err != nil {
		return false, fmt.Errorf("cannot look up the module: %w", err)
	}
	return ok, nil
}

// ProgrammeIDByCode resolves a programme target, uuid.Nil when there is none.
func (n *Notifications) ProgrammeIDByCode(ctx context.Context, code string) (uuid.UUID, error) {
	return NewPlanningMarks(n.pool).ProgrammeIDByCode(ctx, code)
}

// SubjectGroupExists resolves a subject group target.
func (n *Notifications) SubjectGroupExists(ctx context.Context, id uuid.UUID) (bool, error) {
	ok, err := New(n.pool).SubjectGroupExists(ctx, id)
	if err != nil {
		return false, fmt.Errorf("cannot look up the subject group: %w", err)
	}
	return ok, nil
}

// --- the dispatcher --------------------------------------------------------------------------

// Detect runs every detection query in one transaction, under a lock only one dispatcher holds.
func (n *Notifications) Detect(ctx context.Context, cutoff, today time.Time) (int, error) {
	tx, err := n.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("cannot begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := New(tx)

	locked, err := q.LockNotificationDispatch(ctx)
	if err != nil {
		return 0, fmt.Errorf("cannot lock the dispatch: %w", err)
	}
	if !locked {
		return 0, nil
	}

	total := int64(0)
	for _, step := range []struct {
		name string
		run  func() (int64, error)
	}{
		{"demand releases", func() (int64, error) { return q.DetectDemandReleases(ctx, cutoff) }},
		{"review completions", func() (int64, error) { return q.DetectReviewCompletions(ctx, cutoff) }},
		{"wish windows", func() (int64, error) { return q.DetectWishWindows(ctx, cutoff) }},
		{"phases", func() (int64, error) { return q.DetectPhases(ctx, cutoff) }},
		{"publications", q.detectPublications(ctx)},
		{"confirmation dues", func() (int64, error) { return q.DetectConfirmationDues(ctx, cutoff) }},
		{"confirmation reminders", func() (int64, error) {
			return q.DetectConfirmationReminders(ctx, pgtype.Date{Time: today, Valid: true})
		}},
		{"offered instances", func() (int64, error) { return q.DetectOfferedInstances(ctx, cutoff) }},
		{"withdrawn instances", q.detectWithdrawn(ctx)},
		{"advance wishes", func() (int64, error) { return q.DetectAdvanceWishes(ctx, cutoff) }},
	} {
		rows, err := step.run()
		if err != nil {
			return 0, fmt.Errorf("cannot detect %s: %w", step.name, err)
		}
		total += rows
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("cannot commit: %w", err)
	}
	return int(total), nil
}

func (q *Queries) detectPublications(ctx context.Context) func() (int64, error) {
	return func() (int64, error) { return q.DetectPublications(ctx) }
}

func (q *Queries) detectWithdrawn(ctx context.Context) func() (int64, error) {
	return func() (int64, error) { return q.DetectWithdrawnInstances(ctx) }
}

// FanOut matches the events not yet fanned out.
func (n *Notifications) FanOut(ctx context.Context, today time.Time) (int, error) {
	tx, err := n.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("cannot begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := New(tx)

	ids, err := q.PendingNotificationEvents(ctx)
	if err != nil {
		return 0, fmt.Errorf("cannot read the pending events: %w", err)
	}
	if len(ids) == 0 {
		return 0, nil
	}
	rows, err := q.FanOutNotificationEvents(ctx, FanOutNotificationEventsParams{
		EventIds: ids, Today: pgtype.Date{Time: today, Valid: true},
	})
	if err != nil {
		return 0, fmt.Errorf("cannot fan out: %w", err)
	}
	if err := q.MarkNotificationEventsFannedOut(ctx, ids); err != nil {
		return 0, fmt.Errorf("cannot mark the events: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("cannot commit: %w", err)
	}
	return int(rows), nil
}

// DueDigests groups the due deliveries by person.
func (n *Notifications) DueDigests(ctx context.Context, dailyCutoff time.Time) ([]domain.Digest, error) {
	rows, err := New(n.pool).DueNotificationDeliveries(ctx, dailyCutoff)
	if err != nil {
		return nil, fmt.Errorf("cannot read the due deliveries: %w", err)
	}
	var out []domain.Digest
	for _, r := range rows {
		if len(out) == 0 || out[len(out)-1].PersonID != r.PersonID {
			out = append(out, domain.Digest{PersonID: r.PersonID, PersonName: r.PersonName})
		}
		event := domain.NotificationEvent{
			ID:               r.EventID,
			Kind:             domain.NotificationKind(r.Kind),
			Detail:           r.Detail,
			OccurredAt:       r.OccurredAt,
			SemesterCode:     r.SemesterCode,
			CourseInstanceID: r.CourseInstanceID.UUID,
		}
		if r.ModuleID.Valid {
			event.Module = &domain.ModuleRef{ID: r.ModuleID.UUID, Name: r.ModuleName,
				HomeProgrammeCode: r.ModuleProgrammeCode}
		}
		if r.ProgrammeID.Valid {
			event.Programme = &domain.Programme{ID: r.ProgrammeID.UUID, Code: r.ProgrammeCode,
				Title: r.ProgrammeTitle, Active: true}
		}
		if r.SubjectGroupID.Valid {
			event.SubjectGroup = &domain.SubjectGroupRef{ID: r.SubjectGroupID.UUID,
				Code: r.SubjectGroupCode, Name: r.SubjectGroupName, Active: true}
		}
		reason := domain.SubscriptionReason{
			Kind:             domain.NotificationKind(deref(r.SubKind)),
			TargetSet:        domain.NotificationTargetSet(deref(r.SubTargetSet)),
			SemesterCode:     deref(r.SubSemesterCode),
			ModuleName:       r.SubModuleName,
			ProgrammeCode:    r.SubProgrammeCode,
			SubjectGroupName: r.SubSubjectGroupName,
		}
		last := &out[len(out)-1]
		last.Items = append(last.Items, domain.DigestItem{Event: event, Reason: reason})
	}
	return out, nil
}

// QueueDigest stores the mail and links its deliveries, in one transaction: a digest is queued
// once or not at all.
func (n *Notifications) QueueDigest(ctx context.Context, d domain.Digest, m domain.Mail) error {
	tx, err := n.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("cannot begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := New(tx)

	id, err := q.QueueMail(ctx, QueueMailParams{
		PersonID: d.PersonID, Category: string(domain.MailNotification), ReplyTo: m.ReplyTo,
		Subject: m.Subject, Html: m.HTML, TextBody: m.Text,
	})
	if err != nil {
		return fmt.Errorf("cannot queue the mail: %w", err)
	}
	events := make([]uuid.UUID, 0, len(d.Items))
	for _, item := range d.Items {
		events = append(events, item.Event.ID)
	}
	if err := q.LinkDeliveriesToMail(ctx, LinkDeliveriesToMailParams{
		MailID: id, PersonID: d.PersonID, EventIds: events,
	}); err != nil {
		return fmt.Errorf("cannot link the deliveries: %w", err)
	}
	return tx.Commit(ctx)
}

// DropMailToInactive marks mail for deactivated accounts as not to be sent.
func (n *Notifications) DropMailToInactive(ctx context.Context) error {
	if _, err := New(n.pool).DropMailToInactive(ctx); err != nil {
		return fmt.Errorf("cannot drop mail: %w", err)
	}
	return nil
}

// ClaimMail leases the next mails.
func (n *Notifications) ClaimMail(ctx context.Context, now time.Time, limit int) ([]domain.OutgoingMail, error) {
	rows, err := New(n.pool).ClaimMail(ctx, ClaimMailParams{Now: now, MaxRows: int32(limit)}) //nolint:gosec // bounded by mailBatch
	if err != nil {
		return nil, fmt.Errorf("cannot claim mail: %w", err)
	}
	out := make([]domain.OutgoingMail, 0, len(rows))
	for _, r := range rows {
		out = append(out, domain.OutgoingMail{
			ID: r.ID, To: r.Address, ToName: r.PersonName, Category: domain.MailCategory(r.Category),
			ReplyTo: r.ReplyTo, Subject: r.Subject, HTML: r.Html, Text: r.TextBody,
			Attempts: int(r.Attempts),
		})
	}
	return out, nil
}

// MarkMailSent records a mail as sent.
func (n *Notifications) MarkMailSent(ctx context.Context, id uuid.UUID) error {
	if err := New(n.pool).MarkMailSent(ctx, id); err != nil {
		return fmt.Errorf("cannot mark the mail: %w", err)
	}
	return nil
}

// MarkMailFailed records a failed attempt.
func (n *Notifications) MarkMailFailed(ctx context.Context, id uuid.UUID, reason string, next time.Time,
	maxAttempts int) error {
	if err := New(n.pool).MarkMailFailed(ctx, MarkMailFailedParams{
		LastError: reason, NextAttemptAt: next, MaxAttempts: int32(maxAttempts), ID: id, //nolint:gosec // a small constant
	}); err != nil {
		return fmt.Errorf("cannot mark the mail: %w", err)
	}
	return nil
}

// Prune forgets what is older than before.
func (n *Notifications) Prune(ctx context.Context, before time.Time) error {
	if err := New(n.pool).PruneNotifications(ctx, before); err != nil {
		return fmt.Errorf("cannot prune: %w", err)
	}
	return nil
}

// MailCounts are the figures for the report.
func (n *Notifications) MailCounts(ctx context.Context, since time.Time) (domain.MailCounts, error) {
	r, err := New(n.pool).MailCounts(ctx, since)
	if err != nil {
		return domain.MailCounts{}, fmt.Errorf("cannot count mail: %w", err)
	}
	out := domain.MailCounts{Sent: int(r.Sent), Failed: int(r.Failed), Pending: int(r.Pending),
		AnnouncementMails: int(r.AnnouncementMails)}
	if r.LastSentAt.After(time.Unix(0, 0)) {
		out.LastSentAt = r.LastSentAt
	}
	return out, nil
}

// --- announcements ---------------------------------------------------------------------------

// ResolveAudience reads what an input names.
func (n *Notifications) ResolveAudience(ctx context.Context, in domain.AnnouncementAudienceInput,
) (domain.AnnouncementAudience, error) {
	q := New(n.pool)
	out := domain.AnnouncementAudience{Kind: in.Kind}
	switch in.Kind {
	case policy.AudienceSubjectGroup:
		if in.SubjectGroupID == uuid.Nil {
			return out, domain.ErrAnnouncementInvalid
		}
		g, err := q.SubjectGroupRefByID(ctx, in.SubjectGroupID)
		if errors.Is(err, pgx.ErrNoRows) {
			return out, domain.ErrSubjectGroupNotFound
		}
		if err != nil {
			return out, fmt.Errorf("cannot read the subject group: %w", err)
		}
		out.SubjectGroup = &domain.SubjectGroupRef{ID: g.ID, Code: g.Code, Name: g.Name, Active: g.Active}
	case policy.AudienceProgramme:
		if in.ProgrammeCode == "" || in.SemesterCode == "" {
			return out, domain.ErrAnnouncementInvalid
		}
		p, err := q.ProgrammeRefByCode(ctx, in.ProgrammeCode)
		if errors.Is(err, pgx.ErrNoRows) {
			return out, domain.ErrProgrammeNotFound
		}
		if err != nil {
			return out, fmt.Errorf("cannot read the programme: %w", err)
		}
		out.Programme = &domain.Programme{ID: p.ID, Code: p.Code, Title: p.Title, Active: p.Active}
		out.SemesterCode = in.SemesterCode
		// A semester nothing has been decided about has no row, and then no wishes and no
		// assignments: the audience is empty rather than an error.
		semester, err := NewSemesters(n.pool).SemesterByCode(ctx, in.SemesterCode)
		if err != nil {
			return out, err
		}
		out.SemesterID = semester.ID
		out.State = semester.State()
	case policy.AudienceColleagues, policy.AudienceLeads, policy.AudienceAllAccounts:
	default:
		return out, domain.ErrAnnouncementInvalid
	}
	return out, nil
}

// Recipients are the active accounts an audience reaches: the holders from announcements.sql and,
// for a programme, the wishers through the filtered query in wish.sql.
func (n *Notifications) Recipients(ctx context.Context, a domain.AnnouncementAudience,
	wishes policy.WishFilter) ([]uuid.UUID, error) {
	params := AnnouncementRecipientsParams{Kind: string(a.Kind)}
	if a.SubjectGroup != nil {
		params.SubjectGroupID = optionalUUID(a.SubjectGroup.ID)
	}
	if a.Programme != nil {
		params.ProgrammeID = optionalUUID(a.Programme.ID)
	}
	params.SemesterID = optionalUUID(a.SemesterID)
	q := New(n.pool)
	ids, err := q.AnnouncementRecipients(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("cannot read the recipients: %w", err)
	}
	if a.Kind != policy.AudienceProgramme || a.Programme == nil || a.SemesterID == uuid.Nil {
		return ids, nil
	}
	scope, owner, programmes, groups := wishFilterParams(wishes)
	wishers, err := q.WishersOfProgramme(ctx, WishersOfProgrammeParams{
		SemesterID: a.SemesterID, ProgrammeID: a.Programme.ID,
		Scope: scope, OwnerID: owner, ProgrammeIds: programmes, SubjectGroupIds: groups,
	})
	if err != nil {
		return nil, fmt.Errorf("cannot read the wishers: %w", err)
	}
	for _, id := range wishers {
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	slices.SortFunc(ids, func(x, y uuid.UUID) int { return strings.Compare(x.String(), y.String()) })
	return ids, nil
}

// SentSince counts a sender's announcements.
func (n *Notifications) SentSince(ctx context.Context, senderID uuid.UUID, since time.Time) (int, error) {
	c, err := New(n.pool).AnnouncementsSentSince(ctx, AnnouncementsSentSinceParams{SenderID: senderID, Since: since})
	if err != nil {
		return 0, fmt.Errorf("cannot count the announcements: %w", err)
	}
	return int(c), nil
}

// CreateAnnouncement stores one with its recipients and its mail, in one transaction.
func (n *Notifications) CreateAnnouncement(ctx context.Context, a domain.NewAnnouncement,
) (*domain.Announcement, error) {
	tx, err := n.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := New(tx)

	params := CreateAnnouncementParams{
		SenderID: a.SenderID, AudienceKind: string(a.Audience.Kind),
		SemesterID: optionalUUID(a.Audience.SemesterID),
		Subject:    a.Subject, Body: a.Body, RecipientCount: int32(len(a.Recipients)), //nolint:gosec // accounts of one faculty
	}
	if a.Audience.SubjectGroup != nil {
		params.SubjectGroupID = optionalUUID(a.Audience.SubjectGroup.ID)
	}
	if a.Audience.Programme != nil {
		params.ProgrammeID = optionalUUID(a.Audience.Programme.ID)
	}
	row, err := q.CreateAnnouncement(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("cannot store the announcement: %w", err)
	}
	if err := q.AddAnnouncementRecipients(ctx, AddAnnouncementRecipientsParams{
		AnnouncementID: row.ID, PersonIds: a.Recipients,
	}); err != nil {
		return nil, fmt.Errorf("cannot store the recipients: %w", err)
	}
	if err := q.QueueAnnouncementMails(ctx, QueueAnnouncementMailsParams{
		PersonIds: a.Recipients, ReplyTo: a.Mail.ReplyTo, Subject: a.Mail.Subject,
		Html: a.Mail.HTML, TextBody: a.Mail.Text,
	}); err != nil {
		return nil, fmt.Errorf("cannot queue the mails: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("cannot commit: %w", err)
	}

	list, err := n.Announcements(ctx, a.SenderID)
	if err != nil {
		return nil, err
	}
	for _, sent := range list {
		if sent.ID == row.ID {
			return &sent, nil
		}
	}
	return nil, fmt.Errorf("cannot read back announcement %s", row.ID)
}

// Announcements are those a person sent or received.
func (n *Notifications) Announcements(ctx context.Context, personID uuid.UUID) ([]domain.Announcement, error) {
	rows, err := New(n.pool).AnnouncementsOf(ctx, personID)
	if err != nil {
		return nil, fmt.Errorf("cannot read the announcements: %w", err)
	}
	out := make([]domain.Announcement, 0, len(rows))
	for _, r := range rows {
		a := domain.Announcement{
			ID:             r.ID,
			Sender:         domain.Person{ID: r.SenderID, Mail: r.SenderMail, Name: r.SenderName, Active: r.SenderActive},
			Audience:       domain.AnnouncementAudience{Kind: policy.AnnouncementAudienceKind(r.AudienceKind), SemesterCode: r.SemesterCode},
			Subject:        r.Subject,
			Body:           r.Body,
			RecipientCount: int(r.RecipientCount),
			SentAt:         r.SentAt,
		}
		if r.SubjectGroupID.Valid {
			a.Audience.SubjectGroup = &domain.SubjectGroupRef{ID: r.SubjectGroupID.UUID,
				Code: r.SubjectGroupCode, Name: r.SubjectGroupName, Active: r.SubjectGroupActive}
		}
		if r.ProgrammeID.Valid {
			a.Audience.Programme = &domain.Programme{ID: r.ProgrammeID.UUID, Code: r.ProgrammeCode,
				Title: r.ProgrammeTitle, Active: true}
		}
		out = append(out, a)
	}
	return out, nil
}

// AudienceCandidates are the active subject groups and the planned programmes.
func (n *Notifications) AudienceCandidates(ctx context.Context,
) ([]domain.SubjectGroupRef, []domain.Programme, error) {
	q := New(n.pool)
	groups, err := q.AnnouncementSubjectGroups(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot read the subject groups: %w", err)
	}
	programmes, err := q.AnnouncementProgrammes(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot read the programmes: %w", err)
	}
	g := make([]domain.SubjectGroupRef, 0, len(groups))
	for _, r := range groups {
		g = append(g, domain.SubjectGroupRef{ID: r.ID, Code: r.Code, Name: r.Name, Active: r.Active})
	}
	p := make([]domain.Programme, 0, len(programmes))
	for _, r := range programmes {
		p = append(p, domain.Programme{ID: r.ID, Code: r.Code, Title: r.Title, Active: r.Active})
	}
	return g, p, nil
}

// PlanningSemesterCode is the semester the faculty is planning, or "".
func (n *Notifications) PlanningSemesterCode(ctx context.Context) (string, error) {
	code, err := New(n.pool).PlanningSemesterCodeForAnnouncements(ctx)
	if err != nil {
		return "", fmt.Errorf("cannot read the planning semester: %w", err)
	}
	return code, nil
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func optionalUUID(id uuid.UUID) uuid.NullUUID {
	return uuid.NullUUID{UUID: id, Valid: id != uuid.Nil}
}
