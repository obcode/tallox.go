-- The demand: which course instances a study programme needs in a semester.
--
-- Same shape as the catalogue file next to it — a list costs a fixed number of statements and is
-- stitched in Go — and for the same measured reason. A semester's demand for one programme is
-- tens of instances with a handful of parts each, so "load the instances, load their parts, join
-- them here" is two statements where a field resolver per relation would be one per row.
--
-- Two things in here are transactions rather than statements, and both are in this file's Go
-- neighbour: declaring an instance writes the instance and the parts made from the module's
-- split, and copying a semester's demand writes all of it or none of it. A half-copied semester
-- is worse than an uncopied one, because the person looking at it cannot tell which half.

-- name: ListCourseInstances :many
-- The demand of a semester, optionally narrowed to one programme or one module.
--
-- The semester is addressed by its code rather than its id, the way it is everywhere outside
-- this package: the code is what a URL and a colleague's script carry.
--
-- Ordered the way the demand is read — by cohort year, then by module, then by parallel cohort,
-- so that IF3A and IF3B sit next to each other and a programme's list walks up the semesters.
-- The module's name is ordered by without being selected: it is attached from the catalogue
-- afterwards, and selecting it here would be a second copy of the same string that could drift
-- from the one the interface shows.
SELECT ci.id, ci.semester_id, ci.module_id, ci.programme_id, ci.track, ci.programme_semester,
       ci.created_at, ci.updated_at,
       s.code AS semester_code, s.phase AS semester_phase,
       p.code AS programme_code, p.title AS programme_title, p.active AS programme_active
FROM course_instance ci
JOIN semester s ON s.id = ci.semester_id
JOIN programme p ON p.id = ci.programme_id
JOIN module m ON m.id = ci.module_id
WHERE s.code = sqlc.arg('semester')::text
  AND (sqlc.narg('programme')::text IS NULL OR p.code = sqlc.narg('programme')::text)
  AND (sqlc.narg('module')::uuid IS NULL OR ci.module_id = sqlc.narg('module')::uuid)
ORDER BY ci.programme_semester NULLS LAST, (m.name = ''), m.name, ci.track, ci.id;

-- name: CourseInstanceByID :one
SELECT ci.id, ci.semester_id, ci.module_id, ci.programme_id, ci.track, ci.programme_semester,
       ci.created_at, ci.updated_at,
       s.code AS semester_code, s.phase AS semester_phase,
       p.code AS programme_code, p.title AS programme_title, p.active AS programme_active
FROM course_instance ci
JOIN semester s ON s.id = ci.semester_id
JOIN programme p ON p.id = ci.programme_id
WHERE ci.id = $1;

-- name: CourseInstanceByPartID :one
-- The instance a part belongs to, for the permission check on a part-level write.
--
-- One statement rather than "read the part, then read its instance": the thing being decided is
-- whether this actor may write this programme's demand in this phase, and both halves of that
-- come off the instance.
SELECT ci.id, ci.semester_id, ci.module_id, ci.programme_id, ci.track, ci.programme_semester,
       ci.created_at, ci.updated_at,
       s.code AS semester_code, s.phase AS semester_phase,
       p.code AS programme_code, p.title AS programme_title, p.active AS programme_active
FROM instance_part ip
JOIN course_instance ci ON ci.id = ip.course_instance_id
JOIN semester s ON s.id = ci.semester_id
JOIN programme p ON p.id = ci.programme_id
WHERE ip.id = $1;

-- name: InstancePartsFor :many
-- The parts of a set of instances, in one statement.
SELECT id, course_instance_id, kind, position, teaching_hours, serves_sibling_tracks
FROM instance_part
WHERE course_instance_id = ANY (sqlc.arg(instance_ids)::uuid[])
ORDER BY course_instance_id, position, id;

-- name: BorrowedInstancePartsFor :many
-- The parts of *sibling* cohorts that are held for this one as well.
--
-- The other half of instance_part.serves_sibling_tracks. A lecture given once for IF3A and IF3B
-- is one row, owned by one of them; the other has to render it, or its screen shows a cohort
-- with laboratories and no lecture and looks like a planning mistake.
--
-- Siblings are the instances of the same module, in the same semester, for the same programme —
-- which is the identity minus the parallel cohort, and exactly what course_instance_cohort_idx
-- covers. Sharing reaches no further than that on purpose: a part shared between two programmes
-- would belong to both, and the import/export figure would lose its denominator.
SELECT me.id AS for_instance_id,
       sib.track AS from_track,
       p.id, p.course_instance_id, p.kind, p.position, p.teaching_hours, p.serves_sibling_tracks
FROM course_instance me
JOIN course_instance sib
  ON sib.semester_id = me.semester_id
 AND sib.module_id = me.module_id
 AND sib.programme_id = me.programme_id
 AND sib.id <> me.id
JOIN instance_part p ON p.course_instance_id = sib.id AND p.serves_sibling_tracks
WHERE me.id = ANY (sqlc.arg(instance_ids)::uuid[])
ORDER BY me.id, sib.track, p.position, p.id;

-- name: SiblingInstanceIDs :many
-- The other parallel cohorts of one instance's module, in the same semester and programme.
SELECT sib.id
FROM course_instance me
JOIN course_instance sib
  ON sib.semester_id = me.semester_id
 AND sib.module_id = me.module_id
 AND sib.programme_id = me.programme_id
 AND sib.id <> me.id
WHERE me.id = $1
ORDER BY sib.track, sib.id;

-- name: SeedProgrammeSemester :one
-- Which cohort year to propose for a new instance, from the regulations of its programme.
--
-- The earliest the module may be taken in, across every version of that programme's regulations.
-- 23 of 1076 module/programme pairs disagree across versions, and the earliest is the one that
-- makes the instance appear where somebody looking for it expects it — the same choice the
-- catalogue projection makes for the same conflict.
--
-- Zero means "the regulations do not say", which the CHECK on the column (1 to 12) makes an
-- unambiguous answer. COALESCE with the cast on the outside: without it sqlc types the aggregate
-- as an interface, and MIN over no rows is NULL, which is exactly the case this seeds for.
SELECT COALESCE(MIN(o.min_programme_semester), 0)::integer AS programme_semester
FROM module_offering o
JOIN spo s ON s.id = o.spo_id
WHERE o.module_id = $1
  AND s.programme_id = $2;

-- name: InsertCourseInstance :one
-- Declare an instance. The unique key is the identity — semester, module, programme, cohort.
--
-- A conflict is reported rather than absorbed: somebody declaring IF3A twice has made a mistake
-- worth naming, and the demand is not confidential, so naming it leaks nothing. The copy path is
-- the one place that wants a conflict to be a no-op, and it uses the statement below.
INSERT INTO course_instance (semester_id, module_id, programme_id, track, programme_semester,
                             created_by)
VALUES ($1, $2, $3, $4, sqlc.narg('programme_semester'), sqlc.narg('created_by'))
RETURNING id;

-- name: InsertCourseInstanceIfAbsent :one
-- The copy path's insert: an instance that is already declared is left exactly as it is.
--
-- DO NOTHING with a RETURNING of the id, so "created" and "was already there" are distinguished
-- by whether a row comes back rather than by a count read afterwards. DO UPDATE would overwrite
-- a cohort year somebody has since corrected in the target semester — a copy must never silently
-- undo work in the semester it is copying into.
INSERT INTO course_instance (semester_id, module_id, programme_id, track, programme_semester,
                             created_by)
VALUES ($1, $2, $3, $4, sqlc.narg('programme_semester'), sqlc.narg('created_by'))
ON CONFLICT (semester_id, module_id, programme_id, track) DO NOTHING
RETURNING id;

-- name: UpdateCourseInstance :exec
-- The two things about an instance that are editable: which cohort it is, and which year.
--
-- The module, the programme and the semester are not. Changing one of those is not an edit of
-- this instance, it is a different instance — and its parts, and later its wishes, belong to the
-- one that was declared.
UPDATE course_instance
SET track = $2,
    programme_semester = sqlc.narg('programme_semester'),
    updated_at = now()
WHERE id = $1;

-- name: DeleteCourseInstance :execrows
-- Withdraw an instance. Its parts go with it; anything else pointing at it stops it.
--
-- instance_part is ON DELETE CASCADE, which is what makes this one statement. Everything that
-- will later hang off a part — a wish, an assignment — is a foreign key that refuses, and the Go
-- side maps that refusal to one opaque answer without a count. A message naming what stopped the
-- withdrawal would be a wish oracle: "this instance has 3 wishes" is the confidential fact, with
-- the names removed and nothing else.
DELETE FROM course_instance WHERE id = $1;

-- name: InsertInstancePart :one
INSERT INTO instance_part (course_instance_id, kind, position, teaching_hours,
                           serves_sibling_tracks)
VALUES ($1, $2, $3, sqlc.narg('teaching_hours'), $4)
RETURNING id;

-- name: NextInstancePartPosition :one
-- Where a newly added part goes: after the last one.
--
-- COALESCE with the cast outside, for the same reason as above — MAX over no rows is NULL, and
-- an instance with no parts yet is the ordinary state of one whose module has an empty split.
SELECT COALESCE(MAX(position) + 1, 0)::integer AS position
FROM instance_part
WHERE course_instance_id = $1;

-- name: UpdateInstancePart :exec
UPDATE instance_part
SET kind = $2,
    teaching_hours = sqlc.narg('teaching_hours'),
    updated_at = now()
WHERE id = $1;

-- name: SetInstancePartShared :exec
UPDATE instance_part
SET serves_sibling_tracks = $2,
    updated_at = now()
WHERE id = $1;

-- name: DeleteInstancePart :execrows
DELETE FROM instance_part WHERE id = $1;

-- name: DeleteInstancePartsOfKind :execrows
-- The other half of merging a part across the parallel cohorts: the siblings' own copies of it
-- go away, because from now on this one is held for them too.
--
-- By kind rather than by id, because that is what "the lecture" means to the person doing it: a
-- cohort has one lecture, and after the merge the faculty holds one lecture for both. A sibling
-- part that something already hangs off refuses to go, and the merge fails as a whole.
DELETE FROM instance_part
WHERE course_instance_id = ANY (sqlc.arg(instance_ids)::uuid[])
  AND kind = sqlc.arg('kind')::text;

-- name: InstancePartsOfKindExist :one
-- Whether a cohort already has a part of this kind, so that undoing a merge does not give it two.
SELECT EXISTS (
    SELECT 1 FROM instance_part
    WHERE course_instance_id = $1 AND kind = $2
)::boolean AS exists;

-- name: CourseInstancesOfProgramme :many
-- What one programme declared in one semester, by id — the input of a copy and of a plan.
--
-- Addressed by semester id rather than by code, unlike the list above, because both callers
-- already hold the semester as a row: looking it up again by code inside the transaction would be
-- a second chance to disagree about which semester is meant.
SELECT ci.id, ci.module_id, ci.track, ci.programme_semester
FROM course_instance ci
WHERE ci.semester_id = $1
  AND ci.programme_id = $2
ORDER BY ci.module_id, ci.track;
