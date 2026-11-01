-- Migration: advance wishes ("Vormerkungen") — interest in a semester whose demand is not settled.
--
-- Since 2026-10-31 nobody registers a wish on a programme's instances until its lead has released
-- the demand. Asked for from the faculty on 2026-11-01: colleagues keep a private plan of what they
-- intend to teach in the coming years, and want it in the tool — so that the subject group's lead
-- knows without a separate page, and so that they hear when they turn out not to be needed. With
-- no claim attached: an advance wish is a statement, not a reservation.
--
-- WHAT IT IS
--
-- One person's interest in a module, in one programme, for one semester — the identity of an
-- instance without the cohort, because cohorts are what the demand planning decides. Priority,
-- note and an optional "at most this many cohorts" like a wish and its track limit.
--
-- When the programme releases its demand, or an instance appears in a programme already
-- released, every pending advance wish for which there is now a cohort becomes a wish on each
-- such cohort, its bound a wish_track_limit, and converted_at is set. Once, and only forwards: a
-- wish withdrawn afterwards does not come back on the next release. Entering one is refused once
-- the programme has released that semester — then the wish itself is what one enters.
--
-- WHO SEES IT
--
-- Exactly who would see the wish it is going to be: the owner; with full reach the leads of the
-- programme it names and of the module's subject group, and the dean's office; everybody once the
-- semester's wishes are published. db/queries/advance_wish.sql carries the same filter as
-- wish.sql and, like it, no COUNT.
--
-- WHY A SEMESTER CODE AND NOT A semester_id
--
-- The semester row is the record of decisions about a semester and comes into existence with the
-- first one. Somebody noting down what they would like to teach in four years has decided nothing
-- about that semester, and the semester list must not grow a row for it. The same format check as
-- semester.code.
--
-- ROLLBACK
--
-- A new table nobody else references. The previous image does not read it; wishes converted from
-- it are ordinary wishes.

-- +goose Up

CREATE TABLE advance_wish (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    person_id uuid NOT NULL REFERENCES person (id) ON DELETE CASCADE,
    semester_code text NOT NULL,
    module_id uuid NOT NULL REFERENCES module (id) ON DELETE CASCADE,
    programme_id uuid NOT NULL REFERENCES programme (id) ON DELETE CASCADE,
    -- 1 unbedingt, 2 gerne, 3 notfalls, as on wish.
    priority smallint NOT NULL DEFAULT 2,
    note text NOT NULL DEFAULT '',
    -- "Of the cohorts there will be, at most this many"; NULL for no bound.
    max_tracks smallint,
    -- When it became wishes. NULL while pending.
    converted_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    -- One per person and offering. Only ever the owner trips it, so it is no leak — see the wish
    -- table, whose argument is the same.
    UNIQUE (person_id, semester_code, module_id, programme_id),

    CONSTRAINT advance_wish_semester_is_year_and_term CHECK (semester_code ~ '^[0-9]{4}-(SS|WS)$'),
    CONSTRAINT advance_wish_priority_is_a_level CHECK (priority BETWEEN 1 AND 3),
    CONSTRAINT advance_wish_note_is_short CHECK (length(note) <= 500),
    CONSTRAINT advance_wish_max_tracks_in_range CHECK (max_tracks BETWEEN 1 AND 9)
);

-- The conversion asks "pending ones for this semester and programme".
CREATE INDEX advance_wish_offering_idx ON advance_wish (semester_code, programme_id)
    WHERE converted_at IS NULL;
CREATE INDEX advance_wish_person_idx ON advance_wish (person_id);

-- +goose Down

DROP TABLE advance_wish;
