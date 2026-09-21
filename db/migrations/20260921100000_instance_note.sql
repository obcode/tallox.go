-- Migration 22: a note on a course instance.
--
-- The demand table shows what a programme offers and nothing about why. Most rows need no why;
-- the ones that do are exactly the ones a reader stumbles over. The case that asked for this: in
-- a summer where a degree changes its regulations, one module runs for the old fourth semester
-- and the new second one at once, and four cohorts of one module read as a planning mistake to
-- everybody except the person who planned them. A sentence beside the row is what turns that
-- into a decision.
--
-- ONE COLUMN, NOT A TABLE
--
-- The note is a fact about the row the way the cohort year is: the person writes it once for
-- "this module, this programme, this semester", and the planning table applies it to every
-- cohort of that row, exactly as it does the cohort year. Each cohort carries its own copy for
-- the same reason each carries its own year — the cohort is the row of this table, and a note
-- table keyed by (semester, module, programme) would be a second identity for a thing that has
-- one already.
--
-- NOT CONFIDENTIAL, NOT PERSONNEL DATA
--
-- The demand is readable by anybody with an account, through both doors, and so is this. A note
-- about *why a module is offered* is planning; anything about a person belongs in the wish note
-- (own-only) or nowhere. The length is bounded because a free-text column without a bound is a
-- place somebody pastes a document into.

-- +goose Up

ALTER TABLE course_instance
    ADD COLUMN note text NOT NULL DEFAULT '',
    ADD CONSTRAINT course_instance_note_length CHECK (char_length(note) <= 2000);

-- +goose Down

ALTER TABLE course_instance
    DROP CONSTRAINT course_instance_note_length,
    DROP COLUMN note;
