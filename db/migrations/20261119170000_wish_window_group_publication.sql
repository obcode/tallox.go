-- +goose Up

-- A subject group's lead shows the wishes on the group's modules to everybody in the group, once
-- its wish round is shut — before the dean's office publishes the whole semester.
--
-- Decided 2026-11-19: the wish rounds run per subject group and some groups finish weeks before
-- the last one, and the members of a group that has finished want to see who offered what on
-- their modules. The first-come argument behind the confidentiality rule holds, because nobody
-- can enter anything in a shut round; which is why publishing requires the round to be shut at
-- that moment (enforced in PublishSubjectGroupWishes, not here: reopening the round afterwards
-- stays possible, the same as after the semester publication, and is the lead's deliberate act).
--
-- This narrows the sentence in 20260825100000_subject_groups.sql that membership is no licence to
-- read unpublished wishes: it still is none on its own. Only the lead's explicit, irreversible act
-- after the round is shut makes it one, only for the modules of that group, and only for people
-- in that group (person_subject_group), never for the rest of the faculty.
--
-- Additive: the previous image never reads the columns.
ALTER TABLE wish_window
    ADD COLUMN wishes_published_at timestamptz,
    ADD COLUMN wishes_published_by uuid REFERENCES person (id) ON DELETE SET NULL;

-- +goose Down

ALTER TABLE wish_window
    DROP COLUMN wishes_published_by,
    DROP COLUMN wishes_published_at;
