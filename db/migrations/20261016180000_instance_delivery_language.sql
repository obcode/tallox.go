-- Migration 28: how a cohort is taught — in a block or across the semester, and in which language.
--
-- Both were asked for at the assignment: the subject group lead who staffs a cohort is the one
-- who knows that this year it runs as a block week, or that this cohort is the English one. The
-- old planning sheet carried both as free text in the note cells ("TG1 - Block", "Courses in
-- English"), which is exactly what a screen cannot filter, count or export as a column.
--
-- ON THE COHORT, NOT ON THE PART
--
-- One value per course instance, holding for all of its parts. A laboratory group that runs as a
-- block while the lecture runs weekly exists, and the note beside the row still says so; what the
-- faculty plans, publishes and exports is the cohort.
--
-- DELIVERY: TWO VALUES AND A DEFAULT
--
-- Across the semester is what nearly every cohort is, so it is the default and only the block is
-- ever set by hand. Two values rather than a boolean, because a third way of delivering teaching
-- is a plausible future and an enum column grows where a boolean would have to be replaced.
--
-- LANGUAGE: AN OVERRIDE OF WHAT THE CATALOGUE SAYS
--
-- The examination office records a default language per module (std_lang). That is the default
-- here too, and the cohort stores only a deviation from it: NULL means "as the catalogue says".
-- Copying the catalogue's value into every cohort at the time it is declared would go stale the
-- moment the catalogue changes and nobody could tell a decision from a stale copy.
--
-- The module gains the language the way it has the frequency and the course type: the mapped
-- value, NULL where the source says nothing or something this version does not recognise, and
-- the raw phrase beside it so an unrecognised one is reported rather than lost.
--
-- Additive only. The previous image inserts cohorts and modules without naming these columns and
-- gets the defaults; it skips a report note it does not know (store.CatalogueProjections).

-- +goose Up

ALTER TABLE course_instance
    ADD COLUMN delivery text NOT NULL DEFAULT 'WEEKLY',
    ADD CONSTRAINT course_instance_delivery_is_known CHECK (delivery IN ('WEEKLY', 'BLOCK')),
    ADD COLUMN taught_in_english boolean;

ALTER TABLE module
    ADD COLUMN teaching_language text,
    ADD CONSTRAINT module_teaching_language_is_known CHECK (teaching_language IN ('DE', 'EN')),
    ADD COLUMN teaching_language_source text NOT NULL DEFAULT '';

ALTER TABLE zpa_catalogue_projection_note DROP CONSTRAINT zpa_catalogue_projection_note_code_is_known;
ALTER TABLE zpa_catalogue_projection_note ADD CONSTRAINT zpa_catalogue_projection_note_code_is_known
    CHECK (code IN (
        'MODULE_WITHOUT_HOME_PROGRAMME',
        'PROGRAMME_CODE_MALFORMED',
        'PROGRAMME_WITHOUT_REGULATIONS',
        'MODULE_WITHOUT_NAME',
        'MODULE_INACTIVE',
        'ASSOCIATION_WITH_UNKNOWN_REGULATIONS',
        'FREQUENCY_UNMAPPED',
        'COURSE_TYPE_UNMAPPED',
        'MIN_SEMESTER_CONFLICT',
        'DUTY_CONFLICT',
        'MODULE_RESPONSIBLE_UNKNOWN',
        'TEACHER_WITHOUT_MAIL',
        -- A default language the mapping does not recognise. Became "no language stated", which
        -- reads as German.
        'LANGUAGE_UNMAPPED'
    ));

-- +goose Down

DELETE FROM zpa_catalogue_projection_note WHERE code = 'LANGUAGE_UNMAPPED';
ALTER TABLE zpa_catalogue_projection_note DROP CONSTRAINT zpa_catalogue_projection_note_code_is_known;
ALTER TABLE zpa_catalogue_projection_note ADD CONSTRAINT zpa_catalogue_projection_note_code_is_known
    CHECK (code IN (
        'MODULE_WITHOUT_HOME_PROGRAMME',
        'PROGRAMME_CODE_MALFORMED',
        'PROGRAMME_WITHOUT_REGULATIONS',
        'MODULE_WITHOUT_NAME',
        'MODULE_INACTIVE',
        'ASSOCIATION_WITH_UNKNOWN_REGULATIONS',
        'FREQUENCY_UNMAPPED',
        'COURSE_TYPE_UNMAPPED',
        'MIN_SEMESTER_CONFLICT',
        'DUTY_CONFLICT',
        'MODULE_RESPONSIBLE_UNKNOWN',
        'TEACHER_WITHOUT_MAIL'
    ));

ALTER TABLE module
    DROP CONSTRAINT module_teaching_language_is_known,
    DROP COLUMN teaching_language,
    DROP COLUMN teaching_language_source;

ALTER TABLE course_instance
    DROP CONSTRAINT course_instance_delivery_is_known,
    DROP COLUMN delivery,
    DROP COLUMN taught_in_english;
