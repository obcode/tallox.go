-- Migration 17: assignments — who actually holds each part of an instance.
--
-- The third step of the process gets its table. The demand says what has to be offered, the
-- wishes say who would like to teach it, and this says who does. Migration 16 wrote the division
-- of labour down a day before there was anything to point at:
--
--     wish       → the instance.  What somebody is offering to teach.
--     assignment → the part.      Who holds the lecture and who the laboratory.
--
-- PERSON OR TEACHER, EXACTLY ONE
--
-- Decided 2026-08-27. Somebody who teaches here does not necessarily hold a Tallox account, and
-- must not have to. Six of the 257 teachers the examination office knows carry addresses
-- sso.hm.edu will never assert (haw-landshut.de, doubleslash.de, calpoly.edu) and three carry
-- none at all — lecturers on contract are exactly the group a plan has to be able to name.
--
-- The alternative was to give each of them a person row, and it is the one thing this schema has
-- consistently refused: person is the access list, and the import grants nothing. Filling it to
-- make somebody assignable would move the decision about who may sign in into a side effect of
-- planning. See the teachers migration for the same argument in the other direction.
--
-- So two nullable references and a CHECK, and internal/domain canonicalises: assigning a teacher
-- who has a person row writes the person. Otherwise the same colleague would sit in this table
-- under two identities and "my assignments" would find half of them.
--
-- WHAT THIS TABLE MAKES REACHABLE, AGAIN
--
-- Migration 16 took the incoming RESTRICT off instance_part deliberately: re-cutting the parts of
-- an instance must not be blocked by somebody's *interest*. This puts one back, and the asymmetry
-- is the point — an assignment is not an interest. A lecture that is filled may not quietly
-- disappear because somebody edited the number of laboratory groups.
--
--     DELETE instance_part → RESTRICT from assignment → SQLSTATE 23001
--
-- Note the SQLSTATE. RESTRICT checks immediately and raises 23001; NO ACTION waits until the end
-- of the statement and raises 23503. internal/store checks both, which it learned the hard way
-- when the wish table made the same path live.
--
-- internal/store maps it to domain.ErrPartAssigned, which says that something hangs off the part
-- and never who — before publication the assignment is confidential, so a refusal naming the
-- holder would be the leak the row filter exists to prevent.
--
-- ASSIGNED_BY EXISTS, AND WISH.ENTERED_BY DOES NOT
--
-- The exact opposite of migration 15, for the same reason. A wish registered on somebody's behalf
-- is not an expression of interest, so provenance there could only record a mistake. An assignment
-- is *always* somebody's decision about somebody else — that is what distinguishes it from a wish
-- — so the deciding person belongs on the row.
--
-- RESTRICT ON THE PEOPLE, NOT CASCADE
--
-- Also the opposite of the wish table, and also on purpose. A person's wishes are that person's
-- data and go with them. An assignment is a decision the faculty made about its teaching; it is
-- not the assignee's property and must not vanish silently with a row. Neither table is emptied
-- in practice — person.active is the removal this system has, and a teacher gets retired_at — so
-- this is the reading for the case that is not supposed to happen.
--
-- WHAT IS DELIBERATELY NOT A COLUMN
--
-- The semester, the study programme and the subject group, for the reason migration 15 gives:
-- all three are reachable through the part, and the third one *must* be derived, because
-- re-cutting a subject group has to change who is responsible for what hangs off its modules.
--
-- The teaching hours as well. They are on instance_part, they are what one person is credited
-- with, and copying them here would make two numbers that have to agree and one day will not.
--
-- +goose Up

CREATE TABLE assignment (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    -- UNIQUE, and that is the decision "exactly one person per part". Splitting the teaching of
    -- one unit means splitting the unit — laboratory groups are separate parts already, and
    -- splitInstancePartAcrossTracks exists for the lecture. Shares of an hour figure would have
    -- to sum to the part's hours on every write, and a sum that silently stops adding up is how
    -- the faculty's teaching load becomes a plausible wrong number.
    instance_part_id uuid NOT NULL UNIQUE REFERENCES instance_part (id) ON DELETE RESTRICT,

    -- Exactly one of these two is set; see the header and the CHECK below.
    person_id uuid REFERENCES person (id) ON DELETE RESTRICT,
    teacher_id uuid REFERENCES teacher (id) ON DELETE RESTRICT,

    -- What the subject group lead wants recorded about this decision: "übernimmt die Vorlesung
    -- vertretungsweise", "nur im Wintersemester". Read by whoever may read the row.
    note text NOT NULL DEFAULT '',

    -- Who decided. NULL once that person's row is gone; the assignment itself stays.
    assigned_by uuid REFERENCES person (id) ON DELETE SET NULL,

    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    -- num_nonnulls rather than a hand-written OR pair: it says "exactly one" in the words the
    -- decision uses, and it does not have to be re-read to be believed.
    CONSTRAINT assignment_has_exactly_one_assignee
        CHECK (num_nonnulls(person_id, teacher_id) = 1),

    -- Long enough for a sentence, short enough that nobody keeps a document in here. Same bound
    -- as the wish note, deliberately: the two are read side by side on the same screen.
    CONSTRAINT assignment_note_is_short CHECK (length(note) <= 500)
);

-- "What am I teaching next semester" — the query every lecturer runs, and the one the assignment
-- screen starts from when it groups by person. The join to the instance carries the semester, so
-- the assignee is the selective half and belongs first.
CREATE INDEX assignment_person_idx ON assignment (person_id) WHERE person_id IS NOT NULL;
CREATE INDEX assignment_teacher_idx ON assignment (teacher_id) WHERE teacher_id IS NOT NULL;

-- The publication mark for the assignments, and it is its own column rather than a second reading
-- of wishes_published_at.
--
-- Same shape and same argument as that one: the rule in internal/policy is exactly `IS NOT NULL`,
-- there is no constraint tying it to the phase, and there is no un-publishing. What it protects is
-- different, though, and worth writing down. A wish is confidential so that registering interest
-- is not a move against whoever is already in the list. A half-finished assignment is confidential
-- because it invites questions about decisions nobody has taken yet — and a subject group lead who
-- expects those will prepare the plan somewhere else, which is the outcome this system exists to
-- prevent.
ALTER TABLE semester ADD COLUMN assignments_published_at timestamptz;

-- +goose Down

DROP TABLE assignment;
ALTER TABLE semester DROP COLUMN assignments_published_at;
