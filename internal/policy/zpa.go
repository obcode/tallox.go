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
// Interactive only, like everything else about running the installation.
func MayReadZPAImport(a principal.Actor) bool {
	if !MayReadInteractiveOnly(a) {
		return false
	}
	roles := RolesOf(a)
	return roles.Has(RoleAdmin) || roles.Has(RoleDeansOffice)
}

// MayTriggerZPASync reports whether an actor may set off an import run by hand.
//
// The same set as reading it. Whoever is allowed to see that the catalogue is three weeks stale
// is the person who should be able to do something about it; splitting the two would produce a
// screen that reports a problem and offers no way to act on it, which is how people learn to
// ignore a screen.
//
// What keeps this from being a way to hammer the examination office's system is not the role
// but the two limits around it: a run already in progress is returned rather than a second one
// started, and a run that succeeded minutes ago is refused.
func MayTriggerZPASync(a principal.Actor) bool {
	return MayReadZPAImport(a)
}

// ZPAImportReason is what a caller who failed those checks is told. German, like every other
// string a person reads.
const ZPAImportReason = "Nur die Administration und das Dekanat sehen den ZPA-Import."
