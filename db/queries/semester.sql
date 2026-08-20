-- Semesters and the phase each one is in.
--
-- Two of these are compare-and-set rather than plain updates, and that is the theme of the
-- file. A phase advance and a publication are both "change this only if it is still what I
-- think it is": the alternative is to read the row, decide in Go, and write — which is correct
-- in a unit test and a race in production, because two people from the dean's office clicking
-- at the same moment is precisely the situation a phase switch happens in.

-- name: CreateSemester :one
-- The code carries the format constraint, so an invalid one is refused by the database and
-- not only by the service. The phase defaults to DEMAND_PLANNING: a semester that exists but
-- has not been planned yet is at the start of the process, which is the only sensible reading.
INSERT INTO semester (code)
VALUES ($1)
RETURNING id, code, phase, wishes_published_at, created_at, updated_at;

-- name: SemesterByID :one
SELECT id, code, phase, wishes_published_at, created_at, updated_at
FROM semester
WHERE id = $1;

-- name: SemesterByCode :one
SELECT id, code, phase, wishes_published_at, created_at, updated_at
FROM semester
WHERE code = $1;

-- name: Semesters :many
-- Newest first, which for this code is also chronological: the year leads and SS sorts before
-- WS within a year, in the order the terms actually happen. Ordering by created_at instead
-- would list them by when somebody got round to entering them.
SELECT id, code, phase, wishes_published_at, created_at, updated_at
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
RETURNING id, code, phase, wishes_published_at, created_at, updated_at;

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
RETURNING id, code, phase, wishes_published_at, created_at, updated_at;
