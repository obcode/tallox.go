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
		TeachingHours: i.TeachingHours(),
		Parts:         make([]*model.InstancePart, 0, len(i.Parts)),
		BorrowedParts: make([]*model.BorrowedPart, 0, len(i.BorrowedParts)),
		CreatedAt:     i.CreatedAt,
		UpdatedAt:     i.UpdatedAt,
	}
	for _, p := range i.Parts {
		out.Parts = append(out.Parts, instancePartModel(p))
	}
	for _, b := range i.BorrowedParts {
		out.BorrowedParts = append(out.BorrowedParts, &model.BorrowedPart{
			Part:      instancePartModel(b.Part),
			FromTrack: b.FromTrack,
		})
	}
	return out
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
		From:         r.From,
		To:           r.To,
		Programme:    programmeModel(r.Programme),
		Created:      r.Counts.Created,
		Skipped:      r.Counts.Skipped,
		PartsCreated: r.Counts.PartsCreated,
		Instances:    make([]*model.CourseInstance, 0, len(r.Instances)),
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
		})
	}
	return out
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
//     named. The first table to point at a part will be the wish table, and a helpful message
//     here would be the confidential fact with the names taken out.
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
