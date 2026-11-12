-- Migration: advance wishes noted down after the programme released its demand.
--
-- Until now an advance wish was refused once the programme had released that semester's demand.
-- Decided on 2026-11-10: a released demand can still grow — an elective is added, a cohort is
-- split — and the programme's lead wants to see who would teach a module that is not in it yet.
-- So an advance wish stays possible after the release for every module the programme has no
-- cohort of; for one it has, the wish itself is still what one enters.
--
-- WHY A FLAG
--
-- An advance wish noted down before the release was stated before any subject group's wish round
-- on this demand could have opened or shut, which is why the conversion never asked about the
-- round. One noted down after the release can be stated after the round has closed. Converting it
-- regardless would turn the closed round into a door one walks around: note it down, wait for
-- the module to enter the demand. So the conversion asks the round for these, and only for these
-- — and the row has to say which kind it is. A timestamp compared with the release does not: the
-- release's completed_at moves when the lead announces again, and withdrawing it deletes the row.
--
-- Fixed when the row is inserted and never updated: changing a pending one later does not make
-- it a later statement. Existing rows are all pre-release ones, which the DEFAULT says.
--
-- +goose Up

ALTER TABLE advance_wish ADD COLUMN noted_after_release boolean NOT NULL DEFAULT false;

-- +goose Down

ALTER TABLE advance_wish DROP COLUMN noted_after_release;
