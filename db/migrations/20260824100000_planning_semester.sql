-- Migration 10: which semester the faculty is planning right now.
--
-- Everything in this system is "of a semester", and until now every screen and every script
-- had to start by choosing one. That is a question the faculty has a single answer to at any
-- moment — the planning runs on one semester at a time, and the others are either finished or
-- not yet begun — so asking it once, here, is cheaper than asking it on every page.
--
-- Deliberately not derived. The obvious rule, "the newest semester that is not FINAL", is
-- ambiguous precisely when it matters: while the summer is being assigned, the winter is
-- already in demand planning, and both are open. Which of the two the faculty means is a
-- decision somebody makes, the same way the phase is, and this column is its record.
--
-- # Why a row is inserted here
--
-- Nobody creates a semester — a semester is there the way next March is there, and the row is
-- the protocol of a decision about one. The INSERT below is not an exception to that; it is an
-- instance of it. *This* is the decision: the faculty plans 2027-SS. Recording it in the
-- migration rather than leaving it to a click after the deploy avoids a window in which
-- planningSemester is null and every screen falls back to no preselection at all.
--
-- # Rollback
--
-- The previous image runs against this schema unchanged: EnsureSemester writes only `code` and
-- the default carries the new column, and every read lists its columns explicitly. Down drops
-- the index and the columns and leaves the 2027-SS row standing — a Down that deleted it would
-- delete a planning.
--
-- +goose Up

ALTER TABLE semester
    -- The one that is being planned. A boolean rather than a pointer from somewhere else,
    -- because "at most one row is true" is expressible as an index and "exactly one row of a
    -- settings table points here" is not.
    ADD COLUMN is_planning_semester boolean NOT NULL DEFAULT false,

    -- When it was made the planning semester, and by whom. The audit is on the row, as it is
    -- for the phase: this system has no audit-log table yet, and the two columns that answer
    -- "who moved this, and when" are worth having on the day somebody asks.
    ADD COLUMN planning_set_at timestamptz,
    ADD COLUMN planning_set_by uuid REFERENCES person (id) ON DELETE SET NULL;

-- At most one. A CHECK cannot say this — it sees one row at a time — and a trigger would say it
-- in a place nobody reads. A partial unique index over the constant true is the standard form,
-- and it is also the lock two simultaneous setters serialise on.
CREATE UNIQUE INDEX semester_one_planning_semester_idx
    ON semester (is_planning_semester) WHERE is_planning_semester;

-- The first decision, taken here. ON CONFLICT rather than a plain INSERT because the row may
-- already exist — the development database has been planned in for days — and because it makes
-- Up idempotent, which is what makes Down/Up survivable.
INSERT INTO semester (code, is_planning_semester, planning_set_at)
VALUES ('2027-SS', true, now())
ON CONFLICT (code) DO UPDATE
SET is_planning_semester = true,
    planning_set_at      = COALESCE(semester.planning_set_at, now());

-- +goose Down

DROP INDEX semester_one_planning_semester_idx;

ALTER TABLE semester
    DROP COLUMN planning_set_by,
    DROP COLUMN planning_set_at,
    DROP COLUMN is_planning_semester;
