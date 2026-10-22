-- Migration: desired teaching hours as a range, frozen by the dean's office, then published.
--
-- What the dean wrote on 2026-10-22, in place of the traffic light: everybody states how many
-- hours of module teaching (theses not included) they would like to take in a semester, ideally
-- as a minimum and a maximum. At a deadline the figures freeze, the faculty leadership reads them
-- for plausibility and settles anything implausible with the person, and then the figures are
-- made visible to everybody — "dann sehen auch alle, ob sie wem was wegnehmen, der es noch
-- bräuchte". This answers question 5 of docs/rueckfragen-2026-10.md (private repo).
--
-- THE RANGE: ONE NEW COLUMN, NOT TWO
--
-- hours stays and is the upper end; min_hours is the lower one. A single figure is min = max,
-- which is what every existing row means and what the previous image keeps writing: it inserts
-- and updates hours and never names min_hours. Two new columns would have left that image
-- updating a column nobody reads any more, and after a rollback and a roll forward the screen
-- would show the figure from before the rollback. With one, a row the previous image wrote reads
-- as min = max, and a range it shortened from above reads with the lower end clamped
-- (LEAST(min_hours, hours) in the queries) — no CHECK across the two columns for that reason,
-- because the previous image would trip it. The domain refuses min > max on the way in.
--
-- WHO CORRECTED IT
--
-- Until now the row had no author column, because a figure stated on somebody's behalf is not
-- their statement. After the freeze it can be the dean's office's correction, agreed with the
-- person, and that has to be visible: corrected_by is set when the dean's office writes and
-- cleared when the person writes again. NULL is "as the person stated it".
--
-- THE TWO MARKS
--
-- On semester, beside wishes_published_at and assignments_published_at, with the same idempotent
-- first-timestamp-wins shape. Each with who did it, unlike the two older ones: freezing is a
-- deadline somebody enforces, and the first question about it will be "who closed it".

-- +goose Up

ALTER TABLE desired_teaching_hours
    ADD COLUMN min_hours numeric(4, 1),
    ADD COLUMN corrected_by uuid REFERENCES person (id) ON DELETE SET NULL,
    ADD CONSTRAINT desired_teaching_hours_min_in_range
        CHECK (min_hours IS NULL OR (min_hours >= 0 AND min_hours <= 40));

ALTER TABLE semester
    ADD COLUMN desired_hours_frozen_at timestamptz,
    ADD COLUMN desired_hours_frozen_by uuid REFERENCES person (id) ON DELETE SET NULL,
    ADD COLUMN desired_hours_published_at timestamptz,
    ADD COLUMN desired_hours_published_by uuid REFERENCES person (id) ON DELETE SET NULL,
    -- Published implies frozen. The query that publishes says so too; this is the version the
    -- database keeps when a statement someday forgets.
    ADD CONSTRAINT semester_desired_hours_published_after_freeze
        CHECK (desired_hours_published_at IS NULL OR desired_hours_frozen_at IS NOT NULL);

-- +goose Down

ALTER TABLE semester
    DROP CONSTRAINT IF EXISTS semester_desired_hours_published_after_freeze,
    DROP COLUMN IF EXISTS desired_hours_published_by,
    DROP COLUMN IF EXISTS desired_hours_published_at,
    DROP COLUMN IF EXISTS desired_hours_frozen_by,
    DROP COLUMN IF EXISTS desired_hours_frozen_at;

ALTER TABLE desired_teaching_hours
    DROP CONSTRAINT IF EXISTS desired_teaching_hours_min_in_range,
    DROP COLUMN IF EXISTS corrected_by,
    DROP COLUMN IF EXISTS min_hours;
