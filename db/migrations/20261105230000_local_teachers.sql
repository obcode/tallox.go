-- Migration: teachers entered by hand, until the ZPA lists them.
--
-- A lecturer on contract is in the examination office's teacher list at the latest once they
-- hold a course — but planning happens before that. Somebody newly won for next semester is
-- assigned in the spring and appears in the source in the autumn, and until then the assignment
-- screen had nobody to pick.
--
-- The `teacher` table was built for this from the start: `zpa_teacher_ref` is nullable "because a
-- teacher entered by hand has to be possible" (migration 9). What it lacked is a way to say which
-- rows those are, and a rule for the day the source catches up.
--
-- WHY THE SAME TABLE AND NOT A TABLE OF ITS OWN
--
-- The same argument migration 11 makes for local modules: assignments, competences, the
-- contract-lecturer hours and every list of people who teach already point at `teacher`. A row
-- of a second kind would be a second branch at each of them, for rows that behave exactly alike.
--
-- WHAT HAPPENS WHEN THE ZPA CATCHES UP
--
-- The projection adopts the local row: same address, so same human. It writes the ZPA reference
-- into the existing row and switches the source, and the ordinary upsert then overwrites the
-- names and flags with the examination office's. The id stays, so the assignments and
-- competences made in the meantime stay where they are. Reported as LOCAL_TEACHER_ADOPTED.
--
-- +goose Up

ALTER TABLE teacher
    -- Where the row comes from. A CHECK rather than an enum, as for module.source.
    ADD COLUMN source text NOT NULL DEFAULT 'ZPA'
        CONSTRAINT teacher_source_is_known CHECK (source IN ('ZPA', 'LOCAL')),
    -- Who entered it — an audit column and nothing else. Deliberately without the foreign key
    -- module.created_by has: no key runs from teacher to person (store test
    -- TestProjectingTeachersAdmitsNobody), because the link between the two is the address,
    -- and a key here would be the first column a later reader could mistake for that link.
    ADD COLUMN created_by uuid;

-- Nothing writes a row without a reference today, but the column has always allowed it, and a
-- migration that fails on a row somebody inserted by hand is a server that does not start.
UPDATE teacher SET source = 'LOCAL' WHERE zpa_teacher_ref IS NULL;

ALTER TABLE teacher
    -- The load-bearing one, in both directions: a local row carries no reference, and a row
    -- without a reference is local. Adoption flips both in one statement.
    ADD CONSTRAINT teacher_source_matches_ref
        CHECK ((source = 'ZPA') = (zpa_teacher_ref IS NOT NULL)),
    -- The address is a local row's identity and the only way the projection can ever recognise
    -- it. A row nobody can recognise would stay local forever.
    ADD CONSTRAINT teacher_local_has_mail
        CHECK (source = 'ZPA' OR mail IS NOT NULL),
    -- retired_at means "a successful import stopped mentioning it", which the import can never
    -- say about a row it never mentioned. A local row nobody needs is `active = false`.
    ADD CONSTRAINT teacher_local_is_never_retired
        CHECK (source = 'ZPA' OR retired_at IS NULL);

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
        'LANGUAGE_UNMAPPED',
        -- A teacher entered by hand whom the source now lists under the same address. Merged
        -- into the existing row, so what was planned with it stays.
        'LOCAL_TEACHER_ADOPTED'
    ));

-- +goose Down

DELETE FROM zpa_catalogue_projection_note WHERE code = 'LOCAL_TEACHER_ADOPTED';
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
        'LANGUAGE_UNMAPPED'
    ));

-- Local rows stay: the previous schema allows a row without a reference, and assignments point
-- at them.
ALTER TABLE teacher
    DROP CONSTRAINT teacher_local_is_never_retired,
    DROP CONSTRAINT teacher_local_has_mail,
    DROP CONSTRAINT teacher_source_matches_ref,
    DROP COLUMN created_by,
    DROP COLUMN source;
