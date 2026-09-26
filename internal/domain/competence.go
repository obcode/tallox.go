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

// Competences: who can teach which module, and who would like to.
//
// Nothing in this file decides who may read what — internal/policy does, and internal/store
// applies its filter inside the query. What this file decides is who may write, because two of
// the three write rules need facts only the database has: whether the caller is a member of the
// module's subject group, and whether a teacher holds an account.

var (
	// ErrCompetenceNotFound is a competence that is not there — or not the caller's to change,
	// which is deliberately the same answer.
	ErrCompetenceNotFound = errors.New("dieser Eintrag ist nicht (mehr) da")
	// ErrCompetenceOutsideGroups is stating one's own competence for a module outside one's
	// subject groups.
	// The interface shows policy.CompetenceOutsideGroupsReason, which says what to do about it.
	ErrCompetenceOutsideGroups = errors.New("dieses Modul gehört zu keiner Ihrer Fachgruppen")
	// ErrCompetenceForTeacherRefused is writing for a teacher without being the lead of the
	// module's subject group.
	// The interface shows policy.CompetenceForTeacherReason.
	ErrCompetenceForTeacherRefused = errors.New("für dieses Modul sind Sie nicht zuständig")
	// ErrCompetenceTeacherHasAccount is writing for a teacher who can sign in and say it
	// themselves. A row the lead wrote would be indistinguishable from their own statement.
	ErrCompetenceTeacherHasAccount = errors.New(
		"diese Person hat ein Konto und trägt ihre Kompetenzen selbst ein")
	// ErrCompetenceModuleRetired is a module the catalogue no longer carries.
	ErrCompetenceModuleRetired = errors.New("dieses Modul wird nicht mehr angeboten")
	// ErrCompetenceLevelInvalid is a level outside the two.
	ErrCompetenceLevelInvalid = errors.New("diese Stufe gibt es nicht")
	// ErrCompetenceNoteTooLong mirrors competence_note_is_short.
	ErrCompetenceNoteTooLong = errors.New("die Notiz ist auf 500 Zeichen begrenzt")
	// ErrCompetenceGroupRefused is asking for the counts over a subject group one may not read
	// in full. A refusal rather than an empty list, because the answer depends only on who is
	// asking and not on anything in the table.
	ErrCompetenceGroupRefused = errors.New(
		"diese Übersicht sehen nur die Leitung der Fachgruppe und das Dekanat")
)

// MaxCompetenceNote mirrors competence_note_is_short.
const MaxCompetenceNote = 500

// CompetenceLevel is which of the two statements a competence is.
type CompetenceLevel string

const (
	// CompetenceCanTeach is "kann ich halten, wenn es brennt". The one that counts towards the
	// minimum, and the one the assignment reads first.
	CompetenceCanTeach CompetenceLevel = "CAN_TEACH"
	// CompetenceWouldLike is "würde ich in Zukunft gern halten". Confidential for the same reason
	// a wish is.
	CompetenceWouldLike CompetenceLevel = "WOULD_LIKE"
)

// AllCompetenceLevels returns both, the stronger statement first.
func AllCompetenceLevels() []CompetenceLevel {
	return []CompetenceLevel{CompetenceCanTeach, CompetenceWouldLike}
}

// Valid reports whether l is one of the two. store.TestDatabaseAndDomainAgreeOnCompetenceLevels
// keeps this list and the CHECK constraint in step.
func (l CompetenceLevel) Valid() bool {
	for _, known := range AllCompetenceLevels() {
		if l == known {
			return true
		}
	}
	return false
}

// CompetenceModule is the module a competence is about: enough to render and group a row, and
// nothing that would read as a claim about fields that were not loaded.
type CompetenceModule struct {
	ID   uuid.UUID
	Name string
	// Compulsory is whether the module is compulsory under at least one set of regulations —
	// what the kickoff calls the "Pflichtkatalog", and what the minimum counts.
	Compulsory bool
	// SubjectGroup is the module's group, or nil while it has none.
	SubjectGroup *SubjectGroupRef
}

// Competence is one statement about who can teach a module.
type Competence struct {
	ID     uuid.UUID
	Module CompetenceModule
	// Holder is who the statement is about: an account, or a teacher without one. The same
	// shape an assignment names its holder in, because it is the same question.
	Holder Assignee
	Level  CompetenceLevel
	Note   string
	// OutsideSubjectGroups is a person's statement on a module outside every group they are in —
	// they have left the group since. Kept, and shown as such, rather than deleted behind their
	// back.
	OutsideSubjectGroups bool

	CreatedAt time.Time
	UpdatedAt time.Time
}

// CompetenceQuery narrows the list. uuid.Nil means no narrowing. The filter is not in here, for
// the reason WishQuery gives: a caller cannot express a query without one.
type CompetenceQuery struct {
	Module       uuid.UUID
	SubjectGroup uuid.UUID
	Person       uuid.UUID
	Teacher      uuid.UUID
}

// CompetenceGroupStatus is one subject group of the caller's, and how far they are from the
// minimum there.
type CompetenceGroupStatus struct {
	SubjectGroup       SubjectGroupRef
	CanTeachCompulsory int
	Minimum            int
}

// MemberCompetenceStatus is one member of a subject group and their count, for the lead's list.
type MemberCompetenceStatus struct {
	Person             Person
	CanTeachCompulsory int
	Minimum            int
}

// ModuleWithoutCompetence is a module nobody has said they can teach.
type ModuleWithoutCompetence struct {
	ID         uuid.UUID
	Name       string
	Compulsory bool
}

// CompetenceModuleContext is what the write rule needs about a module.
type CompetenceModuleContext struct {
	// Found is false for a module that does not exist.
	Found bool
	// Open is false for a module the catalogue has retired.
	Open bool
	// SubjectGroupID is the module's group, or uuid.Nil.
	SubjectGroupID uuid.UUID
	// CallerIsMember is whether the person asked about is in that group.
	CallerIsMember bool
}

// CompetenceStore is what the service needs from persistence.
type CompetenceStore interface {
	Competences(ctx context.Context, q CompetenceQuery, filter policy.CompetenceFilter) ([]Competence, error)
	CompetenceByID(ctx context.Context, id uuid.UUID, filter policy.CompetenceFilter) (*Competence, error)
	ModuleContext(ctx context.Context, moduleID, personID uuid.UUID) (CompetenceModuleContext, error)
	SetOwn(ctx context.Context, moduleID, personID uuid.UUID, level CompetenceLevel, note string) (uuid.UUID, error)
	SetForTeacher(ctx context.Context, moduleID, teacherID, enteredBy uuid.UUID, level CompetenceLevel,
		note string) (uuid.UUID, error)
	WithdrawOwn(ctx context.Context, id, personID uuid.UUID) error
	// TeacherRowGroup is the subject group of a teacher row's module; found is false for an id
	// that is not a teacher row.
	TeacherRowGroup(ctx context.Context, id uuid.UUID) (group uuid.UUID, found bool, err error)
	WithdrawForTeacher(ctx context.Context, id uuid.UUID) error
	TeacherExists(ctx context.Context, teacherID uuid.UUID) (bool, error)
	AccountOfTeacher(ctx context.Context, teacherID uuid.UUID) (uuid.UUID, error)
	OwnGroupStatus(ctx context.Context, personID uuid.UUID) ([]CompetenceGroupStatus, error)
	MemberStatus(ctx context.Context, subjectGroupID uuid.UUID) ([]MemberCompetenceStatus, error)
	ModulesWithoutCompetence(ctx context.Context, subjectGroupID uuid.UUID) ([]ModuleWithoutCompetence, error)
}

// CompetenceService is the business logic of the competence profile.
type CompetenceService struct {
	store CompetenceStore
}

// NewCompetenceService wires one up.
func NewCompetenceService(s CompetenceStore) *CompetenceService {
	return &CompetenceService{store: s}
}

// List returns the competences this actor may read, narrowed by the query.
//
// Asking for somebody else's is not an error: it narrows the same filtered set — for the reason
// WishService.List gives.
func (s *CompetenceService) List(ctx context.Context, actor principal.Actor,
	q CompetenceQuery) ([]Competence, error) {
	if !actor.Authenticated() {
		return nil, ErrNotAuthenticated
	}
	return s.store.Competences(ctx, q, policy.CompetenceVisibility(actor))
}

// Mine is the caller's own statements.
func (s *CompetenceService) Mine(ctx context.Context, actor principal.Actor) ([]Competence, error) {
	if !actor.Authenticated() {
		return nil, ErrNotAuthenticated
	}
	return s.store.Competences(ctx, CompetenceQuery{Person: actor.ID},
		policy.CompetenceFilter{Scope: policy.CompetenceScopeOwn, OwnerID: actor.ID})
}

// SetMine states the caller's own competence for a module, or changes it.
func (s *CompetenceService) SetMine(ctx context.Context, actor principal.Actor, moduleID uuid.UUID,
	level CompetenceLevel, note string) (*Competence, error) {
	if !actor.Authenticated() {
		return nil, ErrNotAuthenticated
	}
	note, err := validCompetence(level, note)
	if err != nil {
		return nil, err
	}

	where, err := s.store.ModuleContext(ctx, moduleID, actor.ID)
	if err != nil {
		return nil, err
	}
	if !where.Found {
		return nil, ErrModuleNotFound
	}
	if !where.Open {
		return nil, ErrCompetenceModuleRetired
	}
	if !policy.MayStateOwnCompetence(actor, where.CallerIsMember) {
		return nil, ErrCompetenceOutsideGroups
	}

	id, err := s.store.SetOwn(ctx, moduleID, actor.ID, level, note)
	if err != nil {
		return nil, err
	}
	return s.readBack(ctx, id, policy.CompetenceFilter{Scope: policy.CompetenceScopeOwn, OwnerID: actor.ID})
}

// WithdrawMine removes one of the caller's own statements — also one on a module of a group they
// have since left, which is the one repair such a row needs.
func (s *CompetenceService) WithdrawMine(ctx context.Context, actor principal.Actor, id uuid.UUID) error {
	if !actor.Authenticated() {
		return ErrNotAuthenticated
	}
	return s.store.WithdrawOwn(ctx, id, actor.ID)
}

// SetForTeacher states a competence on behalf of a teacher without an account.
func (s *CompetenceService) SetForTeacher(ctx context.Context, actor principal.Actor,
	moduleID, teacherID uuid.UUID, level CompetenceLevel, note string) (*Competence, error) {
	if !actor.Authenticated() {
		return nil, ErrNotAuthenticated
	}
	note, err := validCompetence(level, note)
	if err != nil {
		return nil, err
	}

	where, err := s.store.ModuleContext(ctx, moduleID, actor.ID)
	if err != nil {
		return nil, err
	}
	if !where.Found {
		return nil, ErrModuleNotFound
	}
	// Refused before anything about the teacher is looked up, so that a caller who may not write
	// here learns nothing about who has an account.
	if !policy.MayStateCompetenceForTeacher(actor, where.SubjectGroupID) {
		return nil, ErrCompetenceForTeacherRefused
	}
	if !where.Open {
		return nil, ErrCompetenceModuleRetired
	}

	exists, err := s.store.TeacherExists(ctx, teacherID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrAssigneeNotFound
	}
	account, err := s.store.AccountOfTeacher(ctx, teacherID)
	if err != nil {
		return nil, err
	}
	if account != uuid.Nil {
		return nil, ErrCompetenceTeacherHasAccount
	}

	id, err := s.store.SetForTeacher(ctx, moduleID, teacherID, actor.ID, level, note)
	if err != nil {
		return nil, err
	}
	return s.readBack(ctx, id, policy.CompetenceVisibility(actor))
}

// WithdrawForTeacher removes a statement about a teacher without an account.
//
// A row that is not there, is not a teacher row, or is on a module the caller does not lead
// answers the same: not found. Telling the last apart would confirm that the row exists.
func (s *CompetenceService) WithdrawForTeacher(ctx context.Context, actor principal.Actor, id uuid.UUID) error {
	if !actor.Authenticated() {
		return ErrNotAuthenticated
	}
	group, found, err := s.store.TeacherRowGroup(ctx, id)
	if err != nil {
		return err
	}
	if !found || !policy.MayStateCompetenceForTeacher(actor, group) {
		return ErrCompetenceNotFound
	}
	return s.store.WithdrawForTeacher(ctx, id)
}

// MyStatus is, for each subject group the caller is in, how many of its compulsory modules they
// can teach and how many they should. Their own numbers, so no rule applies beyond signing in.
func (s *CompetenceService) MyStatus(ctx context.Context, actor principal.Actor) ([]CompetenceGroupStatus, error) {
	if !actor.Authenticated() {
		return nil, ErrNotAuthenticated
	}
	status, err := s.store.OwnGroupStatus(ctx, actor.ID)
	for i := range status {
		status[i].Minimum = policy.MinCompulsoryCompetences
	}
	return status, err
}

// MemberStatus is every member of one subject group and their count — the lead's work list.
func (s *CompetenceService) MemberStatus(ctx context.Context, actor principal.Actor,
	subjectGroupID uuid.UUID) ([]MemberCompetenceStatus, error) {
	if !policy.MayReadSubjectGroupCompetences(actor, subjectGroupID) {
		return nil, ErrCompetenceGroupRefused
	}
	status, err := s.store.MemberStatus(ctx, subjectGroupID)
	for i := range status {
		status[i].Minimum = policy.MinCompulsoryCompetences
	}
	return status, err
}

// ModulesWithoutCompetence is the modules of one subject group nobody has said they can teach.
func (s *CompetenceService) ModulesWithoutCompetence(ctx context.Context, actor principal.Actor,
	subjectGroupID uuid.UUID) ([]ModuleWithoutCompetence, error) {
	if !policy.MayReadSubjectGroupCompetences(actor, subjectGroupID) {
		return nil, ErrCompetenceGroupRefused
	}
	return s.store.ModulesWithoutCompetence(ctx, subjectGroupID)
}

func (s *CompetenceService) readBack(ctx context.Context, id uuid.UUID,
	filter policy.CompetenceFilter) (*Competence, error) {
	c, err := s.store.CompetenceByID(ctx, id, filter)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, ErrCompetenceNotFound
	}
	return c, nil
}

func validCompetence(level CompetenceLevel, note string) (string, error) {
	if !level.Valid() {
		return "", ErrCompetenceLevelInvalid
	}
	note = strings.TrimSpace(note)
	if len([]rune(note)) > MaxCompetenceNote {
		return "", ErrCompetenceNoteTooLong
	}
	return note, nil
}
