-- Competences: who can teach which module, and who would like to.
--
-- THE RULE THIS FILE IS MADE OF
--
-- Every SELECT that returns rows about other people carries the same four filter parameters, the
-- way wish.sql does, and for the same reason: WOULD_LIKE is a wish without a semester, and a query
-- written without the predicate is not a slow query, it is a leak.
--
--     @scope = 'all'            no restriction
--     @scope = 'own'            the caller's own entries
--     @scope = 'own_or_scoped'  their own, plus the modules of the programmes and subject groups
--                               they lead
--     anything else             nothing at all
--
-- The programme of a competence is the module's HOME programme. A competence has no instance and
-- therefore no demanding programme; the home programme is the one responsible for the module.
--
-- The three aggregates at the end are not filtered by that predicate, and
-- store.TestEveryCompetenceQueryIsFiltered knows them by name: the first counts only the caller's
-- own rows, the other two are refused in internal/domain unless the caller may read the whole
-- subject group they count over. That is the same rule applied before the query instead of inside
-- it — acceptable exactly because the answer is about one group the caller either reaches or not.

-- name: Competences :many
-- The competences the filter allows, narrowed by whatever the caller asked for.
SELECT
    c.id, c.module_id, c.person_id, c.teacher_id, c.level, c.note, c.created_at, c.updated_at,
    m.name AS module_name,
    -- Compulsory somewhere: under at least one version of some programme's regulations. What the
    -- kickoff means by "Pflichtkatalog", and what the minimum counts.
    EXISTS (SELECT 1 FROM module_offering o WHERE o.module_id = m.id AND o.is_duty) AS compulsory,
    sg.id AS subject_group_id, sg.code AS subject_group_code, sg.name AS subject_group_name,
    COALESCE(person.name, t.full_name, '')::text AS holder_name,
    COALESCE(person.mail, t.mail, '')::citext AS holder_mail,
    COALESCE(NULLIF(ts.short_name, ''), NULLIF(t.short_name, ''), person.name, t.full_name, '')::text
        AS holder_sort_name,
    -- A person's entry on a module outside every subject group they are in. Stays readable and
    -- removable — silently deleting somebody's statement because they left a group loses what
    -- they typed — and is shown as such. Always false for a teacher row, who has no memberships.
    COALESCE(c.person_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM person_subject_group psg
        WHERE psg.person_id = c.person_id AND psg.subject_group_id = msg.subject_group_id
    ), false)::bool AS outside_subject_groups
FROM competence c
JOIN module m ON m.id = c.module_id
LEFT JOIN module_subject_group msg ON msg.module_id = m.id
LEFT JOIN subject_group sg ON sg.id = msg.subject_group_id
LEFT JOIN person ON person.id = c.person_id
LEFT JOIN teacher t ON t.id = c.teacher_id
-- The examination office's short name for a holder who has an account, as in assignment.sql.
LEFT JOIN teacher ts ON ts.mail = person.mail
WHERE (sqlc.narg('module')::uuid IS NULL OR c.module_id = sqlc.narg('module')::uuid)
  AND (sqlc.narg('subject_group')::uuid IS NULL
       OR msg.subject_group_id = sqlc.narg('subject_group')::uuid)
  AND (sqlc.narg('person')::uuid IS NULL OR c.person_id = sqlc.narg('person')::uuid)
  AND (sqlc.narg('teacher')::uuid IS NULL OR c.teacher_id = sqlc.narg('teacher')::uuid)
  AND (
      sqlc.arg('scope')::text = 'all'
      OR (sqlc.arg('scope')::text = 'own'
          AND c.person_id = sqlc.arg(owner_id)::uuid)
      OR (sqlc.arg('scope')::text = 'own_or_scoped'
          AND (c.person_id = sqlc.arg(owner_id)::uuid
               OR m.home_programme_id = ANY (sqlc.arg(programme_ids)::uuid[])
               OR msg.subject_group_id = ANY (sqlc.arg(subject_group_ids)::uuid[])))
  )
ORDER BY m.name, m.id, c.level,
         COALESCE(NULLIF(ts.short_name, ''), NULLIF(t.short_name, ''), person.name, t.full_name),
         c.id;

-- name: CompetenceByID :one
-- One competence, through the same filter.
SELECT
    c.id, c.module_id, c.person_id, c.teacher_id, c.level, c.note, c.created_at, c.updated_at,
    m.name AS module_name,
    EXISTS (SELECT 1 FROM module_offering o WHERE o.module_id = m.id AND o.is_duty) AS compulsory,
    sg.id AS subject_group_id, sg.code AS subject_group_code, sg.name AS subject_group_name,
    COALESCE(person.name, t.full_name, '')::text AS holder_name,
    COALESCE(person.mail, t.mail, '')::citext AS holder_mail,
    COALESCE(NULLIF(ts.short_name, ''), NULLIF(t.short_name, ''), person.name, t.full_name, '')::text
        AS holder_sort_name,
    COALESCE(c.person_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM person_subject_group psg
        WHERE psg.person_id = c.person_id AND psg.subject_group_id = msg.subject_group_id
    ), false)::bool AS outside_subject_groups
FROM competence c
JOIN module m ON m.id = c.module_id
LEFT JOIN module_subject_group msg ON msg.module_id = m.id
LEFT JOIN subject_group sg ON sg.id = msg.subject_group_id
LEFT JOIN person ON person.id = c.person_id
LEFT JOIN teacher t ON t.id = c.teacher_id
LEFT JOIN teacher ts ON ts.mail = person.mail
WHERE c.id = sqlc.arg(id)::uuid
  AND (
      sqlc.arg('scope')::text = 'all'
      OR (sqlc.arg('scope')::text = 'own'
          AND c.person_id = sqlc.arg(owner_id)::uuid)
      OR (sqlc.arg('scope')::text = 'own_or_scoped'
          AND (c.person_id = sqlc.arg(owner_id)::uuid
               OR m.home_programme_id = ANY (sqlc.arg(programme_ids)::uuid[])
               OR msg.subject_group_id = ANY (sqlc.arg(subject_group_ids)::uuid[])))
  );

-- name: CompetenceModuleContext :one
-- What the write rule needs about a module: whether it may be stated for at all, its subject
-- group, and whether this person is a member of that group.
--
-- One statement, so that the decision is taken against one state. A module in no subject group
-- answers with a NULL group and is_member false: nobody can state a competence for it yet, because
-- the rule is "the subjects of your groups" and it is in none.
SELECT
    m.id,
    COALESCE(m.active AND m.retired_at IS NULL, false)::bool AS open,
    msg.subject_group_id,
    EXISTS (
        SELECT 1 FROM person_subject_group psg
        WHERE psg.person_id = sqlc.arg(person_id)::uuid
          AND psg.subject_group_id = msg.subject_group_id
    ) AS is_member
FROM module m
LEFT JOIN module_subject_group msg ON msg.module_id = m.id
WHERE m.id = sqlc.arg(module_id)::uuid;

-- name: UpsertOwnCompetence :one
-- State one's own competence for a module, or change it. person_id and entered_by are both the
-- caller, and the CHECK says they have to be.
INSERT INTO competence (module_id, person_id, level, note, entered_by)
VALUES (sqlc.arg(module_id)::uuid, sqlc.arg(person_id)::uuid, sqlc.arg(level)::text,
        sqlc.arg(note)::text, sqlc.arg(person_id)::uuid)
ON CONFLICT (person_id, module_id) WHERE person_id IS NOT NULL DO UPDATE
SET level = EXCLUDED.level,
    note = EXCLUDED.note,
    updated_at = now()
RETURNING id;

-- name: UpsertTeacherCompetence :one
-- State a teacher's competence on their behalf — only for somebody without an account, which
-- internal/domain makes sure of. entered_by moves to whoever changed it last.
INSERT INTO competence (module_id, teacher_id, level, note, entered_by)
VALUES (sqlc.arg(module_id)::uuid, sqlc.arg(teacher_id)::uuid, sqlc.arg(level)::text,
        sqlc.arg(note)::text, sqlc.arg(entered_by)::uuid)
ON CONFLICT (teacher_id, module_id) WHERE teacher_id IS NOT NULL DO UPDATE
SET level = EXCLUDED.level,
    note = EXCLUDED.note,
    entered_by = EXCLUDED.entered_by,
    updated_at = now()
RETURNING id;

-- name: DeleteOwnCompetence :execrows
-- Withdraw one's own. Ownership in the WHERE clause, as DeleteOwnWish does: "not there" and "not
-- yours" are the same empty result.
DELETE FROM competence WHERE id = sqlc.arg(id)::uuid AND person_id = sqlc.arg(person_id)::uuid;

-- name: DeleteTeacherCompetence :execrows
-- Withdraw a teacher row. Only teacher rows: a person's statement is removed by that person.
DELETE FROM competence WHERE id = sqlc.arg(id)::uuid AND teacher_id IS NOT NULL;

-- name: TeacherCompetenceContext :one
-- The subject group of a teacher row's module, for the guard on removing it. No row for an id that
-- is not a teacher row.
SELECT c.id, msg.subject_group_id
FROM competence c
LEFT JOIN module_subject_group msg ON msg.module_id = c.module_id
WHERE c.id = sqlc.arg(id)::uuid AND c.teacher_id IS NOT NULL;

-- name: OwnCompulsoryCounts :many
-- For each subject group the caller is in: how many of its compulsory modules the caller has
-- stated they can teach. Only the caller's own rows are counted — the answer is their own number.
SELECT
    sg.id, sg.code, sg.name,
    COUNT(c.id)::int AS can_teach_compulsory
FROM person_subject_group psg
JOIN subject_group sg ON sg.id = psg.subject_group_id
LEFT JOIN module_subject_group msg ON msg.subject_group_id = sg.id
LEFT JOIN module m ON m.id = msg.module_id
LEFT JOIN competence c ON c.module_id = m.id
    AND c.person_id = psg.person_id
    AND c.level = 'CAN_TEACH'
    AND m.active AND m.retired_at IS NULL
    AND EXISTS (SELECT 1 FROM module_offering o WHERE o.module_id = m.id AND o.is_duty)
WHERE psg.person_id = sqlc.arg(person_id)::uuid
  AND sg.active
GROUP BY sg.id, sg.code, sg.name
ORDER BY sg.name, sg.id;

-- name: MemberCompulsoryCounts :many
-- For one subject group: every member and how many of its compulsory modules they can teach.
-- Refused in internal/domain unless the caller may read every competence in the group.
SELECT
    person.id, person.name, person.mail::text AS mail,
    COALESCE(t.short_name, '')::text AS sort_name,
    (SELECT COUNT(*) FROM competence c
       JOIN module_subject_group msg ON msg.module_id = c.module_id
       JOIN module m ON m.id = c.module_id
      WHERE c.person_id = person.id
        AND msg.subject_group_id = psg.subject_group_id
        AND c.level = 'CAN_TEACH'
        AND m.active AND m.retired_at IS NULL
        AND EXISTS (SELECT 1 FROM module_offering o WHERE o.module_id = m.id AND o.is_duty)
    )::int AS can_teach_compulsory
FROM person_subject_group psg
JOIN person ON person.id = psg.person_id
LEFT JOIN teacher t ON t.mail = person.mail
WHERE psg.subject_group_id = sqlc.arg(subject_group_id)::uuid
  AND person.active
ORDER BY COALESCE(NULLIF(t.short_name, ''), person.name), person.id;

-- name: ModulesWithoutCompetence :many
-- The active modules of one subject group that nobody has said they can teach — "wenn es brennt,
-- kann es niemand". Refused in internal/domain on the same terms as the query above.
SELECT m.id, m.name,
       EXISTS (SELECT 1 FROM module_offering o WHERE o.module_id = m.id AND o.is_duty) AS compulsory
FROM module_subject_group msg
JOIN module m ON m.id = msg.module_id
WHERE msg.subject_group_id = sqlc.arg(subject_group_id)::uuid
  AND m.active AND m.retired_at IS NULL
  AND NOT EXISTS (
      SELECT 1 FROM competence c WHERE c.module_id = m.id AND c.level = 'CAN_TEACH'
  )
ORDER BY (m.name = ''), m.name, m.id;
