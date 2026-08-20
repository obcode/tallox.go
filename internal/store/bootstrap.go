package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ProtectedAdmin is one entry of the list in tallox.yaml: somebody who must be able to
// administer this installation whatever the database currently says.
type ProtectedAdmin struct {
	// Mail is the identity the auth proxy asserts. The natural key of a person.
	Mail string
	// Name is optional. It is written only when the person row is created — see EnsurePerson.
	Name string
}

// ReconcileOutcome reports what reconciling one entry changed, so the caller can log the
// interesting cases and stay silent about the ordinary one.
type ReconcileOutcome struct {
	Mail        string
	Created     bool
	Reactivated bool
	Granted     bool
}

// Changed reports whether anything happened at all. On a healthy installation every outcome
// answers false, which is what makes a log line about the others worth reading.
func (o ReconcileOutcome) Changed() bool { return o.Created || o.Reactivated || o.Granted }

// ReconcileProtectedAdmins makes the listed people exist, be active, and hold ADMIN.
//
// # Why this exists
//
// Two problems, and it is one mechanism for both.
//
// The first is the first boot. Both doors resolve identity against the person table, and
// handing out a Personal Access Token is itself something only a signed-in administrator can
// do — so a freshly created database is locked from the outside with the key on the inside.
// Somebody has to be in it before anybody can be let in.
//
// The second is the one this was actually asked for: an administrator removing another one by
// accident. On an installation reachable only through a VPN, whose other repair is psql on
// the host at a moment when somebody is already having a bad afternoon, "restart the
// container" is a far better recovery procedure. Running on every start rather than only on
// an empty database is the entire difference between the two.
//
// # Additive, always
//
// It creates, reactivates and grants. It never revokes, never deactivates, never renames. A
// list that could take something away would turn an edit to a YAML file — a deleted line, a
// typo in an address — into a silent mass demotion discovered at the next restart. The list
// answers "who must be able to get in", not "who may".
//
// # Why not decide this in the authenticator instead
//
// The tempting alternative is to hand the listed addresses ADMIN at request time, with no
// database row at all. It does not work: an actor with no person row has no id, and the id is
// what granted_by references, what a token belongs to, and what the audit log resolves. The
// result would be an administrator who cannot grant anybody anything and whose actions cannot
// be attributed. The row is what makes the rest of the system able to talk about this person.
func ReconcileProtectedAdmins(ctx context.Context, pool *pgxpool.Pool,
	admins []ProtectedAdmin) ([]ReconcileOutcome, error) {
	outcomes := make([]ReconcileOutcome, 0, len(admins))

	for _, admin := range admins {
		if admin.Mail == "" {
			return nil, errors.New("protected admin without a mail address")
		}
		outcome, err := reconcileOne(ctx, pool, admin)
		if err != nil {
			return nil, fmt.Errorf("cannot reconcile %s: %w", admin.Mail, err)
		}
		outcomes = append(outcomes, outcome)
	}

	return outcomes, nil
}

// reconcileOne does the three steps for a single entry, in one transaction.
//
// One transaction per entry rather than one for the whole list: a malformed entry halfway
// down must not undo the entries above it. The list exists to get somebody back in, and
// getting four of five people back in beats getting nobody back in because the fifth address
// has a typo.
func reconcileOne(ctx context.Context, pool *pgxpool.Pool,
	admin ProtectedAdmin) (ReconcileOutcome, error) {
	outcome := ReconcileOutcome{Mail: admin.Mail}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return outcome, fmt.Errorf("cannot begin: %w", err)
	}
	// Rollback after a successful commit is a no-op, so this needs no branching.
	defer func() { _ = tx.Rollback(ctx) }()

	q := New(tx)

	// The id is offered rather than defaulted, which is what makes "did this create the row"
	// answerable without a second query: EnsurePerson returns the existing row on conflict,
	// so getting our own id back means there was no conflict.
	offered := uuid.New()
	person, err := q.EnsurePerson(ctx, EnsurePersonParams{
		ID:   offered,
		Mail: admin.Mail,
		Name: admin.Name,
	})
	if err != nil {
		return outcome, fmt.Errorf("cannot ensure the person row: %w", err)
	}
	outcome.Created = person.ID == offered

	if !person.Active {
		if err := q.SetPersonActive(ctx, SetPersonActiveParams{
			ID: person.ID, Active: true,
		}); err != nil {
			return outcome, fmt.Errorf("cannot reactivate: %w", err)
		}
		outcome.Reactivated = true
	}

	// PersonByID filters expired grants, so somebody whose ADMIN ran out counts as not
	// holding it — which is the correct reading. An expired grant lets nobody in.
	current, err := q.PersonByID(ctx, person.ID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return outcome, fmt.Errorf("cannot read the grants: %w", err)
	}
	if !hasRole(current.Roles, roleAdmin) {
		// granted_by stays NULL: no human decided this, and that is exactly what the
		// column's NULL means. A self-reference would claim the person granted it to
		// themselves.
		//
		// No expiry either. A grant that repairs a lock-out and then expires is a lock-out
		// with a delay on it.
		if err := q.GrantRole(ctx, GrantRoleParams{
			PersonID: person.ID,
			Role:     roleAdmin,
		}); err != nil {
			return outcome, fmt.Errorf("cannot grant ADMIN: %w", err)
		}
		outcome.Granted = true
	}

	if err := tx.Commit(ctx); err != nil {
		return outcome, fmt.Errorf("cannot commit: %w", err)
	}
	return outcome, nil
}

// roleAdmin is the one role name this package needs to know.
//
// Spelled out rather than imported from internal/policy, because the CHECK constraint in the
// migration is what actually validates it: a typo here fails the insert rather than granting
// something meaningless. The three-way agreement between schema, policy and constraint is
// tested elsewhere in this package.
const roleAdmin = "ADMIN"

// EnsureDevelopmentUser gives the development identity a person row, and returns the id it
// actually has.
//
// Separate from ReconcileProtectedAdmins although both call the same query, because they are
// different acts: that one restores access somebody is entitled to and grants ADMIN; this one
// only makes an id referenceable, and grants nothing at all. Folding them together would put a
// role grant one boolean away from a mode meant for a laptop.
func EnsureDevelopmentUser(ctx context.Context, pool *pgxpool.Pool,
	id uuid.UUID, mail, name string) (uuid.UUID, error) {
	row, err := New(pool).EnsurePerson(ctx, EnsurePersonParams{ID: id, Mail: mail, Name: name})
	if err != nil {
		return uuid.Nil, fmt.Errorf("cannot ensure the development user: %w", err)
	}
	return row.ID, nil
}
