package policy

import (
	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/principal"
)

// Who may write to whom: announcements, decided 2026-11-10.
//
// An announcement is a mail in somebody's name to a group of people, sent from here so that it is
// kept and readable again. Two properties carry the rules below.
//
// Only in a signed-in browser session. A mail to the faculty from a token in a script would
// decouple "who sent this" from any sign-in, and a leaked token would become a way to write to
// everybody in somebody else's name. The schema says so with @interactiveOnly; MaySendAnnouncement
// says it again, so the rule does not depend on which door a future caller comes through.
//
// The sender may read every row the audience is made of. Recipients are told nothing about each
// other — each gets their own mail — but the sender learns how many it reached, and for a
// programme that figure is a count of the people who wished or were assigned there. The lead of
// that programme and the dean's office read those rows anyway (WishVisibility,
// AssignmentVisibility); nobody else is offered the audience.
//
// There is no subscription to opt out of, deliberately: this is official post from the people
// who lead the planning.

// AnnouncementAudienceKind is who an announcement goes to.
type AnnouncementAudienceKind string

const (
	// AudienceSubjectGroup is the members of one subject group.
	AudienceSubjectGroup AnnouncementAudienceKind = "SUBJECT_GROUP"
	// AudienceProgramme is whoever holds a part of, or has registered interest in, an instance of
	// one programme in one semester.
	AudienceProgramme AnnouncementAudienceKind = "PROGRAMME"
	// AudienceColleagues is every account in a role that teaches or plans.
	AudienceColleagues AnnouncementAudienceKind = "COLLEAGUES"
	// AudienceLeads is everybody who leads: subject groups, programmes, the dean's office.
	AudienceLeads AnnouncementAudienceKind = "LEADS"
	// AudienceAllAccounts is every active account, whatever its roles.
	AudienceAllAccounts AnnouncementAudienceKind = "ALL_ACCOUNTS"
)

// AnnouncementAudience is one audience, as the rule needs it: the kind and, where the kind has
// one, what it is about.
type AnnouncementAudience struct {
	Kind           AnnouncementAudienceKind
	SubjectGroupID uuid.UUID
	ProgrammeID    uuid.UUID
}

// AnnouncementReason is what somebody is told who may not write to an audience.
const AnnouncementReason = "An diese Gruppe kannst Du keine Rundmail schicken."

// AnnouncementInteractiveReason is what a token is told that tries to send one.
const AnnouncementInteractiveReason = "Rundmails lassen sich nur im Browser verschicken, " +
	"nicht über ein Token."

// MaySendAnnouncement reports whether this actor may write to this audience.
//
//	SUBJECT_GROUP  its lead (scoped to it) and the dean's office
//	PROGRAMME      its lead (scoped to it) and the dean's office
//	COLLEAGUES     the dean's office
//	LEADS          the dean's office and anybody who leads a subject group or a programme
//	ALL_ACCOUNTS   an administrator — an operational act, like the person administration it
//	               mirrors: the audience is exactly the list of accounts ADMIN already keeps
//
// Always only interactively. An onlooker writes to nobody: their roles leave every line false.
func MaySendAnnouncement(a principal.Actor, audience AnnouncementAudience) bool {
	if !a.Authenticated() || !a.Interactive() {
		return false
	}
	roles := RolesOf(a)

	switch audience.Kind {
	case AudienceSubjectGroup:
		return audience.SubjectGroupID != uuid.Nil && MayActInSubjectGroup(a, audience.SubjectGroupID)
	case AudienceProgramme:
		return audience.ProgrammeID != uuid.Nil && MayPlanProgramme(a, audience.ProgrammeID)
	case AudienceColleagues:
		return roles.Has(RoleDeansOffice)
	case AudienceLeads:
		return roles.Has(RoleDeansOffice) ||
			!AssignmentScope(a).Empty() || !PlanningScope(a).Empty()
	case AudienceAllAccounts:
		return roles.Has(RoleAdmin)
	}
	// An audience this package does not know is one nobody may write to.
	return false
}

// AnnouncementAudienceKinds is every kind, in the order a screen offers them.
func AnnouncementAudienceKinds() []AnnouncementAudienceKind {
	return []AnnouncementAudienceKind{
		AudienceSubjectGroup, AudienceProgramme, AudienceColleagues, AudienceLeads, AudienceAllAccounts,
	}
}

// ColleagueRoles are the roles the COLLEAGUES audience is made of: the ones that teach or plan.
// Not the onlookers, and not ADMIN alone — running the system is not teaching in it.
func ColleagueRoles() []Role {
	return []Role{RoleLecturer, RoleSubjectGroupLead, RoleProgrammeLead, RoleDeansOffice}
}
