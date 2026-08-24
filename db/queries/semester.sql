-- Semesters and the phase each one is in.
--
-- Two of these are compare-and-set rather than plain updates, and that is the theme of the
-- file. A phase advance and a publication are both "change this only if it is still what I
-- think it is": the alternative is to read the row, decide in Go, and write — which is correct
-- in a unit test and a race in production, because two people from the dean's office clicking
-- at the same moment is precisely the situation a phase switch happens in.

-- name: EnsureSemester :one
-- The row for a semester, created if this is the first decision anybody records about it.
--
-- Nobody creates a semester — a semester is a name for a stretch of time and is there the way
-- next March is there. The row holds the decisions taken about one, so it comes into existence
-- with the first of them, and its defaults are what an untouched semester already means:
-- DEMAND_PLANNING, wishes confidential.
--
-- ON CONFLICT DO UPDATE rather than DO NOTHING, because DO NOTHING returns nothing when the
-- row is already there and would need a second query to find out what it looks like — a race
-- for the sake of a statement that reads slightly better. Assigning the code to itself is the
-- idiom for "give me the row either way"; nothing is changed by it, and updated_at stays where
-- it was so that arriving at a semester does not look like deciding something about it.
INSERT INTO semester (code)
VALUES ($1)
ON CONFLICT (code) DO UPDATE SET code = EXCLUDED.code
RETURNING id, code, phase, wishes_published_at, created_at, updated_at,
       is_planning_semester, planning_set_at, planning_set_by;

-- name: SemesterByCode :one
SELECT id, code, phase, wishes_published_at, created_at, updated_at,
       is_planning_semester, planning_set_at, planning_set_by
FROM semester
WHERE code = $1;

-- name: Semesters :many
-- The recorded ones — the semesters somebody has decided something about. The ones nobody has
-- touched are not here to be listed, and the domain adds them from the calendar.
--
-- Newest first, which for this code is also chronological: the year leads and SS sorts before
-- WS within a year, in the order the terms actually happen. Ordering by created_at instead
-- would list them by when somebody got round to entering them.
SELECT id, code, phase, wishes_published_at, created_at, updated_at,
       is_planning_semester, planning_set_at, planning_set_by
FROM semester
ORDER BY code DESC;

-- name: AdvanceSemesterPhase :one
-- Compare-and-set: $3 is the phase the caller believes the semester is in, and no rows come
-- back if it has moved on since they looked.
--
-- That is what makes "one step at a time" hold under concurrency. Without the second
-- predicate, two switches issued together would both read DEMAND_PLANNING, and the second
-- would write ASSIGNMENT over the first one's WISHES — a skipped phase, arrived at by nobody's
-- decision, and invisible afterwards because the row looks like somebody chose it.
UPDATE semester
SET phase = $2,
    updated_at = now()
WHERE id = $1
  AND phase = $3
RETURNING id, code, phase, wishes_published_at, created_at, updated_at,
       is_planning_semester, planning_set_at, planning_set_by;

-- name: PublishSemesterWishes :one
-- Idempotent, and it keeps the *first* timestamp.
--
-- Publishing twice is not an error — the second caller wanted the wishes published and they
-- are — but the moment it happened is a fact about the process that a second call must not
-- overwrite. COALESCE does both in one statement, so there is no window between checking and
-- writing.
--
-- updated_at stays untouched when nothing changed, so a repeated call does not make the row
-- look edited.
UPDATE semester
SET wishes_published_at = COALESCE(wishes_published_at, now()),
    updated_at = CASE WHEN wishes_published_at IS NULL THEN now() ELSE updated_at END
WHERE id = $1
RETURNING id, code, phase, wishes_published_at, created_at, updated_at,
       is_planning_semester, planning_set_at, planning_set_by;

-- name: PlanningSemester :one
-- The semester the faculty is planning, or no row while nobody has said.
--
-- No LIMIT: the partial unique index makes "at most one" a property of the table rather than
-- of this statement, and a LIMIT here would quietly return one of several if that ever stopped
-- being true.
SELECT id, code, phase, wishes_published_at, created_at, updated_at,
       is_planning_semester, planning_set_at, planning_set_by
FROM semester
WHERE is_planning_semester;

-- name: ClearPlanningSemester :exec
-- Take the mark off whichever semester carries it, except the one about to receive it.
--
-- Runs first in the transaction that moves the mark, and that order is what makes concurrency
-- boring: this UPDATE takes a row lock on the current planning semester, so two people setting
-- different semesters at the same moment serialise here instead of colliding on the unique
-- index afterwards. The second one wins, which is the right outcome for a decision somebody is
-- taking on purpose.
--
-- The exception for the target is not decoration: without it, setting the semester that is
-- already set would clear the mark and then set it again, moving planning_set_at and making a
-- no-op look like a decision.
UPDATE semester
SET is_planning_semester = false,
    updated_at = now()
WHERE is_planning_semester
  AND id <> $1;

-- name: MarkPlanningSemester :one
-- Make this semester the one being planned.
--
-- Unconditional and idempotent in effect: setting the semester that is already set rewrites the
-- same values. planning_set_at moves, and that is intended — it records the most recent time
-- somebody decided this, which is what a reader of the audit wants to know.
UPDATE semester
SET is_planning_semester = true,
    planning_set_at = now(),
    planning_set_by = sqlc.narg('set_by'),
    updated_at = now()
WHERE id = $1
RETURNING id, code, phase, wishes_published_at, created_at, updated_at,
       is_planning_semester, planning_set_at, planning_set_by;
