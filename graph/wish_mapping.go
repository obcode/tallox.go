package graph

import (
	"time"

	"errors"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/graph/model"
	"github.com/obcode/tallox.go/internal/domain"
	"github.com/obcode/tallox.go/internal/policy"
)

// The wish area, on the way out and on the way back.
//
// Nothing here filters. What a caller sees was decided by a WHERE clause before these rows
// existed in memory, which is the whole arrangement: a mapping layer that dropped rows would be a
// second implementation of the confidentiality rule, and the exports and digests that will not
// pass through here would not have it.
//
// What this file does have to get right is the refusals. Two of them say less than they could on
// purpose, and both are marked where they are built.

func wishModel(w domain.Wish) *model.Wish {
	return &model.Wish{
		ID:        w.ID.String(),
		Person:    personModel(w.Person),
		Instance:  courseInstanceModel(w.Instance),
		Priority:  w.Priority,
		Note:      w.Note,
		CreatedAt: timeOrNil(w.CreatedAt),
		UpdatedAt: timeOrNil(w.UpdatedAt),
	}
}

func desiredHoursModel(d domain.DesiredHours) *model.DesiredTeachingHours {
	return &model.DesiredTeachingHours{
		Semester:    d.SemesterCode,
		Person:      personModel(d.Person),
		MinHours:    d.MinHours,
		MaxHours:    d.MaxHours,
		Hours:       d.MaxHours,
		Note:        d.Note,
		CorrectedBy: d.CorrectedBy,
		UpdatedAt:   d.UpdatedAt,
	}
}

func desiredHoursRowModels(rows []domain.DesiredHoursRow,
	loads []domain.AssignedLoad) []*model.DesiredHoursOverviewRow {
	byPerson := make(map[uuid.UUID]domain.AssignedLoad, len(loads))
	for _, l := range loads {
		byPerson[l.Person.ID] = l
	}
	out := make([]*model.DesiredHoursOverviewRow, 0, len(rows))
	for _, r := range rows {
		groups := make([]*model.SubjectGroupRef, 0, len(r.SubjectGroups))
		for _, g := range r.SubjectGroups {
			groups = append(groups, subjectGroupRefModel(&g))
		}
		row := &model.DesiredHoursOverviewRow{
			Person:        personModel(r.Person),
			SubjectGroups: groups,
			Stated:        r.Stated,
		}
		if r.Stated {
			low, high := r.MinHours, r.MaxHours
			row.MinHours, row.MaxHours = &low, &high
		}
		if l, ok := byPerson[r.Person.ID]; ok {
			row.AssignedLoad = assignedLoadModel(l)
		}
		out = append(out, row)
	}
	return out
}

func desiredHoursModels(rows []domain.DesiredHours) []*model.DesiredTeachingHours {
	out := make([]*model.DesiredTeachingHours, 0, len(rows))
	for _, d := range rows {
		out = append(out, desiredHoursModel(d))
	}
	return out
}

func wishModels(wishes []domain.Wish) []*model.Wish {
	out := make([]*model.Wish, 0, len(wishes))
	for _, w := range wishes {
		out = append(out, wishModel(w))
	}
	return out
}

// wishError maps the refusals of this area to codes the interface branches on.
//
// The German sentence is the half that gets reworded after a support question; the code is the
// stable half of the contract, and src/lib/server/graphqlError.ts keeps an allowlist of the ones
// whose wording it passes through.
//
// Two of these are deliberately uninformative:
//
//   - WISH_NOT_FOUND covers "there is no such wish" and "it is not yours". Telling them apart
//     would say whose it is, which is the fact the whole area protects.
//   - WISH_ALREADY_SET can say so in plain words, and that is a consequence of a decision rather
//     than of this table: with only-self entry, the only person who can trip the uniqueness
//     constraint is the owner, about their own row. If proxy entry is ever allowed, this has to
//     become generic in the same commit.
func wishError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, domain.ErrNotAuthenticated):
		return err
	case errors.Is(err, domain.ErrPartnerWritesNothing):
		return refusal("PARTNER_READS_ONLY", policy.PartnerReason)
	case errors.Is(err, domain.ErrDesiredHoursPhaseClosed):
		return refusal("DESIRED_HOURS_PHASE_CLOSED", err.Error())
	case errors.Is(err, domain.ErrDesiredHoursOutOfRange):
		return refusal("DESIRED_HOURS_OUT_OF_RANGE", err.Error())
	case errors.Is(err, domain.ErrDesiredHoursRange):
		return refusal("DESIRED_HOURS_RANGE", err.Error())
	case errors.Is(err, domain.ErrDesiredHoursFrozen):
		return refusal("DESIRED_HOURS_FROZEN", policy.DesiredHoursFrozenReason)
	case errors.Is(err, domain.ErrDesiredHoursNotFrozen):
		return refusal("DESIRED_HOURS_NOT_FROZEN", err.Error())
	case errors.Is(err, domain.ErrDesiredHoursUnknownPerson):
		return refusal("PERSON_NOT_FOUND", err.Error())
	case errors.Is(err, domain.ErrSemesterRequired):
		return refusal("SEMESTER_REQUIRED", err.Error())
	case errors.Is(err, domain.ErrWishPhaseClosed):
		return refusal("WISH_PHASE_CLOSED", err.Error())
	case errors.Is(err, domain.ErrWishWindowClosed):
		// Its own code and not WISH_PHASE_CLOSED, because the repair is a different person: a shut
		// window is one switch held by this subject group's lead, a finished semester is the end
		// of the process and nobody's to reopen.
		return refusal("WISH_WINDOW_CLOSED", policy.WishWindowClosedReason)
	case errors.Is(err, domain.ErrInstanceTaughtElsewhere):
		return refusal("INSTANCE_TAUGHT_ELSEWHERE", err.Error())
	case errors.Is(err, domain.ErrWishNotFound):
		return refusal("WISH_NOT_FOUND", err.Error())
	case errors.Is(err, domain.ErrWishNoteTooLong):
		return refusal("WISH_NOTE_TOO_LONG", err.Error())
	case errors.Is(err, domain.ErrWishPriorityInvalid):
		return refusal("WISH_PRIORITY_INVALID", err.Error())
	case errors.Is(err, domain.ErrInstanceNotFound):
		// Withdrawn between the screen being rendered and the form being sent, which is the
		// ordinary race here — the same code the demand area uses for it, because it is the same
		// fact about the same row.
		return refusal("INSTANCE_NOT_FOUND", err.Error())
	case semesterRefusal(err) != nil:
		// Shared with the semester workflow and the demand: one meaning per code, and a German
		// sentence rather than the English one domain.Err… carries for the log.
		return semesterRefusal(err)
	}
	// Anything else keeps its own shape rather than being dressed up with a code that would then
	// mean nothing. The database noise a driver error carries never reaches here: internal/store
	// wraps it, and graphqltest.AssertNoLeak is what says so.
	return err
}

// timeOrNil is a moment for the schema, or null where the domain left it zero — a timestamp the
// reader may not see.
func timeOrNil(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
