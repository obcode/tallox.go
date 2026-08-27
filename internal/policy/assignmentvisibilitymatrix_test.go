package policy_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/obcode/tallox.go/internal/golden"
	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/testdata"
)

// TestAssignmentVisibilityMatrix renders who sees which assignment, and when.
//
// The fourth committed matrix, and the second one that answers a confidentiality question rather
// than a permission question. It exists for the two reasons the wish matrix gives: every change to
// who sees what becomes a reviewable diff, and the file is the slide for the faculty retreat when
// somebody asks why the plan is not visible while it is being made.
//
// Re-record with: go test ./internal/policy/ -update-golden — and then read the diff.
func TestAssignmentVisibilityMatrix(t *testing.T) {
	t.Parallel()

	golden.Assert(t, "assignment_visibility_matrix", renderAssignmentVisibilityMatrix())
}

// Its own widths: one column fewer than the wish matrix, because the phase is not a dimension of
// this rule and a column of identical answers is a column that teaches nobody anything.
var assignmentVisibilityWidths = []int{20, 13, 17, 15, 6, 9, 9, 9, 16}

func assignmentVisibilityRow(cells ...string) string {
	var b strings.Builder
	for i, c := range cells {
		if i == len(cells)-1 {
			b.WriteString(c)
			break
		}
		b.WriteString(pad(c, assignmentVisibilityWidths[i]))
	}
	b.WriteString("\n")
	return b.String()
}

func renderAssignmentVisibilityMatrix() string {
	var b strings.Builder

	b.WriteString(assignmentMatrixPreamble)

	header := assignmentVisibilityRow("Role", "Door", "Responsible for", "Assignments",
		"own", "prog. 1", "group 1", "neither", "Filter")
	b.WriteString(header)
	b.WriteString(rule(header))

	previous := ""
	for _, c := range callers() {
		if previous != "" && c.label != previous {
			b.WriteString("\n")
		}
		previous = c.label

		for _, state := range []struct {
			label string
			state policy.SemesterState
		}{
			{"confidential", policy.SemesterState{Phase: policy.PhaseAssignment}},
			{"published", assignmentsPublished},
		} {
			// Four assignments, one per column. The caller holds only the first; the other three
			// are held by a colleague and differ in what they hang off, which is what makes the
			// two reaches readable as separate columns.
			own := policy.Assignment{
				AssigneeID: c.actor.ID, ProgrammeID: programmeTwo, SubjectGroupID: groupTwo,
			}
			inProgramme := policy.Assignment{
				AssigneeID: testdata.Zwei.ID(), ProgrammeID: programmeOne, SubjectGroupID: groupTwo,
			}
			inGroup := policy.Assignment{
				AssigneeID: testdata.Zwei.ID(), ProgrammeID: programmeTwo, SubjectGroupID: groupOne,
			}
			neither := policy.Assignment{
				AssigneeID: testdata.Zwei.ID(), ProgrammeID: programmeTwo, SubjectGroupID: groupTwo,
			}

			scoped := c.scoped
			if scoped == "" {
				scoped = "—"
			}

			b.WriteString(assignmentVisibilityRow(
				c.label,
				c.door,
				scoped,
				state.label,
				answer(policy.CanSeeAssignment(c.actor, state.state, own)),
				answer(policy.CanSeeAssignment(c.actor, state.state, inProgramme)),
				answer(policy.CanSeeAssignment(c.actor, state.state, inGroup)),
				answer(policy.CanSeeAssignment(c.actor, state.state, neither)),
				assignmentFilterLabel(policy.AssignmentVisibility(c.actor, state.state)),
			))
		}
		b.WriteString("\n")
	}

	return b.String()
}

func assignmentFilterLabel(f policy.AssignmentFilter) string {
	switch f.Scope {
	case policy.AssignmentReadScopeAll:
		return "all"
	case policy.AssignmentReadScopeOwnOrScoped:
		return "own + scoped"
	case policy.AssignmentReadScopeOwn:
		return "own only"
	case policy.AssignmentReadScopeNone:
		return "no access"
	default:
		return fmt.Sprintf("?? %s", f.Scope)
	}
}

const assignmentMatrixPreamble = `Assignment visibility — who sees which assignment, and when
==========================================================

Generated from internal/policy (TestAssignmentVisibilityMatrix). Do not edit by hand:

    go test ./internal/policy/ -update-golden

The rule
--------

An assignment is visible if and only if

  · the person asking holds it themselves, or
  · the assignments of that semester have been published
    (semester.assignments_published_at IS NOT NULL), or
  · the person is responsible for it — they lead the study programme whose demand the instance
    is, or the subject group the instance's module belongs to, or they are the dean's office
    — and then only in an interactive session, never through a Personal Access Token.

Word for word the wish rule with one noun changed, and deliberately so: two rules that read the
same are two rules a reader can hold in their head at once. What differs is why they exist.

Why this one exists
-------------------

The wish window exists to end a first-come-first-served race: a new colleague should be able to
register interest without it looking like an attack on somebody who has taught the subject for
years.

The assignment window exists for a different reason, and it is worth stating because it sounds
weaker and is not. A plan that is half made is not a plan; a faculty that can watch it being made
will ask about decisions nobody has taken yet, and the subject group lead who expects those
questions will prepare the plan somewhere else — in a spreadsheet, in a mail thread — and enter
the finished result here. That is the failure this system was built to end, arriving through the
front door.

There is no un-publishing, in either case. Once colleagues have seen it, clearing the timestamp
would only be a lie about it.

Two marks, not one
------------------

semester.wishes_published_at and semester.assignments_published_at are independent in both
directions. The ordinary case is wishes published while the assignment runs — that is what lets
the assignment be made from a complete picture. The reverse is unusual and legitimate: a finished
plan may be published to a faculty whose wishes were never made public at all.

The phase is not a dimension of this rule
-----------------------------------------

Reading an assignment does not depend on where the semester stands, only on the publication mark
and on responsibility — asserted by TestAssignmentVisibilityDoesNotDependOnThePhase. *Writing* one
does depend on the phase, and closes in the two phases before the assignment; that is the write
matrix.

Held by somebody with no account
--------------------------------

An assignment may name a teacher who has no person row — a lecturer on contract, somebody at
another institution. Such a row has no assignee id, so nobody reads it as "their own"; it is
reached through responsibility or after publication, like any other. That is a consequence of the
rule and not an exception to it, but it is the case a hand-written check gets wrong, because both
sides of "is this mine" are then the nil id.

The one that surprises people
-----------------------------

**A Personal Access Token never reads somebody else's unpublished assignment**, not even for the
dean's office. A token is long-lived, sits in a script, and decouples "who saw this" from any
login event. What one holds oneself stays readable through both doors — that is one's own
timetable, and building a calendar from it is the first script a colleague will write.

**ADMIN is not on the exception list.** Running the system is a different job from planning with
it. An administrator who genuinely has to look is granted DEANS_OFFICE, visibly and with an
expiry.

The columns
-----------

  Responsible for   Which study programme or subject group this person has been assigned.
  own               An assignment this caller holds.
  prog. 1 / group 1 A colleague's assignment, reached through one axis or the other.
  neither           A colleague's assignment on neither axis.
  Filter            What AssignmentVisibility narrows a query to. The list and the count go
                    through it alike — "zwei der drei Praktika sind vergeben" is the
                    confidential fact with the names taken out.

`
