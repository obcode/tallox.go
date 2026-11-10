package domain

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
)

// Subscriptions to what the planning makes official — decided 2026-11-10.
//
// Three rules, and the code below is mostly about keeping them:
//
//	Only what is official.  Nothing in the planning writes an event. The dispatcher compares the
//	                        state the planning keeps with what it has announced, and announces
//	                        the difference once it has stood for NotificationSettle — a release
//	                        clicked by mistake and withdrawn in that time never reaches an inbox.
//	Always opt-in.          A subscription is made by its owner and by nobody else. There is no
//	                        call that subscribes somebody else and no default subscription.
//	A mail is an export.    A mail carries what every account may read, and somebody's own rows
//	                        only to them. A target set is always over the subscriber's own rows.

// NotificationSettle is how long an official act has to stand before anybody is told.
//
// A constant and not a configuration key, like the access log's retention: "a release withdrawn
// within ten minutes sends nothing" is a sentence the faculty can be told, and it should not depend
// on what a file on the host says.
const NotificationSettle = 10 * time.Minute

// NotificationRetention is how long events, deliveries and sent mail are kept.
const NotificationRetention = 90 * 24 * time.Hour

// MaxSubscriptions bounds what one person may follow. Generous — the recommended set is about
// a dozen — and there so that a script in a loop cannot make the matching query slow for everybody.
const MaxSubscriptions = 200

// NotificationKind is one kind of official event.
type NotificationKind string

// The kinds, as the schema's NotificationKind documents them.
const (
	NotifyModuleOffered         NotificationKind = "MODULE_OFFERED"
	NotifyModuleWithdrawn       NotificationKind = "MODULE_WITHDRAWN"
	NotifyDemandReleased        NotificationKind = "DEMAND_RELEASED"
	NotifyWishWindowChanged     NotificationKind = "WISH_WINDOW_CHANGED"
	NotifyWishesPublished       NotificationKind = "WISHES_PUBLISHED"
	NotifyAssignmentsPublished  NotificationKind = "ASSIGNMENTS_PUBLISHED"
	NotifyConfirmationDue       NotificationKind = "CONFIRMATION_DUE"
	NotifyDesiredHoursFrozen    NotificationKind = "DESIRED_HOURS_FROZEN"
	NotifyDesiredHoursPublished NotificationKind = "DESIRED_HOURS_PUBLISHED"
	NotifyPhaseAdvanced         NotificationKind = "PHASE_ADVANCED"
	NotifyReviewCompleted       NotificationKind = "REVIEW_COMPLETED"
	NotifyAdvanceWishResolved   NotificationKind = "ADVANCE_WISH_RESOLVED"
)

// NotificationKinds is every kind, in the order a screen lists them.
func NotificationKinds() []NotificationKind {
	return []NotificationKind{
		NotifyModuleOffered, NotifyModuleWithdrawn, NotifyDemandReleased, NotifyWishWindowChanged,
		NotifyWishesPublished, NotifyAssignmentsPublished, NotifyConfirmationDue,
		NotifyDesiredHoursFrozen, NotifyDesiredHoursPublished, NotifyPhaseAdvanced,
		NotifyReviewCompleted, NotifyAdvanceWishResolved,
	}
}

// NotificationTargetSet is a set of the subscriber's own rows a subscription follows.
type NotificationTargetSet string

// The sets, as the schema's NotificationTargetSet documents them — every one over own rows.
const (
	TargetMyAdvanceWishes NotificationTargetSet = "MY_ADVANCE_WISHES"
	TargetMyWishes        NotificationTargetSet = "MY_WISHES"
	TargetMyAssignments   NotificationTargetSet = "MY_ASSIGNMENTS"
	TargetMyCompetences   NotificationTargetSet = "MY_COMPETENCES"
	TargetMySubjectGroups NotificationTargetSet = "MY_SUBJECT_GROUPS"
	TargetMyProgrammes    NotificationTargetSet = "MY_PROGRAMMES"
)

// targetSetKinds is which kinds each set can say something about — what the matching query in
// internal/store resolves. Withdrawal is not among them for wishes and assignments: an instance
// somebody wished for or holds cannot be withdrawn (INSTANCE_IN_USE), so there is nothing to say. A subscription outside it would match nothing, ever, and a person who
// made one would wait for a mail that cannot come; refusing it says so at once.
var targetSetKinds = map[NotificationTargetSet][]NotificationKind{
	TargetMyAdvanceWishes: {NotifyModuleOffered, NotifyModuleWithdrawn, NotifyAdvanceWishResolved},
	TargetMyWishes:        {NotifyWishWindowChanged, NotifyAssignmentsPublished},
	TargetMyAssignments:   {NotifyAssignmentsPublished, NotifyConfirmationDue},
	TargetMyCompetences:   {NotifyModuleOffered, NotifyModuleWithdrawn},
	TargetMySubjectGroups: {NotifyModuleOffered, NotifyModuleWithdrawn, NotifyWishWindowChanged},
	TargetMyProgrammes: {NotifyModuleOffered, NotifyModuleWithdrawn, NotifyDemandReleased,
		NotifyReviewCompleted},
}

// recommendedSubscriptions is the one-click set: everything about one's own rows. Offered, never
// applied without the click.
var recommendedSubscriptions = []struct {
	kind NotificationKind
	set  NotificationTargetSet
}{
	{NotifyAdvanceWishResolved, TargetMyAdvanceWishes},
	{NotifyModuleOffered, TargetMyAdvanceWishes},
	{NotifyWishWindowChanged, TargetMyWishes},
	{NotifyWishWindowChanged, TargetMySubjectGroups},
	{NotifyAssignmentsPublished, TargetMyAssignments},
	{NotifyConfirmationDue, TargetMyAssignments},
	{NotifyModuleWithdrawn, TargetMyAdvanceWishes},
	{NotifyModuleOffered, TargetMyCompetences},
}

// recommendedForLeads is added for somebody who leads a programme.
var recommendedForLeads = []struct {
	kind NotificationKind
	set  NotificationTargetSet
}{
	{NotifyDemandReleased, TargetMyProgrammes},
	{NotifyReviewCompleted, TargetMyProgrammes},
}

// NotificationDelivery is when a subscription's mails go out.
type NotificationDelivery string

// The two deliveries: a few minutes after the event, or the next morning.
const (
	DeliverImmediately NotificationDelivery = "IMMEDIATE"
	DeliverDaily       NotificationDelivery = "DAILY"
)

// NotificationSubscription is one person's subscription.
type NotificationSubscription struct {
	ID           uuid.UUID
	PersonID     uuid.UUID
	Kind         NotificationKind
	SemesterCode string
	Module       *ModuleRef
	Programme    *Programme
	SubjectGroup *SubjectGroupRef
	TargetSet    NotificationTargetSet
	Delivery     NotificationDelivery
	CreatedAt    time.Time
}

// NotificationSubscriptionInput is a subscription on the way in.
type NotificationSubscriptionInput struct {
	Kind           NotificationKind
	SemesterCode   string
	ModuleID       uuid.UUID
	ProgrammeCode  string
	SubjectGroupID uuid.UUID
	TargetSet      NotificationTargetSet
	Delivery       NotificationDelivery
}

// NewNotificationSubscription is a validated input, resolved to ids, ready to be stored.
type NewNotificationSubscription struct {
	PersonID       uuid.UUID
	Kind           NotificationKind
	SemesterCode   string
	ModuleID       uuid.UUID
	ProgrammeID    uuid.UUID
	SubjectGroupID uuid.UUID
	TargetSet      NotificationTargetSet
	Delivery       NotificationDelivery
}

// NotificationSettings are a person's settings for all their subscriptions.
type NotificationSettings struct {
	// PausedUntil is the last day no subscription mail goes out, or the zero time.
	PausedUntil time.Time
}

var (
	// ErrSubscriptionInvalid is a subscription that cannot match anything: more than one target, a
	// target that does not exist, a set that says nothing about the kind.
	ErrSubscriptionInvalid = errors.New("dieses Abo ist so nicht möglich")
	// ErrTooManySubscriptions is the MaxSubscriptions bound.
	ErrTooManySubscriptions = errors.New("mehr als 200 Abos sind nicht möglich")
	// ErrSubscriptionNotFound is a subscription id that is not one of the caller's.
	ErrSubscriptionNotFound = errors.New("dieses Abo gibt es nicht")
	// ErrOnlookerSubscribes is an onlooker — a partner faculty or an observer, held alone —
	// trying to subscribe. They read what they read in the tool and take no mail from it.
	ErrOnlookerSubscribes = errors.New("mit dieser Rolle lassen sich keine Benachrichtigungen abonnieren")
)

// NotificationStore is what the service needs from persistence.
type NotificationStore interface {
	// Subscriptions lists one person's, newest first.
	Subscriptions(ctx context.Context, personID uuid.UUID) ([]NotificationSubscription, error)
	// Subscribe stores one, or changes the delivery of the identical one already there.
	Subscribe(ctx context.Context, s NewNotificationSubscription) (*NotificationSubscription, error)
	// SetDelivery changes one of the person's. (nil, nil) when it is not theirs.
	SetDelivery(ctx context.Context, personID, id uuid.UUID, d NotificationDelivery,
	) (*NotificationSubscription, error)
	// Unsubscribe removes one of the person's and reports whether there was one.
	Unsubscribe(ctx context.Context, personID, id uuid.UUID) (bool, error)
	// SubscriptionCount is how many the person has.
	SubscriptionCount(ctx context.Context, personID uuid.UUID) (int, error)
	// Settings reads, SetPausedUntil writes, the person's settings.
	Settings(ctx context.Context, personID uuid.UUID) (NotificationSettings, error)
	SetPausedUntil(ctx context.Context, personID uuid.UUID, until time.Time) (NotificationSettings, error)
	// ModuleExists, ProgrammeIDByCode and SubjectGroupExists resolve targets.
	ModuleExists(ctx context.Context, id uuid.UUID) (bool, error)
	ProgrammeIDByCode(ctx context.Context, code string) (uuid.UUID, error)
	SubjectGroupExists(ctx context.Context, id uuid.UUID) (bool, error)
}

// NotificationService is the business logic of subscriptions.
type NotificationService struct {
	store NotificationStore
}

// NewNotificationService wires one up.
func NewNotificationService(s NotificationStore) *NotificationService {
	return &NotificationService{store: s}
}

// MySubscriptions lists the caller's own. Every door: they are the caller's.
func (s *NotificationService) MySubscriptions(ctx context.Context, actor principal.Actor,
) ([]NotificationSubscription, error) {
	if !actor.Authenticated() {
		return nil, ErrNotAuthenticated
	}
	return s.store.Subscriptions(ctx, actor.ID)
}

// Subscribe adds a subscription for the caller — never for anybody else: the owner is the actor,
// and there is no argument that could name another.
func (s *NotificationService) Subscribe(ctx context.Context, actor principal.Actor,
	in NotificationSubscriptionInput) (*NotificationSubscription, error) {
	if err := s.maySubscribe(actor); err != nil {
		return nil, err
	}
	resolved, err := s.resolve(ctx, actor, in)
	if err != nil {
		return nil, err
	}
	if err := s.room(ctx, actor, 1); err != nil {
		return nil, err
	}
	return s.store.Subscribe(ctx, resolved)
}

// SubscribeRecommended adds the one-click set and answers all of the caller's subscriptions.
//
// The programme sets only for somebody who leads one: offered to everybody else they would be a
// subscription that can never match, which is the kind Subscribe refuses.
func (s *NotificationService) SubscribeRecommended(ctx context.Context, actor principal.Actor,
) ([]NotificationSubscription, error) {
	if err := s.maySubscribe(actor); err != nil {
		return nil, err
	}
	wanted := slices.Clone(recommendedSubscriptions)
	if !policy.PlanningScope(actor).Empty() {
		wanted = append(wanted, recommendedForLeads...)
	}
	if err := s.room(ctx, actor, len(wanted)); err != nil {
		return nil, err
	}
	for _, w := range wanted {
		if _, err := s.store.Subscribe(ctx, NewNotificationSubscription{
			PersonID: actor.ID, Kind: w.kind, TargetSet: w.set, Delivery: DeliverImmediately,
		}); err != nil {
			return nil, err
		}
	}
	return s.store.Subscriptions(ctx, actor.ID)
}

// SetDelivery changes when one of the caller's subscriptions sends.
func (s *NotificationService) SetDelivery(ctx context.Context, actor principal.Actor, id uuid.UUID,
	d NotificationDelivery) (*NotificationSubscription, error) {
	if !actor.Authenticated() {
		return nil, ErrNotAuthenticated
	}
	if d != DeliverImmediately && d != DeliverDaily {
		return nil, ErrSubscriptionInvalid
	}
	sub, err := s.store.SetDelivery(ctx, actor.ID, id, d)
	if err != nil {
		return nil, err
	}
	if sub == nil {
		return nil, ErrSubscriptionNotFound
	}
	return sub, nil
}

// Unsubscribe removes one of the caller's. Removing one that is not theirs removes nothing and
// answers false — the same answer as an id that never existed, so the call says nothing about
// other people's subscriptions.
func (s *NotificationService) Unsubscribe(ctx context.Context, actor principal.Actor, id uuid.UUID,
) (bool, error) {
	if !actor.Authenticated() {
		return false, ErrNotAuthenticated
	}
	return s.store.Unsubscribe(ctx, actor.ID, id)
}

// Settings reads the caller's settings.
func (s *NotificationService) Settings(ctx context.Context, actor principal.Actor,
) (NotificationSettings, error) {
	if !actor.Authenticated() {
		return NotificationSettings{}, ErrNotAuthenticated
	}
	return s.store.Settings(ctx, actor.ID)
}

// Pause stops the caller's subscription mails until the end of a day, or resumes them with the
// zero time.
func (s *NotificationService) Pause(ctx context.Context, actor principal.Actor, until time.Time,
) (NotificationSettings, error) {
	if !actor.Authenticated() {
		return NotificationSettings{}, ErrNotAuthenticated
	}
	return s.store.SetPausedUntil(ctx, actor.ID, until)
}

func (s *NotificationService) maySubscribe(actor principal.Actor) error {
	if !actor.Authenticated() {
		return ErrNotAuthenticated
	}
	if policy.Onlooker(actor) {
		return ErrOnlookerSubscribes
	}
	return nil
}

func (s *NotificationService) room(ctx context.Context, actor principal.Actor, adding int) error {
	n, err := s.store.SubscriptionCount(ctx, actor.ID)
	if err != nil {
		return err
	}
	if n+adding > MaxSubscriptions {
		return ErrTooManySubscriptions
	}
	return nil
}

// resolve validates an input and turns its codes into ids.
func (s *NotificationService) resolve(ctx context.Context, actor principal.Actor,
	in NotificationSubscriptionInput) (NewNotificationSubscription, error) {
	out := NewNotificationSubscription{
		PersonID:       actor.ID,
		Kind:           in.Kind,
		SemesterCode:   in.SemesterCode,
		ModuleID:       in.ModuleID,
		SubjectGroupID: in.SubjectGroupID,
		TargetSet:      in.TargetSet,
		Delivery:       in.Delivery,
	}
	if out.Delivery == "" {
		out.Delivery = DeliverImmediately
	}
	if !slices.Contains(NotificationKinds(), in.Kind) ||
		(out.Delivery != DeliverImmediately && out.Delivery != DeliverDaily) {
		return out, ErrSubscriptionInvalid
	}
	if in.SemesterCode != "" {
		if !semesterCodePattern.MatchString(in.SemesterCode) {
			return out, ErrSubscriptionInvalid
		}
	}

	targets := 0
	for _, set := range []bool{in.ModuleID != uuid.Nil, in.ProgrammeCode != "",
		in.SubjectGroupID != uuid.Nil, in.TargetSet != ""} {
		if set {
			targets++
		}
	}
	if targets > 1 {
		return out, ErrSubscriptionInvalid
	}

	switch {
	case in.ModuleID != uuid.Nil:
		ok, err := s.store.ModuleExists(ctx, in.ModuleID)
		if err != nil {
			return out, err
		}
		if !ok {
			return out, ErrSubscriptionInvalid
		}
	case in.ProgrammeCode != "":
		id, err := s.store.ProgrammeIDByCode(ctx, in.ProgrammeCode)
		if err != nil {
			return out, err
		}
		if id == uuid.Nil {
			return out, ErrSubscriptionInvalid
		}
		out.ProgrammeID = id
	case in.SubjectGroupID != uuid.Nil:
		ok, err := s.store.SubjectGroupExists(ctx, in.SubjectGroupID)
		if err != nil {
			return out, err
		}
		if !ok {
			return out, ErrSubscriptionInvalid
		}
	case in.TargetSet != "":
		kinds, known := targetSetKinds[in.TargetSet]
		if !known || !slices.Contains(kinds, in.Kind) {
			return out, ErrSubscriptionInvalid
		}
	}
	return out, nil
}
