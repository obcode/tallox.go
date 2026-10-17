package store_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/store"
	"github.com/obcode/tallox.go/internal/store/storetest"
	"github.com/obcode/tallox.go/internal/testdata"
)

// Dividing a part, and handing one to another faculty (migration 29), against a real database.
//
// The rules here are statements about rows: the composite foreign key that keeps a piece the same
// kind as its part, the numeric arithmetic that keeps an instance's hours to the cent, the FOR
// SHARE that keeps a fill from landing on a part being given away, and the FILTER that keeps
// another faculty's hours out of the teaching load. A fake passes all four.

// hoursOf reads a part's hours, failing on a part that states none.
func hoursOf(t *testing.T, p domain.InstancePart) float64 {
	t.Helper()
	if p.TeachingHours == nil {
		t.Fatalf("part %s states no hours", p.ID)
	}
	return *p.TeachingHours
}

func partByID(t *testing.T, instance *domain.CourseInstance, id uuid.UUID) domain.InstancePart {
	t.Helper()
	for _, p := range instance.Parts {
		if p.ID == id {
			return p
		}
	}
	t.Fatalf("instance %s has no part %s", instance.ID, id)
	return domain.InstancePart{}
}

func piecesOf(instance *domain.CourseInstance, root uuid.UUID) []domain.InstancePart {
	var out []domain.InstancePart
	for _, p := range instance.Parts {
		if p.DividedFrom == root {
			out = append(out, p)
		}
	}
	return out
}

// fourAndFour is the instance from the request: a lecture of 4 and a laboratory of 4.
func (f demandFixture) fourAndFour(t *testing.T, track string) (instance *domain.CourseInstance, lecture, lab uuid.UUID) {
	t.Helper()

	declared := f.declare(t, track)
	four := 4.0
	lecture = partOfKind(t, declared, domain.PartKindLecture).ID
	lab = partOfKind(t, declared, domain.PartKindLab).ID
	if _, err := f.demand.UpdateInstancePart(t.Context(), lecture, domain.PartKindLecture, &four); err != nil {
		t.Fatalf("cannot correct the lecture: %v", err)
	}
	instance, err := f.demand.UpdateInstancePart(t.Context(), lab, domain.PartKindLab, &four)
	if err != nil {
		t.Fatalf("cannot correct the laboratory: %v", err)
	}
	return instance, lecture, lab
}

// The case from the request: 2 + 2 lecture and 2 + 2 laboratory, and the instance still costs 8.
func TestDividingAPartKeepsTheInstancesHours(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()
	_, lecture, lab := f.fourAndFour(t, "")

	if _, err := f.demand.DivideInstancePart(ctx, lecture, 2); err != nil {
		t.Fatalf("cannot divide the lecture: %v", err)
	}
	divided, err := f.demand.DivideInstancePart(ctx, lab, 1.5)
	if err != nil {
		t.Fatalf("cannot divide the laboratory: %v", err)
	}

	if divided.TeachingHours() != 8 {
		t.Errorf("the divided instance costs %v hours, want the 8 it cost before", divided.TeachingHours())
	}
	if got := hoursOf(t, partByID(t, divided, lecture)); got != 2 {
		t.Errorf("the lecture kept %v hours, want 2", got)
	}
	if got := hoursOf(t, partByID(t, divided, lab)); got != 2.5 {
		t.Errorf("the laboratory kept %v hours, want 2.5", got)
	}

	pieces := piecesOf(divided, lecture)
	if len(pieces) != 1 || pieces[0].Kind != domain.PartKindLecture || hoursOf(t, pieces[0]) != 2 {
		t.Fatalf("the lecture's pieces are %+v, want one lecture of 2", pieces)
	}

	// Dividing a piece cuts from the piece, and the new piece names the undivided lecture: one
	// part with two pieces beside it, not a chain.
	again, err := f.demand.DivideInstancePart(ctx, pieces[0].ID, 1)
	if err != nil {
		t.Fatalf("cannot divide the piece: %v", err)
	}
	if got := len(piecesOf(again, lecture)); got != 2 {
		t.Errorf("the lecture has %d pieces, want two, both naming it", got)
	}
	if got := hoursOf(t, partByID(t, again, pieces[0].ID)); got != 1 {
		t.Errorf("the divided piece kept %v hours, want 1", got)
	}
	if again.TeachingHours() != 8 {
		t.Errorf("the instance costs %v hours, want 8 still", again.TeachingHours())
	}

	// Listed behind the part they were cut from, so a screen shows them together.
	if again.Parts[0].ID != lecture || again.Parts[1].DividedFrom != lecture || again.Parts[2].DividedFrom != lecture {
		t.Errorf("the lecture's pieces are not listed right behind it: %v", kinds(again.Parts))
	}
}

func TestWhatCannotBeDivided(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()
	instance := f.declare(t, "")
	lecture := partOfKind(t, instance, domain.PartKindLecture).ID

	for _, hours := range []float64{2, 3, 0.001} {
		if _, err := f.demand.DivideInstancePart(ctx, lecture, hours); !errors.Is(err, domain.ErrPartInvalid) {
			t.Errorf("cutting %v off a lecture of 2 gave %v, want ErrPartInvalid", hours, err)
		}
	}

	if _, err := f.demand.UpdateInstancePart(ctx, lecture, domain.PartKindLecture, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.demand.DivideInstancePart(ctx, lecture, 1); !errors.Is(err, domain.ErrPartInvalid) {
		t.Errorf("dividing a part with no hours gave %v, want ErrPartInvalid", err)
	}

	if _, err := f.demand.DivideInstancePart(ctx, uuid.New(), 1); !errors.Is(err, domain.ErrPartNotFound) {
		t.Errorf("dividing a part that does not exist gave %v, want ErrPartNotFound", err)
	}

	lab := partOfKind(t, instance, domain.PartKindLab).ID
	if _, err := f.demand.SetInstancePartTaughtBy(ctx, lab, "FK 04"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.demand.DivideInstancePart(ctx, lab, 1); !errors.Is(err, domain.ErrPartTaughtElsewhere) {
		t.Errorf("dividing another faculty's part gave %v, want ErrPartTaughtElsewhere", err)
	}
}

func TestRejoiningGivesThePieceBack(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()
	_, lecture, _ := f.fourAndFour(t, "")

	divided, err := f.demand.DivideInstancePart(ctx, lecture, 1.25)
	if err != nil {
		t.Fatal(err)
	}
	piece := piecesOf(divided, lecture)[0].ID

	if _, err := f.demand.RejoinInstancePart(ctx, lecture); !errors.Is(err, domain.ErrPartNotDivided) {
		t.Errorf("rejoining an undivided part gave %v, want ErrPartNotDivided", err)
	}

	rejoined, err := f.demand.RejoinInstancePart(ctx, piece)
	if err != nil {
		t.Fatalf("cannot rejoin: %v", err)
	}
	if len(rejoined.Parts) != 2 || hoursOf(t, partByID(t, rejoined, lecture)) != 4 {
		t.Errorf("after rejoining the instance holds %v with a lecture of %v, want the two parts "+
			"of 4 it started with", kinds(rejoined.Parts), partByID(t, rejoined, lecture).TeachingHours)
	}
}

// The group count is the number of laboratory parts, and a piece is not one: without that a
// laboratory cut in two would become a second group on the next save of the planning screen.
func TestAPieceIsNotALaboratoryGroup(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()
	instance := f.declare(t, "")
	lab := partOfKind(t, instance, domain.PartKindLab).ID
	if _, err := f.demand.DivideInstancePart(ctx, lab, 1); err != nil {
		t.Fatal(err)
	}

	plan, err := f.demand.PlanDemand(ctx, f.semester.Code, f.programme,
		[]domain.DemandEntry{planEntry(f.module, track("", 1))}, uuid.Nil, false)
	if err != nil {
		t.Fatalf("planning gave %v", err)
	}
	if len(plan.Changed) != 0 || len(plan.Refused) != 0 {
		t.Errorf("planning the one group that is there changed %v and refused %v, want nothing",
			plan.Changed, plan.Refused)
	}
	if got := len(f.partsOf(t, f.module)); got != 3 {
		t.Errorf("the instance holds %d parts, want lecture, laboratory and its piece", got)
	}

	// A second group is added beside the divided one, and taking it away again leaves the
	// division alone.
	if _, err := f.demand.PlanDemand(ctx, f.semester.Code, f.programme,
		[]domain.DemandEntry{planEntry(f.module, track("", 2))}, uuid.Nil, false); err != nil {
		t.Fatal(err)
	}
	if got := len(f.partsOf(t, f.module)); got != 4 {
		t.Errorf("with two groups the instance holds %d parts, want four", got)
	}

	plan, err = f.demand.PlanDemand(ctx, f.semester.Code, f.programme,
		[]domain.DemandEntry{planEntry(f.module, track("", 0))}, uuid.Nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Refused) != 1 || plan.Refused[0].Code != "PART_DIVIDED" {
		t.Errorf("removing the divided group was answered with %v, want one PART_DIVIDED", plan.Refused)
	}
	if got := len(f.partsOf(t, f.module)); got != 3 {
		t.Errorf("after the refusal the instance holds %d parts, want the divided group kept", got)
	}
}

func TestADividedPartKeepsItsShape(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()
	_, lecture, _ := f.fourAndFour(t, "")
	divided, err := f.demand.DivideInstancePart(ctx, lecture, 2)
	if err != nil {
		t.Fatal(err)
	}
	piece := piecesOf(divided, lecture)[0].ID

	if _, err := f.demand.DeleteInstancePart(ctx, lecture); !errors.Is(err, domain.ErrPartDivided) {
		t.Errorf("removing a part with a piece gave %v, want ErrPartDivided", err)
	}
	two := 2.0
	if _, err := f.demand.UpdateInstancePart(ctx, lecture, domain.PartKindExercise, &two); !errors.Is(err, domain.ErrPartDivided) {
		t.Errorf("changing the kind of a divided part gave %v, want ErrPartDivided", err)
	}
	if _, err := f.demand.UpdateInstancePart(ctx, piece, domain.PartKindExercise, &two); !errors.Is(err, domain.ErrPartDivided) {
		t.Errorf("changing the kind of a piece gave %v, want ErrPartDivided", err)
	}

	// Withdrawing the whole instance takes the part and its pieces in one cascade, which is what
	// NO ACTION rather than RESTRICT on the piece's foreign key is for.
	if err := f.demand.DeleteCourseInstance(ctx, divided.ID); err != nil {
		t.Errorf("withdrawing an instance with a divided part gave %v", err)
	}
}

// Sharing a lecture across cohorts deletes the siblings' lectures and flags one, so a division
// on either side would be lost or half-shared. Refused, both ways round.
func TestSharingAcrossCohortsLeavesADivisionAlone(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()
	first := f.declare(t, "A")
	if _, err := f.demand.DuplicateCourseInstance(ctx, first.ID, "B", "", uuid.Nil); err != nil {
		t.Fatal(err)
	}
	lecture := partOfKind(t, first, domain.PartKindLecture).ID

	divided, err := f.demand.DivideInstancePart(ctx, lecture, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.demand.ShareInstancePartAcrossTracks(ctx, lecture); !errors.Is(err, domain.ErrPartDivided) {
		t.Errorf("sharing a divided lecture gave %v, want ErrPartDivided", err)
	}

	if _, err := f.demand.RejoinInstancePart(ctx, piecesOf(divided, lecture)[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.demand.ShareInstancePartAcrossTracks(ctx, lecture); err != nil {
		t.Fatalf("sharing the rejoined lecture gave %v", err)
	}

	// A shared lecture may be divided — the piece is shared as well — but then not un-shared,
	// because each cohort would get a copy of one piece.
	shared, err := f.demand.DivideInstancePart(ctx, lecture, 1)
	if err != nil {
		t.Fatalf("dividing the shared lecture gave %v", err)
	}
	if piece := piecesOf(shared, lecture)[0]; !piece.SharedAcrossTracks {
		t.Errorf("the piece of a shared lecture is not shared")
	}
	if _, err := f.demand.SplitInstancePartAcrossTracks(ctx, lecture); !errors.Is(err, domain.ErrPartDivided) {
		t.Errorf("un-sharing a divided lecture gave %v, want ErrPartDivided", err)
	}
}

// An arrangement with another faculty is usually next year's too. A copy that dropped it would
// put the hours back into this faculty's load without anybody deciding that.
func TestCopiesCarryDivisionsAndOtherFaculties(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()

	source, err := f.demand.CreateCourseInstance(ctx, domain.NewCourseInstance{
		SemesterID: f.previous.ID, ModuleID: f.module, ProgrammeID: f.programme, Track: "A",
	})
	if err != nil {
		t.Fatal(err)
	}
	lab := partOfKind(t, source, domain.PartKindLab).ID
	divided, err := f.demand.DivideInstancePart(ctx, lab, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.demand.SetInstancePartTaughtBy(ctx, piecesOf(divided, lab)[0].ID, "FK 04"); err != nil {
		t.Fatal(err)
	}

	assertCarried := func(t *testing.T, what string, instance *domain.CourseInstance) {
		t.Helper()
		if len(instance.Parts) != 3 {
			t.Fatalf("the %s holds %v, want lecture, laboratory and its piece", what, kinds(instance.Parts))
		}
		root := partOfKind(t, instance, domain.PartKindLab)
		if root.IsPiece() {
			root = partByID(t, instance, root.DividedFrom)
		}
		pieces := piecesOf(instance, root.ID)
		if len(pieces) != 1 || pieces[0].TaughtBy != "FK 04" || hoursOf(t, pieces[0]) != 1 {
			t.Errorf("the %s's laboratory has pieces %+v, want one of 1 hour taught by FK 04", what, pieces)
		}
		if root.TaughtElsewhere() || hoursOf(t, root) != 1 {
			t.Errorf("the %s's laboratory is %+v, want ours with 1 hour", what, root)
		}
	}

	if _, err := f.demand.CopyDemand(ctx, f.previous, f.semester, f.programme, uuid.Nil); err != nil {
		t.Fatalf("cannot copy: %v", err)
	}
	copied := f.instances(t)
	if len(copied) != 1 {
		t.Fatalf("the copy declared %d instances, want one", len(copied))
	}
	assertCarried(t, "copy", &copied[0])

	duplicate, err := f.demand.DuplicateCourseInstance(ctx, source.ID, "B", "", uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	assertCarried(t, "second cohort", duplicate)
}

// Another faculty's hours are not this faculty's teaching. They stay out of the load and are
// reported beside it.
func TestTeachingLoadLeavesOutWhatAnotherFacultyTeaches(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()
	_, lecture, lab := f.fourAndFour(t, "")

	for _, part := range []uuid.UUID{lecture, lab} {
		divided, err := f.demand.DivideInstancePart(ctx, part, 2)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.demand.SetInstancePartTaughtBy(ctx, piecesOf(divided, part)[0].ID, "FK 04"); err != nil {
			t.Fatal(err)
		}
	}

	load := loadOf(t, f.teachingLoad(t), storetest.FixtureProgrammeA)
	if load.TeachingHours() != 4 || load.ExternalHours() != 4 {
		t.Errorf("PA pays %v hours with %v taught elsewhere, want 4 and 4", load.TeachingHours(),
			load.ExternalHours())
	}

	// A part of another faculty's that states no hours is not an unfinished split of ours.
	current := f.instances(t)[0]
	if _, err := f.demand.UpdateInstancePart(ctx, piecesOf(&current, lab)[0].ID, domain.PartKindLab, nil); err != nil {
		t.Fatal(err)
	}
	if got := loadOf(t, f.teachingLoad(t), storetest.FixtureProgrammeA).PartsWithoutHours(); got != 0 {
		t.Errorf("another faculty's part without hours counts as %d of ours without hours, want 0", got)
	}
}

func TestATrailingSpaceIsNotAFaculty(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	instance := f.declare(t, "")
	lab := partOfKind(t, instance, domain.PartKindLab).ID

	// The service trims; the column refuses what it was not handed trimmed, so a caller that
	// bypasses the service gets a refusal rather than a value with a space nobody can see.
	if _, err := f.demand.SetInstancePartTaughtBy(t.Context(), lab, " FK 04"); !errors.Is(err, domain.ErrTaughtByTooLong) {
		t.Errorf("an untrimmed name gave %v, want the CHECK's refusal", err)
	}
}

// The assignment side: another faculty's part has nobody here to hold it, and a part somebody of
// ours holds is not given away without taking it from them first.

func TestAPartTaughtElsewhereCannotBeStaffed(t *testing.T) {
	t.Parallel()

	f := newAssignmentFixture(t)
	demand := store.NewDemand(f.schema.Pool, store.NewModules(f.schema.Pool))

	if _, err := demand.SetInstancePartTaughtBy(t.Context(), f.lab, "FK 04"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.assignments.FillPart(t.Context(), f.lab, domain.Assignee{PersonID: testdata.Eins.ID()},
		"", testdata.Drei.ID()); !errors.Is(err, domain.ErrPartTaughtElsewhere) {
		t.Errorf("filling another faculty's part gave %v, want ErrPartTaughtElsewhere", err)
	}

	// Taken back, it is ours to fill again.
	if _, err := demand.SetInstancePartTaughtBy(t.Context(), f.lab, ""); err != nil {
		t.Fatal(err)
	}
	f.fill(t, f.lab, domain.Assignee{PersonID: testdata.Eins.ID()})
}

func TestAStaffedPartIsNotGivenAway(t *testing.T) {
	t.Parallel()

	f := newAssignmentFixture(t)
	demand := store.NewDemand(f.schema.Pool, store.NewModules(f.schema.Pool))
	f.fill(t, f.lecture, domain.Assignee{PersonID: testdata.Eins.ID()})

	if _, err := demand.SetInstancePartTaughtBy(t.Context(), f.lecture, "FK 04"); !errors.Is(err, domain.ErrPartAssigned) {
		t.Errorf("handing a staffed part to another faculty gave %v, want ErrPartAssigned", err)
	}

	// Dividing a staffed part is allowed — the person keeps what is left — and the staffed piece
	// cannot be rejoined, because that would take teaching away from somebody.
	divided, err := demand.DivideInstancePart(t.Context(), f.lecture, 1)
	if err != nil {
		t.Fatalf("dividing a staffed part gave %v", err)
	}
	piece := piecesOf(divided, f.lecture)[0].ID
	f.fill(t, piece, domain.Assignee{PersonID: testdata.Zwei.ID()})
	if _, err := demand.RejoinInstancePart(t.Context(), piece); !errors.Is(err, domain.ErrPartAssigned) {
		t.Errorf("rejoining a staffed piece gave %v, want ErrPartAssigned", err)
	}
}

// The race the locks are for: a part handed to another faculty at the same moment somebody
// fills it. Whichever comes second must see the first, so the two can never both succeed — an
// assignment on a part nobody here teaches would be a person credited with teaching that another
// faculty gives.
func TestHandingAwayAndFillingAtOnceNeverBothSucceed(t *testing.T) {
	t.Parallel()

	f := newAssignmentFixture(t)
	ctx := t.Context()
	demand := store.NewDemand(f.schema.Pool, store.NewModules(f.schema.Pool))

	for round := range 20 {
		two := 2.0
		grown, err := demand.AddInstancePart(ctx, f.instance, domain.PartKindLab, &two)
		if err != nil {
			t.Fatal(err)
		}
		part := grown.Parts[len(grown.Parts)-1].ID

		var wg sync.WaitGroup
		var fillErr, handErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, fillErr = f.assignments.FillPart(ctx, part, domain.Assignee{PersonID: testdata.Eins.ID()},
				"", testdata.Drei.ID())
		}()
		go func() {
			defer wg.Done()
			_, handErr = demand.SetInstancePartTaughtBy(ctx, part, "FK 04")
		}()
		wg.Wait()

		if fillErr == nil && handErr == nil {
			t.Fatalf("round %d: the part was filled and handed away at once", round)
		}
		if fillErr != nil && !errors.Is(fillErr, domain.ErrPartTaughtElsewhere) {
			t.Fatalf("round %d: the fill failed with %v", round, fillErr)
		}
		if handErr != nil && !errors.Is(handErr, domain.ErrPartAssigned) {
			t.Fatalf("round %d: handing away failed with %v", round, handErr)
		}
	}

	var both int
	if err := f.schema.Pool.QueryRow(ctx, `
		SELECT count(*) FROM assignment a JOIN instance_part p ON p.id = a.instance_part_id
		WHERE p.taught_by IS NOT NULL`).Scan(&both); err != nil {
		t.Fatal(err)
	}
	if both != 0 {
		t.Errorf("%d assignment(s) sit on parts another faculty teaches", both)
	}
}

// An instance another faculty teaches entirely has nothing in it to want; one it teaches in part
// is wished for as usual.
func TestTheWishContextKnowsAnInstanceTaughtElsewhere(t *testing.T) {
	t.Parallel()

	f := newAssignmentFixture(t)
	ctx := t.Context()
	demand := store.NewDemand(f.schema.Pool, store.NewModules(f.schema.Pool))
	wishes := store.NewWishes(f.schema.Pool)

	elsewhere := func() bool {
		t.Helper()
		where, err := wishes.WishWriteContext(ctx, f.instance)
		if err != nil {
			t.Fatal(err)
		}
		return where.TaughtElsewhere
	}

	if elsewhere() {
		t.Errorf("an ordinary instance reads as taught elsewhere")
	}
	if _, err := demand.SetInstancePartTaughtBy(ctx, f.lab, "FK 04"); err != nil {
		t.Fatal(err)
	}
	if elsewhere() {
		t.Errorf("an instance with our lecture reads as taught elsewhere")
	}
	if _, err := demand.SetInstancePartTaughtBy(ctx, f.lecture, "FK 04"); err != nil {
		t.Fatal(err)
	}
	if !elsewhere() {
		t.Errorf("an instance another faculty teaches entirely reads as ours")
	}
}

// A split that names a kind twice is a part held divided in every instance (2026-10-17): the
// second lecture of 2 is a piece of the first, not a second lecture. Both ways an instance is made
// — declared on its own and created by a plan — and what a group then adds: a whole laboratory.
func TestASplitWithAKindTwiceMakesAPartAndItsPiece(t *testing.T) {
	t.Parallel()

	f := newDemandFixture(t)
	ctx := t.Context()
	if _, err := store.NewModules(f.schema.Pool).SetModuleComponents(ctx, f.module, []domain.ModuleComponent{
		{Kind: domain.PartKindLecture, TeachingHours: 2, Position: 0},
		{Kind: domain.PartKindLecture, TeachingHours: 2, Position: 1},
		{Kind: domain.PartKindLab, TeachingHours: 2, Position: 2},
		{Kind: domain.PartKindLab, TeachingHours: 2, Position: 3},
	}, uuid.Nil); err != nil {
		t.Fatal(err)
	}

	assertDivided := func(t *testing.T, what string, instance *domain.CourseInstance) {
		t.Helper()
		if instance.TeachingHours() != 8 {
			t.Errorf("the %s costs %v hours, want 8", what, instance.TeachingHours())
		}
		for _, kind := range []domain.InstancePartKind{domain.PartKindLecture, domain.PartKindLab} {
			var roots, pieces int
			for _, p := range instance.Parts {
				if p.Kind != kind {
					continue
				}
				if p.IsPiece() {
					pieces++
					if root := partByID(t, instance, p.DividedFrom); root.Kind != kind || root.IsPiece() {
						t.Errorf("the %s's %s piece names %+v as its part", what, kind, root)
					}
				} else {
					roots++
				}
			}
			if roots != 1 || pieces != 1 {
				t.Errorf("the %s holds %d %s part(s) and %d piece(s), want one of each", what, roots, kind, pieces)
			}
		}
	}

	declared := f.declare(t, "A")
	assertDivided(t, "declared cohort", declared)

	plan, err := f.demand.PlanDemand(ctx, f.semester.Code, f.programme,
		[]domain.DemandEntry{planEntry(f.module, track("A", 1), track("B", 1))}, uuid.Nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Refused) != 0 {
		t.Fatalf("planning refused %v", plan.Refused)
	}
	var planned *domain.CourseInstance
	for _, i := range f.instances(t) {
		if i.Track == "B" {
			planned = &i
		}
	}
	if planned == nil {
		t.Fatal("the plan declared no cohort B")
	}
	assertDivided(t, "planned cohort", planned)

	// One laboratory group already, as the stepper should read it; a second one is a whole
	// laboratory of 4, not one of the two entries.
	if _, err := f.demand.PlanDemand(ctx, f.semester.Code, f.programme,
		[]domain.DemandEntry{planEntry(f.module, track("A", 2), track("B", 1))}, uuid.Nil, false); err != nil {
		t.Fatal(err)
	}
	for _, i := range f.instances(t) {
		if i.Track == "A" && i.TeachingHours() != 12 {
			t.Errorf("with a second laboratory group cohort A costs %v hours, want 12", i.TeachingHours())
		}
	}
}
