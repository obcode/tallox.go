package domain

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/obcode/tallox.go/internal/policy"
	"github.com/obcode/tallox.go/internal/principal"
)

// MaxTemporaryGrantDays bounds a grant that expires.
//
// A temporary grant exists so that an administrator who has to look at something can step
// over a threshold and have the database step back over it for them — see the migration that
// added person_role.expires_at. Thirty days is generous for that and short enough that it
// cannot quietly become the way roles are held: a grant meant to last a semester is a
// permanent grant, and it should be visible as one in the list rather than as a date nobody
// looks at.
const MaxTemporaryGrantDays = 30

// AdmissionRole is the role somebody gets when they are admitted from the examination office's
// list of the people who teach.
//
// Everywhere else in this package a new account holds no roles at all, deliberately: who may do
// what should be a list somebody wrote rather than a default nobody chose. Admitting from that
// list is the exception, and it is not really one — standing in it is the statement that this
// person teaches at the faculty, which is exactly what the role says. It is also the smallest
// role there is: it opens the module catalogue, which the examination office publishes anyway,
// and the holder's own profile and entries. Nothing about anybody else.
//
// An account with no role at all is worse than a decision nobody made: it can sign in and see
// nothing, which reads as a defect and arrives as a support question.
const AdmissionRole = policy.RoleLecturer

// MaxNameLength bounds what the administration can type into a name.
const MaxNameLength = 200

// MaxMailLength is the practical ceiling on an address. RFC 5321 says 254 for the whole path;
// anything near it in this faculty is a typo.
const MaxMailLength = 254

// The refusals this part of the domain produces.
var (
	// ErrNotAdministrator: the caller may not administer people. Interactive ADMIN only —
	// see policy.MayAdministerPeople, which is where the rule is.
	ErrNotAdministrator = errors.New("not allowed to administer people")
	// ErrInvalidMail: an address that cannot be the identity a proxy asserts.
	ErrInvalidMail = errors.New("that is not a usable mail address")
	// ErrNameTooLong is self-explanatory.
	ErrNameTooLong = fmt.Errorf("a name may be at most %d characters", MaxNameLength)
	// ErrPersonExists: somebody with that address is already known. Not a leak — the
	// administration screen lists everybody anyway, so this tells the caller nothing they
	// could not see.
	ErrPersonExists = errors.New("a person with that mail address already exists")
	// ErrNoSuchPerson is what a wrong id gets.
	ErrNoSuchPerson = errors.New("no such person")
	// ErrNoSuchTeacher: an id no teacher has.
	ErrNoSuchTeacher = errors.New("no such teacher")
	// ErrTeacherHasNoMail: somebody the examination office gives no address for.
	//
	// The address is the whole link between the two lists, so this is not a gap to be filled
	// in here — it is the reason this person can never sign in, and the fix is in the source.
	// Three of 257 are like this.
	ErrTeacherHasNoMail = errors.New("the examination office gives no address for this person")
	// ErrUnknownRole: a role string internal/policy does not recognise.
	ErrUnknownRole = errors.New("no such role")
	// ErrUnknownProgramme: a study programme code no programme has.
	ErrUnknownProgramme = errors.New("no such study programme")
	// ErrNotAProgrammeLead: assigning programmes to somebody who does not lead one.
	//
	// A refusal rather than a silent no-op, because the two ways of getting here are different
	// mistakes: setting the roles and the programmes in the wrong order, or assigning a
	// programme to the wrong person entirely.
	ErrNotAProgrammeLead = errors.New("this person does not hold the study programme lead role")
	// ErrGrantExpiryOutOfRange: an expiry in the past, or further out than the ceiling.
	ErrGrantExpiryOutOfRange = fmt.Errorf(
		"a temporary grant runs from now to at most %d days from now", MaxTemporaryGrantDays)
	// ErrLastAdmin: the change would leave the installation with nobody who can administer
	// it.
	//
	// The one refusal here that is not about the caller getting something wrong. It is the
	// guard against the failure this whole feature was asked for: an administrator removing
	// the last one, on an installation reachable only through a VPN whose other repair is
	// psql on the host.
	ErrLastAdmin = errors.New("this would leave the installation without an administrator")
)

// LastAdminReason is what that refusal says to the person reading it.
const LastAdminReason = "Das würde Tallox ohne Administration zurücklassen. " +
	"Erst jemand anderem ADMIN geben, dann diese Änderung noch einmal versuchen."

// Person is a person as the administration sees them.
type Person struct {
	ID   uuid.UUID
	Mail string
	Name string
	// SortName is the surname-first spelling, for a list that has to be in an order somebody can
	// follow. Empty for everybody the examination office does not publish — there is no way to
	// tell which word of a written-out name is the surname, and guessing would put a colleague
	// under the wrong letter with nothing to show for it.
	//
	// Filled in by the administration reads. The authentication path does not ask for it.
	SortName string
	Active   bool
	// Roles are the grants in force right now — expired ones are not here. RoleGrants is
	// where the full history of a person's grants lives.
	Roles []policy.Role
	// Programmes are the study programmes this person's PROGRAMME_LEAD grant applies to.
	//
	// Empty for everybody else, and empty for a lead nobody has assigned one to yet — which is
	// a state with consequences rather than a gap: an unassigned lead may plan nothing, and
	// telling them that is different from telling them they are not allowed.
	Programmes []Programme
}

// TeacherAccount is somebody the examination office publishes, together with the account they
// have in this installation — or the absence of one.
//
// The two lists are joined by the mail address and nothing else, which is the whole point: a
// teacher is imported master data and grants nothing, and who may sign in is a decision
// somebody made. Person is nil for the great majority, who teach here and cannot sign in, and
// for everybody the source gives no address for, who never can.
//
// A deactivated account is a Person with Active false, not a nil one. "Nobody has admitted
// them" and "somebody took it away" are different states with different next steps, and this
// is the one place that has to keep them apart.
type TeacherAccount struct {
	Teacher Teacher
	Person  *Person
}

// Admitted reports whether somebody of this address may sign in.
func (a TeacherAccount) Admitted() bool { return a.Person != nil && a.Person.Active }

// RoleGrant is one grant, as stored, expired ones included.
type RoleGrant struct {
	Role      policy.Role
	GrantedAt time.Time
	// GrantedBy is uuid.Nil for a grant that predates a human decision — the reconciliation
	// of the protected administrators, a future import.
	GrantedBy uuid.UUID
	// ExpiresAt is the zero time for a grant that does not expire.
	ExpiresAt time.Time
}

// Expired reports whether this grant has stopped taking effect.
func (g RoleGrant) Expired(now time.Time) bool {
	return !g.ExpiresAt.IsZero() && !g.ExpiresAt.After(now)
}

// PeopleStore is the persistence this service needs, and nothing more.
//
// SetActive and RevokeRole carry the last-administrator guard, and they carry it because it
// cannot live up here: the rule is "there is at least one other active administrator", which
// is a statement about rows that a second caller can invalidate between the check and the
// write. internal/store takes an advisory lock and does both under it. That is the same
// reasoning as everywhere else in this repository — a rule whose truth is a property of the
// database belongs in the database, and a version of it in a service layer passes its unit
// test while the shipped code races.
type PeopleStore interface {
	ListPeople(ctx context.Context, search string, includeInactive bool) ([]Person, error)
	PersonByID(ctx context.Context, id uuid.UUID) (*Person, error)
	PersonByMail(ctx context.Context, mail string) (*Person, error)
	CreatePerson(ctx context.Context, mail, name string) (*Person, error)
	SetPersonName(ctx context.Context, id uuid.UUID, name string) error
	// SetPersonActive refuses with ErrLastAdmin when deactivating would remove the last
	// administrator.
	SetPersonActive(ctx context.Context, id uuid.UUID, active bool) error
	GrantRole(ctx context.Context, personID uuid.UUID, role policy.Role,
		grantedBy uuid.UUID, expiresAt time.Time) error
	// RevokeRole refuses with ErrLastAdmin when the role is ADMIN and nobody else holds it.
	RevokeRole(ctx context.Context, personID uuid.UUID, role policy.Role) error
	RoleGrants(ctx context.Context, personID uuid.UUID) ([]RoleGrant, error)
	// SetPersonProgrammes replaces the study programmes one person's PROGRAMME_LEAD grant
	// applies to. Refuses with ErrUnknownProgramme for a code no programme has.
	SetPersonProgrammes(ctx context.Context, personID uuid.UUID, codes []string,
		grantedBy uuid.UUID) error
	TeacherAccounts(ctx context.Context) ([]TeacherAccount, error)
	// TeacherAccountByID returns (nil, nil) for an id no teacher has.
	TeacherAccountByID(ctx context.Context, teacherID uuid.UUID) (*TeacherAccount, error)
	// AdmitTeacher gets one teacher an active account and grants it this role, in one
	// transaction. Idempotent: admitting somebody already admitted changes nothing.
	AdmitTeacher(ctx context.Context, teacherID uuid.UUID, role policy.Role,
		grantedBy uuid.UUID) (*TeacherAccount, error)
}

// PeopleService is user administration: who may use this installation, and as what.
type PeopleService struct {
	store PeopleStore
	now   func() time.Time
}

// NewPeopleService wires the service. now is injectable so that expiry has a test that does
// not sleep.
func NewPeopleService(store PeopleStore, now func() time.Time) *PeopleService {
	if now == nil {
		now = time.Now
	}
	return &PeopleService{store: store, now: now}
}

// List returns the people this installation knows.
func (s *PeopleService) List(ctx context.Context, actor principal.Actor,
	search string, includeInactive bool) ([]Person, error) {
	if !policy.MayAdministerPeople(actor) {
		return nil, ErrNotAdministrator
	}
	return s.store.ListPeople(ctx, strings.TrimSpace(search), includeInactive)
}

// Get returns one person, or ErrNoSuchPerson.
func (s *PeopleService) Get(ctx context.Context, actor principal.Actor,
	id uuid.UUID) (*Person, error) {
	if !policy.MayAdministerPeople(actor) {
		return nil, ErrNotAdministrator
	}
	person, err := s.store.PersonByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if person == nil {
		return nil, ErrNoSuchPerson
	}
	return person, nil
}

// ByMail returns one person by the address the login asserts, or ErrNoSuchPerson.
//
// The lookup the support question arrives with: "Kollegin X says she cannot see the demand
// planning" carries an address, not an id.
func (s *PeopleService) ByMail(ctx context.Context, actor principal.Actor,
	mail string) (*Person, error) {
	if !policy.MayAdministerPeople(actor) {
		return nil, ErrNotAdministrator
	}
	person, err := s.store.PersonByMail(ctx, strings.TrimSpace(mail))
	if err != nil {
		return nil, err
	}
	if person == nil {
		return nil, ErrNoSuchPerson
	}
	return person, nil
}

// Grants returns one person's grants, expired ones included.
func (s *PeopleService) Grants(ctx context.Context, actor principal.Actor,
	id uuid.UUID) ([]RoleGrant, error) {
	if !policy.MayAdministerPeople(actor) {
		return nil, ErrNotAdministrator
	}
	return s.store.RoleGrants(ctx, id)
}

// Create adds somebody to the installation.
//
// The mail address is all that is required, on purpose. It is the only thing that has to be
// right — it is what the proxy asserts, so it is what decides whether the person can sign in
// at all — and everything else can be filled in later by the person themselves or by the ZPA
// import. Requiring a name here would mean a study-programme lead who wants to add a new
// colleague has to go and find out how they spell their first name before the account can
// exist.
//
// A new person holds no roles. LECTURER is granted like any other, visibly, so that the list
// of who may do what is a list somebody wrote rather than a default nobody chose.
func (s *PeopleService) Create(ctx context.Context, actor principal.Actor,
	mail, name string) (*Person, error) {
	if !policy.MayAdministerPeople(actor) {
		return nil, ErrNotAdministrator
	}

	mail = strings.TrimSpace(mail)
	name = strings.TrimSpace(name)

	if err := ValidateMail(mail); err != nil {
		return nil, err
	}
	if len([]rune(name)) > MaxNameLength {
		return nil, ErrNameTooLong
	}

	// Checked rather than left to the unique index, because the index would raise SQLSTATE
	// 23505 and this path has to produce a sentence somebody can act on. Unlike the wish
	// write path, saying "that person already exists" leaks nothing: the caller is an
	// administrator looking at a screen that lists everybody.
	existing, err := s.store.PersonByMail(ctx, mail)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, ErrPersonExists
	}

	return s.store.CreatePerson(ctx, mail, name)
}

// Rename sets the display name.
func (s *PeopleService) Rename(ctx context.Context, actor principal.Actor,
	id uuid.UUID, name string) (*Person, error) {
	if !policy.MayAdministerPeople(actor) {
		return nil, ErrNotAdministrator
	}
	name = strings.TrimSpace(name)
	if len([]rune(name)) > MaxNameLength {
		return nil, ErrNameTooLong
	}
	if err := s.store.SetPersonName(ctx, id, name); err != nil {
		return nil, err
	}
	return s.Get(ctx, actor, id)
}

// SetActive activates or deactivates somebody.
//
// Deactivation is how a leaver loses everything at once, tokens included, and it is the only
// removal this system has — a person row is never deleted, because the assignments they held
// stay in the history and the audit log has to keep resolving who did what.
func (s *PeopleService) SetActive(ctx context.Context, actor principal.Actor,
	id uuid.UUID, active bool) (*Person, error) {
	if !policy.MayAdministerPeople(actor) {
		return nil, ErrNotAdministrator
	}
	if err := s.store.SetPersonActive(ctx, id, active); err != nil {
		return nil, err
	}
	return s.Get(ctx, actor, id)
}

// SetRoles brings somebody's grants to exactly this set.
//
// A whole-set operation rather than grant/revoke pairs, because that is what the screen shows
// and because the alternative loses to a race the moment two administrators have the same
// person open: with add/remove calls, the second one's view of what the first did decides the
// outcome. Sending the set says what the caller means.
//
// expiresAt applies to the grants being *added*, and it is what makes the DEANS_OFFICE
// threshold usable: "let me look at this until this evening" is a set operation with a date
// on it, and nothing has to be remembered afterwards. Existing grants keep their own expiry
// unless they are re-granted.
func (s *PeopleService) SetRoles(ctx context.Context, actor principal.Actor,
	id uuid.UUID, roles []policy.Role, expiresAt time.Time) (*Person, error) {
	if !policy.MayAdministerPeople(actor) {
		return nil, ErrNotAdministrator
	}

	if !expiresAt.IsZero() {
		now := s.now()
		if !expiresAt.After(now) || expiresAt.After(now.AddDate(0, 0, MaxTemporaryGrantDays)) {
			return nil, ErrGrantExpiryOutOfRange
		}
	}

	current, err := s.Get(ctx, actor, id)
	if err != nil {
		return nil, err
	}

	wanted := make(map[policy.Role]bool, len(roles))
	for _, r := range roles {
		if _, ok := policy.ParseRole(string(r)); !ok {
			return nil, fmt.Errorf("%w: %q", ErrUnknownRole, r)
		}
		wanted[r] = true
	}

	held := make(map[policy.Role]bool, len(current.Roles))
	for _, r := range current.Roles {
		held[r] = true
	}

	// Revoke first. If the caller is taking ADMIN away from somebody and giving it to
	// somebody else in two separate calls, doing the removals first is what makes the
	// last-administrator guard fire on the call that would actually cause the harm rather
	// than one call later, when it is already too late to be a guard.
	for _, r := range policy.AllRoles() {
		if held[r] && !wanted[r] {
			if err := s.store.RevokeRole(ctx, id, r); err != nil {
				return nil, err
			}
		}
	}
	for _, r := range policy.AllRoles() {
		if wanted[r] && !held[r] {
			if err := s.store.GrantRole(ctx, id, r, actor.ID, expiresAt); err != nil {
				return nil, err
			}
		}
	}

	return s.Get(ctx, actor, id)
}

// SetProgrammes replaces the study programmes somebody's leadership applies to.
//
// The whole set at once, like SetRoles and for the same reason: a per-programme mutation would
// let the two calls of a swap be separated, and the interval between them is one in which
// somebody leads a programme nobody meant them to.
//
// An empty list is allowed and means they lead none, which is the state a fresh grant is in.
// That state is not "unrestricted": a lead with no programme may plan nothing, deliberately —
// see policy.PlanningScope.
//
// Administration only, and interactive only, on the same terms as every other grant: this is
// the granting of access, and doing it from a long-lived token in a script would decouple it
// from any sign-in.
func (s *PeopleService) SetProgrammes(ctx context.Context, actor principal.Actor,
	id uuid.UUID, codes []string) (*Person, error) {
	if !policy.MayAdministerPeople(actor) {
		return nil, ErrNotAdministrator
	}

	person, err := s.Get(ctx, actor, id)
	if err != nil {
		return nil, err
	}

	// Refused rather than silently stored. The grant is what the scope narrows, so a scope
	// without one is a row the database would refuse anyway — reporting it here turns a
	// foreign-key violation into a sentence about what the caller did.
	leads := false
	for _, r := range person.Roles {
		if r == policy.RoleProgrammeLead {
			leads = true
			break
		}
	}
	if !leads && len(codes) > 0 {
		return nil, ErrNotAProgrammeLead
	}

	normalised := make([]string, 0, len(codes))
	seen := make(map[string]bool, len(codes))
	for _, code := range codes {
		code = strings.ToUpper(strings.TrimSpace(code))
		if code == "" || seen[code] {
			continue
		}
		seen[code] = true
		normalised = append(normalised, code)
	}

	if err := s.store.SetPersonProgrammes(ctx, id, normalised, actor.ID); err != nil {
		return nil, err
	}
	return s.Get(ctx, actor, id)
}

// TeacherAccounts lists everybody the examination office publishes, with the account they have
// here — the screen an administrator admits people from.
//
// Everybody, unfiltered: it is 257 rows behind an administrator's login, and filtering is what
// the screen does with them. The one thing left out is the people a successful import stopped
// mentioning.
//
// Note what it is not: the list of accounts. Somebody with an account and no teacher row — the
// dean's office, a protected administrator from the configuration file — is not here, and List
// is where they are.
func (s *PeopleService) TeacherAccounts(ctx context.Context,
	actor principal.Actor) ([]TeacherAccount, error) {
	if !policy.MayAdministerPeople(actor) {
		return nil, ErrNotAdministrator
	}
	return s.store.TeacherAccounts(ctx)
}

// SetTeacherAdmitted lets somebody from the examination office's list into this installation, or
// withdraws that.
//
// One operation for both directions, because it is one switch on one screen and its two
// positions are the same statement with opposite signs. Both are idempotent: sending the state
// something is already in changes nothing, which is what a switch means and what a
// double-click deserves.
//
// Admitting grants AdmissionRole — see there for why this is the one place a new account is not
// roleless. Withdrawing deactivates, which is the only removal this system has: it refuses
// authentication on both doors and withdraws every token in the same moment, and it keeps the
// role grants, so re-admitting somebody restores what they had rather than starting them over.
// It meets the last-administrator guard like every other deactivation.
func (s *PeopleService) SetTeacherAdmitted(ctx context.Context, actor principal.Actor,
	teacherID uuid.UUID, admitted bool) (*TeacherAccount, error) {
	if !policy.MayAdministerPeople(actor) {
		return nil, ErrNotAdministrator
	}

	current, err := s.store.TeacherAccountByID(ctx, teacherID)
	if err != nil {
		return nil, err
	}
	if current == nil {
		return nil, ErrNoSuchTeacher
	}

	if !admitted {
		if current.Person == nil || !current.Person.Active {
			return current, nil
		}
		if err := s.store.SetPersonActive(ctx, current.Person.ID, false); err != nil {
			return nil, err
		}
		return s.store.TeacherAccountByID(ctx, teacherID)
	}

	if current.Teacher.Mail == "" {
		return nil, ErrTeacherHasNoMail
	}
	// The address comes from another institution's database, and this is the moment it becomes
	// an identity here. Six of the 257 real ones are addresses the identity provider will never
	// assert; those are still admitted, because "will this person ever sign in" is not ours to
	// answer. What is refused is a string that cannot be an address at all.
	if err := ValidateMail(current.Teacher.Mail); err != nil {
		return nil, err
	}

	return s.store.AdmitTeacher(ctx, teacherID, AdmissionRole, actor.ID)
}

// ValidateMail rejects what cannot be an identity the auth proxy asserts.
//
// Deliberately shallow. A full RFC 5322 parser would reject addresses that work and accept
// addresses that do not, and the authority on whether an address is real is the identity
// provider, not this function. What it catches is the class of mistake that actually happens:
// a name typed into the address field, a trailing comma from a copied list, an address with a
// space in it. Anything subtler shows up as "that colleague cannot sign in", which is a
// question the administration screen can answer by looking at the row.
func ValidateMail(mail string) error {
	if mail == "" {
		return fmt.Errorf("%w: it is empty", ErrInvalidMail)
	}
	if len(mail) > MaxMailLength {
		return fmt.Errorf("%w: it is longer than %d characters", ErrInvalidMail, MaxMailLength)
	}
	if strings.ContainsAny(mail, " \t\r\n,;<>") {
		return fmt.Errorf("%w: it contains a character an address cannot have", ErrInvalidMail)
	}
	local, domain, found := strings.Cut(mail, "@")
	if !found || local == "" || domain == "" {
		return fmt.Errorf("%w: it needs a local part and a domain, separated by @", ErrInvalidMail)
	}
	if strings.Contains(domain, "@") || !strings.Contains(domain, ".") {
		return fmt.Errorf("%w: %q is not a domain", ErrInvalidMail, domain)
	}
	return nil
}
