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
       p.code AS programme_code, p.title AS programme_title, p.active AS programme_active,
       ci.covered_by_instance_id, ci.covered_requested_at, ci.covered_accepted_at,
       cov.code AS covered_by_programme_code, cov.title AS covered_by_programme_title,
       host.track AS covered_by_track, host.programme_semester AS covered_by_programme_semester
FROM course_instance ci
JOIN semester s ON s.id = ci.semester_id
JOIN programme p ON p.id = ci.programme_id
LEFT JOIN course_instance host ON host.id = ci.covered_by_instance_id
LEFT JOIN programme cov ON cov.id = ci.covered_by_programme_id
JOIN module m ON m.id = ci.module_id
WHERE s.code = sqlc.arg('semester')::text
  AND (sqlc.narg('programme')::text IS NULL OR p.code = sqlc.narg('programme')::text)
  AND (sqlc.narg('module')::uuid IS NULL OR ci.module_id = sqlc.narg('module')::uuid)
ORDER BY ci.programme_semester NULLS LAST, (m.name = ''), m.name, ci.track, ci.id;

-- name: CourseInstanceByID :one
SELECT ci.id, ci.semester_id, ci.module_id, ci.programme_id, ci.track, ci.programme_semester,
       ci.created_at, ci.updated_at,
       s.code AS semester_code, s.phase AS semester_phase,
       p.code AS programme_code, p.title AS programme_title, p.active AS programme_active,
       ci.covered_by_instance_id, ci.covered_requested_at, ci.covered_accepted_at,
       cov.code AS covered_by_programme_code, cov.title AS covered_by_programme_title,
       host.track AS covered_by_track, host.programme_semester AS covered_by_programme_semester
FROM course_instance ci
JOIN semester s ON s.id = ci.semester_id
JOIN programme p ON p.id = ci.programme_id
LEFT JOIN course_instance host ON host.id = ci.covered_by_instance_id
LEFT JOIN programme cov ON cov.id = ci.covered_by_programme_id
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
       p.code AS programme_code, p.title AS programme_title, p.active AS programme_active,
       ci.covered_by_instance_id, ci.covered_requested_at, ci.covered_accepted_at,
       cov.code AS covered_by_programme_code, cov.title AS covered_by_programme_title,
       host.track AS covered_by_track, host.programme_semester AS covered_by_programme_semester
FROM instance_part ip
JOIN course_instance ci ON ci.id = ip.course_instance_id
JOIN semester s ON s.id = ci.semester_id
JOIN programme p ON p.id = ci.programme_id
LEFT JOIN course_instance host ON host.id = ci.covered_by_instance_id
LEFT JOIN programme cov ON cov.id = ci.covered_by_programme_id
WHERE ip.id = $1;

-- name: InstancePartsFor :many
-- The parts of a set of instances, in one statement.
SELECT id, course_instance_id, kind, position, teaching_hours, serves_sibling_tracks
FROM instance_part
WHERE course_instance_id = ANY (sqlc.arg(instance_ids)::uuid[])
ORDER BY course_instance_id, position, id;

-- name: BorrowedInstancePartsFor :many
-- The parts another cohort holds for this one — within the programme, and across programmes.
--
-- TWO KINDS OF BORROWING, ONE RENDERING
--
--   * A sibling cohort's shared lecture. IF3A holds it, IF3B borrows it. Bounded to the cohorts of
--     one module in one programme, exactly as it always was: from_programme_code is NULL and the
--     sentence the interface says names a cohort.
--   * The whole of another programme's instance, where this one's demand is covered by it. The
--     guest holds nothing at all and borrows everything, and from_programme_code names who holds
--     it.
--
-- Neither kind moves a row. Every instance_part still belongs to exactly one instance and one
-- programme; what this query answers is "what does this cohort attend", which is a different
-- question from "what does this cohort cost" — and the second one is still SUM over the rows as
-- stored. That is the property migration 8 protects, and this query does not touch it.
--
-- WHY THE SECOND BRANCH REACHES ONE STEP FURTHER
--
-- A guest borrows its host's own parts *and* the parts the host itself borrows from its siblings.
-- Without that, GS covered by DE-B — where DE-A holds the joint lecture — would render
-- laboratories and no lecture, which is the exact screen this query exists to prevent.
--
-- Only accepted coverage borrows. A request that nobody has answered changes nothing about what
-- is held, and rendering it as though it had would show teaching that does not exist yet.
SELECT me.id AS for_instance_id,
       sib.track AS from_track,
       NULL::text AS from_programme_code,
       p.id, p.course_instance_id, p.kind, p.position, p.teaching_hours, p.serves_sibling_tracks
FROM course_instance me
JOIN course_instance sib
  ON sib.semester_id = me.semester_id
 AND sib.module_id = me.module_id
 AND sib.programme_id = me.programme_id
 AND sib.id <> me.id
JOIN instance_part p ON p.course_instance_id = sib.id AND p.serves_sibling_tracks
WHERE me.id = ANY (sqlc.arg(instance_ids)::uuid[])

UNION ALL

-- The host's own parts.
SELECT me.id AS for_instance_id,
       host.track AS from_track,
       hp.code AS from_programme_code,
       p.id, p.course_instance_id, p.kind, p.position, p.teaching_hours, p.serves_sibling_tracks
FROM course_instance me
JOIN course_instance host ON host.id = me.covered_by_instance_id
JOIN programme hp ON hp.id = host.programme_id
JOIN instance_part p ON p.course_instance_id = host.id
WHERE me.id = ANY (sqlc.arg(instance_ids)::uuid[])
  AND me.covered_accepted_at IS NOT NULL

UNION ALL

-- And what the host in turn borrows from its own sibling cohorts.
SELECT me.id AS for_instance_id,
       hsib.track AS from_track,
       hp.code AS from_programme_code,
       p.id, p.course_instance_id, p.kind, p.position, p.teaching_hours, p.serves_sibling_tracks
FROM course_instance me
JOIN course_instance host ON host.id = me.covered_by_instance_id
JOIN course_instance hsib
  ON hsib.semester_id = host.semester_id
 AND hsib.module_id = host.module_id
 AND hsib.programme_id = host.programme_id
 AND hsib.id <> host.id
JOIN programme hp ON hp.id = hsib.programme_id
JOIN instance_part p ON p.course_instance_id = hsib.id AND p.serves_sibling_tracks
WHERE me.id = ANY (sqlc.arg(instance_ids)::uuid[])
  AND me.covered_accepted_at IS NOT NULL

ORDER BY for_instance_id, from_programme_code NULLS FIRST, from_track, position, id;

-- name: CoveringInstancesFor :many
-- The other programmes' demands these instances meet — the host's side of the link.
--
-- A programme lead who has agreed to hold one event for two programmes has to see the second one
-- on their own screen, or the agreement exists only in the other programme's table.
--
-- Pending and accepted both, told apart by covered_accepted_at rather than filtered here: this is
-- the side where a request is answered, so a query that hid the unanswered ones would hide the
-- only thing that needs doing.
SELECT g.covered_by_instance_id AS for_instance_id,
       g.id, g.track, g.programme_semester,
       g.covered_requested_at, g.covered_accepted_at,
       p.id AS programme_id, p.code AS programme_code, p.title AS programme_title
FROM course_instance g
JOIN programme p ON p.id = g.programme_id
WHERE g.covered_by_instance_id = ANY (sqlc.arg(instance_ids)::uuid[])
ORDER BY g.covered_by_instance_id, p.code, g.track, g.id;

-- name: CoverageContextByInstanceID :one
-- Everything the two-sided rule needs about one link, in one statement: the guest's programme and
-- phase, the host's programme, and whether it has been agreed to.
--
-- Deliberately unfiltered and reached by id, like PartWriteContext: it answers "may I act here",
-- and a caller who may not is told so by the policy rather than by an empty row.
SELECT g.id AS guest_id,
       g.programme_id AS guest_programme_id,
       g.semester_id, g.module_id, g.track,
       sem.code AS semester_code, sem.phase AS semester_phase,
       g.covered_by_instance_id AS host_id,
       g.covered_by_programme_id AS host_programme_id,
       g.covered_requested_at, g.covered_accepted_at
FROM course_instance g
JOIN semester sem ON sem.id = g.semester_id
WHERE g.id = $1;

-- name: LockInstanceForCoverage :one
-- Belt to the foreign key's braces. Two leads acting in opposite directions at the same moment is
-- write skew, both checks individually correct; the key would refuse the result anyway, and this
-- is what makes the refusal a refusal rather than a serialisation error somebody has to read.
--
-- Callers lock in id order. Locking them in the order the request names them is how two
-- simultaneous handshakes between the same pair deadlock.
SELECT id, programme_id, semester_id, module_id, covered_by_instance_id, covered_accepted_at
FROM course_instance
WHERE id = $1
FOR UPDATE;

-- name: RequestInstanceCoverage :execrows
-- Ask that this instance's demand be met by another programme's event.
--
-- Every invariant about the host — same semester, same module, another programme, not itself
-- covered — is the composite foreign key's answer, so this statement carries none of them and
-- cannot come to disagree with the schema about any of them. The host's programme is read from
-- the host rather than passed in, for the same reason.
--
-- Guarded on there being no link yet: changing where a request points is releasing and asking
-- again, which is two decisions and reads as two.
UPDATE course_instance g
SET covered_by_instance_id = h.id,
    covered_by_programme_id = h.programme_id,
    covered_by_is_covered = false,
    covered_requested_at = now(),
    covered_requested_by = sqlc.narg('requested_by')::uuid,
    covered_accepted_at = NULL,
    covered_accepted_by = NULL,
    updated_at = now()
FROM course_instance h
WHERE g.id = $1
  AND h.id = sqlc.arg(host_id)::uuid
  AND g.covered_by_instance_id IS NULL;

-- name: AcceptInstanceCoverage :execrows
-- Agree to hold this event for the other programme too.
--
-- Compare-and-set on the host that was asked, the same shape ReplaceAssignment takes and for the
-- same reason: the caller is answering the request they were looking at, and if the guest's lead
-- has since pointed somewhere else, no rows come back and the caller is told rather than silently
-- agreeing to something else.
UPDATE course_instance
SET covered_accepted_at = now(),
    covered_accepted_by = sqlc.narg('accepted_by')::uuid,
    updated_at = now()
WHERE id = $1
  AND covered_by_instance_id = sqlc.arg(host_id)::uuid
  AND covered_accepted_at IS NULL;

-- name: ReleaseInstanceCoverage :execrows
-- End it: a request declined, a request withdrawn, or an agreement revised.
--
-- All seven columns at once, which the all-or-nothing CHECK makes the only way to clear any of
-- them. One statement for all three cases because they are one state — the demand is simply not
-- covered — and three statements would be three places to get the permission wrong.
UPDATE course_instance
SET covered_by_instance_id = NULL,
    covered_by_programme_id = NULL,
    covered_by_is_covered = NULL,
    covered_requested_at = NULL,
    covered_requested_by = NULL,
    covered_accepted_at = NULL,
    covered_accepted_by = NULL,
    updated_at = now()
WHERE id = $1 AND covered_by_instance_id IS NOT NULL;

-- name: HostCandidatesFor :many
-- The instances that could cover this one: same semester, same module, another programme, and not
-- themselves covered.
--
-- The foreign key's four conditions as a list, so the picker offers exactly what the schema would
-- accept. Anything else would be a menu with entries that fail on click.
SELECT c.id, c.track, c.programme_semester,
       p.id AS programme_id, p.code AS programme_code, p.title AS programme_title
FROM course_instance me
JOIN course_instance c
  ON c.semester_id = me.semester_id
 AND c.module_id = me.module_id
 AND c.programme_id <> me.programme_id
 AND c.covered_by_instance_id IS NULL
JOIN programme p ON p.id = c.programme_id
WHERE me.id = $1
ORDER BY p.code, c.track, c.id;

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
SELECT ci.id, ci.module_id, ci.track, ci.programme_semester,
       (ci.covered_accepted_at IS NOT NULL)::boolean AS is_covered
FROM course_instance ci
WHERE ci.semester_id = $1
  AND ci.programme_id = $2
ORDER BY ci.module_id, ci.track;

-- name: DeleteInstancePartsOfInstance :execrows
-- Everything this cohort holds, because from now on another programme holds it.
--
-- By instance rather than by kind, unlike DeleteInstancePartsOfKind above, and the difference is
-- the difference between the two mechanisms: sharing a lecture across parallel cohorts is about
-- one unit, while coverage is of the whole offering. What the covered cohort keeps is nothing.
--
-- A part something already hangs off refuses to go, and the acceptance fails as a whole.
DELETE FROM instance_part WHERE course_instance_id = $1;

-- name: CoverageToCarryForward :many
-- Where these instances' coverage pointed, described by what identifies an instance rather than by
-- id: the covering instance's programme and cohort.
--
-- A copy into another semester cannot carry an id — the row it names is in the semester being
-- copied from. What it can carry is "GS was held by DE's B cohort", which is a sentence that still
-- means something next year, and which InstanceByIdentity below turns back into an id.
SELECT g.id AS for_instance_id,
       host.programme_id AS host_programme_id,
       host.track AS host_track,
       g.covered_accepted_at
FROM course_instance g
JOIN course_instance host ON host.id = g.covered_by_instance_id
WHERE g.id = ANY (sqlc.arg(instance_ids)::uuid[]);

-- name: InstanceByIdentity :one
-- One instance by what identifies it, for a copy that has to find next year's counterpart of a
-- row it only knows by last year's id.
SELECT id
FROM course_instance
WHERE semester_id = $1 AND module_id = $2 AND programme_id = $3 AND track = $4;

-- name: CoverageHostFor :one
-- Who would hold the teaching of a cohort about to be declared.
--
-- The same four conditions the composite foreign key asserts, plus the order the faculty lives:
-- whoever planned first holds. `created_at` and then the id, so that two declarations inside one
-- transaction do not draw lots — `now()` is the transaction's clock, so they tie to the
-- microsecond.
--
-- FOR KEY SHARE is the lock the foreign key would take at the write a statement later. Taken here
-- so that "this instance exists and is not itself covered" is still true when the write happens.
-- Two cohorts coupling at once share the lock and do not queue; only a withdrawal of the host,
-- which takes FOR UPDATE, makes them wait.
--
-- planning_status: an automatic coupling to a programme the faculty has stopped planning would be
-- a fact nobody chose. The manual picker still offers it — that one is somebody's decision.
SELECT h.id, h.programme_id
FROM course_instance h
JOIN programme p ON p.id = h.programme_id
WHERE h.semester_id = $1
  AND h.module_id = $2
  AND h.programme_id <> sqlc.arg(programme_id)::uuid
  AND h.covered_by_instance_id IS NULL
  AND p.planning_status = 'PLANNED'
ORDER BY h.created_at, h.id
LIMIT 1
FOR KEY SHARE;

-- name: CoveredCohortExistsFor :one
-- Whether this programme already has a covered cohort of this module in this semester.
--
-- The fallback the automatic coupling needs: a second cohort holds its own teaching, because the
-- index below refuses to let it be covered as well and a raw unique violation out of a save is not
-- an answer anybody can act on.
SELECT EXISTS (
    SELECT 1 FROM course_instance
     WHERE semester_id = $1 AND module_id = $2 AND programme_id = $3
       AND covered_by_instance_id IS NOT NULL
)::boolean AS covered;

-- name: CoupleInstanceCoverage :execrows
-- Hold this cohort's demand with another programme's event, from the moment it is declared.
--
-- Both timestamps at once, and that is the difference between the two ways in. Declaring a cohort
-- beside another programme's is one act by one lead over a cohort that has nothing yet, so asking
-- and holding happen together. The other way in — RequestInstanceCoverage, on a cohort that
-- already holds parts and possibly an assignment — keeps the two apart, because dissolving what
-- somebody already planned is a bigger act and the other programme answers for it.
UPDATE course_instance g
SET covered_by_instance_id = h.id,
    covered_by_programme_id = h.programme_id,
    covered_by_is_covered = false,
    covered_requested_at = now(),
    covered_requested_by = sqlc.narg('by')::uuid,
    covered_accepted_at = now(),
    covered_accepted_by = sqlc.narg('by')::uuid,
    updated_at = now()
FROM course_instance h
WHERE g.id = $1
  AND h.id = sqlc.arg(host_id)::uuid
  AND g.covered_by_instance_id IS NULL;

-- name: LockCoverageGroup :many
-- An instance and everything whose demand hangs off it, in id order.
--
-- FOR UPDATE on the host is what makes a withdrawal exclusive of a concurrent coupling: the
-- foreign key on a guest's side takes FOR KEY SHARE on the row it points at, and KEY SHARE and
-- UPDATE conflict. So a coupling that would arrive between the read below and the delete waits
-- here instead of turning the delete into a foreign key violation nobody can explain.
--
-- Id order, for the reason lockCoveragePair already gives: locking in the order the caller happens
-- to name things is how two operations on the same pair deadlock. LockRows sits above Sort, so the
-- rows are taken in the sorted order whatever the plan.
SELECT id
FROM course_instance
WHERE id = $1 OR covered_by_instance_id = $1
ORDER BY id
FOR UPDATE;

-- name: CoverageSuccessorFor :one
-- The guest that becomes the holder when the current one is withdrawn: the longest-standing.
--
-- The agreement's own timestamp first, because that is the rule the faculty stated. The rest is a
-- tiebreak somebody can read back off the screen: cohorts coupled in one planDemand share a
-- timestamp to the microsecond — now() is the transaction's clock — and letting a random uuid
-- decide is not something anybody can explain to the lead who lost.
SELECT g.id, g.programme_id, g.track, p.code AS programme_code, p.title AS programme_title
FROM course_instance g
JOIN programme p ON p.id = g.programme_id
WHERE g.covered_by_instance_id = $1
ORDER BY g.covered_accepted_at, g.created_at, p.code, g.track, g.id
LIMIT 1;

-- name: MoveInstancePartsTo :execrows
-- Hand the teaching to another cohort, rows and all.
--
-- An UPDATE rather than a delete and an insert, and that is the whole point: an assignment
-- references instance_part, so moving the row keeps who holds the event. Deleting and rebuilding
-- would lose the assignment — or, since assignment is ON DELETE RESTRICT, refuse the withdrawal
-- outright the moment anybody is teaching it.
--
-- position comes along unchanged. There is no unique constraint on (course_instance_id, position),
-- the successor holds nothing to collide with, and renumbering would be a write with no reader.
UPDATE instance_part
SET course_instance_id = sqlc.arg(to_instance)::uuid, updated_at = now()
WHERE course_instance_id = sqlc.arg(from_instance)::uuid;

-- name: RepointGuestsTo :execrows
-- Point the remaining guests at the cohort that took the teaching over.
--
-- The successor must already have been released, or this fails: the foreign key reads its
-- is_covered, which is generated from covered_by_instance_id.
--
-- It can only ever pass course_instance_coverage_crosses_programmes because of the partial unique
-- index on one covered cohort per programme — with that, every guest of one host is in a different
-- programme, so none of them can end up pointing at the successor's own.
UPDATE course_instance g
SET covered_by_instance_id = s.id,
    covered_by_programme_id = s.programme_id,
    updated_at = now()
FROM course_instance s
WHERE g.covered_by_instance_id = sqlc.arg(from_instance)::uuid
  AND s.id = sqlc.arg(to_instance)::uuid
  AND g.id <> s.id;

-- name: SharedPartsOf :many
-- The parts this cohort holds for its own programme's sibling cohorts.
--
-- What a promotion has to hand back before it hands the rows to another programme: the flag means
-- "serves the other cohorts of my programme", and it would go on meaning that in the programme the
-- rows land in.
SELECT id, kind, teaching_hours
FROM instance_part
WHERE course_instance_id = $1 AND serves_sibling_tracks
ORDER BY position, id;
