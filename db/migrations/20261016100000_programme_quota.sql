-- Migration 24: what a study programme may spend on teaching in a study year.
--
-- The other half of the SWS controlling. teachingLoad says what a programme's modules cost; this
-- says what the programme has. The figure comes out of the faculty's capacity calculation, once a
-- year, for the study year: the teaching available to a programme, theses included. The dean's
-- office enters it. Nothing in the ZPA knows it.
--
-- ONE ROW PER (PROGRAMME, STUDY YEAR), NOT PER SEMESTER
--
-- The figure is stated for a year, so it is stored for a year. Halving it for a semester is
-- arithmetic, and arithmetic belongs in code that can be read, not in a number somebody typed in
-- twice. The study year is named after the year its winter semester starts in, the way the
-- semester codes are: 2026 is 2026-WS and 2027-SS.
--
-- NO FOREIGN KEY TO semester
--
-- A semester row is the record of a decision about that semester. A quota is not one — it is a
-- statement about a year of a programme — and a key to semester would make entering next year's
-- quota the act that creates next winter's row. The year is a plain integer, bounded here as a
-- sanity check and more tightly by the service, which knows what "too far away" means.
--
-- NOT CONFIDENTIAL
--
-- A number per programme, nothing about a person. Readable by everybody who reads the demand,
-- through both doors, like the load it is compared against.

-- +goose Up

CREATE TABLE programme_quota (
    programme_id uuid NOT NULL REFERENCES programme (id) ON DELETE CASCADE,
    study_year integer NOT NULL,
    -- Teaching hours per week over the study year: every laboratory and exercise group counted,
    -- the same hours instance_part.teaching_hours holds.
    teaching_hours numeric(7, 2) NOT NULL,

    -- When and by whom it was last set. NULL once that person's row is gone; the figure stays.
    set_at timestamptz NOT NULL DEFAULT now(),
    set_by uuid REFERENCES person (id) ON DELETE SET NULL,

    PRIMARY KEY (programme_id, study_year),
    CONSTRAINT programme_quota_year_is_plausible CHECK (study_year BETWEEN 2000 AND 2200),
    CONSTRAINT programme_quota_hours_not_negative CHECK (teaching_hours >= 0)
);

-- +goose Down

DROP TABLE programme_quota;
