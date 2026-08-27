package domain

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
)

// Two more refusals, both about the shape of what was asked rather than about permission.
var (
	// ErrProgrammeNotFound is a code that names no study programme.
	ErrProgrammeNotFound = errors.New("diesen Studiengang gibt es nicht")
	// ErrProgrammeSemesterInvalid is a cohort year outside what a degree has.
	ErrProgrammeSemesterInvalid = errors.New("das Fachsemester muss zwischen 1 und 12 liegen")
	// ErrDuplicateEntry is the same module, or the same cohort of one, twice in one plan.
	ErrDuplicateEntry = errors.New("jedes Modul und jeder Zug darf nur einmal vorkommen")
	// ErrTooManyTracks is more parallel cohorts than the alphabet the interface offers.
	ErrTooManyTracks = errors.New("so viele Züge kann ein Modul nicht haben")
	// ErrTooManyGroups is more parallel groups than anybody runs.
	ErrTooManyGroups = errors.New("so viele Gruppen kann ein Zug nicht haben")
	// ErrPhaseClosed is the phase refusing a write.
	//
	// The sentence a person reads is policy.PhaseClosedReason — the refusal is produced by the
	// table there, so the wording belongs there too, next to the decision it explains. This one
	// is what the sentinel says when something prints it.
	ErrPhaseClosed = errors.New("die Phase lässt diese Änderung nicht zu")
)

// trackPattern is the same shape the database enforces in course_instance_track_is_a_label.
//
// Both, deliberately, and for the reason the semester code gives one file over: here so that the
// caller gets a sentence they can act on, and in the database so that a future import or admin
// command cannot write something no interface would accept.
var trackPattern = regexp.MustCompile(`^[A-Z0-9]{0,3}$`)

// DemandService is the demand planning: declare what a study programme needs, split it into
// parallel cohorts, and say what each of them is made of.
//
// # What it is allowed to decide
//
// Nothing about who may do it. Every write here asks policy.MayWriteDemand, which intersects
// the phase table with the programme scope, and the two halves of that are maintained where
// they are decided rather than here. What this service owns is the shape of a request — a
// parallel cohort is a short label, hours are a small positive number, an instance holds a
// bounded number of parts — and the order in which the pieces are read.
//
// # Why the phase is read from the instance
//
// Every write path reads the row it is about before deciding, and the row carries the phase of
// its semester. That is one round trip that answers both halves of the rule at once, and it
// removes the case where a caller names a semester and an instance that are not the same one.
type DemandService struct {
	store     DemandStore
	catalogue CatalogueReader
	semesters *SemesterService
}

// NewDemandService wires the service.
func NewDemandService(store DemandStore, catalogue CatalogueReader, semesters *SemesterService) *DemandService {
	return &DemandService{store: store, catalogue: catalogue, semesters: semesters}
}

// Instances lists the demand of a semester.
//
// Readable by anybody with an account and no particular role, like the catalogue and the
// semester list. The demand is not confidential — it is what the wish phase is about, and a
// lecturer who cannot see which instances exist has nothing to register interest in. What is
// scoped is writing it.
func (s *DemandService) Instances(ctx context.Context, actor principal.Actor,
	filter DemandFilter,
) ([]CourseInstance, error) {
	if err := mayRead(actor); err != nil {
		return nil, err
	}

	semester, err := s.semesters.ByCode(ctx, actor, filter.SemesterCode)
	if err != nil {
		return nil, err
	}
	filter.SemesterCode = semester.Code
	filter.Programme = normaliseProgrammeCode(filter.Programme)

	return s.store.CourseInstances(ctx, filter)
}

// Instance returns one instance, or (nil, nil).
func (s *DemandService) Instance(ctx context.Context, actor principal.Actor, id uuid.UUID) (*CourseInstance, error) {
	if err := mayRead(actor); err != nil {
		return nil, err
	}
	return s.store.CourseInstanceByID(ctx, id)
}

// DeclareInstance is a demand about to be declared, as the interface states it.
//
// The semester and the programme arrive as the names they have in the faculty rather than as
// ids: those are what a URL, a script and a conversation carry.
type DeclareInstance struct {
	SemesterCode string
	Programme    string
	ModuleID     uuid.UUID
	// Track is the parallel cohort, empty for a module that runs once — which is the ordinary
	// case and therefore the default.
	Track string
	// ProgrammeSemester is the cohort year, or nil to take what the programme's regulations say.
	ProgrammeSemester *int
}

// Declare records that a study programme needs this module in this semester, for this cohort.
//
// This is the mutation the whole area exists for, and it is where a semester row comes into
// existence for the third time in the system: nobody creates a semester, but declaring a demand
// for one is a decision about it, and the row is the record of that decision.
func (s *DemandService) Declare(ctx context.Context, actor principal.Actor, spec DeclareInstance) (*CourseInstance, error) {
	if err := mayRead(actor); err != nil {
		return nil, err
	}

	programme, err := s.programme(ctx, spec.Programme)
	if err != nil {
		return nil, err
	}

	// ByCode rather than a bare lookup: it validates the code, refuses one too far from now to
	// plan, and answers for a semester nobody has touched — which is the ordinary state of the
	// semester somebody is about to declare the first demand for.
	semester, err := s.semesters.ByCode(ctx, actor, spec.SemesterCode)
	if err != nil {
		return nil, err
	}

	if err := s.mayWrite(actor, programme.ID, semester.Phase); err != nil {
		return nil, err
	}

	track, err := normaliseTrack(spec.Track)
	if err != nil {
		return nil, err
	}
	if err := validProgrammeSemester(spec.ProgrammeSemester); err != nil {
		return nil, err
	}

	recorded, err := s.semesters.ensure(ctx, semester.Code)
	if err != nil {
		return nil, err
	}

	return s.store.CreateCourseInstance(ctx, NewCourseInstance{
		SemesterID:        recorded.ID,
		ModuleID:          spec.ModuleID,
		ProgrammeID:       programme.ID,
		Track:             track,
		ProgrammeSemester: spec.ProgrammeSemester,
		CreatedBy:         actor.ID,
	})
}

// Duplicate copies an instance to a second parallel cohort.
//
// sourceTrack renames the original, and it is the whole reason this is one operation rather than
// two: a single cohort becoming two is one decision, and doing it in two steps leaves a moment
// in which the pair does not look like a pair.
func (s *DemandService) Duplicate(ctx context.Context, actor principal.Actor, id uuid.UUID,
	track, sourceTrack string,
) (*CourseInstance, error) {
	instance, err := s.writable(ctx, actor, id)
	if err != nil {
		return nil, err
	}

	newTrack, err := normaliseTrack(track)
	if err != nil {
		return nil, err
	}
	renamed, err := normaliseTrack(sourceTrack)
	if err != nil {
		return nil, err
	}
	if newTrack == "" {
		// The second cohort of a module is what makes a cohort letter mean anything. Without
		// one the copy would collide with its own source on the identity, and the message for
		// that ("already declared") would describe the symptom rather than the mistake.
		return nil, ErrTrackInvalid
	}

	return s.store.DuplicateCourseInstance(ctx, instance.ID, newTrack, renamed, actor.ID)
}

// Change writes the two editable things about an instance: its cohort and its cohort year.
func (s *DemandService) Change(ctx context.Context, actor principal.Actor, id uuid.UUID,
	track string, programmeSemester *int,
) (*CourseInstance, error) {
	instance, err := s.writable(ctx, actor, id)
	if err != nil {
		return nil, err
	}

	newTrack, err := normaliseTrack(track)
	if err != nil {
		return nil, err
	}
	if err := validProgrammeSemester(programmeSemester); err != nil {
		return nil, err
	}

	return s.store.UpdateCourseInstance(ctx, instance.ID, newTrack, programmeSemester)
}

// Withdraw removes an instance that is not needed after all.
func (s *DemandService) Withdraw(ctx context.Context, actor principal.Actor, id uuid.UUID) error {
	instance, err := s.writable(ctx, actor, id)
	if err != nil {
		return err
	}
	return s.store.DeleteCourseInstance(ctx, instance.ID)
}

// AddPart appends a part — the second laboratory group, the tutorial nobody planned for.
//
// How the multiplicity of a module's split is expressed: the split says a laboratory is two
// hours, and how many groups of it a cohort runs is a planning decision made here.
func (s *DemandService) AddPart(ctx context.Context, actor principal.Actor, instanceID uuid.UUID,
	kind InstancePartKind, hours *float64,
) (*CourseInstance, error) {
	instance, err := s.writable(ctx, actor, instanceID)
	if err != nil {
		return nil, err
	}
	if len(instance.Parts) >= MaxPartsPerInstance {
		return nil, ErrTooManyParts
	}
	if err := validPart(kind, hours); err != nil {
		return nil, err
	}
	return s.store.AddInstancePart(ctx, instance.ID, kind, hours)
}

// ChangePart writes a part's kind and hours.
func (s *DemandService) ChangePart(ctx context.Context, actor principal.Actor, partID uuid.UUID,
	kind InstancePartKind, hours *float64,
) (*CourseInstance, error) {
	if _, err := s.writablePart(ctx, actor, partID); err != nil {
		return nil, err
	}
	if err := validPart(kind, hours); err != nil {
		return nil, err
	}
	return s.store.UpdateInstancePart(ctx, partID, kind, hours)
}

// RemovePart removes one.
func (s *DemandService) RemovePart(ctx context.Context, actor principal.Actor, partID uuid.UUID) (*CourseInstance, error) {
	if _, err := s.writablePart(ctx, actor, partID); err != nil {
		return nil, err
	}
	return s.store.DeleteInstancePart(ctx, partID)
}

// SharePartAcrossTracks makes one part serve the sibling cohorts too: the lecture given once for
// IF3A and IF3B, whose hours count once.
//
// Deliberately not the default. A cohort holds its own teaching unless somebody says otherwise,
// because that is what usually happens and because the other way round — sharing by default,
// unshared on request — would make the faculty's hours look smaller than they are until somebody
// noticed.
func (s *DemandService) SharePartAcrossTracks(ctx context.Context, actor principal.Actor,
	partID uuid.UUID,
) (*CourseInstance, error) {
	if _, err := s.writablePart(ctx, actor, partID); err != nil {
		return nil, err
	}
	return s.store.ShareInstancePartAcrossTracks(ctx, partID)
}

// SplitPartAcrossTracks undoes that, and every cohort holds its own again.
func (s *DemandService) SplitPartAcrossTracks(ctx context.Context, actor principal.Actor,
	partID uuid.UUID,
) (*CourseInstance, error) {
	if _, err := s.writablePart(ctx, actor, partID); err != nil {
		return nil, err
	}
	return s.store.SplitInstancePartAcrossTracks(ctx, partID)
}

// Coverage: one programme's demand met by another programme's event.
//
// THE HANDSHAKE, AND WHY IT IS ONE
//
// Three methods, and between them they are the reason this is not a single column somebody sets.
// The permission model of the whole demand hangs off course_instance.programme_id — a lead writes
// their own programme and nobody else's. A one-sided link would be a lead writing a fact into a
// programme they do not lead: "your event now also serves my students" is a claim on somebody
// else's teaching, and the person who has to hold it never said yes.
//
// So each half is an ordinary demand write against the caller's own programme:
//
//	RequestCoverage  writable(guest)  — my demand is met elsewhere
//	AcceptCoverage   writable(host)   — yes, I hold it for them too
//	ReleaseCoverage  either           — it is over
//
// No new policy function, and no new cell in the write matrix. Two people who each may write one
// programme can between them express something neither could write alone, and nobody needs a role
// that reaches both — which matters, because the role that reaches every programme is the dean's
// office and the faculty does not want its leads to have it.

// RequestCoverage asks that this instance's demand be met by another programme's event.
//
// The permission is about the *guest* and nothing else. What the caller writes is a statement
// about their own declaration, and the instance they point at is not changed by it — not its
// parts, not its assignments, not what it costs. Which is why the caller may point at an instance
// they have no permission over at all, and why the schema rather than this method decides whether
// that instance is a legitimate target.
func (s *DemandService) RequestCoverage(ctx context.Context, actor principal.Actor,
	guestID, hostID uuid.UUID,
) (*CourseInstance, error) {
	if _, err := s.writable(ctx, actor, guestID); err != nil {
		return nil, err
	}
	return s.store.RequestInstanceCoverage(ctx, guestID, hostID, actor.ID)
}

// AcceptCoverage agrees to hold this event for the asking programme as well.
//
// The permission is about the *host*: the asking instance is read to find out which programme is
// being asked, and that is the programme the caller must be able to write. Deliberately not the
// guest's — a lead who could agree on the strength of leading the programme that asked would be a
// one-sided handshake with an extra step.
//
// Reading the guest first needs no permission of its own: the demand is readable by anybody with
// an account, and what comes back here is which programme was asked, which is not a secret.
func (s *DemandService) AcceptCoverage(ctx context.Context, actor principal.Actor,
	guestID uuid.UUID,
) (*CourseInstance, error) {
	if err := mayRead(actor); err != nil {
		return nil, err
	}

	guest, err := s.store.CourseInstanceByID(ctx, guestID)
	if err != nil {
		return nil, err
	}
	if guest == nil {
		return nil, ErrInstanceNotFound
	}
	if guest.CoveredBy == nil {
		return nil, ErrCoverageNotRequested
	}

	host, err := s.store.CourseInstanceByID(ctx, guest.CoveredBy.Instance.ID)
	if err != nil {
		return nil, err
	}
	if host == nil {
		return nil, ErrInstanceNotFound
	}
	if err := s.mayWrite(actor, host.Programme.ID, host.SemesterPhase); err != nil {
		return nil, err
	}

	return s.store.AcceptInstanceCoverage(ctx, guestID, actor.ID)
}

// ReleaseCoverage ends it: a request withdrawn, a request declined, or an agreement revised.
//
// Either lead may. The asking one because it is their demand; the holding one because it is their
// teaching, and a programme that cannot walk away from an agreement could only correct it by
// asking somebody else to.
//
// One method for all three cases because they are one state — the demand is simply not covered.
// Three would be three places to get the permission wrong, and the difference between "declined"
// and "withdrawn" is a fact about the past that the two timestamps already record.
func (s *DemandService) ReleaseCoverage(ctx context.Context, actor principal.Actor,
	guestID uuid.UUID,
) (*CourseInstance, error) {
	if err := mayRead(actor); err != nil {
		return nil, err
	}

	guest, err := s.store.CourseInstanceByID(ctx, guestID)
	if err != nil {
		return nil, err
	}
	if guest == nil {
		return nil, ErrInstanceNotFound
	}
	if guest.CoveredBy == nil {
		return nil, ErrCoverageNotRequested
	}

	guestErr := s.mayWrite(actor, guest.Programme.ID, guest.SemesterPhase)
	if guestErr == nil {
		return s.store.ReleaseInstanceCoverage(ctx, guestID)
	}

	host, err := s.store.CourseInstanceByID(ctx, guest.CoveredBy.Instance.ID)
	if err != nil {
		return nil, err
	}
	if host == nil {
		return nil, ErrInstanceNotFound
	}
	if err := s.mayWrite(actor, host.Programme.ID, host.SemesterPhase); err != nil {
		// Neither side. The refusal reported is the asking programme's, because that is the
		// instance the caller named: telling somebody they may not write the *other* programme
		// would answer a question they did not ask.
		return nil, guestErr
	}

	return s.store.ReleaseInstanceCoverage(ctx, guestID)
}

// CoverageCandidates lists the instances that could cover this one.
//
// A read, and scoped like every other demand read — anybody with an account may see which
// instances exist. The list is the schema's four conditions, so a picker built from it offers
// exactly what a request would be allowed to name.
func (s *DemandService) CoverageCandidates(ctx context.Context, actor principal.Actor,
	guestID uuid.UUID,
) ([]CourseInstance, error) {
	if err := mayRead(actor); err != nil {
		return nil, err
	}
	return s.store.HostCandidates(ctx, guestID)
}

// CopyFrom declares in one semester what the same programme declared in another.
//
// The permission is about the target semester and nothing else: what is being written is next
// year's demand, and reading last year's is something anybody with an account may do anyway.
//
// The report is returned even when nothing happened. A copy into a semester that already holds
// the same instances writes nothing, and "nothing happened" is indistinguishable from "it
// failed" to the person who pressed the button.
func (s *DemandService) CopyFrom(ctx context.Context, actor principal.Actor,
	fromCode, toCode, programmeCode string,
) (CopyReport, error) {
	if err := mayRead(actor); err != nil {
		return CopyReport{}, err
	}

	programme, err := s.programme(ctx, programmeCode)
	if err != nil {
		return CopyReport{}, err
	}

	from, err := s.semesters.ByCode(ctx, actor, fromCode)
	if err != nil {
		return CopyReport{}, err
	}
	to, err := s.semesters.ByCode(ctx, actor, toCode)
	if err != nil {
		return CopyReport{}, err
	}
	if from.Code == to.Code {
		return CopyReport{}, ErrSameSemester
	}

	if err := s.mayWrite(actor, programme.ID, to.Phase); err != nil {
		return CopyReport{}, err
	}

	report := CopyReport{From: from.Code, To: to.Code, Programme: *programme}

	// A source nobody has recorded anything about holds no demand, so there is nothing to copy
	// and no row to create for it. The target gets its row either way, because copying into it
	// is a decision about it even when the copy turns out to be empty.
	recordedTo, err := s.semesters.ensure(ctx, to.Code)
	if err != nil {
		return report, err
	}
	if from.Recorded() {
		counts, err := s.store.CopyDemand(ctx, from, recordedTo, programme.ID, actor.ID)
		if err != nil {
			return report, err
		}
		report.Counts = counts
	}

	instances, err := s.store.CourseInstances(ctx, DemandFilter{
		SemesterCode: to.Code,
		Programme:    programme.Code,
	})
	if err != nil {
		return report, err
	}
	report.Instances = instances
	return report, nil
}

// PlanDemand writes a whole screen of demand at once: which modules are offered, in how many
// cohorts, with how many groups in each.
//
// # What it is for
//
// The faculty plans a semester as a table, and this is that table as one act. Everything the
// interface can do row by row it can also do here, which is what makes "tick fifteen modules and
// save" a single reconcilable statement instead of fifteen mutations that can half-succeed.
//
// # What it may touch
//
// Only the modules named in entries. A module the caller says nothing about is not planned, not
// unplanned, not touched — because the screen this comes from has filters on it, and a save must
// never withdraw what the person could not see.
//
// # dryRun
//
// Returns the same report without writing anything. The interface asks for one before a save
// that would withdraw something, so that a tick taken away by accident is a sentence to read
// rather than a row that is gone. A dry run also records nothing about the semester: the row that
// says a decision was taken about it comes into being with the decision, not with the preview.
func (s *DemandService) PlanDemand(ctx context.Context, actor principal.Actor,
	semesterCode, programmeCode string, entries []DemandEntry, dryRun bool,
) (DemandPlan, error) {
	if err := mayRead(actor); err != nil {
		return DemandPlan{}, err
	}

	programme, err := s.programme(ctx, programmeCode)
	if err != nil {
		return DemandPlan{}, err
	}

	semester, err := s.semesters.ByCode(ctx, actor, semesterCode)
	if err != nil {
		return DemandPlan{}, err
	}

	if err := s.mayWrite(actor, programme.ID, semester.Phase); err != nil {
		return DemandPlan{}, err
	}

	entries, err = normaliseEntries(entries)
	if err != nil {
		return DemandPlan{}, err
	}

	// The semester row is the store's business, and deliberately so: it has to come into
	// existence inside the same transaction the plan is written in, so that a dry run — which
	// rolls that transaction back — leaves no trace of a semester nobody decided anything about.
	// Doing it here instead meant a preview either created the row it must not create, or handed
	// the store an id that referenced nothing.
	plan, err := s.store.PlanDemand(ctx, semester.Code, programme.ID, entries, actor.ID, dryRun)
	if err != nil {
		return plan, err
	}

	instances, err := s.store.CourseInstances(ctx, DemandFilter{
		SemesterCode: semester.Code,
		Programme:    programme.Code,
	})
	if err != nil {
		return plan, err
	}
	plan.Instances = instances
	for _, instance := range instances {
		plan.TeachingHours += instance.TeachingHours()
	}
	return plan, nil
}

// normaliseEntries checks the shape of a plan and puts its cohort letters in the form the schema
// holds.
//
// Everything here is about the request rather than about permission, and every refusal is one a
// person could hit by holding a key down on a stepper. The rules that matter — who may write,
// whether the module can be planned at all — are asked elsewhere and by the database.
func normaliseEntries(entries []DemandEntry) ([]DemandEntry, error) {
	out := make([]DemandEntry, 0, len(entries))
	seenModule := make(map[uuid.UUID]bool, len(entries))

	for _, entry := range entries {
		if seenModule[entry.ModuleID] {
			return nil, ErrDuplicateEntry
		}
		seenModule[entry.ModuleID] = true

		if len(entry.Tracks) > MaxTracksPerModule {
			return nil, ErrTooManyTracks
		}
		if err := validProgrammeSemester(entry.ProgrammeSemester); err != nil {
			return nil, err
		}

		seenTrack := make(map[string]bool, len(entry.Tracks))
		tracks := make([]DemandTrack, 0, len(entry.Tracks))
		for _, t := range entry.Tracks {
			track, err := normaliseTrack(t.Track)
			if err != nil {
				return nil, err
			}
			if seenTrack[track] {
				return nil, ErrDuplicateEntry
			}
			seenTrack[track] = true

			if t.Groups < 0 || t.Groups > MaxGroupsPerTrack {
				return nil, ErrTooManyGroups
			}
			tracks = append(tracks, DemandTrack{Track: track, Groups: t.Groups})
		}

		entry.Tracks = tracks
		out = append(out, entry)
	}
	return out, nil
}

// writable reads the instance a write is about and decides whether this actor may write it.
//
// One read, both halves: the row names its programme and carries its semester's phase.
func (s *DemandService) writable(ctx context.Context, actor principal.Actor, id uuid.UUID) (*CourseInstance, error) {
	if err := mayRead(actor); err != nil {
		return nil, err
	}

	instance, err := s.store.CourseInstanceByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if instance == nil {
		return nil, ErrInstanceNotFound
	}
	if err := s.mayWrite(actor, instance.Programme.ID, instance.SemesterPhase); err != nil {
		return nil, err
	}
	return instance, nil
}

// writablePart is the same for an operation named by one of an instance's parts.
func (s *DemandService) writablePart(ctx context.Context, actor principal.Actor, partID uuid.UUID) (*CourseInstance, error) {
	if err := mayRead(actor); err != nil {
		return nil, err
	}

	instance, err := s.store.CourseInstanceByPartID(ctx, partID)
	if err != nil {
		return nil, err
	}
	if instance == nil {
		return nil, ErrPartNotFound
	}
	if err := s.mayWrite(actor, instance.Programme.ID, instance.SemesterPhase); err != nil {
		return nil, err
	}
	return instance, nil
}

// mayWrite asks the policy and picks which refusal to report.
//
// Two sentinels rather than one, because the repairs differ and the interface has to be able to
// say which: a phase that is closed is moved by the dean's office, a programme somebody does not
// lead is not.
func (s *DemandService) mayWrite(actor principal.Actor, programmeID uuid.UUID, phase policy.Phase) error {
	if policy.MayWriteDemand(actor, programmeID, phase) {
		return nil
	}
	if !policy.MayPlanProgramme(actor, programmeID) {
		return ErrNotYourProgramme
	}
	return ErrPhaseClosed
}

// programme resolves the study programme a write names.
//
// Only the writes go through here — reading the demand of any programme is an ordinary question,
// including one that has run out, because that is the record of what the faculty did.
//
// A programme this faculty does not plan is refused, and that is a statement about the thing
// rather than about the person: `NOT_OURS` is somebody else's programme, `DISCONTINUED` is one
// of ours that has run out, and in neither case is the repair a role. Which is why it is not
// ErrNotYourProgramme — that sentence would send somebody asking for a grant that would not help.
func (s *DemandService) programme(ctx context.Context, code string) (*Programme, error) {
	programme, err := s.catalogue.ProgrammeByCode(ctx, normaliseProgrammeCode(code))
	if err != nil {
		return nil, err
	}
	if programme == nil {
		return nil, ErrProgrammeNotFound
	}
	if !programme.PlanningStatus.Planned() {
		return nil, ErrProgrammeNotPlanned
	}
	return programme, nil
}

// normaliseTrack accepts what somebody types for a parallel cohort and stores what the schema
// holds: `a` and `A` are the same cohort to a person, and refusing the first would be pedantry.
func normaliseTrack(track string) (string, error) {
	track = strings.ToUpper(strings.TrimSpace(track))
	if !trackPattern.MatchString(track) {
		return "", ErrTrackInvalid
	}
	return track, nil
}

func validProgrammeSemester(n *int) error {
	if n == nil {
		return nil
	}
	if *n < 1 || *n > 12 {
		return ErrProgrammeSemesterInvalid
	}
	return nil
}

// validPart is the shape of a part, not its meaning.
//
// The hours may be absent — an instance can be declared before the detail is settled, which is
// what a demand deadline that comes before the detail requires. What they may not be is zero or
// negative: a part that credits nobody with anything is a statement nobody meant to make, and
// the database says the same thing in instance_part_hours_are_plausible.
func validPart(kind InstancePartKind, hours *float64) error {
	if _, ok := ParseInstancePartKind(string(kind)); !ok {
		return ErrPartInvalid
	}
	if hours != nil && (*hours <= 0 || *hours > 20) {
		return ErrPartInvalid
	}
	return nil
}
