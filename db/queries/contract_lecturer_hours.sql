-- Contract lecturer hours: how much of a programme's teaching is held by lecturers on contract,
-- for the controlling view (ProgrammeTeachingLoad.contractLecturerHours).
--
-- A SUM OVER ASSIGNMENTS, AND THEREFORE FILTERED
--
-- assignment.sql has no COUNT and no SUM, because before publication a sum over assignments the
-- caller may not read one by one is the same leak as the rows. This file sums them anyway, and it
-- may because every read carries the assignment visibility filter itself (policy.AssignmentFilter,
-- the same four branches assignment.sql spells out), and because the service shows a programme's
-- figure only when that filter covers the whole programme (AssignmentFilter.CoversProgramme) — a
-- partial sum would be the confidential fact with the names taken out.
-- store.TestEveryContractLecturerQueryIsFiltered reads this file and insists on the filter.
--
-- WHAT IS COUNTED
--
-- The hours of the parts held by a teacher flagged is_lecturer_on_contract, or by an account
-- whose teacher record (joined by address, the link the assignment's write path canonicalises
-- by) carries the flag. Counted the way TeachingLoadByProgramme counts teachingHours: at the
-- programme of the instance holding the part, a lecture serving both cohorts is one part and
-- counts once, and a part another faculty teaches is not ours (it cannot be held anyway).
-- Per programme only: there is no per-person figure here, by design.

-- name: ContractLecturerHoursByProgramme :many
SELECT prog.id AS programme_id,
       COALESCE(SUM(p.teaching_hours), 0)::float8 AS hours
FROM assignment a
JOIN instance_part p ON p.id = a.instance_part_id
JOIN course_instance ci ON ci.id = p.course_instance_id
JOIN semester sem ON sem.id = ci.semester_id
JOIN programme prog ON prog.id = ci.programme_id
LEFT JOIN module_subject_group msg ON msg.module_id = ci.module_id
LEFT JOIN teacher t ON t.id = a.teacher_id
LEFT JOIN person per ON per.id = a.person_id
LEFT JOIN teacher tp ON tp.mail = per.mail
WHERE sem.code = sqlc.arg(semester)::text
  AND p.taught_by IS NULL
  AND COALESCE(t.is_lecturer_on_contract, tp.is_lecturer_on_contract, false)
  AND (
      sqlc.arg('scope')::text = 'all'
      OR (sqlc.arg('scope')::text = 'own'
          AND a.person_id = sqlc.arg(assignee_id)::uuid)
      OR (sqlc.arg('scope')::text = 'own_or_scoped'
          AND (a.person_id = sqlc.arg(assignee_id)::uuid
               OR prog.id = ANY (sqlc.arg(programme_ids)::uuid[])
               OR msg.subject_group_id = ANY (sqlc.arg(subject_group_ids)::uuid[])))
  )
GROUP BY prog.id;
