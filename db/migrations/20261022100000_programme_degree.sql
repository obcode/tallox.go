-- Migration: whether a study programme leads to a bachelor's or a master's.
--
-- Subject group leads staff bachelor's and master's modules as two separate jobs, and a group
-- such as WI holds both. The assignment screen and the catalogue need to tell them apart, and
-- nothing they read could.
--
-- # Why this is a column and not a rule
--
-- Because the source does not carry it. The programme object inside a set of regulations is
-- `{id, title}`, and `title` repeats the code. The codes do not encode it either — IF, IB, IC, IN
-- are two letters each, with no suffix that says "master". A `-M-` inside some module codes is
-- undocumented and the codes are display-only. So it is a fact the faculty records, like the split
-- of a module into lecture and lab.
--
-- # NULL, not a guess
--
-- There is no value a migration could fill in without guessing, and a guessed value is worse
-- than none: it looks maintained. NULL reads as "not recorded yet". The filters treat it as
-- matching neither degree, and the screens say which programmes that leaves out and who records
-- them. The import never writes this column.
--
-- Who set it and when sits next to it, the same as the planning status.
--
-- Additive only. The previous image lists its columns and gets NULL.

-- +goose Up

ALTER TABLE programme
    ADD COLUMN IF NOT EXISTS degree text
        CONSTRAINT programme_degree_is_known CHECK (degree IN ('BACHELOR', 'MASTER')),
    ADD COLUMN IF NOT EXISTS degree_set_at timestamptz,
    ADD COLUMN IF NOT EXISTS degree_set_by uuid REFERENCES person (id) ON DELETE SET NULL;

-- +goose Down

ALTER TABLE programme
    DROP COLUMN IF EXISTS degree_set_by,
    DROP COLUMN IF EXISTS degree_set_at,
    DROP COLUMN IF EXISTS degree;
