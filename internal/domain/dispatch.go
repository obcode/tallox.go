package domain

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// The dispatcher: from what the planning made official to a mail in an inbox.
//
// One tick does five things, each a statement or two against the database:
//
//	detect   compare the planning's state with what has been announced, and write an event for
//	         each difference that has stood for NotificationSettle
//	fan out  match every new event against the subscriptions, one delivery per person and event
//	compose  bundle each person's due deliveries into one mail and queue it
//	send     hand queued mail to the SMTP server — or leave it queued, when none is configured
//	prune    forget what is older than NotificationRetention
//
// Detection rather than a hook in every write path, decided on 2026-11-10 while building it: a
// hook per path is one the next path forgets, it cannot wait out a release clicked by mistake
// without a cancellation protocol of its own, and the state it would compare against is already
// in the database.

// NotificationEvent is one official act, as a mail tells it. Every field is a fact about the
// process that any account may read — see the migration for why that is the rule.
type NotificationEvent struct {
	ID               uuid.UUID
	Kind             NotificationKind
	Detail           string
	OccurredAt       time.Time
	SemesterCode     string
	Module           *ModuleRef
	Programme        *Programme
	SubjectGroup     *SubjectGroupRef
	CourseInstanceID uuid.UUID
}

// SubscriptionReason is the subscription that brought an event to somebody, for the line "you
// receive this because …". The zero value when that subscription has since been removed.
type SubscriptionReason struct {
	Kind             NotificationKind
	TargetSet        NotificationTargetSet
	SemesterCode     string
	ModuleName       string
	ProgrammeCode    string
	SubjectGroupName string
}

// DigestItem is one event in one person's mail.
type DigestItem struct {
	Event  NotificationEvent
	Reason SubscriptionReason
}

// Digest is one person's mail: everything due for them now.
type Digest struct {
	PersonID   uuid.UUID
	PersonName string
	Items      []DigestItem
}

// MailCategory says what kind of mail a queued one is: it decides headers, not content.
type MailCategory string

// The two categories: a subscription's digest, and an announcement somebody sent.
const (
	MailNotification MailCategory = "NOTIFICATION"
	MailAnnouncement MailCategory = "ANNOUNCEMENT"
)

// Mail is a rendered message without its recipient.
type Mail struct {
	Category MailCategory
	ReplyTo  string
	Subject  string
	HTML     string
	Text     string
}

// OutgoingMail is a queued mail with its recipient, read from the account when it is sent.
type OutgoingMail struct {
	ID       uuid.UUID
	To       string
	ToName   string
	Category MailCategory
	ReplyTo  string
	Subject  string
	HTML     string
	Text     string
	Attempts int
}

// MailRenderer turns what is to be said into a mail. Implemented by internal/mail, where the
// German is: a mail is read by a person, like the GUI.
type MailRenderer interface {
	Digest(d Digest) (Mail, error)
	Announcement(a AnnouncementMail) (Mail, error)
}

// MailSender hands one mail to a server. Implemented by internal/mail's SMTP client; nil when no
// server is configured, and then mail stays queued.
type MailSender interface {
	Send(ctx context.Context, m OutgoingMail) error
}

// MailCounts are the figures the nightly report and the smoke test ask for — never contents.
type MailCounts struct {
	Sent              int
	Failed            int
	Pending           int
	AnnouncementMails int
	LastSentAt        time.Time
}

// DispatchStore is what the dispatcher needs from persistence.
type DispatchStore interface {
	// Detect writes an event for every official fact not yet announced that has stood since
	// cutoff, and for every confirmation reminder due today. Reports how many it wrote; zero, and
	// no error, when another dispatcher holds the lock.
	Detect(ctx context.Context, cutoff, today time.Time) (int, error)
	// FanOut matches the events not yet fanned out against the subscriptions. today decides who
	// has paused.
	FanOut(ctx context.Context, today time.Time) (int, error)
	// DueDigests is everything to be sent now, per person: immediate deliveries, and daily ones
	// that arrived before dailyCutoff.
	DueDigests(ctx context.Context, dailyCutoff time.Time) ([]Digest, error)
	// QueueDigest stores a rendered digest and marks its deliveries as sent in it.
	QueueDigest(ctx context.Context, d Digest, m Mail) error
	// DropMailToInactive marks mail queued for deactivated accounts as not to be sent.
	DropMailToInactive(ctx context.Context) error
	// ClaimMail leases up to max queued mails due by now, so that no other sender takes them.
	ClaimMail(ctx context.Context, now time.Time, max int) ([]OutgoingMail, error)
	MarkMailSent(ctx context.Context, id uuid.UUID) error
	MarkMailFailed(ctx context.Context, id uuid.UUID, reason string, next time.Time, maxAttempts int) error
	// Prune forgets events and sent or failed mail older than before.
	Prune(ctx context.Context, before time.Time) error
	MailCounts(ctx context.Context, since time.Time) (MailCounts, error)
}

// maxMailAttempts is how often one mail is tried before it counts as failed.
const maxMailAttempts = 5

// mailBatch bounds one tick's sending. A faculty-wide announcement is a few hundred mails and
// goes out over a few minutes, which the HM gateway prefers to a burst.
const mailBatch = 60

// Dispatcher runs the ticks.
type Dispatcher struct {
	store    DispatchStore
	renderer MailRenderer
	sender   MailSender
	now      func() time.Time
	// DailyHour and DailyMinute are when the daily mails go out, local time.
	DailyHour, DailyMinute int
}

// NewDispatcher wires one up. sender may be nil: then mail is queued and not sent, which is the
// emergency stop and the state of an installation nobody has given a server yet.
func NewDispatcher(store DispatchStore, renderer MailRenderer, sender MailSender,
	now func() time.Time) *Dispatcher {
	if now == nil {
		now = time.Now
	}
	return &Dispatcher{store: store, renderer: renderer, sender: sender, now: now,
		DailyHour: 7, DailyMinute: 10}
}

// Sending reports whether mail leaves this installation at all.
func (d *Dispatcher) Sending() bool { return d.sender != nil }

// Run ticks every interval until ctx ends. A failed tick is logged and the next one tries again:
// every step is idempotent, so a tick cut short leaves nothing half done that the next cannot
// finish.
func (d *Dispatcher) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := d.Tick(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Error().Err(err).Msg("cannot dispatch notifications")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Tick runs the five steps once.
func (d *Dispatcher) Tick(ctx context.Context) error {
	now := d.now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)

	if _, err := d.store.Detect(ctx, now.Add(-NotificationSettle), today); err != nil {
		return fmt.Errorf("cannot detect: %w", err)
	}
	if _, err := d.store.FanOut(ctx, today); err != nil {
		return fmt.Errorf("cannot fan out: %w", err)
	}
	if err := d.compose(ctx, now); err != nil {
		return err
	}
	if d.sender != nil {
		if err := d.send(ctx, now); err != nil {
			return err
		}
	}
	if err := d.store.Prune(ctx, now.Add(-NotificationRetention)); err != nil {
		return fmt.Errorf("cannot prune: %w", err)
	}
	return nil
}

// dailyCutoff is the most recent morning slot at or before now.
func (d *Dispatcher) dailyCutoff(now time.Time) time.Time {
	slot := time.Date(now.Year(), now.Month(), now.Day(), d.DailyHour, d.DailyMinute, 0, 0, time.Local)
	if slot.After(now) {
		slot = slot.AddDate(0, 0, -1)
	}
	return slot
}

func (d *Dispatcher) compose(ctx context.Context, now time.Time) error {
	digests, err := d.store.DueDigests(ctx, d.dailyCutoff(now))
	if err != nil {
		return fmt.Errorf("cannot read due deliveries: %w", err)
	}
	for _, digest := range digests {
		mail, err := d.renderer.Digest(digest)
		if err != nil {
			return fmt.Errorf("cannot render a digest: %w", err)
		}
		if err := d.store.QueueDigest(ctx, digest, mail); err != nil {
			return fmt.Errorf("cannot queue a digest: %w", err)
		}
	}
	return nil
}

func (d *Dispatcher) send(ctx context.Context, now time.Time) error {
	if err := d.store.DropMailToInactive(ctx); err != nil {
		return fmt.Errorf("cannot drop mail to inactive accounts: %w", err)
	}
	batch, err := d.store.ClaimMail(ctx, now, mailBatch)
	if err != nil {
		return fmt.Errorf("cannot claim mail: %w", err)
	}
	for _, m := range batch {
		if err := d.sender.Send(ctx, m); err != nil {
			// Not log.Error: a gateway refusing one address is the ordinary failure here and is
			// retried; the nightly report counts what finally failed. The address stays out of
			// the line.
			log.Warn().Err(err).Str("mail", m.ID.String()).Int("attempt", m.Attempts+1).
				Msg("cannot send a mail")
			next := now.Add(retryDelay(m.Attempts))
			if err := d.store.MarkMailFailed(ctx, m.ID, err.Error(), next, maxMailAttempts); err != nil {
				return fmt.Errorf("cannot record a failed mail: %w", err)
			}
			continue
		}
		if err := d.store.MarkMailSent(ctx, m.ID); err != nil {
			return fmt.Errorf("cannot record a sent mail: %w", err)
		}
	}
	return nil
}

// retryDelay backs off: five minutes, half an hour, two hours, eight.
func retryDelay(attempts int) time.Duration {
	switch attempts {
	case 0:
		return 5 * time.Minute
	case 1:
		return 30 * time.Minute
	case 2:
		return 2 * time.Hour
	default:
		return 8 * time.Hour
	}
}
