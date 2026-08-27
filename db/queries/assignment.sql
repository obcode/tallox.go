-- Assignments: who holds each part of a course instance.
--
-- THE RULE THIS FILE IS MADE OF
--
-- The same rule wish.sql is made of, and the same file-level contract. Every SELECT here carries
-- the same four filter parameters and they are not optional. The visibility rule is a WHERE
-- clause — internal/policy decides it, this is where it runs — and a query written without it is
-- not a slow query, it is a leak.
--
--     @scope = 'all'            no restriction
--     @scope = 'own'            what the caller holds themselves
--     @scope = 'own_or_scoped'  their own, plus the programmes and subject groups they lead
--     anything else             nothing at all
--
-- The unknown value matching no branch is deliberate and mirrors AssignmentFilter.Matches, whose
-- default arm returns false: the safe reading of "I do not know how much you may see" is nothing.
--
-- WHAT IS NOT IN THIS FILE
--
-- **There is no COUNT.** "Zwei der drei Praktika sind schon vergeben" is the confidential fact
-- with the names taken out, and an aggregate that skips the filter is the same failure as a list
-- that skips it, only harder to notice. Whoever wants a number counts the rows they were allowed
-- to read. store.TestEveryAssignmentQueryIsFiltered reads this file and requires the predicate in
-- every SELECT, and refuses a COUNT anywhere in it.
--
-- THE ASSIGNEE IS TWO COLUMNS AND ONE PERSON
--
-- Exactly one of person_id and teacher_id is set. The name and the address are read from whichever
-- it is, so every projection here coalesces the pair rather than making its caller branch. The
-- filter, though, compares person_id alone: somebody with no account holds no row "of their own",
-- which is the rule and not an oversight.

-- name: AssignmentsOfSemester :many
-- The assignments of one semester — or of every semester — filtered, with everything a screen
-- needs to render a row.
--
-- The semester is optional, and there is exactly one caller allowed to leave it out: "what am I
-- teaching, everywhere". That question does not depend on the confidentiality rule at all, so it
-- needs no semester to read a publication date from. Every other caller passes one, because the
-- rule *is* per semester.
--
-- One query for the assignment screen and for a lecturer's own timetable both, because they
-- differ only in what the filter lets through. A second query "for planners" would be a second
-- place for the rule to be forgotten.
--
-- The joins carry the two things the rule is scoped by — the programme of the instance and the
-- subject group of its module. module_subject_group is a LEFT JOIN and has to be: a module nobody
-- has sorted yet is the ordinary state, and its parts are still held by somebody.
SELECT
    a.id, a.instance_part_id, a.person_id, a.teacher_id, a.note,
    a.assigned_by, a.created_at, a.updated_at,
    p.kind AS part_kind, p.position AS part_position, p.teaching_hours AS part_teaching_hours,
    p.serves_sibling_tracks,
    ci.id AS course_instance_id, ci.track, ci.programme_semester,
    sem.code AS semester_code, sem.phase AS semester_phase,
    prog.id AS programme_id, prog.code AS programme_code, prog.title AS programme_title,
    m.id AS module_id, m.name AS module_name,
    -- One assignee out of two columns. COALESCE rather than two nullable pairs the caller has to
    -- pick between: which table the row came from is a fact about accounts, not about teaching,
    -- and every screen that rendered it separately would render it the same way in the end.
    COALESCE(person.name, t.full_name, '')::text AS assignee_name,
    COALESCE(person.mail, t.mail, '')::citext AS assignee_mail,
    COALESCE(NULLIF(ts.short_name, ''), NULLIF(t.short_name, ''), person.name, t.full_name, '')::text
        AS assignee_sort_name
FROM assignment a
JOIN instance_part p ON p.id = a.instance_part_id
JOIN course_instance ci ON ci.id = p.course_instance_id
JOIN semester sem ON sem.id = ci.semester_id
JOIN programme prog ON prog.id = ci.programme_id
JOIN module m ON m.id = ci.module_id
LEFT JOIN module_subject_group msg ON msg.module_id = m.id
LEFT JOIN person ON person.id = a.person_id
LEFT JOIN teacher t ON t.id = a.teacher_id
-- The examination office's short name for an assignee who does have an account, so that a list
-- sorts the same way whether or not somebody happens to be in the catalogue. Same join wish.sql
-- makes, and on the same key: the address is what makes the two rows one person.
LEFT JOIN teacher ts ON ts.mail = person.mail
WHERE (sqlc.narg('semester_id')::uuid IS NULL
       OR ci.semester_id = sqlc.narg('semester_id')::uuid)
  AND (sqlc.narg('programme')::text IS NULL OR prog.code = sqlc.narg('programme')::text)
  AND (sqlc.narg('module')::uuid IS NULL OR m.id = sqlc.narg('module')::uuid)
  AND (sqlc.narg('instance')::uuid IS NULL OR ci.id = sqlc.narg('instance')::uuid)
  AND (sqlc.narg('person')::uuid IS NULL OR a.person_id = sqlc.narg('person')::uuid)
  AND (
      sqlc.arg('scope')::text = 'all'
      OR (sqlc.arg('scope')::text = 'own'
          AND a.person_id = sqlc.arg(assignee_id)::uuid)
      OR (sqlc.arg('scope')::text = 'own_or_scoped'
          AND (a.person_id = sqlc.arg(assignee_id)::uuid
               OR prog.id = ANY (sqlc.arg(programme_ids)::uuid[])
               OR msg.subject_group_id = ANY (sqlc.arg(subject_group_ids)::uuid[])))
  )
-- The semester first, because the list across all of them is grouped by it — and because the code
-- sorts chronologically as text, which is what its format is for. Then the screen's own order:
-- module, cohort, and the parts of a cohort in the order somebody arranged them.
ORDER BY sem.code, m.name, ci.track, p.position, p.kind, a.id;

-- name: AssignmentByID :one
-- One assignment, through the same filter. A detail view that skipped it would be the hole the
-- list does not have — and the realistic shape of that mistake is somebody adding a by-id lookup
-- because "it is only one row".
SELECT
    a.id, a.instance_part_id, a.person_id, a.teacher_id, a.note,
    a.assigned_by, a.created_at, a.updated_at,
    p.kind AS part_kind, p.position AS part_position, p.teaching_hours AS part_teaching_hours,
    p.serves_sibling_tracks,
    ci.id AS course_instance_id, ci.track, ci.programme_semester,
    sem.code AS semester_code, sem.phase AS semester_phase,
    prog.id AS programme_id, prog.code AS programme_code, prog.title AS programme_title,
    m.id AS module_id, m.name AS module_name,
    COALESCE(person.name, t.full_name, '')::text AS assignee_name,
    COALESCE(person.mail, t.mail, '')::citext AS assignee_mail,
    COALESCE(NULLIF(ts.short_name, ''), NULLIF(t.short_name, ''), person.name, t.full_name, '')::text
        AS assignee_sort_name
FROM assignment a
JOIN instance_part p ON p.id = a.instance_part_id
JOIN course_instance ci ON ci.id = p.course_instance_id
JOIN semester sem ON sem.id = ci.semester_id
JOIN programme prog ON prog.id = ci.programme_id
JOIN module m ON m.id = ci.module_id
LEFT JOIN module_subject_group msg ON msg.module_id = m.id
LEFT JOIN person ON person.id = a.person_id
LEFT JOIN teacher t ON t.id = a.teacher_id
LEFT JOIN teacher ts ON ts.mail = person.mail
WHERE a.id = sqlc.arg(id)::uuid
  AND (
      sqlc.arg('scope')::text = 'all'
      OR (sqlc.arg('scope')::text = 'own'
          AND a.person_id = sqlc.arg(assignee_id)::uuid)
      OR (sqlc.arg('scope')::text = 'own_or_scoped'
          AND (a.person_id = sqlc.arg(assignee_id)::uuid
               OR prog.id = ANY (sqlc.arg(programme_ids)::uuid[])
               OR msg.subject_group_id = ANY (sqlc.arg(subject_group_ids)::uuid[])))
  );

-- name: FillInstancePart :one
-- Fill a part that nobody holds. Returns no row if somebody already does.
--
-- ON CONFLICT DO NOTHING rather than an upsert, and that is the compare-and-set half of this
-- file. A caller who names no assignment to replace is saying "I believe this part is free", so
-- the write has to be conditional on that belief still being true — otherwise the second of two
-- people filling the same part at the same moment would silently overwrite the first, and both
-- would leave believing they had decided it.
--
-- Two roles may write this row since 2026-08-27, so this is not a theoretical case.
INSERT INTO assignment (instance_part_id, person_id, teacher_id, note, assigned_by)
VALUES (
    sqlc.arg(instance_part_id)::uuid,
    sqlc.narg('person_id')::uuid,
    sqlc.narg('teacher_id')::uuid,
    sqlc.arg(note)::text,
    sqlc.narg('assigned_by')::uuid
)
ON CONFLICT (instance_part_id) DO NOTHING
RETURNING id;

-- name: ReplaceAssignment :one
-- Hand a part to somebody else. Returns no row if the assignment being replaced is gone.
--
-- Compare-and-set, the same shape AdvanceSemesterPhase takes and for the same reason: @replacing
-- is the assignment the caller was looking at. If somebody else has since cleared or changed it,
-- no rows come back and the caller is told rather than overwriting a decision they never saw.
--
-- Read-then-write in Go would pass its unit test and race here, which is precisely the situation
-- two write-eligible roles produce.
UPDATE assignment
SET person_id = sqlc.narg('person_id')::uuid,
    teacher_id = sqlc.narg('teacher_id')::uuid,
    note = sqlc.arg(note)::text,
    assigned_by = sqlc.narg('assigned_by')::uuid,
    updated_at = now()
WHERE id = sqlc.arg(replacing)::uuid
  AND instance_part_id = sqlc.arg(instance_part_id)::uuid
RETURNING id;

-- name: ClearAssignment :execrows
-- Give a part back. The row count is the answer: nothing cleared means it was not there.
DELETE FROM assignment WHERE id = sqlc.arg(id)::uuid;

-- name: PartWriteContext :one
-- Everything the write rule needs about one part, in one statement.
--
-- Both halves of MayWriteAssignment hang off the instance the part belongs to — the phase from its
-- semester, the two responsibility axes from its programme and its module's subject group — so
-- reading them one at a time would be three round trips and three chances to decide against a
-- state that has since moved. Same reasoning as CourseInstanceByPartID in demand.sql, which this
-- extends rather than reuses because that one predates the subject group.
--
-- Deliberately not filtered: this answers "may I write here", and a caller who may not is told so
-- by the policy rather than by an empty row. What it exposes is the phase and two ids of a part
-- whose id the caller already had.
SELECT
    p.id AS instance_part_id,
    ci.id AS course_instance_id,
    sem.code AS semester_code,
    sem.phase AS semester_phase,
    sem.assignments_published_at,
    prog.id AS programme_id,
    msg.subject_group_id,
    a.id AS assignment_id
FROM instance_part p
JOIN course_instance ci ON ci.id = p.course_instance_id
JOIN semester sem ON sem.id = ci.semester_id
JOIN programme prog ON prog.id = ci.programme_id
JOIN module m ON m.id = ci.module_id
LEFT JOIN module_subject_group msg ON msg.module_id = m.id
LEFT JOIN assignment a ON a.instance_part_id = p.id
WHERE p.id = sqlc.arg(instance_part_id)::uuid;

-- name: AssignmentWriteContextByID :one
-- The same context, reached from an assignment rather than from a part. For clearing one.
SELECT
    p.id AS instance_part_id,
    ci.id AS course_instance_id,
    sem.code AS semester_code,
    sem.phase AS semester_phase,
    sem.assignments_published_at,
    prog.id AS programme_id,
    msg.subject_group_id,
    a.id AS assignment_id
FROM assignment a
JOIN instance_part p ON p.id = a.instance_part_id
JOIN course_instance ci ON ci.id = p.course_instance_id
JOIN semester sem ON sem.id = ci.semester_id
JOIN programme prog ON prog.id = ci.programme_id
JOIN module m ON m.id = ci.module_id
LEFT JOIN module_subject_group msg ON msg.module_id = m.id
WHERE a.id = sqlc.arg(id)::uuid;

-- name: PersonIDByTeacherID :one
-- The account belonging to a teacher, if there is one.
--
-- The canonicalisation lookup: assigning a teacher who holds an account writes the account, so
-- that the same colleague does not sit in this table under two identities and "my assignments"
-- does not find half of them. The address is the link, the same one Teacher.isUser is answered
-- from — citext on both sides, and never a stored column, because a stored one is only as fresh
-- as the last projection.
SELECT person.id
FROM teacher t
JOIN person ON person.mail = t.mail
WHERE t.id = sqlc.arg(teacher_id)::uuid;

-- name: AssignablePersonExists :one
-- Whether this account may be given teaching.
--
-- active is part of the question and not a detail: person.active is how somebody is removed from
-- this system, so an inactive row must not become the answer to "who holds this next semester".
-- It is asked on the way in only — an assignment already on the books survives its holder being
-- deactivated, which is what the RESTRICT on this column is for.
SELECT EXISTS (SELECT 1 FROM person WHERE id = sqlc.arg(id)::uuid AND active);

-- name: AssignableTeacherExists :one
-- Whether this catalogue entry may be given teaching.
--
-- retired_at rather than active. The two mean different things: active is the examination
-- office's own flag and is routinely stale, retired_at is this system noticing that the source
-- stopped mentioning somebody. The faculty knows better than the flag who is teaching next
-- semester, and refusing on it would refuse exactly the lecturers on contract this column pair
-- exists to make assignable.
SELECT EXISTS (SELECT 1 FROM teacher WHERE id = sqlc.arg(id)::uuid AND retired_at IS NULL);
