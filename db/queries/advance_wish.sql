-- Advance wishes ("Vormerkungen"): interest in a module for a semester whose demand is not
-- settled yet. See migration 20261101100000.
--
-- THE RULE THIS FILE IS MADE OF
--
-- The same as wish.sql, and for the same reason: an advance wish is the wish it is going to be,
-- so every SELECT carries the four filter parameters of WishFilter. The programme is the one the
-- advance wish names — it has no instance — and the subject group is the module's.
--
-- **There is no COUNT** here either. "Two colleagues have noted down Analysis for SS28" is the
-- confidential fact with the names taken out, and it would reach the demand planning exactly when
-- it would distort it. store.TestEveryWishQueryIsFiltered reads this file too.

-- name: AdvanceWishes :many
-- The advance wishes of one semester — or of every semester for the caller's own — filtered.
--
-- released and offered are facts about the process, readable by everybody (demandCompletions and
-- the demand itself), and what the status is derived from: converted, not offered (the programme
-- released its demand without a cohort of the module), or pending.
SELECT
    aw.id, aw.person_id, aw.semester_code, aw.priority, aw.note, aw.max_tracks, aw.converted_at,
    aw.created_at, aw.updated_at,
    prog.id AS programme_id, prog.code AS programme_code, prog.title AS programme_title,
    m.id AS module_id, m.name AS module_name, msg.subject_group_id AS subject_group_id,
    COALESCE(home.code, '')::text AS module_home_programme_code,
    EXISTS (SELECT 1 FROM module_offering o WHERE o.module_id = m.id AND o.is_duty)::boolean
        AS module_compulsory,
    m.course_type AS module_course_type,
    person.mail AS person_mail, person.name AS person_name,
    COALESCE(t.short_name, '')::text AS person_sort_name,
    EXISTS (SELECT 1 FROM demand_completion dc
            JOIN semester s ON s.id = dc.semester_id
            WHERE s.code = aw.semester_code AND dc.programme_id = aw.programme_id
    )::boolean AS released,
    EXISTS (SELECT 1 FROM course_instance ci
            JOIN semester s ON s.id = ci.semester_id
            WHERE s.code = aw.semester_code AND ci.programme_id = aw.programme_id
              AND ci.module_id = aw.module_id
    )::boolean AS offered
FROM advance_wish aw
JOIN programme prog ON prog.id = aw.programme_id
JOIN module m ON m.id = aw.module_id
LEFT JOIN programme home ON home.id = m.home_programme_id
LEFT JOIN module_subject_group msg ON msg.module_id = m.id
JOIN person ON person.id = aw.person_id
LEFT JOIN teacher t ON t.mail = person.mail
WHERE (sqlc.narg('semester')::text IS NULL OR aw.semester_code = sqlc.narg('semester')::text)
  AND (sqlc.narg('programme')::text IS NULL OR prog.code = sqlc.narg('programme')::text)
  AND (sqlc.narg('module')::uuid IS NULL OR m.id = sqlc.narg('module')::uuid)
  AND (sqlc.narg('subject_group')::uuid IS NULL
       OR msg.subject_group_id = sqlc.narg('subject_group')::uuid)
  AND (sqlc.narg('person')::uuid IS NULL OR aw.person_id = sqlc.narg('person')::uuid)
  AND (
      sqlc.arg('scope')::text = 'all'
      OR (sqlc.arg('scope')::text = 'own'
          AND aw.person_id = sqlc.arg(owner_id)::uuid)
      OR (sqlc.arg('scope')::text = 'own_or_scoped'
          AND (aw.person_id = sqlc.arg(owner_id)::uuid
               OR prog.id = ANY (sqlc.arg(programme_ids)::uuid[])
               OR msg.subject_group_id = ANY (sqlc.arg(subject_group_ids)::uuid[])))
  )
ORDER BY aw.semester_code, m.name, prog.code, aw.priority,
         COALESCE(NULLIF(t.short_name, ''), person.name), aw.id;

-- name: UpsertAdvanceWish :one
-- Note one down, or change it while it is pending. A converted one is not touched: from then on
-- it is the wish that is changed, and the store reports zero rows as that refusal.
--
-- noted_after_release is written on insert only: changing a pending one later does not make it a
-- later statement (migration 20261112160000).
INSERT INTO advance_wish (person_id, semester_code, module_id, programme_id, priority, note,
                          max_tracks, noted_after_release)
VALUES (sqlc.arg(person_id)::uuid, sqlc.arg(semester)::text, sqlc.arg(module_id)::uuid,
        sqlc.arg(programme_id)::uuid, sqlc.arg(priority)::smallint, sqlc.arg(note)::text,
        sqlc.narg(max_tracks)::smallint, sqlc.arg(noted_after_release)::boolean)
ON CONFLICT (person_id, semester_code, module_id, programme_id) DO UPDATE
SET priority = EXCLUDED.priority,
    note = EXCLUDED.note,
    max_tracks = EXCLUDED.max_tracks,
    updated_at = now()
WHERE advance_wish.converted_at IS NULL
RETURNING id;

-- name: DeleteOwnAdvanceWish :execrows
-- Withdraw one's own. A converted one may go too: it is the record of a statement, and the wishes
-- it became stay what they are.
DELETE FROM advance_wish WHERE id = sqlc.arg(id)::uuid AND person_id = sqlc.arg(person_id)::uuid;

-- name: ConvertAdvanceWishes :exec
-- Turn the pending advance wishes of one programme and semester into wishes, where there is now a
-- cohort to wish for. Run inside the transaction that released the demand or created a cohort,
-- and — with programme_id NULL, for every programme of the semester — in the one that reopens a
-- subject group's wish round.
--
-- Only once the programme has released the semester, and not in a finished one. A cohort taught
-- entirely by another faculty is skipped, as setWish refuses it.
--
-- The subject group's wish window is consulted only for one noted down after the release. One
-- entered before was stated before any round on this demand could have opened or shut; one
-- entered after can have been stated after the round closed, and converting it then would be a
-- way around the closed round. It stays an advance wish until the round is open again.
-- COALESCE(ww.open, true) is the fail-open reading of WishWriteContext: no row, or a module in no
-- subject group, is open.
--
-- An existing wish of the same person on a cohort keeps the stronger priority and both notes, the
-- rule handOverWishes applies. Reports nothing — "2 advance wishes converted" is the count.
WITH due AS (
    UPDATE advance_wish aw
       SET converted_at = now(), updated_at = now()
      FROM semester s
     WHERE s.code = aw.semester_code
       AND s.id = sqlc.arg(semester_id)::uuid
       AND (sqlc.narg(programme_id)::uuid IS NULL OR aw.programme_id = sqlc.narg(programme_id)::uuid)
       AND aw.converted_at IS NULL
       AND (NOT aw.noted_after_release
            OR NOT EXISTS (SELECT 1 FROM module_subject_group msg
                           JOIN wish_window ww ON ww.subject_group_id = msg.subject_group_id
                                              AND ww.semester_id = s.id
                           WHERE msg.module_id = aw.module_id AND NOT ww.open))
       AND s.phase <> 'FINAL'
       AND EXISTS (SELECT 1 FROM demand_completion dc
                   WHERE dc.semester_id = s.id AND dc.programme_id = aw.programme_id)
       AND EXISTS (SELECT 1 FROM course_instance ci
                   WHERE ci.semester_id = s.id AND ci.programme_id = aw.programme_id
                     AND ci.module_id = aw.module_id
                     AND NOT (EXISTS (SELECT 1 FROM instance_part ip
                                      WHERE ip.course_instance_id = ci.id)
                              AND NOT EXISTS (SELECT 1 FROM instance_part ip
                                              WHERE ip.course_instance_id = ci.id
                                                AND ip.taught_by IS NULL)))
    RETURNING aw.person_id, aw.module_id, aw.programme_id, aw.priority, aw.note, aw.max_tracks,
              s.id AS semester_id
),
wished AS (
    INSERT INTO wish (course_instance_id, person_id, priority, note)
    SELECT ci.id, due.person_id, due.priority, due.note
      FROM due
      JOIN course_instance ci
        ON ci.semester_id = due.semester_id AND ci.programme_id = due.programme_id
       AND ci.module_id = due.module_id
     WHERE NOT (EXISTS (SELECT 1 FROM instance_part ip WHERE ip.course_instance_id = ci.id)
                AND NOT EXISTS (SELECT 1 FROM instance_part ip
                                WHERE ip.course_instance_id = ci.id AND ip.taught_by IS NULL))
    ON CONFLICT (course_instance_id, person_id) DO UPDATE
       SET priority = LEAST(wish.priority, EXCLUDED.priority),
           note = CASE
                      WHEN EXCLUDED.note = '' OR EXCLUDED.note = wish.note THEN wish.note
                      ELSE left(concat_ws(' · ', nullif(wish.note, ''), EXCLUDED.note), 500)
                  END,
           updated_at = now()
    RETURNING 1
)
INSERT INTO wish_track_limit (person_id, semester_id, module_id, programme_id, max_tracks)
SELECT due.person_id, due.semester_id, due.module_id, due.programme_id, due.max_tracks
  FROM due
 WHERE due.max_tracks IS NOT NULL
ON CONFLICT (person_id, semester_id, module_id, programme_id) DO NOTHING;

-- name: AdvanceWishOffering :one
-- What the write rule needs about a semester, programme and module, readable by everybody:
-- whether the programme has released its demand for it, and whether that demand has a cohort of
-- the module (then the wish itself is what one enters). Both are facts about the process, the
-- same ones the status is derived from; neither says anything about anybody's wishes.
SELECT EXISTS (SELECT 1 FROM demand_completion dc
               JOIN semester s ON s.id = dc.semester_id
               WHERE s.code = sqlc.arg(semester)::text
                 AND dc.programme_id = sqlc.arg(programme_id)::uuid)::boolean AS released,
       EXISTS (SELECT 1 FROM course_instance ci
               JOIN semester s ON s.id = ci.semester_id
               WHERE s.code = sqlc.arg(semester)::text
                 AND ci.programme_id = sqlc.arg(programme_id)::uuid
                 AND ci.module_id = sqlc.arg(module_id)::uuid)::boolean AS offered;
