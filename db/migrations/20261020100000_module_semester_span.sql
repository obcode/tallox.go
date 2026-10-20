-- Migration 34: modules that run over two semesters.
--
-- The examination office states such a module as one row with the hours of both semesters added
-- up: "Wirtschaftsinformatik" carries 8 SWS and is taught 4 + 4, in the winter and in the summer.
-- Planned, it is an instance in each semester with half the hours — confirmed by the faculty on
-- 2026-10-20. Nothing in the source says which modules these are, so the faculty marks them, once
-- per module, like the split and the PBLV mark. The import never touches the column.
--
-- A count of semesters rather than a flag, because it is the divisor: the catalogue's figure over
-- this is what one semester's instance holds. Bounded to two because nothing longer exists here,
-- and a larger value would be a typo dividing a module's hours by it.

-- +goose Up

ALTER TABLE module
    ADD COLUMN semester_span smallint NOT NULL DEFAULT 1
        CONSTRAINT module_semester_span_is_one_or_two CHECK (semester_span BETWEEN 1 AND 2);

-- +goose Down

ALTER TABLE module
    DROP COLUMN IF EXISTS semester_span;
