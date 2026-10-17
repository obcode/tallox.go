-- Migration 29: a part divided between people, and a part another faculty teaches.
--
-- Asked for at the assignment: a module of 4 hours lecture and 4 hours laboratory whose lecture
-- and laboratory are each given half by this faculty and half by another one. Until now the
-- smallest unit anybody could be credited with was the part, and the part was what the module's
-- split said it was — 4 and 4.
--
-- DIVIDING IS CUTTING THE PART, NOT SHARING THE ASSIGNMENT
--
-- migration 17 decided "exactly one person per part" and named the way to split teaching: split
-- the unit. That stays. Dividing a part cuts off a piece of the same kind with some of its hours,
-- and the two together keep what the part had — so the faculty's figure cannot drift, and every
-- piece is staffed, published and exported like any other part.
--
-- divided_from_id is what says a part is such a piece, and it has a reader that needs it: the
-- number of laboratory groups is the number of laboratory parts, and without it a laboratory cut
-- in two would read as a second group on the next save of the planning screen. A piece always
-- points at the undivided part it was cut from — the store cuts from the root — so there are no
-- chains to follow.
--
-- The composite foreign key is what makes "same instance, same kind" a fact of the schema rather
-- than of the store. NO ACTION rather than RESTRICT, because the check has to run at the end of the
-- statement: withdrawing an instance deletes the part and its pieces in one cascade, and handing
-- a cohort's teaching to another one moves both rows in one UPDATE.
--
-- TAUGHT BY ANOTHER FACULTY
--
-- taught_by names who gives a part when it is not this faculty: "FK 04". Free text, because the
-- faculties are not master data here and the arrangement is the exception — a sentence the lead
-- types once, not a list somebody has to maintain. On the part rather than on an assignment,
-- because nobody here is assigned: the part is not ours to staff, so it must not show up as
-- open, cannot be filled, and its hours are not this faculty's teaching load.
--
-- teacher.faculty is not this. It is the examination office's hint about a person, empty for
-- more than half of them, and the question here is about a unit of teaching nobody of ours holds.
--
-- Additive only. The previous image never names these columns and gets NULL in both.

-- +goose Up

ALTER TABLE instance_part
    ADD CONSTRAINT instance_part_id_instance_kind_key UNIQUE (id, course_instance_id, kind),
    ADD COLUMN divided_from_id uuid,
    ADD CONSTRAINT instance_part_divided_from_same_instance_and_kind
        FOREIGN KEY (divided_from_id, course_instance_id, kind)
        REFERENCES instance_part (id, course_instance_id, kind)
        ON UPDATE NO ACTION ON DELETE NO ACTION,
    ADD CONSTRAINT instance_part_is_not_divided_from_itself CHECK (divided_from_id <> id),
    ADD COLUMN taught_by text,
    ADD CONSTRAINT instance_part_taught_by_is_trimmed
        CHECK (taught_by IS NULL OR (taught_by = btrim(taught_by) AND length(taught_by) BETWEEN 1 AND 80));

CREATE INDEX instance_part_divided_from_idx ON instance_part (divided_from_id)
    WHERE divided_from_id IS NOT NULL;

-- +goose Down

DROP INDEX IF EXISTS instance_part_divided_from_idx;

-- A piece is a part in its own right once its origin forgets it; nothing is lost but the link.
ALTER TABLE instance_part
    DROP CONSTRAINT IF EXISTS instance_part_taught_by_is_trimmed,
    DROP COLUMN IF EXISTS taught_by,
    DROP CONSTRAINT IF EXISTS instance_part_is_not_divided_from_itself,
    DROP CONSTRAINT IF EXISTS instance_part_divided_from_same_instance_and_kind,
    DROP COLUMN IF EXISTS divided_from_id,
    DROP CONSTRAINT IF EXISTS instance_part_id_instance_kind_key;
