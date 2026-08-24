-- Migration 12: which study programmes this faculty actually plans.
--
-- The examination office's catalogue holds every programme its regulations mention. Some of them
-- are not this faculty's business at all, and some were and have run out. Both kinds must stay in
-- the catalogue — their modules are still taught, and what was planned in them stays on record —
-- and neither belongs in a picker or in a grant.
--
-- # Why this is a column and not a rule
--
-- Because the source cannot tell them apart, and that was measured rather than assumed:
--
--   * IB and IN are planned and their newest regulations are from **2010** — older than every
--     programme on the other list (IC 2019, IS 2017, IT 2020, GAST 2019, RS 2020). Any threshold
--     on the age of the regulations excludes the two that must stay.
--   * IS has run out and carries 13 active modules — more than GN, GS and WD, which are planned.
--     So the number of modules says nothing either.
--   * The examination office's own `active` flag is true for all of them but one.
--   * The programme object inside a set of regulations carries `{id, title}` and nothing else:
--     no faculty, no end date, no successor.
--
-- So it is a decision the faculty takes, and this column is where it is recorded. It is also the
-- shape the question keeps: programmes will be added on both sides of the line.
--
-- # Three values rather than a boolean
--
-- `NOT_OURS` and `DISCONTINUED` do the same thing today and mean quite different things. One is
-- a programme somebody else runs; the other is our own, with demand on record and students still
-- finishing. The day somebody asks "what did we offer in IC" the two need different answers, and
-- a boolean would have thrown the distinction away for the sake of one byte.
--
-- # Why PLANNED is the default
--
-- The safe default elsewhere in this schema is the restrictive one — a new person holds no role,
-- an unscoped programme lead may plan nothing. Those are permissions, and a permission that
-- defaults to "yes" is a hole. This is not one: appearing in a picker grants nothing, and who may
-- plan a programme is still `person_programme_scope`.
--
-- What the two mistakes cost decides it instead. A programme that appears and should not is
-- noise somebody removes in one click. A programme that is silently missing is a support
-- question — "why can I not plan mine" — and the answer is invisible from the screen it is asked
-- on. So a new programme is offered, and taking it out is the deliberate act.
--
-- The previous image runs against this schema: the column has a default and every read lists its
-- columns explicitly.
--
-- +goose Up

ALTER TABLE programme
    ADD COLUMN planning_status text NOT NULL DEFAULT 'PLANNED'
        CONSTRAINT programme_planning_status_is_known
        CHECK (planning_status IN ('PLANNED', 'NOT_OURS', 'DISCONTINUED')),

    -- When the decision was recorded, and by whom. The same on-the-row audit the semester's
    -- planning mark keeps, and for the same reason: there is no audit-log table yet, and these
    -- two columns are what answers "who moved this" on the day somebody asks.
    ADD COLUMN planning_status_set_at timestamptz,
    ADD COLUMN planning_status_set_by uuid REFERENCES person (id) ON DELETE SET NULL;

-- The seven the faculty named on 2026-08-24, and the two reasons they are not planned.
--
-- Written here rather than clicked afterwards, for the same reason migration 10 records the
-- planning semester: it is the decision, and leaving it to a click leaves a window in which
-- every picker offers seven programmes nobody plans. `now()` rather than NULL for the timestamp
-- would claim a person took it, so it stays NULL — the row says what, and the absence says
-- "this came with the release".
UPDATE programme
SET planning_status = 'NOT_OURS'
WHERE code IN ('GAST', 'RS', 'WA', 'ZD');

UPDATE programme
SET planning_status = 'DISCONTINUED'
WHERE code IN ('IC', 'IS', 'IT');

-- Every picker asks for the planned ones, and the list is ordered by code.
CREATE INDEX programme_planning_status_code_idx ON programme (planning_status, code);

-- +goose Down

DROP INDEX programme_planning_status_code_idx;

ALTER TABLE programme
    DROP COLUMN planning_status_set_by,
    DROP COLUMN planning_status_set_at,
    DROP COLUMN planning_status;
