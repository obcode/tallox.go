-- Migration 15: wishes — the table the confidentiality rule was written for.
--
-- Everything about wish visibility has been in internal/policy since the first release, rendered
-- to a golden file and defended in a doc comment, with nothing to point at. This is the thing it
-- points at.
--
-- A wish is one person's interest in one instance_part. Not in an instance: the faculty's own
-- sentence is "one holds the lecture, another the laboratory", so the assignable unit is the part
-- and so is the unit of interest.
--
-- ONLY YOURSELF
--
-- Decided 2026-08-25, and it is why there is no entered_by column here. A wish registered on
-- somebody's behalf is not an expression of interest — it is somebody else's opinion about them,
-- and the process already has a place for that: the assignment. The proposal in the decision
-- paper was "nur selbst" and the faculty took it.
--
-- The recoverable direction, for whoever revisits this: with this rule every row's provenance is
-- known (entered_by would be person_id), so adding the column later is a defined backfill. The
-- other direction is not — once wishes entered on somebody's behalf exist, nothing distinguishes
-- them from the person's own afterwards.
--
-- It also makes one refusal sayable that would otherwise be a leak. See the UNIQUE below.
--
-- WHAT THIS TABLE MAKES REACHABLE
--
-- Nothing has ever pointed at a course instance, so DeleteCourseInstance's INSTANCE_IN_USE branch
-- has been unreachable code since it was written. This is the first table to point, and the path
-- is two steps and therefore not obvious:
--
--     DELETE course_instance → CASCADE to instance_part → RESTRICT from wish → SQLSTATE 23503
--
-- internal/store maps that to domain.ErrInstanceInUse, whose message deliberately says only that
-- something hangs off the instance — never what, never how many. "This instance has three wishes"
-- is the confidential fact with the names taken out.
--
-- WHAT IS DELIBERATELY NOT A COLUMN
--
-- The semester, the study programme and the subject group. All three are reachable —
-- instance_part → course_instance for the first two, and on through module for the third — and
-- the first two are immutable by construction (changeCourseInstance edits the cohort and the
-- cohort year, and nothing else). The third is not immutable and that is exactly why it must not
-- be copied: re-cutting a subject group has to change who is responsible for the wishes on its
-- modules, and a stored copy would freeze responsibility at the moment somebody registered
-- interest.
--
-- +goose Up

CREATE TABLE wish (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    -- RESTRICT, and this is the line that makes a withdrawal refusable. A part somebody wants is
    -- not a part a programme lead may quietly remove; the refusal names no wish and no number.
    instance_part_id uuid NOT NULL REFERENCES instance_part (id) ON DELETE RESTRICT,

    -- CASCADE, unlike the line above, and the asymmetry is the whole question each answers. A
    -- person's wishes are that person's data and go with them. An instance's wishes belong to the
    -- people who registered them, and the instance may not take them.
    --
    -- Nobody is deleted in practice — person.active is the removal this system has — so this is
    -- the reading for the case that is not supposed to happen rather than the one that does.
    person_id uuid NOT NULL REFERENCES person (id) ON DELETE CASCADE,

    -- How much this person wants it: 1 unbedingt, 2 gerne, 3 notfalls.
    --
    -- Three fixed levels rather than a rank per person and semester. A rank is more expressive and
    -- costs a reordering dance on every insert in the middle, a uniqueness constraint whose
    -- violation needs a generic message on the write path, and a number nobody can read off a
    -- list without a legend. Ties are allowed and are the common case: somebody wants four things
    -- equally and would rather say so than invent an order.
    priority smallint NOT NULL DEFAULT 2,

    -- The owner's own words: "would rather have the Tuesday group", "only if nobody else wants
    -- it". Free text and read by whoever may read the wish, which is the same rule as the row.
    note text NOT NULL DEFAULT '',

    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    -- One entry per person per part. Registering twice is changing your mind, not a second wish.
    --
    -- A UNIQUE violation is normally a leak on this table — "somebody has already registered" is
    -- the confidential fact — and here it is not, because of the decision above: with only-self,
    -- the only person who can trip this constraint is the owner, about their own row. So
    -- internal/domain may say "du hast dich hier schon eingetragen" in plain words.
    --
    -- That sentence hangs on the rule and not on the table. If proxy entry is ever allowed, the
    -- message has to become generic in the same commit.
    UNIQUE (instance_part_id, person_id),

    CONSTRAINT wish_priority_is_a_level CHECK (priority BETWEEN 1 AND 3),
    -- Long enough for a sentence, short enough that nobody keeps a document in here.
    CONSTRAINT wish_note_is_short CHECK (length(note) <= 500)
);

-- "My wishes in this semester" — the query the wish screen starts from, and the one every
-- lecturer runs. The join to the instance is what carries the semester, so the person is the
-- selective half and belongs first.
CREATE INDEX wish_person_idx ON wish (person_id);

-- The other direction: everybody who wants this part. Used by the planning screens after
-- publication, and by the confidentiality filter before it — which is the same query with a
-- predicate, deliberately, so that the count and the list cannot come apart.
CREATE INDEX wish_part_idx ON wish (instance_part_id);

-- +goose Down

DROP TABLE wish;
