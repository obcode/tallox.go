package domain

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
)

// Carrying a corrected split over to the instances that already exist.
//
// An instance's parts are made from the module's split when it is declared, copied from last
// year, or duplicated, and from then on they are the instance's own: what a lecturer is credited
// with starts as what the split says and is editable afterwards. That is right for the hours
// somebody changed on purpose and wrong for the split somebody corrected — the corrected split
// reached nothing that existed, the demand page multiplied the new split by the group count, and
// the controlling summed the old parts. Two figures for one cohort, both plausible.
//
// So the correction is carried over by a step of its own, with a preview, rather than as a side
// effect of saving the split: it can overwrite hours somebody set by hand, and the person saving
// the split is owed the chance to see that first.

// ErrSplitLeftToProgramme is a part the new split no longer names, in an instance whose
// assignments the caller cannot read.
//
// Removing it could fail because somebody holds it, and a report that said so would be the
// unpublished assignment with the name taken out. So for these instances nothing is removed at
// all, whether anybody holds the part or not, and the answer is the same either way.
var ErrSplitLeftToProgramme = errors.New(
	"diesen Teil sieht die neue Aufteilung nicht mehr vor — entfernen kann ihn die " +
		"Studiengangsleitung der Instanz")

// SplitApplication is what carrying a split over did, or — with DryRun — what it would do.
type SplitApplication struct {
	// DryRun is true when nothing was written.
	DryRun bool
	// Changed are the instances whose parts were rewritten, at least in part.
	Changed []SplitChange
	// Refused is what was left as it was, one entry per instance and kind.
	Refused []SplitRefusal
	// Unchanged counts the instances that already matched the split. Covered cohorts hold no
	// parts and are not counted.
	Unchanged int
}

// SplitChange is one instance whose parts now follow the split.
type SplitChange struct {
	// Instance is as it is now — after a dry run, untouched.
	Instance CourseInstance
	// HoursBefore and HoursAfter are the instance's teaching hours, summed over its parts.
	HoursBefore float64
	HoursAfter  float64
}

// SplitRefusal is one kind of part in one instance that was left as it was.
type SplitRefusal struct {
	Instance CourseInstance
	Kind     InstancePartKind
	// Code is the machine-readable half: PART_ASSIGNED, PART_DIVIDED, SPLIT_LEFT_TO_PROGRAMME.
	Code   string
	Reason string
}

// SplitInstanceScope is what the store needs to know about an instance to decide how far it may
// go: whose demand it is and where its semester stands.
type SplitInstanceScope struct {
	ProgrammeID uuid.UUID
	Semester    policy.SemesterState
}

// ApplyModuleSplit carries a module's split over to its existing instances.
//
// # Who
//
// Whoever may state the split — policy.MayWriteModuleSplit — and for every instance of the module,
// not only those of their own programme. The split is a fact about the module, and an instance of
// another programme that still holds the old one is the same wrong figure in a column the home
// programme does not lead.
//
// # How far
//
// Parts that the split still names get its hours, and its pieces where it names a kind twice.
// Kinds it names and the instance lacks are added; for the practical kind as many groups as the
// instance had of the practical kind it replaces. Kinds it no longer names are removed — except
// where somebody holds them, where they are divided, and in instances whose assignments the caller
// cannot read (ErrSplitLeftToProgramme). Hours change on a held part too: the person holding it
// teaches what the module now requires.
//
// Semesters that are FINAL are history and are not rewritten. A cohort covered by another
// programme holds nothing and is skipped.
func (s *DemandService) ApplyModuleSplit(ctx context.Context, actor principal.Actor,
	moduleID uuid.UUID, dryRun bool,
) (SplitApplication, error) {
	if err := mayRead(actor); err != nil {
		return SplitApplication{}, err
	}

	module, err := s.catalogue.ModuleByID(ctx, moduleID)
	if err != nil {
		return SplitApplication{}, err
	}
	if module == nil {
		return SplitApplication{}, ErrModuleNotFound
	}

	var group uuid.UUID
	if module.SubjectGroup != nil {
		group = module.SubjectGroup.ID
	}
	if !policy.MayWriteModuleSplit(actor, group, module.HomeProgramme.ID) {
		return SplitApplication{}, ErrNotYourModuleSplit
	}

	// "Could this caller read an assignment on this instance, held by somebody else?" — asked
	// with no assignee, so that it is the rule's answer for the instance and not for a person.
	mayRemove := func(scope SplitInstanceScope) bool {
		return policy.CanSeeAssignment(actor, scope.Semester, policy.Assignment{
			ProgrammeID:    scope.ProgrammeID,
			SubjectGroupID: group,
		})
	}
	return s.store.ApplyModuleSplit(ctx, moduleID, mayRemove, dryRun)
}
