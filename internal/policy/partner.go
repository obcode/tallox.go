package policy

import (
	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/principal"
)

// Partner faculties: FK10, MUC.DAI, MUC.HEALTH and DPH follow the planning of the study
// programmes they share with this faculty. Decided 2026-10-18: a role of its own, scoped to study
// programmes, that reads *less* than an ordinary account.
//
// An ordinary account reads the whole demand, the controlling and every published assignment —
// none of it is confidential inside the faculty. A partner is not inside the faculty, so held
// alone the role narrows all three to its programmes, reads no wishes at all (not even published
// ones: they are the faculty's colleagues talking to each other), and writes nothing — no wishes,
// no desired hours, no competences, no subject group memberships.
//
// Held together with any other role it changes nothing: whoever is also a lecturer here is a
// colleague, and the colleague's reach wins. That is a union of grants, the same as everywhere
// else, and it keeps the role preview honest — narrowing to PARTNER_READER shows exactly what a
// partner sees.

// PartnerOnly reports whether the actor's reach is the partner's: authenticated, holding
// PARTNER_READER and nothing else.
func PartnerOnly(a principal.Actor) bool {
	roles := RolesOf(a)
	return a.Authenticated() && roles.Has(RolePartnerReader) && len(roles) == 1
}

// DemandReadScope is which study programmes' demand, controlling and published assignments the
// actor reads. Everybody else with an account reads all of them; a partner reads the programmes
// it has been assigned, and an unassigned partner reads none — the empty set is the empty grant
// here, as it is for a programme lead.
func DemandReadScope(a principal.Actor) ProgrammeScope {
	if !PartnerOnly(a) {
		return ProgrammeScope{All: true}
	}
	return ProgrammeScope{IDs: scopedIDs(a, RolePartnerReader, func(s principal.RoleScope) uuid.UUID {
		return s.ProgrammeID
	})}
}

// MayServeOneself reports whether the actor may maintain the statements that are about oneself as
// a member of this faculty: joining subject groups and stating competences. Wishes and desired
// hours need LECTURER in the write matrix already; these two needed only an account.
func MayServeOneself(a principal.Actor) bool {
	return a.Authenticated() && !PartnerOnly(a)
}

// PartnerReason is the refusal a partner meets on a write path.
const PartnerReason = "Als Partnerfakultät liest Du die Planung Deiner Studiengänge; " +
	"eintragen lässt sich hier nichts."
