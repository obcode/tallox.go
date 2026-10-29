-- Confirming one's assignments, and reading whether somebody has (migration of 2026-10-29).
--
-- The state is derived here and nowhere else: a confirmation row whose fingerprint equals the
-- person's current one is confirmed, a row that differs is out of date, no row is open. Both sides
-- come from the view assignment_fingerprint, so the comparison cannot drift from the write.
--
-- No visibility filter in this file, on purpose: these queries take the person ids to answer for,
-- and the domain derives those ids from the assignments the caller may already read. A person
-- whose assignments are invisible to the caller is never asked about.

-- name: ConfirmAssignments :one
-- Record "passt so" over what the person holds right now. No row back means they hold nothing in
-- this semester — there is nothing to confirm, and an empty confirmation would read as "checked"
-- on the day somebody is first given something.
INSERT INTO assignment_confirmation (semester_id, person_id, fingerprint)
SELECT f.semester_id, f.person_id, f.fingerprint
  FROM assignment_fingerprint f
 WHERE f.semester_id = sqlc.arg(semester_id)::uuid AND f.person_id = sqlc.arg(person_id)::uuid
ON CONFLICT (semester_id, person_id) DO UPDATE
   SET fingerprint = EXCLUDED.fingerprint, confirmed_at = now()
RETURNING confirmed_at;

-- name: AssignmentConfirmationStates :many
-- One row per person asked about: when they last confirmed (or NULL), and whether that still
-- matches what they hold. A person with no confirmation row comes back with both NULL/false.
SELECT p.id::uuid AS person_id,
       c.confirmed_at,
       COALESCE(c.fingerprint = f.fingerprint, false)::boolean AS current
  FROM unnest(sqlc.arg(person_ids)::uuid[]) AS p (id)
  LEFT JOIN assignment_confirmation c
         ON c.semester_id = sqlc.arg(semester_id)::uuid AND c.person_id = p.id
  LEFT JOIN assignment_fingerprint f
         ON f.semester_id = sqlc.arg(semester_id)::uuid AND f.person_id = p.id
 ORDER BY p.id;
