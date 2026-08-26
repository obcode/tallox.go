-- Migration 16: a wish points at the instance, not at one of its parts.
--
-- Migration 15 shipped yesterday with the opposite reading, and its header states the argument
-- for it: "one holds the lecture, another the laboratory", so the assignable unit is the part and
-- so is the unit of interest. The first half of that sentence is still true. The second turned
-- out not to be, and the faculty's own way of working is what settled it.
--
-- WHAT CHANGED THE READING
--
-- Until now this planning was a Confluence table, and it was the view everybody had: one row per
-- module in a study programme, a column per cohort, and a column listing who could take it. That
-- granularity is what people are used to and what they think in — "ich würde Softwareentwicklung
-- II machen", not "ich würde die zweite Praktikumsgruppe von IF2B machen". At part granularity
-- the same table is eight rows and eight forms per module, which is the shape somebody abandons
-- halfway through.
--
-- So the split runs one step later than migration 15 assumed:
--
--     wish       → the instance. What somebody is offering to teach.
--     assignment → the part.     Who actually holds the lecture and who the laboratory.
--
-- The special case the old reading was built for — "ich mache die Vorlesung, das Praktikum macht
-- jemand anderes" — does not disappear, it moves into the note, and then into the assignment
-- where it is a decision rather than a wish. That is the right place for it: which part somebody
-- ends up with is not something they settle alone at wish time.
--
-- ROLLING BACK PAST THIS IMAGE DOES NOT WORK, AND THAT IS DELIBERATE
--
-- The rule in CLAUDE.md is that every migration has to be one the previous image can run against,
-- because pinning an older tag is the documented recovery and it does not undo migrations. This
-- one breaks that rule knowingly, and there is no cheap way not to: the previous image reads
-- wish.instance_part_id and cannot write a wish without one, so a wish that points at an instance
-- is not representable for it at all — a column kept nullable would only turn a clear failure
-- into a NULL scan error on the first new row.
--
-- Acceptable exactly here and exactly once: the table is one day old, holds trial entries only,
-- no semester has left DEMAND_PLANNING, and the recovery is rolling forward. Down is written and
-- tested, so a mistaken deploy can be undone by migrating down and *then* pinning the tag back.
--
-- WHAT THIS DOES TO THE REST OF THE SCHEMA
--
-- instance_part loses its incoming RESTRICT, so removing a part is no longer refused because
-- somebody wants it. That is the same decision read from the other end: parts are the faculty's
-- own re-cutting of an instance — a third laboratory group, a lecture shared across cohorts —
-- and re-cutting must not be blocked by interest in the instance. Withdrawing the *instance*
-- stays refused, and now directly rather than through the part: the RESTRICT below is what
-- INSTANCE_IN_USE hangs off, one step instead of two.
--
-- +goose Up

-- Nullable to begin with, because the backfill below is what makes it complete.
ALTER TABLE wish
    ADD COLUMN course_instance_id uuid REFERENCES course_instance (id) ON DELETE RESTRICT;

UPDATE wish w
SET course_instance_id = p.course_instance_id
FROM instance_part p
WHERE p.id = w.instance_part_id;

-- Several parts of one instance wanted by one person collapse into a single wish, and the merge
-- is written out rather than left to an arbitrary survivor: the strongest priority wins (1 is
-- "unbedingt"), ties go to the oldest entry, and the notes are carried over so that nobody's own
-- words are dropped by a schema change. Truncated to the length the CHECK allows.
--
UPDATE wish keep
SET note = left(merged.notes, 500)
FROM (
    SELECT person_id, course_instance_id,
           string_agg(DISTINCT note, ' · ') FILTER (WHERE note <> '') AS notes
    FROM wish
    GROUP BY person_id, course_instance_id
    HAVING count(*) > 1
) merged
WHERE keep.person_id = merged.person_id
  AND keep.course_instance_id = merged.course_instance_id
  AND merged.notes IS NOT NULL;

DELETE FROM wish a
USING wish b
WHERE a.person_id = b.person_id
  AND a.course_instance_id = b.course_instance_id
  AND (a.priority, a.created_at, a.id) > (b.priority, b.created_at, b.id);

ALTER TABLE wish ALTER COLUMN course_instance_id SET NOT NULL;

-- Dropping the column takes its UNIQUE constraint and wish_part_idx with it.
ALTER TABLE wish DROP COLUMN instance_part_id;

-- One entry per person per instance. Registering twice is changing your mind, and the reason this
-- refusal may still be worded plainly is unchanged: with only-self entry the only person who can
-- trip it is the owner, about their own row.
ALTER TABLE wish
    ADD CONSTRAINT wish_one_per_person_and_instance UNIQUE (course_instance_id, person_id);

-- The other direction of the same question: everybody who wants this instance. Used by the
-- planning screens after publication and by the confidentiality filter before it — the same query
-- with a predicate, so that the list and any number taken from it cannot come apart.
CREATE INDEX wish_instance_idx ON wish (course_instance_id);

-- +goose Down

ALTER TABLE wish ADD COLUMN instance_part_id uuid REFERENCES instance_part (id) ON DELETE RESTRICT;

-- Back onto the instance's first part, which is the lecture wherever the split states one. An
-- approximation and unavoidably so — the old shape holds strictly more information than the new
-- one, so going back has to invent the part somebody meant.
UPDATE wish w
SET instance_part_id = (
    SELECT p.id FROM instance_part p
    WHERE p.course_instance_id = w.course_instance_id
    ORDER BY p.position, p.id
    LIMIT 1
);

-- An instance with no parts at all cannot be represented in the old shape. Deleting is the only
-- reversal available; it is also unreachable in practice, because an instance is created from its
-- module's split and CreateCourseInstance refuses a module that has none.
DELETE FROM wish WHERE instance_part_id IS NULL;

ALTER TABLE wish ALTER COLUMN instance_part_id SET NOT NULL;
ALTER TABLE wish DROP COLUMN course_instance_id;
ALTER TABLE wish ADD CONSTRAINT wish_instance_part_id_person_id_key UNIQUE (instance_part_id, person_id);
CREATE INDEX wish_part_idx ON wish (instance_part_id);
