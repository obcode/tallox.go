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
--
-- TWO FILTERS, BECAUSE PUBLISHING OPENS THE FIGURE AND NOT THE NOTE
--
-- Once the dean's office publishes a semester's figures, everybody reads the range. The note stays
-- where it was — "Forschungssemester im Herbst" is not something the faculty agreed to publish —
-- so it runs through a filter of its own, the note_* parameters, which is the same three-branch
-- predicate with the rule from before publication. A note the caller may not read is the empty
-- string, as is the name of whoever corrected the figure.
--
-- THE RANGE
--
-- hours is the upper end and min_hours the lower one, NULL meaning a single figure; see the
-- migration 20261022120000 for why it is one new column and not two. LEAST clamps a lower end
-- left above an upper one that the previous image shortened.

-- name: DesiredTeachingHours :many
SELECT s.code AS semester_code,
       d.person_id, p.name AS person_name, p.mail::text AS person_mail,
       COALESCE(t.short_name, '')::text AS person_sort_name,
       LEAST(COALESCE(d.min_hours, d.hours), d.hours)::float8 AS min_hours,
       d.hours::float8 AS max_hours,
       (CASE WHEN note_visible.yes THEN d.note ELSE '' END)::text AS note,
       (CASE WHEN note_visible.yes THEN COALESCE(c.name, '') ELSE '' END)::text AS corrected_by_name,
       d.created_at, d.updated_at
FROM desired_teaching_hours d
JOIN semester s ON s.id = d.semester_id
JOIN person p ON p.id = d.person_id
LEFT JOIN person c ON c.id = d.corrected_by
-- The examination office's spelling, for the one register of names (domain.PlainName).
LEFT JOIN teacher t ON t.mail = p.mail
CROSS JOIN LATERAL (
    SELECT (
        sqlc.arg('note_scope')::text = 'all'
        OR (sqlc.arg('note_scope')::text IN ('own', 'own_or_scoped')
            AND d.person_id = sqlc.arg(note_owner_id)::uuid)
        OR (sqlc.arg('note_scope')::text = 'own_or_scoped'
            AND EXISTS (SELECT 1 FROM person_subject_group npsg
                         WHERE npsg.person_id = d.person_id
                           AND npsg.subject_group_id = ANY (sqlc.arg(note_subject_group_ids)::uuid[])))
    ) AS yes
) note_visible
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

-- name: DesiredHoursOverview :many
-- The colleagues of the faculty with their subject groups and, where the filter lets it through,
-- the range they stated for one semester. The table the dean asked for, "für alle sichtbar".
--
-- Who is listed does not depend on the figures: every active person who teaches here — holds
-- LECTURER or is a member of a subject group, both of which every colleague can already read. A
-- list of "those who stated a figure" would be the has-stated flag the confidentiality rule
-- forbids, so the figures are a LEFT JOIN carrying the filter, and a figure the caller may not
-- read and one nobody stated come back the same: stated = false, no hours.
SELECT p.id AS person_id, p.name AS person_name, p.mail::text AS person_mail,
       COALESCE(t.short_name, '')::text AS person_sort_name,
       COALESCE((SELECT array_agg(g.id ORDER BY g.code)
                   FROM person_subject_group m JOIN subject_group g ON g.id = m.subject_group_id
                  WHERE m.person_id = p.id), '{}')::uuid[] AS group_ids,
       COALESCE((SELECT array_agg(g.code ORDER BY g.code)
                   FROM person_subject_group m JOIN subject_group g ON g.id = m.subject_group_id
                  WHERE m.person_id = p.id), '{}')::text[] AS group_codes,
       COALESCE((SELECT array_agg(g.name ORDER BY g.code)
                   FROM person_subject_group m JOIN subject_group g ON g.id = m.subject_group_id
                  WHERE m.person_id = p.id), '{}')::text[] AS group_names,
       COALESCE((SELECT array_agg(g.active ORDER BY g.code)
                   FROM person_subject_group m JOIN subject_group g ON g.id = m.subject_group_id
                  WHERE m.person_id = p.id), '{}')::bool[] AS group_active,
       (d.person_id IS NOT NULL)::bool AS stated,
       COALESCE(LEAST(COALESCE(d.min_hours, d.hours), d.hours), 0)::float8 AS min_hours,
       COALESCE(d.hours, 0)::float8 AS max_hours
FROM person p
LEFT JOIN teacher t ON t.mail = p.mail
LEFT JOIN desired_teaching_hours d
       ON d.person_id = p.id
      AND d.semester_id = (SELECT id FROM semester WHERE code = sqlc.arg(semester)::text)
      AND (
          sqlc.arg('scope')::text = 'all'
          OR (sqlc.arg('scope')::text IN ('own', 'own_or_scoped')
              AND d.person_id = sqlc.arg(owner_id)::uuid)
          OR (sqlc.arg('scope')::text = 'own_or_scoped'
              AND EXISTS (SELECT 1 FROM person_subject_group psg
                           WHERE psg.person_id = d.person_id
                             AND psg.subject_group_id = ANY (sqlc.arg(subject_group_ids)::uuid[])))
      )
WHERE p.active
  AND (EXISTS (SELECT 1 FROM person_role r
                WHERE r.person_id = p.id AND r.role = 'LECTURER'
                  AND (r.expires_at IS NULL OR r.expires_at > now()))
       OR EXISTS (SELECT 1 FROM person_subject_group m WHERE m.person_id = p.id))
ORDER BY COALESCE(NULLIF(t.short_name, ''), p.name), p.mail;

-- name: SetDesiredTeachingHours :one
-- State or change a figure. The semester row exists by the time this runs: the store ensures it
-- in the same transaction, because stating a figure is a decision about the semester.
--
-- corrected_by is the dean's office when it corrects a frozen figure and NULL when the person
-- writes their own — the column says whose statement the current figure is.
INSERT INTO desired_teaching_hours (semester_id, person_id, min_hours, hours, note, corrected_by)
VALUES (sqlc.arg(semester_id)::uuid, sqlc.arg(person_id)::uuid,
        (sqlc.arg(min_hours)::float8)::numeric(4, 1), (sqlc.arg(max_hours)::float8)::numeric(4, 1),
        sqlc.arg(note)::text, sqlc.narg(corrected_by)::uuid)
ON CONFLICT (semester_id, person_id) DO UPDATE
   SET min_hours = EXCLUDED.min_hours, hours = EXCLUDED.hours, note = EXCLUDED.note,
       corrected_by = EXCLUDED.corrected_by, updated_at = now()
RETURNING created_at, updated_at;

-- name: ClearDesiredTeachingHours :execrows
-- Take a figure back. No row is "not stated", which is the state this returns to.
DELETE FROM desired_teaching_hours
 WHERE person_id = sqlc.arg(person_id)::uuid
   AND semester_id = (SELECT id FROM semester WHERE code = sqlc.arg(semester)::text);
