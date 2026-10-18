-- Desired teaching hours: what somebody would like to teach in a semester.
--
-- Every read carries the filter parameters, as wish.sql and competence.sql do, and
-- store.TestEveryDesiredHoursReadIsFiltered reads this file and insists on it:
--
--     @scope = 'all'            no restriction
--     @scope = 'own'            the caller's own statements
--     @scope = 'own_or_scoped'  their own, plus the members of the subject groups they lead
--     anything else             nothing at all
--
-- Membership is read at query time, like a wish's subject group: moving somebody to another group
-- changes who reads their figure, which is what "the lead of their group reads it" means.

-- name: DesiredTeachingHours :many
SELECT s.code AS semester_code,
       d.person_id, p.name AS person_name, p.mail::text AS person_mail,
       COALESCE(t.short_name, '')::text AS person_sort_name,
       d.hours::float8 AS hours, d.note, d.created_at, d.updated_at
FROM desired_teaching_hours d
JOIN semester s ON s.id = d.semester_id
JOIN person p ON p.id = d.person_id
-- The examination office's spelling, for the one register of names (domain.PlainName).
LEFT JOIN teacher t ON t.mail = p.mail
WHERE (sqlc.narg('semester')::text IS NULL OR s.code = sqlc.narg('semester')::text)
  AND (sqlc.narg('person')::uuid IS NULL OR d.person_id = sqlc.narg('person')::uuid)
  AND (
      sqlc.arg('scope')::text = 'all'
      OR (sqlc.arg('scope')::text IN ('own', 'own_or_scoped')
          AND d.person_id = sqlc.arg(owner_id)::uuid)
      OR (sqlc.arg('scope')::text = 'own_or_scoped'
          AND EXISTS (SELECT 1 FROM person_subject_group psg
                       WHERE psg.person_id = d.person_id
                         AND psg.subject_group_id = ANY (sqlc.arg(subject_group_ids)::uuid[])))
  )
ORDER BY s.code, COALESCE(NULLIF(t.short_name, ''), p.name);

-- name: SetDesiredTeachingHours :one
-- State or change one's own figure. The semester row exists by the time this runs: the store
-- ensures it in the same transaction, because stating a figure is a decision about the semester.
INSERT INTO desired_teaching_hours (semester_id, person_id, hours, note)
VALUES (sqlc.arg(semester_id)::uuid, sqlc.arg(person_id)::uuid,
        (sqlc.arg(hours)::float8)::numeric(4, 1), sqlc.arg(note)::text)
ON CONFLICT (semester_id, person_id) DO UPDATE
   SET hours = EXCLUDED.hours, note = EXCLUDED.note, updated_at = now()
RETURNING created_at, updated_at, hours::float8 AS hours;

-- name: ClearDesiredTeachingHours :execrows
-- Take one's own figure back. No row is "not stated", which is the state this returns to.
DELETE FROM desired_teaching_hours
 WHERE person_id = sqlc.arg(person_id)::uuid
   AND semester_id = (SELECT id FROM semester WHERE code = sqlc.arg(semester)::text);
