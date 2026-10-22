-- Assigned load: how many hours a week each person already holds in a semester, by the subject
-- group of the modules they hold them in.
--
-- THE EXCEPTION THIS FILE IS
--
-- assignment.sql has no COUNT and no SUM, because before publication a sum over assignments the
-- caller may not read one by one is the same leak as the rows. This file is the one place that
-- sums them anyway, and it may because its filter is a different one: not "which assignments may
-- you read" but "whose load may you read" — the person, the leads of the subject groups the person
-- is a member of, the dean's office, and everybody once the assignments are published
-- (policy.AssignedLoadVisibility, where the reason is written down). A lead learns how full a
-- member of their group is, and in which groups; never which modules.
--
-- Every read carries the person filter, and store.TestEveryAssignedLoadQueryIsFiltered reads this
-- file and insists on it:
--
--     @scope = 'all'            no restriction
--     @scope = 'own'            the caller's own load
--     @scope = 'own_or_scoped'  their own, plus the members of the subject groups they lead
--     anything else             nothing at all
--
-- WHAT IS COUNTED
--
-- The hours of the parts somebody with an account holds (a.person_id). A part with taught_by
-- cannot be held, and a lecture serving both cohorts is one part, so each hour counts once. A
-- teacher without an account is in no subject group and has no row here.

-- name: AssignedLoadOfSemester :many
-- One row per person and subject group of the module — NULL for a module nobody has sorted into a
-- group yet, which is ordinary and must not make the load disappear. A module has at most one
-- group (module_subject_group.module_id is the key), so the rows of a person add up to their total.
SELECT a.person_id::uuid AS person_id,
       per.name AS person_name, per.mail::text AS person_mail,
       COALESCE(ts.short_name, '')::text AS person_sort_name,
       msg.subject_group_id AS subject_group_id,
       COALESCE(g.code, '')::text AS subject_group_code,
       COALESCE(g.name, '')::text AS subject_group_name,
       COALESCE(g.active, true)::bool AS subject_group_active,
       SUM(p.teaching_hours)::float8 AS hours
FROM assignment a
JOIN instance_part p ON p.id = a.instance_part_id
JOIN course_instance ci ON ci.id = p.course_instance_id
JOIN semester sem ON sem.id = ci.semester_id
JOIN person per ON per.id = a.person_id
-- The examination office's spelling, for the one register of names (domain.PlainName).
LEFT JOIN teacher ts ON ts.mail = per.mail
LEFT JOIN module_subject_group msg ON msg.module_id = ci.module_id
LEFT JOIN subject_group g ON g.id = msg.subject_group_id
WHERE sem.code = sqlc.arg(semester)::text
  AND a.person_id IS NOT NULL
  AND (sqlc.narg('person')::uuid IS NULL OR a.person_id = sqlc.narg('person')::uuid)
  AND (
      sqlc.arg('scope')::text = 'all'
      OR (sqlc.arg('scope')::text IN ('own', 'own_or_scoped')
          AND a.person_id = sqlc.arg(owner_id)::uuid)
      OR (sqlc.arg('scope')::text = 'own_or_scoped'
          AND EXISTS (SELECT 1 FROM person_subject_group psg
                       WHERE psg.person_id = a.person_id
                         AND psg.subject_group_id = ANY (sqlc.arg(subject_group_ids)::uuid[])))
  )
GROUP BY a.person_id, per.name, per.mail, ts.short_name, msg.subject_group_id, g.code, g.name,
         g.active
ORDER BY COALESCE(NULLIF(ts.short_name, ''), per.name), a.person_id, g.code NULLS LAST;
