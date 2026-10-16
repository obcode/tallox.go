-- Migration 27: remove the parts that were made from a guessed split and have since been
-- overtaken by a confirmed one.
--
-- Until today an instance of a module without a confirmed split was declared from the proposal.
-- For the course types "je nach Fach" and "selbständiges Arbeiten" the proposal is one OTHER part
-- carrying the module's full hours, and the group control multiplied it. Once somebody confirmed
-- the real split, the group control added groups of the real practical kind next to the OTHER
-- parts — and nothing ever took the OTHER parts away, nor added the lecture the instance had
-- never had. The case that was reported: a four-hour module, lecture plus exercise, planned as
-- 2 × OTHER 4 + 2 × EXERCISE 2 = 12 hours instead of LECTURE 2 + 2 × EXERCISE 2 = 6.
--
-- The code no longer declares from a proposal (domain.Module.Plannable). This repairs what was
-- declared before, once, and is never consulted again — so the narrowness is the point. A part is
-- removed only if it is exactly what the proposal made:
--
--   - of kind OTHER, and the module's confirmed split has no OTHER,
--   - of a module whose course type proposes a single OTHER part, carrying the module's full
--     contact hours — which is what the proposal gave it,
--   - in a semester that is not over (FINAL is history and is not rewritten),
--   - and nobody is assigned to it: an assignment is a person's decision, and deleting the part
--     under it is not this migration's to make. Such a part stays and is visible on the screen.
--
-- An OTHER part somebody added by hand with other hours is left alone. A module whose split is
-- still not confirmed is left alone as well: there is nothing true to replace the guess with yet.
--
-- After removing, an affected instance gets what the confirmed split names and it lacks: one part
-- per missing kind, and for the practical kind as many groups as OTHER parts were removed, because
-- that is the group count somebody chose. A lecture is not added where a sibling cohort already
-- holds it for every cohort. Finally the positions of an affected instance are renumbered so the
-- lecture comes first again.

-- +goose Up
CREATE TEMP TABLE guessed_part ON COMMIT DROP AS
SELECT p.id, p.course_instance_id
FROM instance_part p
JOIN course_instance ci ON ci.id = p.course_instance_id
JOIN semester s ON s.id = ci.semester_id
JOIN module m ON m.id = ci.module_id
WHERE p.kind = 'OTHER'
  AND s.phase <> 'FINAL'
  AND m.course_type IN ('DEPENDS_ON_SUBJECT', 'SELF_STUDY')
  AND p.teaching_hours = m.contact_hours_per_week
  AND EXISTS (SELECT 1 FROM module_component mc WHERE mc.module_id = m.id)
  AND NOT EXISTS (SELECT 1 FROM module_component mc WHERE mc.module_id = m.id AND mc.kind = 'OTHER')
  AND NOT EXISTS (SELECT 1 FROM assignment a WHERE a.instance_part_id = p.id);

CREATE TEMP TABLE repaired_instance ON COMMIT DROP AS
SELECT course_instance_id, count(*)::int AS removed
FROM guessed_part
GROUP BY course_instance_id;

DELETE FROM instance_part WHERE id IN (SELECT id FROM guessed_part);

INSERT INTO instance_part (course_instance_id, kind, position, teaching_hours, serves_sibling_tracks)
SELECT r.course_instance_id, mc.kind, 1000 + mc.position, mc.teaching_hours, false
FROM repaired_instance r
JOIN course_instance ci ON ci.id = r.course_instance_id
JOIN module_component mc ON mc.module_id = ci.module_id
CROSS JOIN LATERAL generate_series(1,
    CASE
        WHEN mc.position = (SELECT min(x.position) FROM module_component x
                            WHERE x.module_id = mc.module_id AND x.kind <> 'LECTURE')
        THEN r.removed
        ELSE 1
    END) AS g
WHERE NOT EXISTS (
        SELECT 1 FROM instance_part p
        WHERE p.course_instance_id = r.course_instance_id AND p.kind = mc.kind)
  AND NOT (mc.kind = 'LECTURE' AND EXISTS (
        SELECT 1
        FROM course_instance sib
        JOIN instance_part sp ON sp.course_instance_id = sib.id
        WHERE sib.semester_id = ci.semester_id
          AND sib.module_id = ci.module_id
          AND sib.programme_id = ci.programme_id
          AND sib.id <> ci.id
          AND sp.kind = 'LECTURE'
          AND sp.serves_sibling_tracks));

UPDATE instance_part p
SET position = n.rn - 1, updated_at = now()
FROM (
    SELECT ip.id,
           row_number() OVER (
               PARTITION BY ip.course_instance_id
               ORDER BY array_position(
                            ARRAY['LECTURE', 'LAB', 'EXERCISE', 'SEMINAR', 'PROJECT', 'OTHER'], ip.kind),
                        ip.position, ip.created_at) AS rn
    FROM instance_part ip
    WHERE ip.course_instance_id IN (SELECT course_instance_id FROM repaired_instance)
) n
WHERE n.id = p.id;

-- +goose Down
-- Nothing to undo: putting guessed parts back would restore the wrong numbers this repaired.
SELECT 1;
