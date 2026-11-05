-- Migration: programme codes may carry a hyphen.
--
-- The examination office now lists a programme as "DC-M". The old rule allowed upper-case
-- letters, digits and the dot only, so the projection filtered the code out and, with it, every
-- module whose home programme it is — silently but for a PROGRAMME_CODE_MALFORMED note on the
-- import page. The new character class is the subject-group one without the underscore.
--
-- The projection's filter in db/queries/catalogue.sql (ProjectProgrammes, MalformedProgrammeCodes)
-- carries the same expression and has to change with this one; a store test projects a hyphenated
-- code to keep the two in step.

-- +goose Up

ALTER TABLE programme DROP CONSTRAINT programme_code_is_short;
ALTER TABLE programme ADD CONSTRAINT programme_code_is_short
    CHECK (code ~ '^[A-Z][A-Z0-9.-]{0,9}$');

-- +goose Down

-- Fails while a hyphenated programme exists, on purpose: such a programme carries demand,
-- instances and scopes, and a down migration that deleted them would destroy planning to get
-- rid of a constraint. Whoever rolls back decides about those rows first.
ALTER TABLE programme DROP CONSTRAINT programme_code_is_short;
ALTER TABLE programme ADD CONSTRAINT programme_code_is_short
    CHECK (code ~ '^[A-Z][A-Z0-9.]{0,9}$');
