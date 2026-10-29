package policy

import "github.com/obcode/tallox.go/internal/principal"

// MayReadSemesters reports whether an actor may see which semesters exist and where each one
// stands.
//
// Everybody who is signed in, and nothing narrower. The phase is not confidential — it is the
// answer to "may I enter my wishes yet", which every lecturer needs before they can do
// anything at all, and a rule that hid it would produce a tool that refuses writes without
// saying why.
//
// Not anonymous, though. buildInfo is the only field that answers without a credential,
// deliberately: it is the connectivity check. Everything else, including this, requires a row
// in person — which is what makes the person table the access control it is meant to be.
func MayReadSemesters(a principal.Actor) bool {
	return a.Authenticated()
}

// MayAdministerSemesters reports whether an actor may move a semester through the process.
//
// Only the phase, because that is all there is to permit. Nobody creates a semester — it is a
// name for a stretch of time, and it is there whether or not anybody has planned anything for
// it — so there is no act of creation to hold a right over, and a programme lead may plan for
// a semester years ahead without asking anybody to open it first.
//
// The dean's office runs the process, so the dean's office switches the phases. Two roles are
// deliberately *not* on this list:
//
//   - ADMIN. Running the system is a different job from planning with it — the same line the
//     wish rule draws, drawn again. An administrator who genuinely has to advance a phase
//     grants themselves DEANS_OFFICE, visibly and with an expiry, which is the mechanism this
//     codebase already uses for "I need to do a planning thing once".
//   - PROGRAMME_LEAD. Programme leads declare demand *within* a phase. Letting one of them end
//     the wish phase would let one programme's timetable decide the faculty's.
//
// Reachable through a Personal Access Token, unlike MayPublishWishes below. Stepping next
// year's semester forward is ordinary process work, it is reversible, and a script that rolls
// the semesters over is exactly the kind of thing the API exists for.
func MayAdministerSemesters(a principal.Actor) bool {
	return a.Authenticated() && RolesOf(a).Has(RoleDeansOffice)
}

// MayPublishAssignments reports whether an actor may make the assignments of a semester visible
// to everybody.
//
// The same people and the same door as MayPublishWishes, and a function of its own for the reason
// UnpublishedAssignmentScope is one: the two are the same rule today and are not the same
// decision. Publishing the wishes says the registration window is over; publishing the
// assignments says the plan is settled enough to be read as one. The day the faculty wants those
// to be different acts by different people, this is where that happens.
//
// With full reach only, for the reason below: it cannot be undone.
func MayPublishAssignments(a principal.Actor) bool {
	return MayAdministerSemesters(a) && a.ActsWithFullReach()
}

// MayPublishWishes reports whether an actor may end the confidentiality window of a semester.
//
// MayAdministerSemesters *and* full reach — a browser session or a token minted with full reach,
// never a plain token. It is the same role as every other semester operation; the extra condition
// is there because it is the only one that cannot be undone.
//
// Publishing is irreversible as a fact about the world rather than as a property of the
// schema: once colleagues have seen each other's entries, clearing the timestamp would only be
// a lie about it. Until 2026-10-28 that made it browser-only. The faculty decided the dean's
// office may also do it from a script or an assistant — but only through a token minted for
// exactly that: an explicit choice, at most 90 days, listed in the nightly access report, and
// recorded in the access log by its token id. A plain token in a forgotten cron job still cannot.
//
// It is also the moment the project's politically load-bearing rule stops applying, which is why
// the access log and the nightly report are part of this rule and not an afterthought.
func MayPublishWishes(a principal.Actor) bool {
	return MayActWithFullReach(a) && RolesOf(a).Has(RoleDeansOffice)
}

// MayFreezeDesiredHours reports whether an actor may close the desired teaching hours of a
// semester to everybody but the dean's office.
//
// The dean's office, with full reach only, for the reason MayPublishWishes gives: there is no
// unfreezing. A deadline that can be reopened is not one, and the corrections it would be reopened
// for go through MayCorrectDesiredHours instead.
func MayFreezeDesiredHours(a principal.Actor) bool {
	return MayPublishWishes(a)
}

// MayPublishDesiredHours reports whether an actor may make the desired teaching hours of a
// semester visible to everybody. The same people and door as the other publications, and a
// function of its own because it is a different decision.
func MayPublishDesiredHours(a principal.Actor) bool {
	return MayPublishWishes(a)
}

// FreezeDesiredHoursReason is what a caller who failed MayFreezeDesiredHours is told.
const FreezeDesiredHoursReason = "Nur das Dekanat darf die Wunsch-SWS einfrieren. " +
	"Das geht im Browser oder mit einem Planungs-Token mit voller Reichweite."

// PublishDesiredHoursReason is what a caller who failed MayPublishDesiredHours is told.
const PublishDesiredHoursReason = "Nur das Dekanat darf die Wunsch-SWS veröffentlichen. " +
	"Das geht im Browser oder mit einem Planungs-Token mit voller Reichweite."

// SemesterAdminReason is what a caller who failed MayAdministerSemesters is told.
//
// It says nothing about creating a semester, because nothing creates one. A message offering
// to have somebody "angelegt" would send colleagues asking the dean's office for a thing that
// does not exist.
const SemesterAdminReason = "Nur das Dekanat darf die Phase eines Semesters umschalten."

// PlanningSemesterReason is what a caller who may not move the planning mark is told.
//
// Separate from SemesterAdminReason even though the rule behind the two is the same one. That
// message talks about phases, and a colleague who tried to say which semester the faculty is
// planning would read it as an answer to a question they did not ask — and go looking for the
// phase they are supposed to switch instead.
const PlanningSemesterReason = "Nur das Dekanat darf festlegen, welches Semester gerade " +
	"geplant wird."

// PublishWishesReason is what a caller who failed MayPublishWishes is told.
//
// Deliberately two sentences: the first names the role, the second names the door, because the
// caller who hits this is most often somebody who *has* the role and is using a plain token. Telling
// them only "the dean's office may do this" would send them to ask for a grant they already
// hold.
const PublishWishesReason = "Nur das Dekanat darf Wünsche veröffentlichen. " +
	"Das geht im Browser oder mit einem Planungs-Token mit voller Reichweite."

// PublishAssignmentsReason is what a caller who failed MayPublishAssignments is told.
//
// Two sentences for the reason PublishWishesReason gives: the caller who hits this is most often
// somebody who holds the role and is using a token.
const PublishAssignmentsReason = "Nur das Dekanat darf Zuteilungen veröffentlichen. " +
	"Das geht im Browser oder mit einem Planungs-Token mit voller Reichweite."

// PhaseTransitionReason is what a caller attempting an illegal phase change is told.
func PhaseTransitionReason(from, to Phase) string {
	return "Ein Semester kann nur schrittweise umgeschaltet werden — von " + string(from) +
		" aus ist " + string(to) + " nicht erreichbar."
}

// MayMoveTo reports whether a semester in phase p may be switched to other.
//
// # One step at a time, in both directions
//
// Adjacent phases only. Forward is the process; backward is the correction, and it has to
// exist: this installation is reachable only through a VPN, so "somebody clicked FINAL too
// early" must not be a problem whose solution is psql on the host.
//
// What adjacency buys is that neither mistake is silent. A skip cannot happen by accident,
// because it is not expressible; and a correction is a visible, audited step rather than a
// jump that looks like somebody chose the phase it landed on.
//
// # Why not forbid going back from FINAL
//
// It is tempting — FINAL means the plan stands. But the phase is a statement about the
// planning process, not about the assignments, and a plan that has to be reopened is a normal
// event in a faculty, not a corruption of the record. Refusing it here would not prevent the
// reopening; it would move it out of the tool, which is where this project is trying to bring
// things from.
//
// A phase is not "movable to itself": switching a semester to the phase it is already in is
// not a step, and treating it as one would let a no-op count as a decision in the audit log.
func (p Phase) MayMoveTo(other Phase) bool {
	pi, oi := p.index(), other.index()
	if pi < 0 || oi < 0 {
		return false
	}
	return pi-oi == 1 || oi-pi == 1
}

// Neighbours returns the phases a semester in phase p may be switched to, in process order.
//
// For the interface, which has to render the buttons, and for the tests, which have to
// enumerate the legal moves without restating the rule. Derived from MayMoveTo rather than
// from a second list, so the buttons cannot offer a step the rule refuses.
func (p Phase) Neighbours() []Phase {
	var out []Phase
	for _, candidate := range AllPhases() {
		if p.MayMoveTo(candidate) {
			out = append(out, candidate)
		}
	}
	return out
}

// MaySetProgrammeQuota reports whether an actor may enter what a study programme may spend on
// teaching in a study year.
//
// The dean's office, which runs the capacity calculation the figure comes from. The same people
// as MayAdministerSemesters and a function of its own, because it is not the same decision: the
// day the faculty wants the quotas kept by somebody who does not switch phases, this is the one
// line that changes.
//
// Not the programme leads. The quota is the line a programme is measured against, and a line
// the measured can move is not one. Reachable through a Personal Access Token: it is a number
// that is corrected as often as the calculation is, and a script that enters a year's figures
// from the calculation is the obvious way to enter them.
func MaySetProgrammeQuota(a principal.Actor) bool {
	return MayAdministerSemesters(a)
}

// MaySetProgrammeDegree reports whether an actor may record whether a study programme leads to a
// bachelor's or a master's.
//
// The dean's office, which keeps the list of programmes and their planning status. A function of
// its own for the same reason as MaySetProgrammeQuota: it is a different decision that happens to
// sit with the same people today.
//
// Not the programme leads, although they know the answer best: the degree decides which
// programmes a filter shows, and every screen that filters by it is read by somebody else.
func MaySetProgrammeDegree(a principal.Actor) bool {
	return MayAdministerSemesters(a)
}

// MaySetAssignmentConfirmationDue reports whether an actor may ask everybody to confirm their
// assignments by a date, move the date or withdraw the request.
//
// The dean's office with full reach, like the publications. Not because the act is irreversible —
// it is not — but because it is an act on the whole semester's process that every colleague is
// then asked about, and the semester's other such acts sit behind the same door.
func MaySetAssignmentConfirmationDue(a principal.Actor) bool {
	return MayAdministerSemesters(a) && a.ActsWithFullReach()
}

// ConfirmationDueReason is what a caller who failed MaySetAssignmentConfirmationDue is told.
// Two sentences for the reason PublishWishesReason gives.
const ConfirmationDueReason = "Nur das Dekanat legt den Prüftermin für die Zuteilungen fest. " +
	"Das geht im Browser oder mit einem Planungs-Token mit voller Reichweite."

// MayConfirmOwnAssignments reports whether an actor may confirm their own assignments of a
// semester: anybody with an account, through every door, until the semester is FINAL.
//
// Every door because it is about one's own timetable and its refusals say nothing about anybody
// else. FINAL because a semester that is over has nothing left to check against.
func MayConfirmOwnAssignments(a principal.Actor, s SemesterState) bool {
	return a.Authenticated() && s.Phase != PhaseFinal
}
