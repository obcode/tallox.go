package store_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/store/storetest"
)

// The programme quota and the import filter against a real database: the upsert, the delete,
// the study year a figure is kept under, and the WHERE clause that decides what a programme
// takes from elsewhere.

func TestAProgrammeQuotaIsKeptPerStudyYear(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()

	quota, err := f.demand.SetProgrammeQuota(ctx, storetest.FixtureProgrammeA, 2026, 425, uuid.Nil)
	if err != nil {
		t.Fatalf("cannot set the quota: %v", err)
	}
	if quota == nil || quota.Programme.Code != storetest.FixtureProgrammeA ||
		quota.TeachingHours != 425 || quota.StudyYear != 2026 {
		t.Fatalf("set %+v", quota)
	}

	if _, err := f.demand.SetProgrammeQuota(ctx, storetest.FixtureProgrammeA, 2026, 412.5,
		uuid.Nil); err != nil {
		t.Fatalf("cannot correct the quota: %v", err)
	}
	if _, err := f.demand.SetProgrammeQuota(ctx, storetest.FixtureProgrammeA, 2027, 999,
		uuid.Nil); err != nil {
		t.Fatalf("cannot set next year's quota: %v", err)
	}

	quotas, err := f.demand.ProgrammeQuotas(ctx, 2026)
	if err != nil {
		t.Fatal(err)
	}
	if len(quotas) != 1 || quotas[0].TeachingHours != 412.5 {
		t.Errorf("2026 holds %+v, want one corrected quota of 412.5", quotas)
	}

	if err := f.demand.ClearProgrammeQuota(ctx, storetest.FixtureProgrammeA, 2026); err != nil {
		t.Fatalf("cannot take the quota back: %v", err)
	}
	if err := f.demand.ClearProgrammeQuota(ctx, storetest.FixtureProgrammeA, 2026); err != nil {
		t.Errorf("taking back what is not there is an error: %v", err)
	}
	if quotas, _ := f.demand.ProgrammeQuotas(ctx, 2026); len(quotas) != 0 {
		t.Errorf("2026 still holds %+v", quotas)
	}
	if quotas, _ := f.demand.ProgrammeQuotas(ctx, 2027); len(quotas) != 1 {
		t.Errorf("taking back 2026 touched 2027: %+v", quotas)
	}
}

func TestAQuotaForAnUnknownProgrammeIsNothing(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)

	quota, err := f.demand.SetProgrammeQuota(t.Context(), "NOPE", 2026, 10, uuid.Nil)
	if err != nil || quota != nil {
		t.Errorf("an unknown programme gave (%+v, %v), want (nil, nil)", quota, err)
	}
}

// The database refuses what the service would refuse, so that an import or an admin command
// cannot write it either.
func TestTheDatabaseRefusesANegativeQuota(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)

	if _, err := f.demand.SetProgrammeQuota(t.Context(), storetest.FixtureProgrammeA, 2026, -1,
		uuid.Nil); err == nil {
		t.Error("a negative quota was stored")
	}
}

// What a programme takes from elsewhere: PA's module declared for PB is PB's import and not
// PA's, and PA's own instance is nobody's.
func TestTheForeignHomeFilter(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	f.declareIn(t, storetest.FixtureProgrammeA, "")
	f.declareSeparatelyIn(t, storetest.FixtureProgrammeB, "")

	imports := func(programme string) []domain.CourseInstance {
		t.Helper()
		instances, err := f.demand.CourseInstances(t.Context(), domain.DemandFilter{Reach: everyProgramme,
			SemesterCode: f.semester.Code, Programme: programme, ForeignHome: true,
		})
		if err != nil {
			t.Fatalf("cannot read the imports of %s: %v", programme, err)
		}
		return instances
	}

	if got := imports(storetest.FixtureProgrammeA); len(got) != 0 {
		t.Errorf("PA imports %d instances of its own module", len(got))
	}
	got := imports(storetest.FixtureProgrammeB)
	if len(got) != 1 || got[0].Module.HomeProgramme.Code != storetest.FixtureProgrammeA {
		t.Errorf("PB imports %+v, want the one instance of PA's module", got)
	}
	if all := imports(""); len(all) != 1 {
		t.Errorf("across the semester %d instances are imports, want 1", len(all))
	}
}
