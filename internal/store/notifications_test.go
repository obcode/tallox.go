package store_test

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/mail"
	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
	"github.com/obcode/tallox.go/internal/store"
	"github.com/obcode/tallox.go/internal/testdata"
)

// Mail against a real database: the detection is a comparison in SQL, the fan-out a WHERE clause
// over the subscriber's own rows, the address a join on person. A fake store would pass every test
// below while the shipped queries told somebody about another person's wish.

type notificationFixture struct {
	wishFixture
	n      *store.Notifications
	marks  *store.PlanningMarks
	sender *recordingSender
}

func newNotificationFixture(t *testing.T) notificationFixture {
	t.Helper()
	f := newWishFixture(t)
	return notificationFixture{
		wishFixture: f,
		n:           store.NewNotifications(f.schema.Pool),
		marks:       store.NewPlanningMarks(f.schema.Pool),
		sender:      &recordingSender{},
	}
}

// recordingSender is the one seam: an SMTP server that remembers.
type recordingSender struct {
	mu   sync.Mutex
	sent []domain.OutgoingMail
}

func (r *recordingSender) Send(_ context.Context, m domain.OutgoingMail) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, m)
	return nil
}

func (r *recordingSender) take() []domain.OutgoingMail {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.sent
	r.sent = nil
	return out
}

// tick runs the dispatcher once, as if it were `at`.
func (f notificationFixture) tick(t *testing.T, at time.Time) []domain.OutgoingMail {
	t.Helper()
	d := domain.NewDispatcher(f.n, mail.NewRenderer("https://tallox.example.org"), f.sender,
		func() time.Time { return at })
	if err := d.Tick(t.Context()); err != nil {
		t.Fatalf("cannot tick: %v", err)
	}
	return f.sender.take()
}

// later is a moment the settle time has passed for everything done before it, on a weekday at
// noon so that a daily mail is not due by accident.
func later() time.Time {
	return time.Now().Add(domain.NotificationSettle + time.Minute)
}

func (f notificationFixture) subscribe(t *testing.T, who testdata.Persona, s domain.NewNotificationSubscription) {
	t.Helper()
	s.PersonID = who.ID()
	if s.Delivery == "" {
		s.Delivery = domain.DeliverImmediately
	}
	if _, err := f.n.Subscribe(t.Context(), s); err != nil {
		t.Fatalf("cannot subscribe %s: %v", who.Mail, err)
	}
}

func (f notificationFixture) release(t *testing.T) {
	t.Helper()
	if _, err := f.marks.AnnounceDemandComplete(t.Context(), f.semester.Code, programmeCode(t, f), uuid.Nil); err != nil {
		t.Fatalf("cannot release: %v", err)
	}
}

func programmeCode(t *testing.T, f notificationFixture) string {
	t.Helper()
	var code string
	if err := f.schema.Pool.QueryRow(t.Context(), `SELECT code FROM programme WHERE id = $1`,
		f.programme).Scan(&code); err != nil {
		t.Fatalf("cannot read the programme: %v", err)
	}
	return code
}

func recipients(mails []domain.OutgoingMail) []string {
	out := make([]string, 0, len(mails))
	for _, m := range mails {
		out = append(out, strings.ToLower(m.To))
	}
	slices.Sort(out)
	return out
}

func TestNothingIsSentWithoutASubscription(t *testing.T) {
	t.Parallel()
	f := newNotificationFixture(t)

	f.release(t)
	if sent := f.tick(t, later()); len(sent) != 0 {
		t.Fatalf("a release with no subscriptions sent %d mails", len(sent))
	}
}

func TestAnOfferedModuleIsToldOnceAfterTheSettleTime(t *testing.T) {
	t.Parallel()
	f := newNotificationFixture(t)
	f.subscribe(t, testdata.Eins, domain.NewNotificationSubscription{
		Kind: domain.NotifyModuleOffered, ModuleID: f.module,
	})

	f.release(t)
	if sent := f.tick(t, time.Now()); len(sent) != 0 {
		t.Fatalf("a release younger than the settle time sent %d mails", len(sent))
	}

	sent := f.tick(t, later())
	if got := recipients(sent); !slices.Equal(got, []string{testdata.Eins.Mail}) {
		t.Fatalf("recipients %v, want only Eins", got)
	}
	if !strings.Contains(sent[0].Subject, "angeboten") || !strings.Contains(sent[0].HTML, f.module.String()) {
		t.Errorf("the mail does not say which module is offered: %q", sent[0].Subject)
	}

	f.release(t) // re-announcing moves the timestamp and is no news
	if again := f.tick(t, later().Add(domain.NotificationSettle)); len(again) != 0 {
		t.Fatalf("a re-announcement sent %d more mails", len(again))
	}
}

func TestAReleaseWithdrawnWithinTheSettleTimeSendsNothing(t *testing.T) {
	t.Parallel()
	f := newNotificationFixture(t)
	f.subscribe(t, testdata.Eins, domain.NewNotificationSubscription{Kind: domain.NotifyModuleOffered})
	f.subscribe(t, testdata.Eins, domain.NewNotificationSubscription{Kind: domain.NotifyDemandReleased})

	f.release(t)
	if _, err := f.marks.WithdrawDemandComplete(t.Context(), f.semester.Code, programmeCode(t, f)); err != nil {
		t.Fatalf("cannot withdraw: %v", err)
	}
	if sent := f.tick(t, later()); len(sent) != 0 {
		t.Fatalf("a release clicked by mistake sent %d mails", len(sent))
	}
}

func TestAWishRoundShutAndOpenedAgainSaysNothing(t *testing.T) {
	t.Parallel()
	f := newNotificationFixture(t)
	f.subscribe(t, testdata.Eins, domain.NewNotificationSubscription{
		Kind: domain.NotifyWishWindowChanged, SubjectGroupID: f.group,
	})

	ctx := t.Context()
	if _, err := f.marks.SetWishWindow(ctx, f.semester.Code, f.group, false, uuid.Nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.marks.SetWishWindow(ctx, f.semester.Code, f.group, true, uuid.Nil); err != nil {
		t.Fatal(err)
	}
	if sent := f.tick(t, later()); len(sent) != 0 {
		t.Fatalf("a door shut and opened again sent %d mails", len(sent))
	}

	if _, err := f.marks.SetWishWindow(ctx, f.semester.Code, f.group, false, uuid.Nil); err != nil {
		t.Fatal(err)
	}
	sent := f.tick(t, later())
	if len(sent) != 1 || !strings.Contains(sent[0].Subject, "geschlossen") {
		t.Fatalf("shutting the round sent %v", sent)
	}
}

// TestTargetSetsAreTheSubscribersOwnRows is the confidentiality rule inside the fan-out: two
// people with the same subscription, and only the one whose own row matches is told.
func TestTargetSetsAreTheSubscribersOwnRows(t *testing.T) {
	t.Parallel()
	f := newNotificationFixture(t)
	ctx := t.Context()

	// Zwei can teach the module and is a member of its subject group; Eins is neither.
	if _, err := f.schema.Pool.Exec(ctx, `INSERT INTO competence (module_id, person_id, level, entered_by)
		VALUES ($1, $2, 'CAN_TEACH', $2)`, f.module, testdata.Zwei.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.schema.Pool.Exec(ctx, `INSERT INTO person_subject_group (person_id, subject_group_id)
		VALUES ($1, $2)`, testdata.Zwei.ID(), f.group); err != nil {
		t.Fatal(err)
	}
	for _, who := range []testdata.Persona{testdata.Eins, testdata.Zwei} {
		f.subscribe(t, who, domain.NewNotificationSubscription{
			Kind: domain.NotifyModuleOffered, TargetSet: domain.TargetMyCompetences})
		f.subscribe(t, who, domain.NewNotificationSubscription{
			Kind: domain.NotifyWishWindowChanged, TargetSet: domain.TargetMySubjectGroups})
	}

	f.release(t)
	if _, err := f.marks.SetWishWindow(ctx, f.semester.Code, f.group, false, uuid.Nil); err != nil {
		t.Fatal(err)
	}
	sent := f.tick(t, later())
	if got := recipients(sent); !slices.Equal(got, []string{testdata.Zwei.Mail}) {
		t.Fatalf("recipients %v, want only Zwei", got)
	}
	if !strings.Contains(sent[0].Subject, "(2)") {
		t.Errorf("two events for one person should be one mail, subject %q", sent[0].Subject)
	}
}

// TestMyWishesDoesNotReachOtherPeoplesWishes: Eins wished, Zwei did not; both follow the wish
// rounds of "my wishes".
func TestMyWishesDoesNotReachOtherPeoplesWishes(t *testing.T) {
	t.Parallel()
	f := newNotificationFixture(t)
	ctx := t.Context()
	f.release(t)
	if _, err := f.wishes.SetWish(ctx, f.instance, testdata.Eins.ID(), domain.WishFirstChoice, "", false); err != nil {
		t.Fatalf("cannot wish: %v", err)
	}
	for _, who := range []testdata.Persona{testdata.Eins, testdata.Zwei} {
		f.subscribe(t, who, domain.NewNotificationSubscription{
			Kind: domain.NotifyWishWindowChanged, TargetSet: domain.TargetMyWishes})
	}
	if _, err := f.marks.SetWishWindow(ctx, f.semester.Code, f.group, false, uuid.Nil); err != nil {
		t.Fatal(err)
	}
	if got := recipients(f.tick(t, later())); !slices.Equal(got, []string{testdata.Eins.Mail}) {
		t.Fatalf("recipients %v, want only Eins", got)
	}
}

func TestMailGoesToActiveAccountsOnly(t *testing.T) {
	t.Parallel()
	f := newNotificationFixture(t)
	for _, who := range []testdata.Persona{testdata.Eins, testdata.Zwei} {
		f.subscribe(t, who, domain.NewNotificationSubscription{Kind: domain.NotifyDemandReleased})
	}
	if _, err := f.schema.Pool.Exec(t.Context(), `UPDATE person SET active = false WHERE id = $1`,
		testdata.Zwei.ID()); err != nil {
		t.Fatal(err)
	}
	f.release(t)
	if got := recipients(f.tick(t, later())); !slices.Equal(got, []string{testdata.Eins.Mail}) {
		t.Fatalf("recipients %v, want only the active Eins", got)
	}
}

func TestAPausedPersonIsNotToldLater(t *testing.T) {
	t.Parallel()
	f := newNotificationFixture(t)
	f.subscribe(t, testdata.Eins, domain.NewNotificationSubscription{Kind: domain.NotifyDemandReleased})
	if _, err := f.n.SetPausedUntil(t.Context(), testdata.Eins.ID(), time.Now().AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	f.release(t)
	if sent := f.tick(t, later()); len(sent) != 0 {
		t.Fatalf("a paused person got %d mails", len(sent))
	}
	if _, err := f.n.SetPausedUntil(t.Context(), testdata.Eins.ID(), time.Time{}); err != nil {
		t.Fatal(err)
	}
	if sent := f.tick(t, later().Add(time.Hour)); len(sent) != 0 {
		t.Fatalf("resuming sent %d mails from the pause", len(sent))
	}
}

func TestADailySubscriptionWaitsForTheMorning(t *testing.T) {
	t.Parallel()
	f := newNotificationFixture(t)
	f.subscribe(t, testdata.Eins, domain.NewNotificationSubscription{
		Kind: domain.NotifyDemandReleased, Delivery: domain.DeliverDaily})
	f.release(t)

	// Settled, but the delivery arrives after this morning's slot, so tomorrow's takes it.
	now := later()
	if sent := f.tick(t, now); len(sent) != 0 {
		t.Fatalf("a daily subscription sent %d mails at once", len(sent))
	}
	tomorrow := time.Date(now.Year(), now.Month(), now.Day()+1, 7, 30, 0, 0, time.Local)
	if got := recipients(f.tick(t, tomorrow)); !slices.Equal(got, []string{testdata.Eins.Mail}) {
		t.Fatalf("the next morning sent to %v", got)
	}
}

func TestSubscriptionsAreTheOwnersOnly(t *testing.T) {
	t.Parallel()
	f := newNotificationFixture(t)
	f.subscribe(t, testdata.Eins, domain.NewNotificationSubscription{Kind: domain.NotifyDemandReleased})
	mine, err := f.n.Subscriptions(t.Context(), testdata.Eins.ID())
	if err != nil || len(mine) != 1 {
		t.Fatalf("Eins has %v, %v", mine, err)
	}
	if removed, err := f.n.Unsubscribe(t.Context(), testdata.Zwei.ID(), mine[0].ID); err != nil || removed {
		t.Fatalf("Zwei removed Eins' subscription: %v, %v", removed, err)
	}
	if theirs, _ := f.n.Subscriptions(t.Context(), testdata.Zwei.ID()); len(theirs) != 0 {
		t.Fatalf("Zwei reads %d subscriptions", len(theirs))
	}
	// Subscribing twice is the same subscription.
	f.subscribe(t, testdata.Eins, domain.NewNotificationSubscription{Kind: domain.NotifyDemandReleased,
		Delivery: domain.DeliverDaily})
	again, _ := f.n.Subscriptions(t.Context(), testdata.Eins.ID())
	if len(again) != 1 || again[0].Delivery != domain.DeliverDaily {
		t.Fatalf("subscribing again gave %v", again)
	}
}

// TestProgrammeAudienceIsWhoWishedOrHolds: the PROGRAMME announcement reaches the people with a
// wish or an assignment on the programme's instances, and not the rest of the faculty.
func TestProgrammeAudienceIsWhoWishedOrHolds(t *testing.T) {
	t.Parallel()
	f := newNotificationFixture(t)
	ctx := t.Context()
	f.release(t)
	if _, err := f.wishes.SetWish(ctx, f.instance, testdata.Zwei.ID(), domain.WishHappyTo, "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := f.schema.Pool.Exec(ctx, `INSERT INTO assignment (instance_part_id, person_id) VALUES ($1, $2)`,
		f.lecture, testdata.Drei.ID()); err != nil {
		t.Fatal(err)
	}
	audience, err := f.n.ResolveAudience(ctx, domain.AnnouncementAudienceInput{
		Kind: policy.AudienceProgramme, ProgrammeCode: programmeCode(t, f), SemesterCode: f.semester.Code})
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.n.Recipients(ctx, audience, policy.WishFilter{Scope: policy.WishScopeAll})
	if err != nil {
		t.Fatal(err)
	}
	want := []uuid.UUID{testdata.Zwei.ID(), testdata.Drei.ID()}
	slices.SortFunc(want, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	if !slices.Equal(got, want) {
		t.Fatalf("recipients %v, want Zwei and Drei", got)
	}

	// The wishers are read through the filter like every other read of the wish table: a filter
	// that lets only the caller's own rows through finds no wisher but the caller.
	own, err := f.n.Recipients(ctx, audience, policy.WishFilter{Scope: policy.WishScopeOwn, OwnerID: testdata.Eins.ID()})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(own, []uuid.UUID{testdata.Drei.ID()}) {
		t.Fatalf("through an own-only filter the audience is %v, want only the holder Drei", own)
	}
}

func TestAnAnnouncementQueuesOneMailPerRecipient(t *testing.T) {
	t.Parallel()
	f := newNotificationFixture(t)
	ctx := t.Context()

	svc := domain.NewAnnouncementService(f.n, mail.NewRenderer("https://tallox.example.org"), nil)
	admin := testdata.Vier.Actor(principal.KindInteractive, string(policy.RoleAdmin))
	sent, err := svc.Send(ctx, admin, domain.AnnouncementAudienceInput{Kind: policy.AudienceAllAccounts},
		"Neu in Tallox", "Hallo **alle**")
	if err != nil {
		t.Fatalf("cannot send: %v", err)
	}
	if sent.RecipientCount != 4 {
		t.Errorf("went to %d, want the four accounts", sent.RecipientCount)
	}
	mails := f.tick(t, time.Now())
	if len(mails) != 4 {
		t.Fatalf("sent %d mails, want 4", len(mails))
	}
	for _, m := range mails {
		if m.ReplyTo != testdata.Vier.Mail || strings.Count(m.To, "@") != 1 {
			t.Errorf("mail to %q replies to %q", m.To, m.ReplyTo)
		}
	}
	// A recipient sees it in the archive, without the count.
	list, err := svc.List(ctx, testdata.Eins.Actor(principal.KindInteractive, string(policy.RoleLecturer)))
	if err != nil || len(list) != 1 || list[0].RecipientCount != -1 {
		t.Fatalf("Eins reads %v, %v", list, err)
	}
}
