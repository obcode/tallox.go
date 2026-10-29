-- Migration: the other language a module may be taught in, and CiE on a wish.
--
-- WHAT THE CATALOGUE SAYS ABOUT LANGUAGE
--
-- The examination office states two things per module: std_lang, the default, and languages,
-- which despite its name is the one *other* language the module may be taught in. Measured on the
-- production data on 2026-10-29: languages is never equal to std_lang and never a list — German by
-- default with English possible (295), German only (190), English by default with German possible
-- (23), English only (1). So "English is possible" is std_lang = EN or languages = EN, and reading
-- only the second would lose every module whose default is English.
--
-- The module keeps it the way it keeps the default: the mapped value, NULL where the source says
-- nothing or something this version does not recognise, and the raw phrase beside it so an
-- unrecognised one is reported (LANGUAGE_UNMAPPED, which now covers both) rather than lost.
--
-- WHY IT MATTERS
--
-- A cohort may be marked as taught in English, or listed as a course in English (CiE), only where
-- the module description names English — the faculty's rule, decided 2026-10-29. The rule is in
-- domain.Module.EnglishPossible; this is the fact it reads.
--
-- CIE ON A WISH
--
-- A colleague may say with a wish that they would offer the cohort as a course in English. An
-- offer, not a decision: it changes nothing about the cohort and is no criterion of the
-- assignment. On the wish row, so that it is exactly as confidential as the wish.
--
-- Additive only. The previous image projects modules and writes wishes without naming the columns
-- and gets NULL, '' and false.

-- +goose Up

ALTER TABLE module
    ADD COLUMN other_language text,
    ADD CONSTRAINT module_other_language_is_known CHECK (other_language IN ('DE', 'EN')),
    ADD COLUMN other_language_source text NOT NULL DEFAULT '';

ALTER TABLE wish
    ADD COLUMN cie boolean NOT NULL DEFAULT false;

-- +goose Down

ALTER TABLE wish
    DROP COLUMN cie;

ALTER TABLE module
    DROP CONSTRAINT module_other_language_is_known,
    DROP COLUMN other_language,
    DROP COLUMN other_language_source;
