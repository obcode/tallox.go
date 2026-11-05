-- Migration: one programme under more than one examination-office code.
--
-- The examination office renamed a programme by publishing a second one: "DC-M" has its own
-- programme id and its own regulations beside the old "DA", and a module can be at home in the
-- new one while it still counts in the old one's regulations. For the faculty it is one
-- programme — one demand, one budget, one programme lead — and its name is DCM, without the
-- hyphen, because the faculty's other codes carry the degree as a plain letter (DHB). Decided
-- 2026-11-05.
--
-- programme_code_alias maps a code as the source writes it to the code of the programme it
-- belongs to. The projection (db/queries/catalogue.sql) reads every source code through
-- programme_code_for(), so regulations and modules of both source codes land in one row. A code
-- without an entry maps to itself, which is every other programme.
--
-- The existing row is renamed rather than replaced: demand, instances, budgets and scopes point at
-- programme.id, so they stay where they are and read DCM from now on, in past semesters too.
--
-- An alias may only name a source code whose regulations sit under no other programme row: DA's
-- sit under the renamed row itself, DC-M's nowhere (the column refused it). A third row's would
-- collide with the target's on zpa_spo_ref and stop the projection; it has to be merged by hand
-- first.
--
-- Maintained by migrations, not by a screen: a second case is not in sight, and an alias decides
-- where planning data lands — not something to change in passing.

-- +goose Up

CREATE TABLE IF NOT EXISTS programme_code_alias (
    -- As the source writes it, hyphen and all; the projection never stores it.
    zpa_code text PRIMARY KEY,
    -- The programme's own code, under the column's rule.
    programme_code text NOT NULL,

    CONSTRAINT programme_code_alias_target_is_a_code
        CHECK (programme_code ~ '^[A-Z][A-Z0-9.]{0,9}$'),
    -- An alias to itself is no alias, and the fold below would not notice one.
    CONSTRAINT programme_code_alias_not_itself CHECK (zpa_code <> programme_code)
);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION programme_code_for(source_code text) RETURNS text
    LANGUAGE sql STABLE AS $$
    SELECT COALESCE(
        (SELECT a.programme_code FROM programme_code_alias a WHERE a.zpa_code = source_code),
        source_code)
$$;
-- +goose StatementEnd

-- Only when DCM does not exist yet: a schema that already has one has been arranged by hand, and
-- merging two rows here would have to decide about two sets of demand.
UPDATE programme SET code = 'DCM', updated_at = now()
WHERE code = 'DA' AND NOT EXISTS (SELECT 1 FROM programme WHERE code = 'DCM');

INSERT INTO programme_code_alias (zpa_code, programme_code) VALUES
    ('DA', 'DCM'),
    ('DC-M', 'DCM')
ON CONFLICT (zpa_code) DO NOTHING;

-- +goose Down

-- The previous projection knows no aliases and would create DA afresh beside DCM, so the name
-- goes back with the table. Modules it already moved from DC-M stay with the renamed row.
UPDATE programme SET code = 'DA', updated_at = now()
WHERE code = 'DCM' AND NOT EXISTS (SELECT 1 FROM programme WHERE code = 'DA');

DROP FUNCTION IF EXISTS programme_code_for(text);
DROP TABLE IF EXISTS programme_code_alias;
