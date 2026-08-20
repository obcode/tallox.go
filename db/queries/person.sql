-- People and their role grants.
--
-- Every read that resolves an identity returns the roles with it, in one round trip. Two
-- queries would mean a window in which a person exists and their grants do not, and the
-- authenticator would have to decide what to do with a caller whose permissions it could not
-- read — a decision with only bad answers.
--
-- The expiry filter sits in the JOIN condition rather than in a WHERE clause, in every query
-- below. That is not a style preference: in a LEFT JOIN, a WHERE on the right-hand table
-- turns the join into an inner one, so a person whose only grant has expired would disappear
-- from the list entirely instead of appearing with no roles. The version that leaks is the
-- one that hides people from the administration screen.

-- name: CreatePerson :one
-- The id is supplied by the caller rather than defaulted, so that a fixture, a seed and an
-- import can all say who they are creating before the insert happens.
INSERT INTO person (id, mail, name)
VALUES ($1, $2, $3)
RETURNING id, mail, name, active, created_at, updated_at;

-- name: PersonByMail :one
-- The authentication query of the browser door. mail is citext, so the comparison is
-- case-insensitive without a lower() that would defeat the unique index.
--
-- role_scopes carries the grants that name a thing as well as an action — leading *one* study
-- programme rather than leading in general. A correlated subquery rather than a second LEFT
-- JOIN, because joining two one-to-many tables in one SELECT multiplies their rows and the
-- roles array would repeat every role once per scope.
--
-- It applies the same expiry filter as the roles above, for the same reason: a grant the
-- database considers over must not still take effect, and a scope belonging to an expired grant
-- is exactly that. The composite foreign key means a scope cannot outlive a *revoked* grant;
-- this covers the one that merely ran out.
SELECT
    p.id,
    p.mail,
    p.name,
    p.active,
    COALESCE(
        array_agg(pr.role ORDER BY pr.role) FILTER (WHERE pr.role IS NOT NULL),
        ARRAY[]::text[]
    )::text[] AS roles,
    COALESCE((
        SELECT jsonb_agg(jsonb_build_object('role', s.role, 'programme', s.programme_id)
                         ORDER BY s.role, s.programme_id)
        FROM person_programme_scope s
        JOIN person_role r ON r.person_id = s.person_id AND r.role = s.role
        WHERE s.person_id = p.id
          AND (r.expires_at IS NULL OR r.expires_at > now())
    ), '[]'::jsonb)::jsonb AS role_scopes  -- cast: without it sqlc types the column interface{}
FROM person p
LEFT JOIN person_role pr
    ON pr.person_id = p.id
   AND (pr.expires_at IS NULL OR pr.expires_at > now())
WHERE p.mail = $1
GROUP BY p.id;

-- name: PersonByID :one
SELECT
    p.id,
    p.mail,
    p.name,
    p.active,
    COALESCE(
        array_agg(pr.role ORDER BY pr.role) FILTER (WHERE pr.role IS NOT NULL),
        ARRAY[]::text[]
    )::text[] AS roles,
    COALESCE((
        SELECT jsonb_agg(jsonb_build_object('role', s.role, 'programme', s.programme_id)
                         ORDER BY s.role, s.programme_id)
        FROM person_programme_scope s
        JOIN person_role r ON r.person_id = s.person_id AND r.role = s.role
        WHERE s.person_id = p.id
          AND (r.expires_at IS NULL OR r.expires_at > now())
    ), '[]'::jsonb)::jsonb AS role_scopes
FROM person p
LEFT JOIN person_role pr
    ON pr.person_id = p.id
   AND (pr.expires_at IS NULL OR pr.expires_at > now())
WHERE p.id = $1
GROUP BY p.id;

-- name: ListPeople :many
-- The administration screen: everybody, or everybody active, optionally narrowed by a
-- substring of the mail address or the name.
--
-- Ordered by name and then mail so that the list is stable between calls — an administration
-- table whose rows move between reloads is one where somebody eventually clicks the wrong
-- row. People with no name yet sort first, which is also where the work is.
SELECT
    p.id,
    p.mail,
    p.name,
    p.active,
    COALESCE(
        array_agg(pr.role ORDER BY pr.role) FILTER (WHERE pr.role IS NOT NULL),
        ARRAY[]::text[]
    )::text[] AS roles,
    COALESCE((
        SELECT jsonb_agg(jsonb_build_object('role', s.role, 'programme', s.programme_id)
                         ORDER BY s.role, s.programme_id)
        FROM person_programme_scope s
        JOIN person_role r ON r.person_id = s.person_id AND r.role = s.role
        WHERE s.person_id = p.id
          AND (r.expires_at IS NULL OR r.expires_at > now())
    ), '[]'::jsonb)::jsonb AS role_scopes
FROM person p
LEFT JOIN person_role pr
    ON pr.person_id = p.id
   AND (pr.expires_at IS NULL OR pr.expires_at > now())
WHERE (sqlc.narg('search')::text IS NULL
       OR p.mail ILIKE '%' || sqlc.narg('search')::text || '%'
       OR p.name ILIKE '%' || sqlc.narg('search')::text || '%')
  AND (sqlc.arg('include_inactive')::boolean OR p.active)
GROUP BY p.id
ORDER BY p.name, p.mail;

-- name: RoleGrantsByPerson :many
-- One person's grants as they actually are, expired ones included.
--
-- Deliberately not filtered: this is the detail view, and "DEANS_OFFICE, granted on Tuesday
-- by X, expired on Wednesday" is the answer to the only question this table gets asked —
-- who could see what, when. The permission lookups above are the ones that filter.
SELECT
    pr.role,
    pr.granted_at,
    pr.granted_by,
    pr.expires_at
FROM person_role pr
WHERE pr.person_id = $1
ORDER BY pr.role;

-- name: SetPersonActive :exec
-- Deactivation is how a leaver loses access to everything at once, tokens included.
--
-- The guard against deactivating the last administrator is not here. It cannot be: it depends
-- on a count over other rows, and a single statement that read that count would still race
-- with a concurrent deactivation. internal/store takes an advisory lock and checks first —
-- see AdminGrantLockKey.
UPDATE person
SET active = $2,
    updated_at = now()
WHERE id = $1;

-- name: SetPersonName :exec
-- Renaming somebody. Separate from the role and activity paths because it is the one edit
-- here that is not a permission change, and mixing it in would make every rename look like
-- one in the audit log.
UPDATE person
SET name = $2,
    updated_at = now()
WHERE id = $1;

-- name: GrantRole :exec
-- Idempotent in the sense that matters: granting a role somebody already holds updates its
-- expiry rather than failing, so "give me DEANS_OFFICE for another hour" is one call and not
-- a read-then-write race.
--
-- granted_at is refreshed with it. The row records the grant that is in force, and a
-- timestamp that pointed at a superseded one would be the wrong answer to the audit question.
INSERT INTO person_role (person_id, role, granted_by, expires_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (person_id, role) DO UPDATE
SET granted_by = EXCLUDED.granted_by,
    granted_at = now(),
    expires_at = EXCLUDED.expires_at;

-- name: RevokeRole :exec
DELETE FROM person_role
WHERE person_id = $1 AND role = $2;

-- name: LockAdminGrants :exec
-- Serialise every change that could remove the last administrator.
--
-- The rule to protect is "at least one active person holds an unexpired ADMIN grant", and it
-- is a statement about a *set* of rows. Two administrators can each check it, each see the
-- other, and each commit a change that was only safe while the other did not happen — the
-- textbook write skew, and here its outcome is an installation nobody can get into.
--
-- A single clever statement does not fix it, because the two dangerous operations touch
-- different tables: revoking ADMIN writes person_role, deactivating an administrator writes
-- person. What they have in common is these rows, so both take this lock first and the second
-- one waits and then re-reads.
--
-- Row locks rather than an advisory lock on a constant: this is scoped to the current schema,
-- which is what lets the integration tests run in parallel, and it needs no magic number that
-- somebody else could pick again for something unrelated.
SELECT 1 FROM person_role WHERE role = 'ADMIN' FOR UPDATE;

-- name: CountOtherActiveAdmins :one
-- How many people other than this one could still administer the installation.
--
-- "Could still": active person, unexpired grant. A deactivated administrator and one whose
-- temporary grant ran out are both people who cannot let anybody back in, and counting them
-- would make the last-administrator guard pass at exactly the moment it is needed.
--
-- Called under the advisory lock, never on its own.
SELECT count(*)
FROM person_role pr
JOIN person p ON p.id = pr.person_id
WHERE pr.role = 'ADMIN'
  AND p.active
  AND (pr.expires_at IS NULL OR pr.expires_at > now())
  AND pr.person_id <> $1;

-- name: EnsurePerson :one
-- Get this mail address a person row, creating one if there is none.
--
-- The reconciliation of the protected administrators runs through here on every start, so it
-- has to be a no-op on the ordinary case. ON CONFLICT DO UPDATE rather than DO NOTHING,
-- because DO NOTHING returns no row and the caller would need a second query to find the id
-- of the person it just did not create.
--
-- name is only written when the row is created: a person renamed through the interface, or by
-- a future ZPA import, must not be renamed back on the next restart by a value somebody typed
-- into a YAML file once. The DO UPDATE therefore writes a column back to itself — a true
-- no-op that still produces a row to return, which DO NOTHING does not.
INSERT INTO person (id, mail, name)
VALUES ($1, $2, $3)
ON CONFLICT (mail) DO UPDATE
SET updated_at = person.updated_at
RETURNING id, mail, name, active, created_at, updated_at;
