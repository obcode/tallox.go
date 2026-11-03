package policy

import "github.com/obcode/tallox.go/internal/principal"

// Observers: somebody who follows the whole planning without taking part in it — the colleague
// building a campus management system who needs to see Tallox's data and process as they are.
// Decided 2026-11-03: a role of its own, faculty-wide, granted only with an expiry, that reads
// everything not confidential and nothing that is.
//
// What that means in rules, held alone:
//
//   - The demand, the catalogue, the controlling, the subject groups, the planning marks and the
//     published assignments: what any account reads, all programmes.
//   - The examination office import (MayReadZPAImport): the one non-personal view that is
//     otherwise behind a role.
//   - No other people's statements — wishes, desired hours, assigned load — not even after
//     publication. Publication opens them to the colleagues who planned together; an observer
//     is not one of them. The partner's line, drawn for the same reason (partner.go).
//   - Nothing written: no wishes, no competences, no subject group memberships, no desired hours.
//     An observer who joined a subject group would turn up in the faculty's rosters.
//
// Unlike the partner, an observer is not narrowed to programmes and sees the rosters (who
// teaches, who leads): the published assignments name the same people anyway.
//
// It is on no exception list of the confidentiality rule. That is the difference from the
// DEVELOPER role this project refused: the golden matrices before publication do not change.
//
// Held together with any other role it changes nothing, as with the partner.

// ObserverOnly reports whether the actor's reach is the observer's: authenticated, holding
// OBSERVER and nothing else.
func ObserverOnly(a principal.Actor) bool {
	roles := RolesOf(a)
	return a.Authenticated() && roles.Has(RoleObserver) && len(roles) == 1
}

// Onlooker reports whether the actor follows the planning from outside the faculty's colleagues:
// a partner or an observer, held alone. An onlooker reads no other people's statements, in any
// state, and states nothing of their own.
//
// One predicate for both because every rule that names one has to name the other, and a rule
// that remembered only the partner would open published wishes to an observer without anybody
// deciding it.
func Onlooker(a principal.Actor) bool {
	return PartnerOnly(a) || ObserverOnly(a)
}

// ObserverReason is the refusal an observer meets on a write path.
const ObserverReason = "Mit der Rolle Einsicht liest Du die Planung; eintragen lässt sich hier nichts."
