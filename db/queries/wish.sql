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

-- name: WishesOfSemester :many
-- The wishes of one semester — or of every semester — filtered, with everything a screen needs to
-- render a row.
--
-- The semester is optional, and there is exactly one caller allowed to leave it out: "my own
-- entries, everywhere". That question does not depend on the confidentiality rule at all, so it
-- needs no semester to read a publication date from. Every other caller passes one, because the
-- rule *is* per semester — one may be published and the next not — and a filter built without
-- knowing which would be a filter for the wrong one.
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
    sem.code AS semester_code, sem.phase AS semester_phase,
    -- What the cohort costs the faculty, summed here rather than by joining out the parts: this
    -- query renders one row per wish, and a join to instance_part would turn each of them into
    -- one row per part to carry a single figure. Same formula as domain.CourseInstance's sum over
    -- its Parts — a part belongs to the cohort that holds it, and a shared lecture is counted once
    -- there, which is why no exclusion is needed here.
    COALESCE((SELECT SUM(p.teaching_hours) FROM instance_part p
               WHERE p.course_instance_id = ci.id), 0)::float8 AS teaching_hours,
    prog.id AS programme_id, prog.code AS programme_code, prog.title AS programme_title,
    m.id AS module_id, m.name AS module_name,
    person.mail AS person_mail, person.name AS person_name,
    COALESCE(t.short_name, '')::text AS person_sort_name
FROM wish w
JOIN course_instance ci ON ci.id = w.course_instance_id
JOIN semester sem ON sem.id = ci.semester_id
JOIN programme prog ON prog.id = ci.programme_id
JOIN module m ON m.id = ci.module_id
LEFT JOIN module_subject_group msg ON msg.module_id = m.id
JOIN person ON person.id = w.person_id
LEFT JOIN teacher t ON t.mail = person.mail
WHERE (sqlc.narg('semester_id')::uuid IS NULL
       OR ci.semester_id = sqlc.narg('semester_id')::uuid)
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
-- The semester first, because the list across all of them is grouped by it — and because the
-- code sorts chronologically as text, which is what its format is for.
ORDER BY sem.code, m.name, ci.track, w.priority,
         COALESCE(NULLIF(t.short_name, ''), person.name), w.id;

-- name: WishByID :one
-- One wish, through the same filter. A detail view that skipped it would be the hole the list
-- does not have — and the realistic shape of that mistake is somebody adding a by-id lookup
-- because "it is only one row".
SELECT
    w.id, w.course_instance_id, w.person_id, w.priority, w.note, w.created_at, w.updated_at,
    ci.track, ci.programme_semester,
    sem.code AS semester_code, sem.phase AS semester_phase,
    -- What the cohort costs the faculty, summed here rather than by joining out the parts: this
    -- query renders one row per wish, and a join to instance_part would turn each of them into
    -- one row per part to carry a single figure. Same formula as domain.CourseInstance's sum over
    -- its Parts — a part belongs to the cohort that holds it, and a shared lecture is counted once
    -- there, which is why no exclusion is needed here.
    COALESCE((SELECT SUM(p.teaching_hours) FROM instance_part p
               WHERE p.course_instance_id = ci.id), 0)::float8 AS teaching_hours,
    prog.id AS programme_id, prog.code AS programme_code, prog.title AS programme_title,
    m.id AS module_id, m.name AS module_name,
    person.mail AS person_mail, person.name AS person_name,
    COALESCE(t.short_name, '')::text AS person_sort_name
FROM wish w
JOIN course_instance ci ON ci.id = w.course_instance_id
JOIN semester sem ON sem.id = ci.semester_id
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
