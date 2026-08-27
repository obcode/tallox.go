-- Migration 18: the planning opens and closes where the planning happens.
--
-- Until now one column decided when anything could be written: semester.phase, one value for the
-- whole faculty. That was wrong about how the faculty works, and the correction came from it
-- directly (2026-08-28): demand, wishes and assignment are **in principle always possible**, and
-- what opens and closes is decided per study programme and per subject group, by whoever carries
-- that responsibility.
--
-- The phase stays. It keeps exactly one hard meaning — FINAL is finished — and is otherwise a
-- statement about where a semester roughly stands. What may be written is now decided by the two
-- tables below.
--
-- WHY TWO TABLES AND NOT ONE
--
-- They are two different things and only look alike:
--
--     demand_completion  an ANNOUNCEMENT. "IF for SS29 is settled, as far as I know today."
--                        Blocks nothing. Demand may be added afterwards, and adding some does
--                        not make the announcement a lie — it makes it out of date, which is
--                        what withdrawing it is for.
--     wish_window        a DOOR. Closed means nobody registers interest in this subject's
--                        instances any more, and it can be opened again the same afternoon.
--
-- Giving them one shape would force one of the two to grow a meaning it does not have.
--
-- AN ABSENT ROW MEANS OPEN, AND THAT IS THE OPPOSITE OF THE USUAL READING
--
-- Everywhere else in this schema, absent means no: an unscoped grant permits nothing, an unknown
-- phase permits nothing, an unknown scope string matches no branch. Here absent means **open**,
-- and it has to.
--
-- The rule the faculty stated is "in principle always possible"; closing is the intervention. A
-- fail-closed default would make the deploy of this migration the moment every subject group in
-- the faculty silently stopped accepting wishes — a permission change nobody chose, arriving as a
-- side effect of a schema change. The same argument the semester row makes about not existing:
-- no row means nobody has decided anything, and what nobody has decided is the ordinary state.
--
-- It also fails in the direction that is repairable. A wish window that is open when it should
-- have been shut costs an entry somebody has to talk about; one that is shut when it should have
-- been open costs a colleague the belief that the tool is broken, silently, until somebody asks.
--
-- WHAT IS DELIBERATELY NOT HERE
--
-- A window for the demand and one for the assignment. Both are always open until the semester is
-- finished, and a table that could close them would be a table somebody eventually closes — the
-- faculty asked for the opposite. If that changes, the shape is here to copy.
--
-- +goose Up

CREATE TABLE demand_completion (
    -- The grain: one study programme's demand for one semester. Not the faculty's, because the
    -- programmes finish at different times and the whole point of announcing it is to tell the
    -- others that *this* one is done.
    semester_id uuid NOT NULL REFERENCES semester (id) ON DELETE RESTRICT,
    programme_id uuid NOT NULL REFERENCES programme (id) ON DELETE RESTRICT,

    -- When it was announced. Moves when somebody re-announces after adding something, because
    -- what a reader wants to know is how fresh the statement is.
    completed_at timestamptz NOT NULL DEFAULT now(),
    -- Who said so. NULL once that person's row is gone; the statement stays.
    completed_by uuid REFERENCES person (id) ON DELETE SET NULL,

    -- Withdrawing the announcement is deleting the row rather than a flag, and that is the
    -- difference between this table and the one below. "Not announced" and "announced, then
    -- withdrawn" are the same state — the demand is simply not settled — whereas a wish window
    -- that was closed and reopened is not the same as one nobody ever touched: somebody made two
    -- decisions, and the second one is worth being able to see.
    PRIMARY KEY (semester_id, programme_id)
);

CREATE TABLE wish_window (
    -- The grain: one subject group's wishes for one semester. The subject group and not the
    -- programme, because it is the subject group lead who runs the wish round for their subjects
    -- and who then fills the instances from it.
    --
    -- A module in no subject group therefore has no window and stays open. That is the ordinary
    -- state until the catalogue is sorted, and it is the same fail-open reading as an absent row.
    semester_id uuid NOT NULL REFERENCES semester (id) ON DELETE RESTRICT,
    subject_group_id uuid NOT NULL REFERENCES subject_group (id) ON DELETE RESTRICT,

    -- false is the only reason this row usually exists. true is a row that was closed and opened
    -- again — kept rather than deleted, so that the second decision is visible.
    open boolean NOT NULL,

    changed_at timestamptz NOT NULL DEFAULT now(),
    changed_by uuid REFERENCES person (id) ON DELETE SET NULL,

    PRIMARY KEY (semester_id, subject_group_id)
);

-- "Which programmes are settled for this semester" and "which of my subjects are still open" —
-- both are read per semester, on every planning screen.
CREATE INDEX demand_completion_semester_idx ON demand_completion (semester_id);
CREATE INDEX wish_window_semester_idx ON wish_window (semester_id);

-- +goose Down

DROP TABLE wish_window;
DROP TABLE demand_completion;
