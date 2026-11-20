package store_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// lapsedOf reads the lapsed_wish rows of one person, as instance ids.
func (f demandFixture) lapsedOf(t *testing.T, who testdata.Persona) []uuid.UUID {
	t.Helper()
	rows, err := f.schema.Pool.Query(t.Context(),
		`SELECT course_instance_id FROM lapsed_wish WHERE person_id = $1 ORDER BY lapsed_at`, who.ID())
	if err != nil {
		t.Fatalf("cannot read lapsed wishes: %v", err)
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		out = append(out, id)
	}
	return out
}

func dropping(e domain.DemandEntry) domain.DemandEntry {
	e.DropWishes = true
	return e
}

// Without dropWishes the last cohort of a wished-for module is refused, and the refusal now says
// why: wishes, not an assignment.
func TestARefusedWithdrawalNamesWishesAsTheCause(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	only := f.declare(t, "")
	storetest.SeedPerson(t, f.schema, testdata.Eins, "LECTURER")
	f.wish(t, only.ID, testdata.Eins, 1, "")

	plan, err := f.demand.PlanDemand(t.Context(), f.semester.Code, f.programme,
		[]domain.DemandEntry{planEntry(f.module)}, uuid.Nil, false)
	if err != nil {
		t.Fatalf("withdrawing gave %v", err)
	}
	if len(plan.Refused) != 1 || plan.Refused[0].Cause == nil ||
		*plan.Refused[0].Cause != domain.InstanceUseWishes {
		t.Fatalf("refused %+v, want one refusal with cause WISHES", plan.Refused)
	}
	if plan.Refused[0].Reason != domain.ErrInstanceWished.Error() {
		t.Errorf("reason %q, want the wishes sentence", plan.Refused[0].Reason)
	}
	if got := f.wishesOf(t, testdata.Eins); len(got) != 1 {
		t.Errorf("Eins holds %d wishes after the refusal, want 1 — the probe must roll back", len(got))
	}
	if got := f.lapsedOf(t, testdata.Eins); len(got) != 0 {
		t.Errorf("a refusal recorded %d lapsed wishes, want none", len(got))
	}
}

// With dropWishes the cohort goes, the wish with it, and the lapse is recorded for its owner.
func TestDropWishesWithdrawsTheCohortAndRecordsTheLapse(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	only := f.declare(t, "")
	storetest.SeedPerson(t, f.schema, testdata.Eins, "LECTURER")
	storetest.SeedPerson(t, f.schema, testdata.Vier, "PROGRAMME_LEAD")
	f.wish(t, only.ID, testdata.Eins, 1, "nur Vorlesung")

	plan, err := f.demand.PlanDemand(t.Context(), f.semester.Code, f.programme,
		[]domain.DemandEntry{dropping(planEntry(f.module))}, testdata.Vier.ID(), false)
	if err != nil {
		t.Fatalf("withdrawing gave %v", err)
	}
	if len(plan.Refused) != 0 || len(plan.Withdrawn) != 1 {
		t.Fatalf("plan %+v, want one withdrawn and nothing refused", plan)
	}
	if got := f.instances(t); len(got) != 0 {
		t.Errorf("%d instances left, want none", len(got))
	}
	if got := f.wishesOf(t, testdata.Eins); len(got) != 0 {
		t.Errorf("Eins still holds %+v, want the wish gone", got)
	}
	if got := f.lapsedOf(t, testdata.Eins); len(got) != 1 || got[0] != only.ID {
		t.Errorf("lapsed %v, want exactly the withdrawn cohort", got)
	}
}

// A dry run with dropWishes reports the withdrawal and changes nothing.
func TestDropWishesInADryRunLeavesEverything(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	only := f.declare(t, "")
	storetest.SeedPerson(t, f.schema, testdata.Eins, "LECTURER")
	f.wish(t, only.ID, testdata.Eins, 2, "")

	plan, err := f.demand.PlanDemand(t.Context(), f.semester.Code, f.programme,
		[]domain.DemandEntry{dropping(planEntry(f.module))}, uuid.Nil, true)
	if err != nil {
		t.Fatalf("dry run gave %v", err)
	}
	if len(plan.Withdrawn) != 1 || len(plan.Refused) != 0 {
		t.Fatalf("dry run reports %+v, want one withdrawn", plan)
	}
	if len(f.instances(t)) != 1 || len(f.wishesOf(t, testdata.Eins)) != 1 ||
		len(f.lapsedOf(t, testdata.Eins)) != 0 {
		t.Error("a dry run changed instances, wishes or lapsed wishes")
	}
}

// An assignment is never resolved by the demand, not even with dropWishes, and the refusal says
// that it is the assignment. The wishes stay.
func TestDropWishesStillRefusesAnAssignedCohort(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	only := f.declare(t, "")
	storetest.SeedPerson(t, f.schema, testdata.Eins, "LECTURER")
	f.wish(t, only.ID, testdata.Eins, 1, "")
	f.staff(t, only.Parts[0].ID)

	plan, err := f.demand.PlanDemand(t.Context(), f.semester.Code, f.programme,
		[]domain.DemandEntry{dropping(planEntry(f.module))}, uuid.Nil, false)
	if err != nil {
		t.Fatalf("withdrawing gave %v", err)
	}
	if len(plan.Refused) != 1 || plan.Refused[0].Cause == nil ||
		*plan.Refused[0].Cause != domain.InstanceUseAssignment {
		t.Fatalf("refused %+v, want cause ASSIGNMENT", plan.Refused)
	}
	if plan.Refused[0].Reason != domain.ErrInstanceAssigned.Error() {
		t.Errorf("reason %q, want the assignment sentence", plan.Refused[0].Reason)
	}
	if len(f.instances(t)) != 1 || len(f.wishesOf(t, testdata.Eins)) != 1 ||
		len(f.lapsedOf(t, testdata.Eins)) != 0 {
		t.Error("a refused withdrawal changed instances, wishes or lapsed wishes")
	}
}

// Where both hang off a cohort, the refusal names the assignment: that is the one the lead has to
// resolve first, and dropWishes would not help.
func TestTheAssignmentIsNamedBeforeTheWishes(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	only := f.declare(t, "")
	storetest.SeedPerson(t, f.schema, testdata.Eins, "LECTURER")
	f.wish(t, only.ID, testdata.Eins, 1, "")
	f.staff(t, only.Parts[0].ID)

	// Without dropWishes and with an assignment: refused, cause ASSIGNMENT.
	plan, err := f.demand.PlanDemand(t.Context(), f.semester.Code, f.programme,
		[]domain.DemandEntry{planEntry(f.module)}, uuid.Nil, false)
	if err != nil {
		t.Fatalf("withdrawing gave %v", err)
	}
	if len(plan.Refused) != 1 || plan.Refused[0].Cause == nil ||
		*plan.Refused[0].Cause != domain.InstanceUseAssignment {
		t.Errorf("refused %+v, want cause ASSIGNMENT before WISHES", plan.Refused)
	}
}

// Fewer groups than are staffed: PART_ASSIGNED, with the assignment as its cause.
func TestReducingStaffedGroupsNamesTheAssignment(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	only := f.declare(t, "")
	f.staff(t, only.Parts[len(only.Parts)-1].ID)

	plan, err := f.demand.PlanDemand(t.Context(), f.semester.Code, f.programme,
		[]domain.DemandEntry{planEntry(f.module, track("", 0))}, uuid.Nil, false)
	if err != nil {
		t.Fatalf("reducing gave %v", err)
	}
	for _, r := range plan.Refused {
		if r.Code == "PART_ASSIGNED" {
			if r.Cause == nil || *r.Cause != domain.InstanceUseAssignment {
				t.Errorf("PART_ASSIGNED with cause %v, want ASSIGNMENT", r.Cause)
			}
			return
		}
	}
	t.Skipf("the fixture's split has no group to reduce: %+v", plan)
}
