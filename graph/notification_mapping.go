package graph

import (
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/graph/model"
	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/mail"
	"github.com/obcode/tallox.go/internal/policy"
)

// Subscriptions and announcements, on the way out and on the way back.
//
// Nothing here filters: a subscription is only ever read by its owner, and what an announcement
// reveals — its recipient count — is decided in domain.AnnouncementService.List.

func notificationSubscriptionModel(s domain.NotificationSubscription) *model.NotificationSubscription {
	out := &model.NotificationSubscription{
		ID:        s.ID.String(),
		Kind:      model.NotificationKind(s.Kind),
		Delivery:  model.NotificationDelivery(s.Delivery),
		CreatedAt: s.CreatedAt,
	}
	if s.SemesterCode != "" {
		out.Semester = &s.SemesterCode
	}
	if s.Module != nil {
		out.Module = &model.ModuleRef{ID: s.Module.ID.String(), Name: s.Module.Name,
			HomeProgrammeCode: s.Module.HomeProgrammeCode}
	}
	if s.Programme != nil {
		out.Programme = programmeModel(*s.Programme)
	}
	out.SubjectGroup = subjectGroupRefModel(s.SubjectGroup)
	if s.TargetSet != "" {
		set := model.NotificationTargetSet(s.TargetSet)
		out.TargetSet = &set
	}
	return out
}

func notificationSubscriptionModels(list []domain.NotificationSubscription) []*model.NotificationSubscription {
	out := make([]*model.NotificationSubscription, 0, len(list))
	for _, s := range list {
		out = append(out, notificationSubscriptionModel(s))
	}
	return out
}

func notificationSettingsModel(s domain.NotificationSettings) *model.NotificationSettings {
	out := &model.NotificationSettings{}
	if !s.PausedUntil.IsZero() {
		d := model.NewDate(s.PausedUntil)
		out.PausedUntil = &d
	}
	return out
}

func notificationSubscriptionInput(in model.NotificationSubscriptionInput,
) (domain.NotificationSubscriptionInput, error) {
	out := domain.NotificationSubscriptionInput{
		Kind:     domain.NotificationKind(in.Kind),
		Delivery: domain.NotificationDelivery(in.Delivery),
	}
	if in.Semester != nil {
		out.SemesterCode = *in.Semester
	}
	if in.Programme != nil {
		out.ProgrammeCode = *in.Programme
	}
	if in.TargetSet != nil {
		out.TargetSet = domain.NotificationTargetSet(*in.TargetSet)
	}
	var err error
	if out.ModuleID, err = optionalID(in.ModuleID); err != nil {
		return out, err
	}
	if out.SubjectGroupID, err = optionalID(in.SubjectGroupID); err != nil {
		return out, err
	}
	return out, nil
}

func announcementAudienceInput(in model.AnnouncementAudienceInput) (domain.AnnouncementAudienceInput, error) {
	out := domain.AnnouncementAudienceInput{Kind: policy.AnnouncementAudienceKind(in.Kind)}
	if in.Programme != nil {
		out.ProgrammeCode = *in.Programme
	}
	if in.Semester != nil {
		out.SemesterCode = *in.Semester
	}
	var err error
	out.SubjectGroupID, err = optionalID(in.SubjectGroupID)
	return out, err
}

func announcementAudienceModel(a domain.AnnouncementAudience) *model.AnnouncementAudience {
	out := &model.AnnouncementAudience{
		Kind:         model.AnnouncementAudienceKind(a.Kind),
		SubjectGroup: subjectGroupRefModel(a.SubjectGroup),
	}
	if a.Programme != nil {
		out.Programme = programmeModel(*a.Programme)
	}
	if a.SemesterCode != "" {
		code := a.SemesterCode
		out.Semester = &code
	}
	return out
}

func announcementModel(a domain.Announcement) *model.Announcement {
	out := &model.Announcement{
		ID:       a.ID.String(),
		Sender:   personModel(a.Sender),
		Audience: announcementAudienceModel(a.Audience),
		Subject:  a.Subject,
		Body:     a.Body,
		// The same renderer as the mail, so the archive shows what arrived; escaped there.
		BodyHTML: mail.MarkdownFragment(a.Body),
		SentAt:   a.SentAt,
	}
	if a.RecipientCount >= 0 {
		n := a.RecipientCount
		out.RecipientCount = &n
	}
	return out
}

func optionalID(raw *string) (uuid.UUID, error) {
	if raw == nil || *raw == "" {
		return uuid.Nil, nil
	}
	return parseID(*raw)
}

func dateOrZero(d *model.Date) time.Time {
	if d == nil {
		return time.Time{}
	}
	return d.Time()
}

// notificationError maps this area's refusals to codes the interface branches on.
func notificationError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, domain.ErrNotAuthenticated):
		return refusal("UNAUTHENTICATED", "Nicht angemeldet.")
	case errors.Is(err, domain.ErrSubscriptionInvalid):
		return refusal("SUBSCRIPTION_INVALID", err.Error())
	case errors.Is(err, domain.ErrTooManySubscriptions):
		return refusal("TOO_MANY_SUBSCRIPTIONS", err.Error())
	case errors.Is(err, domain.ErrSubscriptionNotFound):
		return refusal("SUBSCRIPTION_NOT_FOUND", err.Error())
	case errors.Is(err, domain.ErrOnlookerSubscribes):
		return refusal("ONLOOKER_SUBSCRIBES", err.Error())
	case errors.Is(err, domain.ErrAudienceNotAllowed):
		return refusal("AUDIENCE_NOT_ALLOWED", policy.AnnouncementReason)
	case errors.Is(err, domain.ErrAudienceEmpty):
		return refusal("AUDIENCE_EMPTY", err.Error())
	case errors.Is(err, domain.ErrAnnouncementInvalid):
		return refusal("ANNOUNCEMENT_INVALID", err.Error())
	case errors.Is(err, domain.ErrAnnouncementRateLimited):
		return refusal("RATE_LIMITED", err.Error())
	case errors.Is(err, domain.ErrProgrammeNotFound):
		return refusal("PROGRAMME_NOT_FOUND", err.Error())
	case errors.Is(err, domain.ErrSubjectGroupNotFound):
		return refusal("SUBJECT_GROUP_NOT_FOUND", err.Error())
	case coded(err):
		return err
	}
	return internalRefusal("notification", err)
}
