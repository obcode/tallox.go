-- What opens and closes the planning, at the grain the planning actually happens in.
--
-- Named marks and not windows, and that is not a matter of taste: sqlc names the generated file
-- after this one, and Go reads `planning_windows.sql.go` as a file that only builds on Windows —
-- the GOOS suffix is matched before the `.sql.go` extension is stripped. The package compiled,
-- the queries were simply absent, and the error pointed at querier.go.
--
-- Two tables, two shapes, and the difference is the point (see migration 18):
-- demand_completion is an announcement that blocks nothing, wish_window is a door.
--
-- Neither is filtered by a visibility rule. Both are facts about the *process* rather than about
-- people: which programmes have settled their demand and which subjects are taking entries is
-- exactly what a colleague needs to know to do their part, and hiding it would produce a tool
-- that refuses writes without saying why.

-- name: DemandCompletionsOfSemester :many
-- Which study programmes have announced their demand as settled, for one semester.
SELECT dc.semester_id, dc.programme_id, dc.completed_at, dc.completed_by,
       prog.code AS programme_code, prog.title AS programme_title
FROM demand_completion dc
JOIN programme prog ON prog.id = dc.programme_id
JOIN semester s ON s.id = dc.semester_id
WHERE s.code = sqlc.arg(semester)::text
ORDER BY prog.code;

-- name: AnnounceDemandComplete :one
-- Announce, or re-announce after adding something.
--
-- ON CONFLICT DO UPDATE and not DO NOTHING: re-announcing is the ordinary act after a late
-- instance, and what it moves is the timestamp — a reader wants to know how fresh the statement
-- is, not when it was first made. That is the opposite of the publication marks, which keep their
-- first timestamp because they record an irreversible event.
INSERT INTO demand_completion (semester_id, programme_id, completed_by)
SELECT s.id, pr.id, sqlc.narg('completed_by')::uuid
  FROM semester s, programme pr
 WHERE s.code = sqlc.arg(semester)::text AND pr.code = sqlc.arg(programme)::text
ON CONFLICT (semester_id, programme_id) DO UPDATE
   SET completed_at = now(), completed_by = EXCLUDED.completed_by
RETURNING semester_id, programme_id, completed_at, completed_by;

-- name: WithdrawDemandComplete :execrows
-- Take the announcement back. Deleting the row rather than a flag: "never announced" and
-- "announced, then withdrawn" are the same state — the demand is not settled — and a table that
-- kept the difference would be keeping something nobody reads.
DELETE FROM demand_completion
 WHERE semester_id = (SELECT id FROM semester WHERE code = sqlc.arg(semester)::text)
   AND programme_id = (SELECT id FROM programme WHERE code = sqlc.arg(programme)::text);

-- name: WishWindowsOfSemester :many
-- The subject groups somebody has decided something about, for one semester.
--
-- Only those: an absent row means open, so this list is the exceptions and not the state of every
-- group. A caller that wants the state of one group asks for it and reads "open" when it is not
-- here, which is what domain.WishWindowsBySubjectGroup does.
SELECT ww.semester_id, ww.subject_group_id, ww.open, ww.changed_at, ww.changed_by,
       g.code AS subject_group_code, g.name AS subject_group_name
FROM wish_window ww
JOIN subject_group g ON g.id = ww.subject_group_id
JOIN semester s ON s.id = ww.semester_id
WHERE s.code = sqlc.arg(semester)::text
ORDER BY g.code;

-- name: SetWishWindow :one
-- Open or shut one subject group's wish round.
--
-- Upsert, and the row is kept when it is opened again rather than deleted. Unlike the
-- announcement above, the two states are not the same: a window that was shut and reopened means
-- somebody took two decisions, and the second one is worth being able to see.
INSERT INTO wish_window (semester_id, subject_group_id, open, changed_by)
SELECT s.id, sqlc.arg(subject_group_id)::uuid, sqlc.arg(open)::boolean,
       sqlc.narg('changed_by')::uuid
  FROM semester s
 WHERE s.code = sqlc.arg(semester)::text
ON CONFLICT (semester_id, subject_group_id) DO UPDATE
   SET open = EXCLUDED.open, changed_at = now(), changed_by = EXCLUDED.changed_by
RETURNING semester_id, subject_group_id, open, changed_at, changed_by;

-- name: WishWriteContext :one
-- Everything the wish write rule needs about one instance, in one statement.
--
-- The semester decides whether anything may be written at all, and the wish window of the
-- module's subject group decides whether *this* subject is taking entries. Reading them
-- separately would be two round trips and two chances to decide against a state that has moved.
--
-- COALESCE(ww.open, true) is the fail-open default written where it is read: a module in no
-- subject group has no window, and a subject group nobody has decided anything about has no row.
-- Both are open, which is the rule — closing is the intervention. See migration 18 for why this
-- one direction is the opposite of every other default in the schema.
SELECT s.id, s.code, s.phase, s.wishes_published_at,
       msg.subject_group_id,
       COALESCE(ww.open, true)::boolean AS wish_window_open
FROM course_instance ci
JOIN semester s ON s.id = ci.semester_id
JOIN module m ON m.id = ci.module_id
LEFT JOIN module_subject_group msg ON msg.module_id = m.id
LEFT JOIN wish_window ww
       ON ww.semester_id = s.id AND ww.subject_group_id = msg.subject_group_id
WHERE ci.id = sqlc.arg(instance_id)::uuid;

-- name: WishWriteContextByWishID :one
-- The same, reached from a wish rather than from an instance. For withdrawing one.
SELECT s.id, s.code, s.phase, s.wishes_published_at,
       msg.subject_group_id,
       COALESCE(ww.open, true)::boolean AS wish_window_open
FROM wish w
JOIN course_instance ci ON ci.id = w.course_instance_id
JOIN semester s ON s.id = ci.semester_id
JOIN module m ON m.id = ci.module_id
LEFT JOIN module_subject_group msg ON msg.module_id = m.id
LEFT JOIN wish_window ww
       ON ww.semester_id = s.id AND ww.subject_group_id = msg.subject_group_id
WHERE w.id = sqlc.arg(id)::uuid;

-- name: ProgrammeIDByCode :one
-- The id behind a code somebody named. For the permission check, which asks about ids.
SELECT id FROM programme WHERE code = sqlc.arg(code)::text;
