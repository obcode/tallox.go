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

// MayPublishWishes reports whether an actor may end the confidentiality window of a semester.
//
// MayAdministerSemesters *and* an interactive session. This is the one semester operation that
// is interactive-only, and the reason is not that it is more privileged — it is the same role
// — but that it is the only one that cannot be undone.
//
// Publishing is irreversible as a fact about the world rather than as a property of the
// schema: once colleagues have seen each other's entries, clearing the timestamp would only be
// a lie about it. An act that cannot be walked back should not be issuable by a string in a
// script that outlives the afternoon it was written on, and it should be attributable to a
// sign-in rather than to a credential.
//
// It is also the moment the project's politically load-bearing rule stops applying. If there is
// one action in this system that should require somebody to be present, it is that one.
func MayPublishWishes(a principal.Actor) bool {
	return MayReadInteractiveOnly(a) && RolesOf(a).Has(RoleDeansOffice)
}

// SemesterAdminReason is what a caller who failed MayAdministerSemesters is told.
//
// It says nothing about creating a semester, because nothing creates one. A message offering
// to have somebody "angelegt" would send colleagues asking the dean's office for a thing that
// does not exist.
const SemesterAdminReason = "Nur das Dekanat darf die Phase eines Semesters umschalten."

// PublishWishesReason is what a caller who failed MayPublishWishes is told.
//
// Deliberately two sentences: the first names the role, the second names the door, because the
// caller who hits this is most often somebody who *has* the role and is using a token. Telling
// them only "the dean's office may do this" would send them to ask for a grant they already
// hold.
const PublishWishesReason = "Nur das Dekanat darf Wünsche veröffentlichen. " +
	"Das geht ausschließlich im Browser, nicht über ein Personal Access Token."

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
