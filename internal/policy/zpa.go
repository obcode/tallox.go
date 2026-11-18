package policy

import "github.com/obcode/tallox.go/internal/principal"

// MayReadZPAImport reports whether an actor may see how the module import is doing.
//
// The union of ADMIN and DEANS_OFFICE, and it is the first rule here that unions those two, so
// it needs its sentence.
//
// The import is an operational thing: it talks to another institution's system with a shared
// credential, it can fail, and its failure is the sort of event an administrator handles. By
// the line this codebase draws everywhere else — running the system is a different job from
// planning with it — that puts it squarely under ADMIN.
//
// But the *need* to look at it arises entirely within planning. The question "did the module
// catalogue update before I enter the demand" belongs to the dean's office, and making them
// acquire ADMIN to answer it would push exactly the behaviour the access design works hardest
// to avoid: people holding a role because a screen is behind it rather than because the job is
// theirs.
//
// Narrowing this later is not a breaking change to the schema, and widening it would be a
// decision made under pressure in October. So it starts as the union.
//
// OBSERVER reads it too (2026-11-03): how the catalogue gets into Tallox is exactly what somebody
// building a neighbouring system needs to see, and nothing in a run report is about a person's
// teaching. Reading only — see MayTriggerZPASync.
//
// The leads read it since 2026-11-18, because they may start a run (see MayTriggerZPASync) and a
// button whose result one cannot read is half a button.
//
// With full reach only: a browser session or a token minted with full reach.
func MayReadZPAImport(a principal.Actor) bool {
	if !MayActWithFullReach(a) {
		return false
	}
	roles := RolesOf(a)
	return mayRunZPAImport(roles) || leadsSomething(a) || roles.Has(RoleObserver)
}

// MayTriggerZPASync reports whether an actor may set off an import run by hand.
//
// The people who read it because running or planning is their job. Whoever is allowed to see that
// the catalogue is three weeks stale for that reason is the person who should be able to do
// something about it; splitting the two would produce a screen that reports a problem and offers
// no way to act on it, which is how people learn to ignore a screen. The observer is the exception
// on purpose: it reads in order to understand, and an observer writes nothing anywhere.
//
// Programme and subject group leads joined on 2026-11-18. A new module is entered in the
// examination office's system at their initiative, and the next thing they want is to plan with
// it — waiting for the night, or for the dean's office to press a button, is a day lost for nothing.
// Only a lead that leads something: an unscoped grant is the empty permission here as everywhere
// else, and "may fetch the catalogue" is not a reason to read it differently.
//
// What keeps this from being a way to hammer the examination office's system is not the role
// but the two limits around it, and they matter more with more people at the button: a run
// already in progress refuses a second one (one advisory lock for the button and the nightly job
// alike), and a run that succeeded minutes ago is refused.
func MayTriggerZPASync(a principal.Actor) bool {
	return MayActWithFullReach(a) && (mayRunZPAImport(RolesOf(a)) || leadsSomething(a))
}

// MayProjectZPACatalogue reports whether an actor may rebuild the catalogue from the cached
// payloads without fetching.
//
// Not opened to the leads with the sync. A rebuild is for when the projection's rules change — an
// operational act after a deploy, not something a planning question ever needs: a lead who wants
// a new module asks for a sync, and every successful sync projects.
func MayProjectZPACatalogue(a principal.Actor) bool {
	return MayActWithFullReach(a) && mayRunZPAImport(RolesOf(a))
}

func mayRunZPAImport(roles RoleSet) bool {
	return roles.Has(RoleAdmin) || roles.Has(RoleDeansOffice)
}

// leadsSomething reports whether an actor leads at least one study programme or subject group.
func leadsSomething(a principal.Actor) bool {
	return !PlanningScope(a).Empty() || !AssignmentScope(a).Empty()
}

// ZPAImportReason is what a caller who failed those checks is told. German, like every other
// string a person reads.
const ZPAImportReason = "Den ZPA-Abgleich stoßen die Administration, das Dekanat und die " +
	"Studiengangs- und Fachgruppenleitungen an; mit der Rolle Einsicht lässt er sich nur lesen."

// ZPAProjectionReason is what a caller refused a rebuild of the catalogue is told.
const ZPAProjectionReason = "Den Katalog ohne Abgleich neu aufbauen können nur die " +
	"Administration und das Dekanat."
