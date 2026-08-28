package graph

import (
	"errors"

	"github.com/rs/zerolog/log"

	"github.com/obcode/tallox.go/graph/model"
	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
)

// What the demand resolvers translate with. Separate from demand.resolvers.go because gqlgen
// rewrites that file from the schema on every generate.

// courseInstanceModel reshapes an instance for the wire.
func courseInstanceModel(i domain.CourseInstance) *model.CourseInstance {
	out := &model.CourseInstance{
		ID:                i.ID.String(),
		Semester:          i.SemesterCode,
		Programme:         programmeModel(i.Programme),
		Module:            moduleModel(i.Module),
		Track:             i.Track,
		ProgrammeSemester: i.ProgrammeSemester,
		// Computed here rather than in a field resolver: it is a sum over a slice that is
		// already loaded, and a resolver would suggest it costs something to ask for.
		TeachingHours:         i.TeachingHours(),
		Parts:                 make([]*model.InstancePart, 0, len(i.Parts)),
		BorrowedParts:         make([]*model.BorrowedPart, 0, len(i.BorrowedParts)),
		CoveredBy:             coverageModel(i.CoveredBy),
		Covers:                make([]*model.InstanceCoverage, 0, len(i.Covers)),
		AlsoPlannedSeparately: make([]*model.CourseInstance, 0, len(i.AlsoPlannedSeparately)),
		CreatedAt:             i.CreatedAt,
		UpdatedAt:             i.UpdatedAt,
	}
	for _, p := range i.Parts {
		out.Parts = append(out.Parts, instancePartModel(p))
	}
	for _, b := range i.BorrowedParts {
		borrowed := &model.BorrowedPart{
			Part:      instancePartModel(b.Part),
			FromTrack: b.FromTrack,
		}
		if b.FromProgramme != "" {
			borrowed.FromProgramme = &model.Programme{Code: b.FromProgramme}
		}
		out.BorrowedParts = append(out.BorrowedParts, borrowed)
	}
	for _, c := range i.Covers {
		out.Covers = append(out.Covers, coverageModel(&c))
	}
	for _, o := range i.AlsoPlannedSeparately {
		// Built here rather than through courseInstanceModel, and for the same reason
		// coverageModel builds its own: that call would map this one's coverage and separate
		// offerings in turn, and the depth would be bounded by luck rather than by construction.
		out.AlsoPlannedSeparately = append(out.AlsoPlannedSeparately, &model.CourseInstance{
			ID:                o.ID.String(),
			Semester:          i.SemesterCode,
			Programme:         programmeModel(o.Programme),
			Module:            moduleModel(i.Module),
			Track:             o.Track,
			ProgrammeSemester: o.ProgrammeSemester,
			Parts:             make([]*model.InstancePart, 0),
			BorrowedParts:     make([]*model.BorrowedPart, 0),
			Covers:            make([]*model.InstanceCoverage, 0),

			AlsoPlannedSeparately: make([]*model.CourseInstance, 0),
		})
	}
	return out
}

// coverageModel reshapes one side of a coverage link.
//
// The instance inside it is built directly rather than through courseInstanceModel, and that is
// the recursion guard: courseInstanceModel would map its CoveredBy and Covers in turn. Depth is
// bounded by the schema — an instance that is covered may not also cover — but bounding it here
// too means the shape does not depend on remembering that.
func coverageModel(c *domain.InstanceCoverage) *model.InstanceCoverage {
	if c == nil {
		return nil
	}
	return &model.InstanceCoverage{
		Instance: &model.CourseInstance{
			ID:                c.Instance.ID.String(),
			Semester:          c.Instance.SemesterCode,
			Programme:         programmeModel(c.Instance.Programme),
			Module:            moduleModel(c.Instance.Module),
			Track:             c.Instance.Track,
			ProgrammeSemester: c.Instance.ProgrammeSemester,
			TeachingHours:     c.Instance.TeachingHours(),
			Parts:             make([]*model.InstancePart, 0),
			BorrowedParts:     make([]*model.BorrowedPart, 0),
			Covers:            make([]*model.InstanceCoverage, 0),
			CreatedAt:         c.Instance.CreatedAt,
			UpdatedAt:         c.Instance.UpdatedAt,
		},
		RequestedAt: c.RequestedAt,
		AcceptedAt:  c.AcceptedAt,
	}
}

func instancePartModel(p domain.InstancePart) *model.InstancePart {
	return &model.InstancePart{
		ID:                 p.ID.String(),
		Kind:               p.Kind,
		Position:           p.Position,
		TeachingHours:      p.TeachingHours,
		SharedAcrossTracks: p.SharedAcrossTracks,
	}
}

func copyReportModel(r domain.CopyReport) *model.CopyDemandReport {
	out := &model.CopyDemandReport{
		From:                r.From,
		To:                  r.To,
		Programme:           programmeModel(r.Programme),
		Created:             r.Counts.Created,
		Skipped:             r.Counts.Skipped,
		PartsCreated:        r.Counts.PartsCreated,
		CoverageRequested:   r.Counts.CoverageRequested,
		CoverageNotPossible: r.Counts.CoverageNotPossible,
		Instances:           make([]*model.CourseInstance, 0, len(r.Instances)),
	}
	for _, i := range r.Instances {
		out.Instances = append(out.Instances, courseInstanceModel(i))
	}
	return out
}

// demandPlanModel reshapes the report of a plan.
func demandPlanModel(p domain.DemandPlan) *model.DemandPlanReport {
	out := &model.DemandPlanReport{
		DryRun:        p.DryRun,
		Created:       demandChanges(p.Created),
		Withdrawn:     demandChanges(p.Withdrawn),
		Changed:       demandChanges(p.Changed),
		Coupled:       demandChanges(p.Coupled),
		Promoted:      demandChanges(p.Promoted),
		Refused:       make([]*model.DemandRefusal, 0, len(p.Refused)),
		Instances:     make([]*model.CourseInstance, 0, len(p.Instances)),
		TeachingHours: p.TeachingHours,
	}
	for _, r := range p.Refused {
		out.Refused = append(out.Refused, &model.DemandRefusal{
			ModuleID:   r.ModuleID.String(),
			ModuleName: r.ModuleName,
			Track:      r.Track,
			Code:       r.Code,
			Message:    r.Reason,
		})
	}
	for _, i := range p.Instances {
		out.Instances = append(out.Instances, courseInstanceModel(i))
	}
	return out
}

func demandChanges(changes []domain.DemandChange) []*model.DemandChange {
	out := make([]*model.DemandChange, 0, len(changes))
	for _, c := range changes {
		out = append(out, &model.DemandChange{
			ModuleID:     c.ModuleID.String(),
			ModuleName:   c.ModuleName,
			Track:        c.Track,
			TrackBefore:  c.TrackBefore,
			GroupsBefore: c.GroupsBefore,
			GroupsAfter:  c.GroupsAfter,
			Programme:    optionalProgrammeModel(c.Programme),
		})
	}
	return out
}

// optionalProgrammeModel maps the programme a change happened in, where it is not the one being
// planned. Nil for everything a save does inside its own programme, which is almost all of it.
func optionalProgrammeModel(p *domain.Programme) *model.Programme {
	if p == nil {
		return nil
	}
	return programmeModel(*p)
}

// demandUserFacing turns a service refusal into an error the interface can branch on.
//
// The code is the contract; the German sentence is the part that gets reworded after a support
// question, and matching prose across a repository boundary breaks the first time somebody
// improves a message.
//
// Two of these carry a decision rather than a translation:
//
//   - A refused write is one of three codes, not one, because the repair differs: an unassigned
//     programme lead needs an administrator, the lead of another programme needs the right
//     programme, and a closed phase needs the phase moved. A single FORBIDDEN would send all
//     three to ask the wrong person.
//   - INSTANCE_IN_USE says nothing about what is using the instance. No count, no kind of thing
//     named. Two things can hang off one now — a wish on the instance, an assignment on one of
//     its parts — and a helpful message would be the confidential fact with the names taken out.
//   - PART_ASSIGNED does name what hangs off a *part*, because only one thing ever does. It is
//     reachable only by somebody who may write this programme's demand, and they may read its
//     assignments; bootstrap.TestPartAssignedTellsNobodySomethingNew asserts that rather than
//     assuming it.
func demandUserFacing(actor principal.Actor, err error) error {
	switch {
	case err == nil:
		return nil

	case errors.Is(err, domain.ErrCatalogueForbidden), errors.Is(err, domain.ErrForbidden):
		return refusal("FORBIDDEN", domain.ErrCatalogueForbidden.Error())

	case errors.Is(err, domain.ErrNotYourProgramme):
		if policy.HoldsProgrammeLeadWithoutScope(actor) {
			return refusal("PROGRAMME_SCOPE_MISSING", policy.ProgrammeScopeMissingReason)
		}
		return refusal("NOT_YOUR_PROGRAMME", policy.PlanningReason)

	case errors.Is(err, domain.ErrPhaseClosed):
		return refusal("DEMAND_PHASE_CLOSED", policy.PhaseClosedReason)

	case errors.Is(err, domain.ErrModuleNotDecomposed):
		return refusal("MODULE_NOT_DECOMPOSED", err.Error())
	case errors.Is(err, domain.ErrTrackTaken):
		return refusal("TRACK_TAKEN", err.Error())
	case errors.Is(err, domain.ErrInstanceInUse):
		return refusal("INSTANCE_IN_USE", err.Error())
	case errors.Is(err, domain.ErrPartAssigned):
		return refusal("PART_ASSIGNED", err.Error())
	case errors.Is(err, domain.ErrInstanceNotFound):
		return refusal("INSTANCE_NOT_FOUND", err.Error())
	case errors.Is(err, domain.ErrPartNotFound):
		return refusal("PART_NOT_FOUND", err.Error())
	case errors.Is(err, domain.ErrTrackInvalid):
		return refusal("TRACK_INVALID", err.Error())
	case errors.Is(err, domain.ErrProgrammeSemesterInvalid):
		return refusal("PROGRAMME_SEMESTER_INVALID", err.Error())
	case errors.Is(err, domain.ErrPartInvalid):
		return refusal("PART_INVALID", err.Error())
	case errors.Is(err, domain.ErrTooManyParts):
		return refusal("TOO_MANY_PARTS", err.Error())
	case errors.Is(err, domain.ErrTooManyTracks):
		return refusal("TOO_MANY_TRACKS", err.Error())
	case errors.Is(err, domain.ErrTooManyGroups):
		return refusal("TOO_MANY_GROUPS", err.Error())
	case errors.Is(err, domain.ErrDuplicateEntry):
		return refusal("DUPLICATE_ENTRY", err.Error())
	case errors.Is(err, domain.ErrNoSiblingTracks):
		return refusal("NO_SIBLING_TRACKS", err.Error())
	case errors.Is(err, domain.ErrNotSharedAcrossTracks):
		return refusal("NOT_SHARED_ACROSS_TRACKS", err.Error())

	// The coverage refusals. Each names its own repair, which is why they are separate codes
	// rather than one COVERAGE_REFUSED: somebody told "that is not allowed" asks for a permission
	// they already hold, and the four impossible-target cases have four different fixes.
	case errors.Is(err, domain.ErrCoverageNotRequested):
		return refusal("COVERAGE_NOT_REQUESTED", err.Error())
	case errors.Is(err, domain.ErrCoverageAlreadySet):
		return refusal("COVERAGE_ALREADY_SET", err.Error())
	case errors.Is(err, domain.ErrCoverageAlreadyAccepted):
		return refusal("COVERAGE_ALREADY_ACCEPTED", err.Error())
	case errors.Is(err, domain.ErrCoverageSameProgramme):
		return refusal("COVERAGE_SAME_PROGRAMME", err.Error())
	case errors.Is(err, domain.ErrCoverageModuleMismatch):
		return refusal("COVERAGE_MODULE_MISMATCH", err.Error())
	case errors.Is(err, domain.ErrCoverageWouldChain):
		return refusal("COVERAGE_WOULD_CHAIN", err.Error())
	case errors.Is(err, domain.ErrCoverageSelf):
		return refusal("COVERAGE_SELF", err.Error())
	case errors.Is(err, domain.ErrInstanceCovered):
		return refusal("INSTANCE_COVERED", err.Error())
	case errors.Is(err, domain.ErrProgrammeNotFound):
		return refusal("PROGRAMME_NOT_FOUND", err.Error())
	case errors.Is(err, domain.ErrProgrammeNotPlanned):
		// Its own code rather than NOT_YOUR_PROGRAMME: the caller may well lead a programme, and
		// the repair is not a grant. Either this faculty does not run this programme, or it has
		// run out — and both need somebody to decide, not somebody to be granted something.
		return refusal("PROGRAMME_NOT_PLANNED", err.Error())
	case errors.Is(err, domain.ErrSameSemester):
		return refusal("SAME_SEMESTER", err.Error())

	// The semester refusals, which arrive through the same service because every demand names a
	// semester. Same codes as the semester area uses, so that a client branches on one meaning
	// per code rather than on one meaning per field.
	case semesterRefusal(err) != nil:
		return semesterRefusal(err)

	default:
		// Anything else is ours, not the caller's. Generic on purpose: a database error in the
		// clear says things about rows nobody asked about — and on this write path a unique
		// violation in the clear is exactly the shape of leak the wish rule is about.
		//
		// Logged, though, because an INTERNAL that leaves no trace is unanswerable twice over:
		// the person reading it cannot act on it, and neither can whoever they report it to. The
		// log line is the other half of the refusal — it names what the sentence deliberately
		// does not, on the one side of the wire where that is safe.
		log.Error().Err(err).
			Str("area", "demand").
			Str("actor", actor.ID.String()).
			Str("kind", string(actor.Kind)).
			Msg("refused with INTERNAL")
		return refusal("INTERNAL", "Das hat nicht geklappt. Bitte später erneut versuchen.")
	}
}
