-- Subject groups: the faculty's own grouping of modules and people.
--
-- No semester anywhere in this file, and that is the shape of the thing rather than an omission.
-- A subject group is a statement about a subject; what is planned is derived through the module.

-- name: SubjectGroups :many
-- The list, newest decisions and all. Ordered by code, which is what a picker and a slide both
-- want and is stable between calls.
--
-- The module count is a correlated subquery rather than a join with a GROUP BY, because the
-- other two things a group carries — its leads and its members — are read for a set of groups in
-- one statement each below. Three round trips for the whole screen, none of them per row.
--
-- Counting modules is safe in a way counting anything over wishes never is: a module assignment
-- is catalogue data, and nobody is protected from it being known.
SELECT
    g.id, g.code, g.name, g.active, g.created_at, g.updated_at,
    (SELECT count(*) FROM module_subject_group m WHERE m.subject_group_id = g.id)::int
        AS module_count
FROM subject_group g
WHERE sqlc.arg(include_inactive)::boolean OR g.active
ORDER BY g.code;

-- name: SubjectGroupByID :one
SELECT
    g.id, g.code, g.name, g.active, g.created_at, g.updated_at,
    (SELECT count(*) FROM module_subject_group m WHERE m.subject_group_id = g.id)::int
        AS module_count
FROM subject_group g
WHERE g.id = $1;

-- name: SubjectGroupsOfPerson :many
-- One person's memberships. What the wish screen offers first.
--
-- Inactive groups are left out: a retired group is not a subject somebody is currently working
-- in, and offering it first would put a wound-up group at the top of the screen it exists to
-- shorten. The membership row survives, so bringing the group back brings the person back with
-- it.
SELECT
    g.id, g.code, g.name, g.active, g.created_at, g.updated_at,
    (SELECT count(*) FROM module_subject_group m WHERE m.subject_group_id = g.id)::int
        AS module_count
FROM person_subject_group s
JOIN subject_group g ON g.id = s.subject_group_id
WHERE s.person_id = $1 AND g.active
ORDER BY g.code;

-- name: SubjectGroupsByIDs :many
-- A handful of subject groups by id.
--
-- For `me`, which has to turn the subject group ids an actor carries into codes a person reads
-- — the counterpart of ProgrammesByIDs, and the same argument: the full list would be three
-- statements for a field that renders a handful of names.
--
-- Retired groups are kept, unlike SubjectGroupsOfPerson. That query answers "which subjects am
-- I working in", where a wound-up group is noise; this one answers "what have I been made
-- responsible for", and a leadership nobody has revoked is still a leadership. Hiding it would
-- be a screen that says she leads nothing while the grant says otherwise.
SELECT
    g.id, g.code, g.name, g.active, g.created_at, g.updated_at,
    (SELECT count(*) FROM module_subject_group m WHERE m.subject_group_id = g.id)::int
        AS module_count
FROM subject_group g
WHERE g.id = ANY (sqlc.arg(ids)::uuid[])
ORDER BY g.code;

-- name: SubjectGroupLeadsFor :many
-- Who leads each of these groups.
--
-- The expiry filter is the same rule person_role_scope applies, applied again because this reads
-- the scope table directly for its people rather than the view for its ids. A lead whose grant
-- ran out is not a lead, and a screen that still showed them would be showing a permission that
-- no longer exists.
SELECT
    s.subject_group_id,
    p.id, p.mail, p.name,
    COALESCE(t.short_name, '')::text AS sort_name,
    p.active
FROM person_subject_group_scope s
JOIN person p ON p.id = s.person_id
JOIN person_role r ON r.person_id = s.person_id AND r.role = s.role
LEFT JOIN teacher t ON t.mail = p.mail
WHERE s.subject_group_id = ANY (sqlc.arg(group_ids)::uuid[])
  AND (r.expires_at IS NULL OR r.expires_at > now())
ORDER BY s.subject_group_id, COALESCE(NULLIF(t.short_name, ''), p.name), p.mail;

-- name: SubjectGroupMembersFor :many
-- Who is in each of these groups.
--
-- No expiry filter, and the asymmetry with the query above is the point: membership is not a
-- grant. It says which subjects a colleague works in, it grants nothing, and it does not run out.
SELECT
    s.subject_group_id,
    p.id, p.mail, p.name,
    COALESCE(t.short_name, '')::text AS sort_name,
    p.active
FROM person_subject_group s
JOIN person p ON p.id = s.person_id
LEFT JOIN teacher t ON t.mail = p.mail
WHERE s.subject_group_id = ANY (sqlc.arg(group_ids)::uuid[])
ORDER BY s.subject_group_id, COALESCE(NULLIF(t.short_name, ''), p.name), p.mail;

-- name: CreateSubjectGroup :one
INSERT INTO subject_group (code, name)
VALUES ($1, $2)
RETURNING id, code, name, active, created_at, updated_at, 0::int AS module_count;

-- name: RenameSubjectGroup :one
-- The name only. The code is the address — it is in URLs and in colleagues' scripts — and
-- changing it is not a rename but a different group.
UPDATE subject_group
SET name = $2, updated_at = now()
WHERE id = $1
RETURNING id, code, name, active, created_at, updated_at,
          (SELECT count(*) FROM module_subject_group m WHERE m.subject_group_id = subject_group.id)::int
              AS module_count;

-- name: SetSubjectGroupActive :one
-- Retiring, and the way back. There is no delete: a group that was split still has to render in
-- the planning it was part of, and its module assignments are weeks of somebody's judgement.
--
-- updated_at stays put when nothing changed, so retiring a group that is already retired does not
-- make the row look edited.
UPDATE subject_group
SET active = $2,
    updated_at = CASE WHEN active IS DISTINCT FROM $2 THEN now() ELSE updated_at END
WHERE id = $1
RETURNING id, code, name, active, created_at, updated_at,
          (SELECT count(*) FROM module_subject_group m WHERE m.subject_group_id = subject_group.id)::int
              AS module_count;

-- name: AssignModulesToSubjectGroup :execrows
-- A batch, because the task this exists for is assigning 506 modules and a screen that saves one
-- row per click is a task nobody finishes.
--
-- An upsert rather than delete-then-insert: moving a module between groups is one statement, so
-- there is no moment in which the module belongs to nothing. assigned_by moves with it, because
-- the question this column answers — who says this module is mathematics — is about the current
-- assignment and not about the first one.
INSERT INTO module_subject_group (module_id, subject_group_id, assigned_by)
SELECT m, sqlc.arg(subject_group_id)::uuid, sqlc.narg(assigned_by)::uuid
FROM unnest(sqlc.arg(module_ids)::uuid[]) AS m
ON CONFLICT (module_id) DO UPDATE
SET subject_group_id = EXCLUDED.subject_group_id,
    assigned_at = now(),
    assigned_by = EXCLUDED.assigned_by;

-- name: ClearModulesSubjectGroup :execrows
-- Taking modules out of every group, which is the same screen's other button. A module with no
-- group is a normal state and the whole of October's work list.
DELETE FROM module_subject_group
WHERE module_id = ANY (sqlc.arg(module_ids)::uuid[]);

-- name: CountModulesFiledOutsideGroups :one
-- How many of these modules are currently filed under a group that is not in the given list.
--
-- The filter half of policy.MayFileModule, and the reason it is a query rather than a loop in
-- Go: filing is the one act that touches two subject groups, and the second one — where the
-- module is *today* — is a column. Reading the rows first and deciding afterwards would rest
-- the decision on a state from before the write; asked here, inside the same transaction, it
-- cannot.
--
-- A module with no row at all is not counted, and that is the rule rather than an omission: a
-- module nobody has sorted yet may be pulled in by whoever wants it. Same for a module that
-- does not exist — the foreign key on the write refuses that one, and it says so about the
-- thing somebody chose rather than about a list the screen just rendered.
--
-- Callers with an unrestricted reach do not ask. Passing every group that exists would be a
-- snapshot, and a group created between the two statements would fall outside it.
SELECT count(*)::int
FROM module_subject_group
WHERE module_id = ANY (sqlc.arg(module_ids)::uuid[])
  AND subject_group_id <> ALL (sqlc.arg(allowed_groups)::uuid[]);

-- name: ModulesWithoutSubjectGroup :one
-- The work list as a number: "37 modules still have no subject group".
--
-- Retired modules do not count. A module the examination office stopped publishing is not work
-- anybody has to finish, and counting it would make the list unfinishable.
SELECT count(*)::int
FROM module m
LEFT JOIN module_subject_group g ON g.module_id = m.id
WHERE g.module_id IS NULL AND m.retired_at IS NULL AND m.active;

-- name: ModulesOfSubjectGroup :many
-- Which modules a subject group holds, for the screen that shows somebody what a group is about.
--
-- The names and their home programme, and nothing else: this answers "is this my subject", not
-- "what does this module cost". Retired modules are left out — a group is described by what it
-- currently covers.
SELECT m.id, m.name, p.code AS home_programme_code
FROM module_subject_group g
JOIN module m ON m.id = g.module_id
JOIN programme p ON p.id = m.home_programme_id
WHERE g.subject_group_id = $1
  AND m.retired_at IS NULL
  AND m.active
ORDER BY (m.name = ''), m.name, m.id;

-- name: ClearSubjectGroupsOfPerson :exec
-- The person side of the membership table.
--
-- Its own statement rather than a filter on the group side, because the two are different acts:
-- an administrator sets up a group, and a colleague says which subjects they work in. Both write
-- this table and neither may quietly rewrite the other's rows.
DELETE FROM person_subject_group WHERE person_id = $1;

-- name: ClearSubjectGroupMembers :exec
DELETE FROM person_subject_group WHERE subject_group_id = $1;

-- name: AddSubjectGroupMembership :exec
INSERT INTO person_subject_group (person_id, subject_group_id, granted_by)
VALUES ($1, $2, sqlc.narg(granted_by))
ON CONFLICT (person_id, subject_group_id) DO NOTHING;

-- name: ClearSubjectGroupLeads :exec
DELETE FROM person_subject_group_scope
WHERE subject_group_id = $1 AND role = 'SUBJECT_GROUP_LEAD';

-- name: AddSubjectGroupLead :exec
-- The foreign key to person_role is what refuses a lead who does not hold the role. The service
-- checks first so that the ordinary case gets a sentence; this is the race the constraint closes.
INSERT INTO person_subject_group_scope (person_id, role, subject_group_id, granted_by)
VALUES ($1, 'SUBJECT_GROUP_LEAD', $2, sqlc.narg(granted_by))
ON CONFLICT (person_id, role, subject_group_id) DO NOTHING;

-- name: SubjectGroupsWithoutLead :one
-- "Keine Fachgruppe ohne Person, die sich ihrer annimmt", as a number rather than as a NOT NULL.
--
-- A constraint would mean a group could not be created before its lead was decided, and that a
-- lead could not be revoked without destroying the group. A count is the same requirement in the
-- form the beta testers can actually finish.
SELECT count(*)::int
FROM subject_group g
WHERE g.active
  AND NOT EXISTS (
      SELECT 1
      FROM person_subject_group_scope s
      JOIN person_role r ON r.person_id = s.person_id AND r.role = s.role
      WHERE s.subject_group_id = g.id
        AND (r.expires_at IS NULL OR r.expires_at > now())
  );
