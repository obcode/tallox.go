-- Migration: Courses in English (CiE) — a cohort explicitly listed as one, per cohort.
--
-- NOT THE SAME AS TAUGHT IN ENGLISH
--
-- taught_in_english says which language a cohort runs in. CiE is a listing: the faculty offers the
-- cohort as a course in English, for incoming exchange students among others, and that is decided
-- and set by hand. A module the catalogue states as German may well be offered as CiE where its
-- description names English as a possible language, and a cohort that happens to run in English is
-- not listed as CiE until somebody says so. Hence a column of its own rather than a reading of the
-- other one.
--
-- ON THE COHORT, LIKE THE LANGUAGE
--
-- One value per course instance, because one cohort of a module can be listed and its parallel
-- cohort not. A plain boolean with a default rather than an override: there is no catalogue value
-- to fall back on.
--
-- Additive only. The previous image inserts cohorts without naming the column and gets false.

-- +goose Up

ALTER TABLE course_instance
    ADD COLUMN cie boolean NOT NULL DEFAULT false;

-- +goose Down

ALTER TABLE course_instance
    DROP COLUMN cie;
