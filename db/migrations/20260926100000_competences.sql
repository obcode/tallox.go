-- Migration 23: competences — who can teach which module, and who would like to.
--
-- The kickoff's two sentences, and they are the two levels: "wer kann was halten, wenn es brennt"
-- and "wer würde mal gerne in Zukunft". Together they are a pool of people for every module, which
-- is what the assignment reads when a wish round leaves a gap.
--
-- ON THE MODULE, NOT ON AN INSTANCE, AND WITHOUT A SEMESTER
--
-- A wish is about one semester's cohort; a competence is about a subject and outlives every
-- semester, the way a subject group does. Copying it forward each term would be a chore nobody
-- does, and the pool would be empty exactly when it is needed.
--
-- PERSON OR TEACHER, AND WHO SAID SO
--
-- Decided 2026-09-26. A person states their own competences — the wish's "only yourself", for the
-- wish's reason: somebody else's statement about you is an opinion, not a profile. But lecturers
-- on contract have no account and cannot state anything, and they are precisely who the subject
-- group lead needs in the pool. So a teacher row is entered by the lead of the module's subject
-- group, and this table records who did — unlike the wish table, which has no entered_by because
-- every row there is its owner's.
--
-- The two rules meet in one CHECK: a row about an account was entered by that account. A teacher
-- who has an account is not entered for at all — internal/domain refuses, because that colleague
-- can say it themselves, and a row the lead wrote would be indistinguishable from theirs.
--
-- CONFIDENTIAL, PERMANENTLY
--
-- WOULD_LIKE is a wish without a semester, so the same rule applies to it — and there is no
-- publication date to lift it, because there is no semester. Read by the person, the lead of the
-- module's subject group, the lead of its home programme and the dean's office. Nobody else, and
-- through a token only one's own. The rule is in internal/policy/competence.go.
--
-- WHAT IS DELIBERATELY NOT A COLUMN
--
-- The subject group. Derived through module_subject_group, for the reason migration 15 gives:
-- re-cutting a group has to change who is responsible for what hangs off its modules.
--
-- +goose Up

CREATE TABLE competence (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    -- RESTRICT: modules are retired, never deleted, and a statement about one must not vanish
    -- with a row somebody removed by hand.
    module_id uuid NOT NULL REFERENCES module (id) ON DELETE RESTRICT,

    -- Exactly one of these two, as on the assignment. CASCADE on both: a competence is a
    -- statement about somebody and goes with them — the wish's reading, not the assignment's.
    person_id uuid REFERENCES person (id) ON DELETE CASCADE,
    teacher_id uuid REFERENCES teacher (id) ON DELETE CASCADE,

    -- CAN_TEACH or WOULD_LIKE. Text rather than a smallint scale: the two are not degrees of one
    -- thing but two different statements, and only CAN_TEACH counts towards the minimum.
    level text NOT NULL,

    -- "nur die Übung", "zuletzt 2019 gehalten". Read by whoever may read the row.
    note text NOT NULL DEFAULT '',

    -- Who wrote it. Always the person for a person row, see the CHECK; the subject group lead for
    -- a teacher row. NULL once the writer's row is gone — the statement itself stays.
    entered_by uuid REFERENCES person (id) ON DELETE SET NULL,

    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT competence_has_exactly_one_holder
        CHECK (num_nonnulls(person_id, teacher_id) = 1),
    -- A person's competences are that person's own statement. NULL passes, which is the row whose
    -- writer has since been deleted — and for a person row that is the person, whose deletion
    -- takes the row with it anyway.
    CONSTRAINT competence_of_a_person_is_their_own
        CHECK (person_id IS NULL OR entered_by = person_id),
    CONSTRAINT competence_level_is_known
        CHECK (level IN ('CAN_TEACH', 'WOULD_LIKE')),
    -- The same bound as the wish and the assignment note: they are read side by side.
    CONSTRAINT competence_note_is_short CHECK (length(note) <= 500)
);

-- One statement per holder per module. Changing the level is changing your mind, not a second
-- entry. Two partial indexes rather than one UNIQUE over both columns, because exactly one of
-- them is NULL in every row and NULLs are distinct.
CREATE UNIQUE INDEX competence_person_module_key ON competence (person_id, module_id)
    WHERE person_id IS NOT NULL;
CREATE UNIQUE INDEX competence_teacher_module_key ON competence (teacher_id, module_id)
    WHERE teacher_id IS NOT NULL;

-- The pool of one module — what the assignment screen asks for every row.
CREATE INDEX competence_module_idx ON competence (module_id);

-- +goose Down

DROP TABLE competence;
