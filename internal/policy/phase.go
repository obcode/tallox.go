package policy

// Phase is where a semester stands in the planning process.
//
// Two properties are load-bearing and neither is obvious from the type:
//
// The phase is stored on the semester row and advanced by an audited mutation — never derived
// from the calendar. Dates slip, and a process that silently moves on because a Tuesday
// arrived is a process nobody trusts. It also makes the switch a visible artefact at the
// faculty retreat: somebody flips it, and everyone can see who and when.
//
// The phase is separate from wishes_published_at. It is tempting to fold them together —
// "publication happens when the wish phase ends" — but the process needs both halves
// independently: the wish phase can close without publishing (so late entries stop while the
// planners work), and publication can happen while the assignment is already running. Folding
// them would make one of those two impossible, and it is not knowable today which one the
// faculty will need first.
type Phase string

const (
	// PhaseDemandPlanning is when the programme leads declare which instances are needed.
	PhaseDemandPlanning Phase = "DEMAND_PLANNING"
	// PhaseWishes is when lecturers register interest. Entries are confidential — that is
	// the point of the phase, not a property of it.
	PhaseWishes Phase = "WISHES"
	// PhaseAssignment is when the subject group leads fill the instances.
	PhaseAssignment Phase = "ASSIGNMENT"
	// PhaseReview is the programme leads' look at the whole after the assignment, and it lasts
	// until the semester is over.
	//
	// Asked for by the faculty (2026-10-16): the programme leads answer to the dean's office for
	// what their programme spends, and that can only be judged once the instances are filled —
	// whether three external lecturers were found or the lead takes three cohorts themselves and
	// merges their lectures changes the demand. So everything stays writable here, the demand,
	// the cut of it and who teaches what, and stays so until the semester has run: a colleague
	// who falls ill between the planning and the first lecture is a review, not a reopening.
	PhaseReview Phase = "REVIEW"
	// PhaseFinal is when the semester is over and its plan is the record of what was taught.
	// Nothing is written from here on.
	PhaseFinal Phase = "FINAL"
)

// AllPhases returns the phases in process order.
//
// The order is the process, so Before and After below derive from this slice rather than from
// a separately maintained rank — one list to get wrong instead of two.
func AllPhases() []Phase {
	return []Phase{
		PhaseDemandPlanning,
		PhaseWishes,
		PhaseAssignment,
		PhaseReview,
		PhaseFinal,
	}
}

// ParsePhase turns a stored phase string into a Phase.
//
// Unlike an unknown role, an unknown phase is not something a rule can shrug off: it means
// the semester row says something this code cannot act on. Callers get the false and are
// expected to treat it as an error rather than substituting a default — a default here would
// most likely be DEMAND_PLANNING, which is the most permissive phase for writes.
func ParsePhase(s string) (Phase, bool) {
	for _, p := range AllPhases() {
		if string(p) == s {
			return p, true
		}
	}
	return "", false
}

// Valid reports whether p is one of the known phases.
func (p Phase) Valid() bool {
	_, ok := ParsePhase(string(p))
	return ok
}

// index is the position in the process, or -1 for an unknown phase.
func (p Phase) index() int {
	for i, known := range AllPhases() {
		if known == p {
			return i
		}
	}
	return -1
}

// Before reports whether p comes earlier in the process than other.
//
// An unknown phase is neither before nor after anything: comparing against a value that is
// not in the process cannot produce a meaningful answer, and returning one would let a typo
// in the database decide whether a deadline has passed.
func (p Phase) Before(other Phase) bool {
	pi, oi := p.index(), other.index()
	return pi >= 0 && oi >= 0 && pi < oi
}

// After reports whether p comes later in the process than other.
func (p Phase) After(other Phase) bool {
	pi, oi := p.index(), other.index()
	return pi >= 0 && oi >= 0 && pi > oi
}
