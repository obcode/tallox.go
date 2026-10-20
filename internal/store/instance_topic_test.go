package store_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/domain"
)

// The topic of a cohort — which seminar a cohort of an umbrella module is — against a real
// database: the CHECK that gives "no topic" one spelling, and which of the paths that make a
// cohort carry the topic along.

func TestATopicHasOneSpellingForNone(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()

	if _, err := f.demand.PlanDemand(ctx, f.semester.Code, f.programme,
		[]domain.DemandEntry{planEntry(f.module, track("", 1))}, uuid.Nil, false); err != nil {
		t.Fatalf("planning gave %v", err)
	}
	id := f.instances(t)[0].ID

	for _, tc := range []struct {
		name  string
		topic any
		ok    bool
	}{
		{"no topic", nil, true},
		{"a heading", "Green Software Engineering", true},
		{"the longest heading", strings.Repeat("ä", 120), true},
		// The empty string would be a second way of saying "none".
		{"the empty string", "", false},
		{"untrimmed", " Design Thinking", false},
		{"a paragraph", strings.Repeat("x", 121), false},
	} {
		_, err := f.schema.Pool.Exec(ctx, `UPDATE course_instance SET topic = $2 WHERE id = $1`,
			id, tc.topic)
		if tc.ok && err != nil {
			t.Errorf("%s: refused with %v", tc.name, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("%s: admitted", tc.name)
		}
	}
}

func TestTheTopicOfACohortIsCarriedOnlyIntoTheSameSeminar(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()

	topics := func() []string {
		t.Helper()
		out := []string{}
		for _, i := range f.instances(t) {
			out = append(out, i.Track+":"+i.Topic)
		}
		return out
	}
	expect := func(want ...string) {
		t.Helper()
		if got := topics(); !slices.Equal(got, want) {
			t.Fatalf("the cohorts are %v, want %v", got, want)
		}
	}
	set := func(id uuid.UUID, topic *string) {
		t.Helper()
		if _, err := f.demand.SetCourseInstanceTopic(ctx, id, topic); err != nil {
			t.Fatalf("setting the topic gave %v", err)
		}
	}
	design := "Design Thinking"

	if _, err := f.demand.PlanDemand(ctx, f.semester.Code, f.programme,
		[]domain.DemandEntry{planEntry(f.module, track("A", 0))}, uuid.Nil, false); err != nil {
		t.Fatalf("planning gave %v", err)
	}
	expect("A:")

	set(f.instances(t)[0].ID, &design)
	expect("A:Design Thinking")

	// A cohort added to the row is another seminar and arrives without a title.
	if _, err := f.demand.PlanDemand(ctx, f.semester.Code, f.programme,
		[]domain.DemandEntry{planEntry(f.module, track("A", 0), track("B", 0))},
		uuid.Nil, false); err != nil {
		t.Fatalf("adding a cohort gave %v", err)
	}
	expect("A:Design Thinking", "B:")

	// So does a duplicate — unlike the format, which a second cohort shares with its first.
	if _, err := f.demand.DuplicateCourseInstance(ctx, f.instances(t)[0].ID, "C", "",
		uuid.Nil); err != nil {
		t.Fatalf("duplicating gave %v", err)
	}
	expect("A:Design Thinking", "B:", "C:")

	// Renaming a cohort is the same cohort, and keeps what it is about.
	if _, err := f.demand.UpdateCourseInstance(ctx, f.instances(t)[0].ID, "D", nil); err != nil {
		t.Fatalf("renaming gave %v", err)
	}
	expect("B:", "C:", "D:Design Thinking")

	// nil takes the topic away.
	set(f.instances(t)[2].ID, nil)
	expect("B:", "C:", "D:")
}

// A semester's demand copied into the next one keeps its seminars' titles: last year's seminar
// is the best guess for this year's, and fourteen titles typed again is the work the copy exists
// to save.
func TestCopyingASemesterKeepsTheTopics(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()

	first, err := f.demand.CreateCourseInstance(ctx, domain.NewCourseInstance{
		SemesterID: f.previous.ID, ModuleID: f.module, ProgrammeID: f.programme, Track: "A",
	})
	if err != nil {
		t.Fatalf("cannot declare last semester's demand: %v", err)
	}
	green := "Green Software Engineering"
	if _, err := f.demand.SetCourseInstanceTopic(ctx, first.ID, &green); err != nil {
		t.Fatalf("setting the topic gave %v", err)
	}

	if _, err := f.demand.CopyDemand(ctx, f.previous, f.semester, f.programme, uuid.Nil); err != nil {
		t.Fatalf("cannot copy the demand: %v", err)
	}
	instances := f.instances(t)
	if len(instances) != 1 || instances[0].Topic != green {
		t.Fatalf("the copy holds %+v, want cohort A as %q", instances, green)
	}
}
