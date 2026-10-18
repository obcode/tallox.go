-- Migration 31: how many hours a week somebody wants to teach in a semester.
--
-- From the kickoff, under the teaching load: the figure a lecturer states, which the subject
-- group lead reads beside the wishes when filling instances — and which should freeze at some
-- point, "sonst wird erst wenig angegeben und später nachgetragen, um Spezial-LVs zu bekommen".
-- When it freezes is open (docs/rueckfragen-2026-10.md in the private repo); the write rule is a
-- row of its own in policy.writeMatrix so that the answer lands in one place.
--
-- ONE ROW PER PERSON AND SEMESTER, AND ONLY ONE'S OWN
--
-- No entered_by, for the reason the wish table has none: a figure stated on somebody's behalf is
-- not their statement. No row means "not stated", which is different from 0 — somebody on
-- research leave states 0.
--
-- A NUMBER, BUT NOT A BALANCE
--
-- This is not the teaching obligation and not the overtime balance; those are the dean's office's
-- figures and are not stored (CLAUDE.md, "Deputat / EMS / Ampel"). It is what one person says
-- they would like, and the read rule (policy.DesiredHoursVisibility) is the competences' rule
-- turned towards the person: their own, the leads of their subject groups, the dean's office.
--
-- The range is generous on purpose: a full professorship is 18 hours, a teaching professorship
-- more, and a CHECK that refuses an honest answer gets a lie instead.

-- +goose Up

CREATE TABLE desired_teaching_hours (
    semester_id uuid NOT NULL REFERENCES semester (id) ON DELETE CASCADE,
    person_id uuid NOT NULL REFERENCES person (id) ON DELETE CASCADE,
    -- Half hours exist (a laboratory group of 1.5), so not an integer.
    hours numeric(4, 1) NOT NULL,
    note text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (semester_id, person_id),
    CONSTRAINT desired_teaching_hours_in_range CHECK (hours >= 0 AND hours <= 40),
    CONSTRAINT desired_teaching_hours_note_is_short CHECK (length(note) <= 500)
);

-- The other direction: "everything this person has stated", for their own list across semesters.
CREATE INDEX desired_teaching_hours_person_idx ON desired_teaching_hours (person_id);

-- +goose Down

DROP TABLE IF EXISTS desired_teaching_hours;
