package domain

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
)

// Subject groups: the faculty's own grouping of modules and people.
//
// Not a planning object. A subject group has no semester, is not copied between semesters, and
// survives every change of examination regulations — so the subject group of anything planned is
// derived through the module rather than stored beside it. Migration 14 carries the argument and
// the consequence.
//
// Nothing here comes from the examination office; its data model has no notion of one.

var (
	// ErrSubjectGroupNotFound is a group nobody has created, or one named by a code with a typo
	// in it.
	ErrSubjectGroupNotFound = errors.New("diese Fachgruppe gibt es nicht")
	// ErrSubjectGroupCodeTaken is a second group claiming a code.
	//
	// Named rather than generic, and safe to name: subject groups are not confidential, and
	// "MATHE is taken" is readable off the same screen by the same person.
	ErrSubjectGroupCodeTaken = errors.New("dieses Kürzel ist schon vergeben")
	// ErrSubjectGroupCodeInvalid is a code the schema will not store.
	ErrSubjectGroupCodeInvalid = errors.New(
		"ein Fachgruppen-Kürzel besteht aus bis zu 16 Großbuchstaben, Ziffern, Punkt oder Bindestrich")
	// ErrSubjectGroupNameBlank is a group nobody can name.
	ErrSubjectGroupNameBlank = errors.New("eine Fachgruppe braucht einen Namen")
	// ErrSubjectGroupInUse is retiring the wrong way round: a DELETE that would take the module
	// assignments with it.
	ErrSubjectGroupInUse = errors.New(
		"dieser Fachgruppe sind noch Module zugeordnet — bitte erst umhängen")
	// ErrNotASubjectGroupLead is assigning a group to somebody who does not hold the role.
	//
	// The composite foreign key refuses it anyway; this turns the refusal into a sentence about
	// what the caller did, exactly as ErrNotAProgrammeLead does one table over.
	ErrNotASubjectGroupLead = errors.New(
		"diese Person hat die Rolle Fachgruppenleitung nicht")
)

// maxSubjectGroupCode mirrors subject_group_code_is_short. One place fewer to disagree with the
// schema than a second regular expression would be.
const maxSubjectGroupCode = 16

// SubjectGroup is a subject the faculty groups modules and people by.
type SubjectGroup struct {
	ID   uuid.UUID
	Code string
	Name string
	// Active is false for a group that has been retired — split into two, or wound up. Its
	// modules keep their assignment, because last year's planning still has to render.
	Active bool
	// Leads are the people who hold SUBJECT_GROUP_LEAD for this group. Empty is a real state
	// and the reason the faculty's "keine Fachgruppe ohne Person, die sich ihrer annimmt" is a
	// work list rather than a constraint: a group has to be creatable before its lead is
	// decided.
	Leads []Person
	// Members are the people who work in this group's subjects.
	//
	// Not a permission. It decides what the wish screen offers first and nothing else — see
	// policy.AssignmentScope, which deliberately does not read it.
	Members []Person
	// ModuleCount is how many modules are assigned to it. A count of catalogue entries, which is
	// not confidential — unlike anything counted over wishes.
	ModuleCount int

	CreatedAt time.Time
	UpdatedAt time.Time
}

// ModuleRef is a module as a subject group refers to it: enough to recognise, and no more.
//
// The counterpart of SubjectGroupRef in module.go, and it exists for the same reason: a full
// Module carries its split, its offerings and its regulations, and a screen that asks "what is in
// this group" wants a list of names. Naming the reference is what stops a half-populated Module
// being passed around as if it were a whole one.
type ModuleRef struct {
	ID   uuid.UUID
	Name string
	// HomeProgrammeCode is the programme that plans it — IF, IG. Carried because a subject group
	// reaches across programmes, so the code is what tells two similarly named modules apart.
	HomeProgrammeCode string
}

// SubjectGroupStore is what the service needs from persistence.
type SubjectGroupStore interface {
	// SubjectGroups lists them, with their leads, members and module counts.
	SubjectGroups(ctx context.Context, includeInactive bool) ([]SubjectGroup, error)
	// SubjectGroupByID returns one, or (nil, nil).
	SubjectGroupByID(ctx context.Context, id uuid.UUID) (*SubjectGroup, error)
	// CreateSubjectGroup adds one. Returns ErrSubjectGroupCodeTaken on a duplicate code.
	CreateSubjectGroup(ctx context.Context, code, name string) (*SubjectGroup, error)
	// RenameSubjectGroup changes the name only. The code is the address and does not move.
	RenameSubjectGroup(ctx context.Context, id uuid.UUID, name string) (*SubjectGroup, error)
	// SetSubjectGroupActive retires a group or brings it back.
	SetSubjectGroupActive(ctx context.Context, id uuid.UUID, active bool) (*SubjectGroup, error)
	// SetModulesSubjectGroup assigns a batch of modules to one group, or — with the nil group —
	// clears their assignment.
	SetModulesSubjectGroup(ctx context.Context, moduleIDs []uuid.UUID, group uuid.NullUUID,
		assignedBy uuid.UUID) (int, error)
	// SetSubjectGroupMembers replaces the members of one group.
	SetSubjectGroupMembers(ctx context.Context, groupID uuid.UUID, people []uuid.UUID,
		grantedBy uuid.UUID) error
	// SetSubjectGroupLeads replaces the set of people leading one group.
	SetSubjectGroupLeads(ctx context.Context, groupID uuid.UUID, people []uuid.UUID,
		grantedBy uuid.UUID) error
	// SubjectGroupsOfPerson is one person's memberships, for their own session.
	SubjectGroupsOfPerson(ctx context.Context, personID uuid.UUID) ([]SubjectGroup, error)
	// SetSubjectGroupsOfPerson replaces one person's memberships.
	SetSubjectGroupsOfPerson(ctx context.Context, personID uuid.UUID, groups []uuid.UUID,
		grantedBy uuid.UUID) error
	// ModulesOfSubjectGroup is what a group holds, for the screen that describes it.
	ModulesOfSubjectGroup(ctx context.Context, groupID uuid.UUID) ([]ModuleRef, error)
	// ModulesWithoutSubjectGroup counts the active modules nobody has assigned yet.
	ModulesWithoutSubjectGroup(ctx context.Context) (int, error)
	// SubjectGroupsWithoutLead counts the active groups nobody leads.
	SubjectGroupsWithoutLead(ctx context.Context) (int, error)
}

// SubjectGroupService is the business logic around them.
type SubjectGroupService struct {
	store SubjectGroupStore
}

// NewSubjectGroupService wires one up.
func NewSubjectGroupService(s SubjectGroupStore) *SubjectGroupService {
	return &SubjectGroupService{store: s}
}

// List returns the subject groups.
//
// Readable by anybody with an account, like the catalogue and the demand: who leads mathematics
// is not confidential inside the faculty, and a lecturer who cannot see the groups cannot be
// shown their own subjects on the wish screen. What is administered is writing them.
func (s *SubjectGroupService) List(ctx context.Context, actor principal.Actor,
	includeInactive bool) ([]SubjectGroup, error) {
	if !actor.Authenticated() {
		return nil, ErrNotAuthenticated
	}
	return s.store.SubjectGroups(ctx, includeInactive)
}

// Mine is the groups the caller is a member of.
//
// Its own method rather than a filter over List, because it is the caller's own data and the
// interface asks for it on every wish screen. It answers for the actor and never for somebody
// else — the administration screen is where another person's memberships are read.
func (s *SubjectGroupService) Mine(ctx context.Context,
	actor principal.Actor) ([]SubjectGroup, error) {
	if !actor.Authenticated() {
		return nil, ErrNotAuthenticated
	}
	return s.store.SubjectGroupsOfPerson(ctx, actor.ID)
}

// Create adds a subject group.
func (s *SubjectGroupService) Create(ctx context.Context, actor principal.Actor,
	code, name string) (*SubjectGroup, error) {
	if !policy.MayAdministerPeople(actor) {
		return nil, ErrNotAdministrator
	}

	code, name, err := normaliseSubjectGroup(code, name)
	if err != nil {
		return nil, err
	}
	return s.store.CreateSubjectGroup(ctx, code, name)
}

// Rename changes the name. The code is the address — it appears in URLs and in colleagues'
// scripts — and moving it is not a rename but a different group.
func (s *SubjectGroupService) Rename(ctx context.Context, actor principal.Actor,
	id uuid.UUID, name string) (*SubjectGroup, error) {
	if !policy.MayAdministerPeople(actor) {
		return nil, ErrNotAdministrator
	}

	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrSubjectGroupNameBlank
	}
	return s.store.RenameSubjectGroup(ctx, id, name)
}

// SetActive retires a group or brings it back.
//
// Retiring rather than deleting, and there is no delete: a group that has been split still has
// to render in the planning it was part of, and its modules keep their assignment.
func (s *SubjectGroupService) SetActive(ctx context.Context, actor principal.Actor,
	id uuid.UUID, active bool) (*SubjectGroup, error) {
	if !policy.MayAdministerPeople(actor) {
		return nil, ErrNotAdministrator
	}
	return s.store.SetSubjectGroupActive(ctx, id, active)
}

// AssignModules puts a batch of modules into one group, or takes them out of any.
//
// A batch rather than one module at a time, because the task this exists for is assigning 506
// modules and a screen that saves one row per click is a task nobody finishes. It returns how
// many rows it wrote, so that "nothing happened" and "it failed" are distinguishable — the same
// reason CopyDemandReport reports its zeroes.
func (s *SubjectGroupService) AssignModules(ctx context.Context, actor principal.Actor,
	moduleIDs []uuid.UUID, group uuid.NullUUID) (int, error) {
	if !policy.MayAdministerPeople(actor) {
		return 0, ErrNotAdministrator
	}
	if len(moduleIDs) == 0 {
		return 0, nil
	}
	return s.store.SetModulesSubjectGroup(ctx, moduleIDs, group, actor.ID)
}

// SetMembers replaces the members of one group.
//
// The whole set at once, like SetRoles and SetProgrammes and for the same reason: a per-person
// mutation would let the two calls of a swap be separated, and the interval between them is one
// in which somebody is in a group nobody meant them to be in.
//
// Membership grants nothing, so this is administration for tidiness rather than for safety — but
// it is still administration, because who works in which subject group is what the faculty's
// organisation looks like, not a preference somebody sets for themselves.
func (s *SubjectGroupService) SetMembers(ctx context.Context, actor principal.Actor,
	groupID uuid.UUID, people []uuid.UUID) (*SubjectGroup, error) {
	if !policy.MayAdministerPeople(actor) {
		return nil, ErrNotAdministrator
	}
	if err := s.store.SetSubjectGroupMembers(ctx, groupID, dedupe(people), actor.ID); err != nil {
		return nil, err
	}
	return s.get(ctx, groupID)
}

// get reads a group back after a write, turning "no row" into the refusal rather than into a nil
// nobody upstream expects.
func (s *SubjectGroupService) get(ctx context.Context, id uuid.UUID) (*SubjectGroup, error) {
	group, err := s.store.SubjectGroupByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if group == nil {
		return nil, ErrSubjectGroupNotFound
	}
	return group, nil
}

// SetLeads replaces the people leading one group.
//
// This one *is* a grant: it is what policy.AssignmentScope reads, and — once wishes exist — what
// decides who reads unpublished ones. Interactive-only administration for the reason every other
// grant is: doing it from a long-lived token in a script would decouple the granting of access
// from any sign-in.
func (s *SubjectGroupService) SetLeads(ctx context.Context, actor principal.Actor,
	groupID uuid.UUID, people []uuid.UUID) (*SubjectGroup, error) {
	if !policy.MayAdministerPeople(actor) {
		return nil, ErrNotAdministrator
	}
	if err := s.store.SetSubjectGroupLeads(ctx, groupID, dedupe(people), actor.ID); err != nil {
		return nil, err
	}
	return s.get(ctx, groupID)
}

// normaliseSubjectGroup upper-cases and trims a code and checks it against what the schema will
// accept, so that a typo is a sentence rather than a constraint violation.
func normaliseSubjectGroup(code, name string) (string, string, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	name = strings.TrimSpace(name)

	if name == "" {
		return "", "", ErrSubjectGroupNameBlank
	}
	if code == "" || len(code) > maxSubjectGroupCode {
		return "", "", ErrSubjectGroupCodeInvalid
	}
	for i, r := range code {
		switch {
		case r >= 'A' && r <= 'Z':
		case i > 0 && r >= '0' && r <= '9':
		case i > 0 && (r == '.' || r == '_' || r == '-'):
		default:
			return "", "", ErrSubjectGroupCodeInvalid
		}
	}
	return code, name, nil
}

// dedupe drops repeats and the nil uuid, keeping the caller's order.
//
// The nil uuid goes because it is what an empty form field arrives as, and a scope naming
// nothing is a row every rule then has to be careful about — see principal.RoleScope.
func dedupe(ids []uuid.UUID) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(ids))
	seen := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		if id == uuid.Nil || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// Get returns one subject group, or ErrSubjectGroupNotFound.
func (s *SubjectGroupService) Get(ctx context.Context, actor principal.Actor,
	id uuid.UUID) (*SubjectGroup, error) {
	if !actor.Authenticated() {
		return nil, ErrNotAuthenticated
	}
	return s.get(ctx, id)
}

// ModulesWithoutSubjectGroup is October's work list as a number.
//
// Readable by anybody with an account and deliberately so: it is the one figure that says how far
// the faculty has got with a task the faculty is doing together, and a progress number nobody but
// an administrator can see is a progress number nobody acts on.
func (s *SubjectGroupService) ModulesWithoutSubjectGroup(ctx context.Context,
	actor principal.Actor) (int, error) {
	if !actor.Authenticated() {
		return 0, ErrNotAuthenticated
	}
	return s.store.ModulesWithoutSubjectGroup(ctx)
}

// SubjectGroupsWithoutLead is the other half of it.
func (s *SubjectGroupService) SubjectGroupsWithoutLead(ctx context.Context,
	actor principal.Actor) (int, error) {
	if !actor.Authenticated() {
		return 0, ErrNotAuthenticated
	}
	return s.store.SubjectGroupsWithoutLead(ctx)
}

// SetMine replaces the caller's own subject group memberships.
//
// **Not administration, and that is the decision in this method.** Membership grants nothing —
// policy.AssignmentScope deliberately does not read it — so what somebody is changing here is a
// statement about which subjects they work in, and that is theirs to make. Requiring an
// administrator for it would make the wish screen's preselection something a colleague has to ask
// for, which is how a preselection turns into a barrier.
//
// The whole set at once, like every other membership write: the page is a list of ticks with one
// button, and a per-group mutation would let the two halves of a swap be separated.
//
// What this deliberately does not touch is who *leads* a group. That one is a grant, it is what
// decides who reads unpublished wishes, and it stays with the administration.
func (s *SubjectGroupService) SetMine(ctx context.Context, actor principal.Actor,
	groups []uuid.UUID) ([]SubjectGroup, error) {
	if !actor.Authenticated() {
		return nil, ErrNotAuthenticated
	}
	if err := s.store.SetSubjectGroupsOfPerson(ctx, actor.ID, dedupe(groups), actor.ID); err != nil {
		return nil, err
	}
	return s.store.SubjectGroupsOfPerson(ctx, actor.ID)
}

// Modules is what a subject group holds.
//
// Readable by anybody with an account: which modules belong to mathematics is catalogue data, and
// somebody deciding whether a group is theirs has to be able to see what is in it.
func (s *SubjectGroupService) Modules(ctx context.Context, actor principal.Actor,
	groupID uuid.UUID) ([]ModuleRef, error) {
	if !actor.Authenticated() {
		return nil, ErrNotAuthenticated
	}
	return s.store.ModulesOfSubjectGroup(ctx, groupID)
}
