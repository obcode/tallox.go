-- Migration 9: the people who teach, and the module's responsible person.
--
-- A fifth endpoint and one new relationship. The endpoint is routine — the cache takes another
-- kind, the nightly job picks it up because the list of kinds is one place. The relationship is
-- not routine, and the reason it needs saying out loud is the table it does *not* touch.
--
-- WHY A TABLE OF ITS OWN AND NOT ROWS IN `person`
--
-- `person` is the access control of this installation. Everybody with an HM login reaches the
-- door; who has a row decides who gets through it, and the people administration in the
-- interface is that decision rather than a convenience. Projecting 257 imported teachers into it
-- would move that decision to another institution's database, silently, as a side effect of an
-- import — and 6 of those 257 carry addresses the identity provider will never assert
-- (haw-landshut.de, doubleslash.de, calpoly.edu), so "teacher" and "person who can sign in" are
-- not the same set even in principle.
--
-- So a teacher is imported master data, like a module: cached, projected, read-only here, and
-- never a grant of anything. Whoever should also be able to *use* Tallox gets a person row the
-- way everybody else does — by somebody deciding it.
--
-- WHY THERE IS NO STORED LINK BETWEEN THE TWO
--
-- The obvious column would be `teacher.person_id`. It is absent, and the mail address is the
-- link instead — the same address the authentication resolves an identity by, `citext` on both
-- sides, unique on both sides. Two reasons:
--
--   * A stored link is only as fresh as the last projection. Somebody admitted this morning
--     would not be connected to their own modules until tonight, and nothing would say why.
--   * There is nothing to store that is not already there. The address is what makes the two
--     rows the same human; a uuid beside it would be a second opinion about that.
--
-- WHAT THE MODULE STORES, AND WHAT IT DELIBERATELY DOES NOT
--
-- `module.responsible_teacher_id` and nothing else. The source writes an address into
-- `module.responsible`, and 490 of 506 resolve to a teacher; the remaining 16 are 7 placeholders
-- ("N.N", "ex_prof_003") and 9 addresses of people the teacher list does not contain. Those 9
-- are **not** copied into a column. A mail address belongs in the table about people, not in the
-- table about modules — store.TestProjectionNeverCopiesTheResponsibleMail says so about `module`
-- and keeps saying it — and an unresolvable one is a finding for the import report rather than a
-- value to keep. The raw string stays in the cached payload, which is where untyped source data
-- lives.
--
-- +goose Up

-- The cache takes a fifth kind.
--
-- Widening a CHECK is one statement, which is the reason migration 5 used one instead of a
-- PostgreSQL enum. This is that reason being collected.
ALTER TABLE zpa_object DROP CONSTRAINT zpa_object_kind_is_known;
ALTER TABLE zpa_object ADD CONSTRAINT zpa_object_kind_is_known
    CHECK (kind IN ('MODULE', 'BASKET', 'MSBA', 'SPO', 'TEACHER'));

ALTER TABLE zpa_sync_run_kind DROP CONSTRAINT zpa_sync_run_kind_kind_is_known;
ALTER TABLE zpa_sync_run_kind ADD CONSTRAINT zpa_sync_run_kind_kind_is_known
    CHECK (kind IN ('MODULE', 'BASKET', 'MSBA', 'SPO', 'TEACHER'));

-- Somebody who teaches, as the examination office publishes them.
--
-- The one endpoint that types its values: real booleans, a real integer id, no Python `None`
-- wearing the costume of a string. The coercions below are guarded all the same — not out of
-- suspicion of this endpoint but because the shape of another one has already changed once, and
-- a view that throws on one malformed row is a cache nobody can read.
CREATE VIEW zpa_teacher_v AS
SELECT o.zpa_id                                        AS teacher_id,
       -- Lower-cased here rather than at every join: this is the address the authentication
       -- resolves an identity by, and the proxy's casing comes from the identity provider.
       lower(zpa_text(o.payload ->> 'email'))          AS mail,
       zpa_text(o.payload ->> 'person_fullname')       AS full_name,
       -- "Nachname, Vorname" for all 257. The form a list sorts by.
       zpa_text(o.payload ->> 'person_shortname')      AS short_name,
       (o.payload ->> 'is_prof')::boolean              AS is_professor,
       (o.payload ->> 'is_lba')::boolean               AS is_lecturer_on_contract,
       (o.payload ->> 'is_profhc')::boolean            AS is_honorary_professor,
       (o.payload ->> 'is_staff')::boolean             AS is_staff,
       (o.payload ->> 'is_active')::boolean            AS active,
       -- Empty for 146 of 257, so not usable as a filter and stored as what it is: an
       -- occasional hint. FK07 for 93, the rest spread over five other faculties.
       zpa_text(o.payload ->> 'fk')                    AS faculty,
       -- "2026 WS" in the source, and 13 say "unknown". Turned into this system's own spelling
       -- where it parses, so that it can be compared with semester.code, and NULL where it does
       -- not — the same guarded-coercion rule as everywhere in this layer.
       CASE
           WHEN o.payload ->> 'last_semester' ~ '^[0-9]{4} (SS|WS)$'
               THEN replace(o.payload ->> 'last_semester', ' ', '-')
       END                                             AS last_semester,
       o.gone_at IS NULL                               AS present,
       o.last_changed_at
FROM zpa_object o
WHERE o.kind = 'TEACHER';

CREATE TABLE teacher (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    -- The import key, on the same terms as everywhere else: unique, nullable, and nothing joins
    -- on it at request time. Nullable because a teacher entered by hand has to be possible —
    -- the same argument that keeps zpa_module_ref nullable.
    zpa_teacher_ref bigint UNIQUE,

    -- The link to a Tallox person, and the reason there is no person_id column above.
    --
    -- citext, so that the casing the identity provider happens to use does not decide whether a
    -- colleague is connected to her own modules. Nullable and not unique-by-force: three of the
    -- 257 carry no address at all.
    mail citext UNIQUE,

    full_name text NOT NULL DEFAULT '',
    short_name text NOT NULL DEFAULT '',

    -- What the examination office says about the employment. Kept as four booleans rather than
    -- one enum because the source has them as four and they are not exclusive: somebody can be
    -- staff and hold an honorary professorship.
    is_professor boolean NOT NULL DEFAULT false,
    is_lecturer_on_contract boolean NOT NULL DEFAULT false,
    is_honorary_professor boolean NOT NULL DEFAULT false,
    is_staff boolean NOT NULL DEFAULT false,

    -- The source's own flag: 208 of 257 are true. Distinct from retired_at, which is this
    -- system noticing that the source stopped mentioning somebody.
    active boolean NOT NULL DEFAULT true,

    faculty text,
    last_semester text,

    retired_at timestamptz,

    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT teacher_mail_not_blank CHECK (mail IS NULL OR length(trim(mail::text)) > 0),
    CONSTRAINT teacher_last_semester_is_a_code CHECK (
        last_semester IS NULL OR last_semester ~ '^[0-9]{4}-(SS|WS)$'
    )
);

-- "Who teaches here", sorted the way a list of people is read.
CREATE INDEX teacher_short_name_idx ON teacher (short_name);

-- Who is responsible for a module.
--
-- SET NULL rather than RESTRICT, and it is the one place in this schema where losing the
-- reference is better than keeping it: a teacher row only disappears if somebody removes it by
-- hand, and a module whose responsible person is gone is a module with an open question — not a
-- reason to refuse the removal. Everything pointing at a module or a programme is RESTRICT for
-- the opposite reason: it holds a decision somebody made.
ALTER TABLE module
    ADD COLUMN responsible_teacher_id uuid REFERENCES teacher (id) ON DELETE SET NULL;

CREATE INDEX module_responsible_idx ON module (responsible_teacher_id);

-- The report counts what it wrote, so it gains a counter.
ALTER TABLE zpa_catalogue_projection
    ADD COLUMN teachers_written integer NOT NULL DEFAULT 0;

-- A finding for the import report: the source names somebody the teacher list does not contain.
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
        -- The source names a responsible person the teacher list does not contain. 16 of 506
        -- today: 7 placeholders and 9 addresses. Reported rather than stored, because a mail
        -- address belongs in the table about people.
        'MODULE_RESPONSIBLE_UNKNOWN',
        -- A teacher with no address at all. Three of 257, and they can never be connected to a
        -- Tallox person — worth seeing, not worth refusing.
        'TEACHER_WITHOUT_MAIL'
    ));

-- +goose Down

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
        'DUTY_CONFLICT'
    ));

ALTER TABLE zpa_catalogue_projection DROP COLUMN teachers_written;

DROP INDEX module_responsible_idx;
ALTER TABLE module DROP COLUMN responsible_teacher_id;
DROP TABLE teacher;
DROP VIEW zpa_teacher_v;

-- The cached teacher objects go with the constraint that permitted them; otherwise the narrowed
-- CHECK would refuse rows that are already there and the migration would fail halfway.
DELETE FROM zpa_change WHERE kind = 'TEACHER';
DELETE FROM zpa_object WHERE kind = 'TEACHER';
DELETE FROM zpa_sync_run_kind WHERE kind = 'TEACHER';

ALTER TABLE zpa_sync_run_kind DROP CONSTRAINT zpa_sync_run_kind_kind_is_known;
ALTER TABLE zpa_sync_run_kind ADD CONSTRAINT zpa_sync_run_kind_kind_is_known
    CHECK (kind IN ('MODULE', 'BASKET', 'MSBA', 'SPO'));

ALTER TABLE zpa_object DROP CONSTRAINT zpa_object_kind_is_known;
ALTER TABLE zpa_object ADD CONSTRAINT zpa_object_kind_is_known
    CHECK (kind IN ('MODULE', 'BASKET', 'MSBA', 'SPO'));
