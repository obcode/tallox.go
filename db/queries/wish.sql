-- Wishes: one person's interest in one course instance.
--
-- THE RULE THIS FILE IS MADE OF
--
-- Every SELECT here carries the same four filter parameters, and they are not optional. The
-- visibility rule is a WHERE clause — internal/policy has it in two forms precisely so that this
-- one can run in the database — and a query written without it is not a slow query, it is a leak.
--
--     @scope = 'all'            no restriction
--     @scope = 'own'            the caller's own entries
--     @scope = 'own_or_scoped'  their own, plus the programmes and subject groups they lead
--     anything else             nothing at all
--
-- The last line is the important one: an unrecognised scope string matches no branch and
-- therefore no row, which is the same fail-closed reading WishFilter.Matches takes in its
-- default arm.
--
-- WHAT IS NOT IN THIS FILE
--
-- **There is no COUNT.** Not an oversight and not a gap to be filled: "3 Kolleg:innen haben
-- bereits Interesse" is the confidential fact with the names taken out, and an aggregate that
-- skipped the filter would be the same failure as a list that skipped it, only harder to notice.
-- Anybody who wants a number counts the rows they were allowed to see. There is nothing here that
-- could answer differently.
--
-- store.TestEveryWishQueryIsFiltered reads this file and requires the predicate in every SELECT,
-- so a new query cannot quietly be written without it.

-- name: WishesInSemester :many
-- The wishes of one semester, filtered, with everything a screen needs to render a row.
--
-- One query for the wish screen and for the planning screens both, because they differ only in
-- what the filter lets through — which is the property the whole design rests on. A second query
-- "for planners" would be a second place for the rule to be forgotten.
--
-- The joins carry the two things the rule is scoped by — the programme of the instance and the
-- subject group of its module. module_subject_group is a LEFT JOIN and has to be: a module nobody has sorted into a subject group yet is the ordinary state until the
-- faculty has worked through its catalogue, and its wishes still belong to somebody.
SELECT
    w.id, w.course_instance_id, w.person_id, w.priority, w.note, w.created_at, w.updated_at,
    ci.track, ci.programme_semester,
    prog.id AS programme_id, prog.code AS programme_code, prog.title AS programme_title,
    m.id AS module_id, m.name AS module_name,
    person.mail AS person_mail, person.name AS person_name,
    COALESCE(t.short_name, '')::text AS person_sort_name
FROM wish w
JOIN course_instance ci ON ci.id = w.course_instance_id
JOIN programme prog ON prog.id = ci.programme_id
JOIN module m ON m.id = ci.module_id
LEFT JOIN module_subject_group msg ON msg.module_id = m.id
JOIN person ON person.id = w.person_id
LEFT JOIN teacher t ON t.mail = person.mail
WHERE ci.semester_id = sqlc.arg(semester_id)::uuid
  AND (sqlc.narg('programme')::text IS NULL OR prog.code = sqlc.narg('programme')::text)
  AND (sqlc.narg('module')::uuid IS NULL OR m.id = sqlc.narg('module')::uuid)
  AND (sqlc.narg('instance')::uuid IS NULL OR ci.id = sqlc.narg('instance')::uuid)
  AND (sqlc.narg('person')::uuid IS NULL OR w.person_id = sqlc.narg('person')::uuid)
  AND (
      sqlc.arg('scope')::text = 'all'
      OR (sqlc.arg('scope')::text = 'own'
          AND w.person_id = sqlc.arg(owner_id)::uuid)
      OR (sqlc.arg('scope')::text = 'own_or_scoped'
          AND (w.person_id = sqlc.arg(owner_id)::uuid
               OR prog.id = ANY (sqlc.arg(programme_ids)::uuid[])
               OR msg.subject_group_id = ANY (sqlc.arg(subject_group_ids)::uuid[])))
  )
ORDER BY m.name, ci.track, w.priority,
         COALESCE(NULLIF(t.short_name, ''), person.name), w.id;

-- name: WishByID :one
-- One wish, through the same filter. A detail view that skipped it would be the hole the list
-- does not have — and the realistic shape of that mistake is somebody adding a by-id lookup
-- because "it is only one row".
SELECT
    w.id, w.course_instance_id, w.person_id, w.priority, w.note, w.created_at, w.updated_at,
    ci.track, ci.programme_semester,
    prog.id AS programme_id, prog.code AS programme_code, prog.title AS programme_title,
    m.id AS module_id, m.name AS module_name,
    person.mail AS person_mail, person.name AS person_name,
    COALESCE(t.short_name, '')::text AS person_sort_name
FROM wish w
JOIN course_instance ci ON ci.id = w.course_instance_id
JOIN programme prog ON prog.id = ci.programme_id
JOIN module m ON m.id = ci.module_id
LEFT JOIN module_subject_group msg ON msg.module_id = m.id
JOIN person ON person.id = w.person_id
LEFT JOIN teacher t ON t.mail = person.mail
WHERE w.id = sqlc.arg(id)::uuid
  AND (
      sqlc.arg('scope')::text = 'all'
      OR (sqlc.arg('scope')::text = 'own'
          AND w.person_id = sqlc.arg(owner_id)::uuid)
      OR (sqlc.arg('scope')::text = 'own_or_scoped'
          AND (w.person_id = sqlc.arg(owner_id)::uuid
               OR prog.id = ANY (sqlc.arg(programme_ids)::uuid[])
               OR msg.subject_group_id = ANY (sqlc.arg(subject_group_ids)::uuid[])))
  );

-- name: UpsertWish :one
-- Register interest, or change your mind about something you already registered.
--
-- An upsert rather than an insert that can fail: registering twice for the same instance is not a
-- second wish and not an error, it is a correction. The unique constraint still exists — it is
-- what makes this one row per person per instance — but the ordinary path never trips it.
--
-- Only ever the caller's own row. person_id comes from the actor and never from the request; see
-- the header of migration 15.
INSERT INTO wish (course_instance_id, person_id, priority, note)
VALUES ($1, $2, $3, $4)
ON CONFLICT (course_instance_id, person_id) DO UPDATE
SET priority = EXCLUDED.priority,
    note = EXCLUDED.note,
    updated_at = now()
RETURNING id, course_instance_id, person_id, priority, note, created_at, updated_at;

-- name: DeleteOwnWish :execrows
-- Withdraw one's own wish.
--
-- Ownership is in the WHERE clause rather than in a read-then-write in Go, which collapses three
-- things into one, exactly as RevokeTokenOfOwner does: the check cannot race with a concurrent
-- change, "no such wish" and "not your wish" become the same empty result — the difference is
-- not information the caller is entitled to — and there is no window in which an id has been
-- confirmed to exist before the write is refused.
DELETE FROM wish WHERE id = $1 AND person_id = $2;

-- name: SemesterOfCourseInstance :one
-- Which semester an instance belongs to, and whether it is still there.
--
-- Needed before a write: the phase that decides whether wishes may be entered is the *semester's*
-- phase, and the instance is all the caller names. One statement rather than two round trips, and
-- an empty result is the answer to both questions at once — an instance that has been withdrawn
-- has no semester to ask about.
SELECT s.id, s.code, s.phase, s.wishes_published_at
FROM course_instance ci
JOIN semester s ON s.id = ci.semester_id
WHERE ci.id = $1;
