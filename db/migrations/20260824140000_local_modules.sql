-- Migration 11: modules the faculty enters itself.
--
-- Two cases the examination office's catalogue does not cover, and both come from the same
-- sentence in the requirements: a course that has to be offered and is not in the ZPA (yet),
-- and an FWP placeholder — "we need three electives, ideally something technical" — declared
-- before anybody knows which subjects they will be.
--
-- # Why a row in `module` and not a table of its own
--
-- Because everything downstream already knows what a module is. A local row inherits the split,
-- the instance, its parts, the hours arithmetic, and later the wishes and the assignment,
-- without a second path through any of them. The alternative — `demand_wildcard` beside
-- `course_instance` — was the shape considered in August, and its cost is a second branch at
-- every one of those layers, forever, for rows that behave exactly like modules.
--
-- The placeholder is then not a new concept: it is a module called "FWP-Platzhalter
-- (technisch)" with hours and a split. "We need three of them" is three cohorts of it, which is
-- what the identity already says — three instances of one module in one programme and semester
-- *must* differ in their track. So the demand table can already express it, with the stepper it
-- already has.
--
-- # What keeps the import out
--
-- `module_local_has_no_zpa_ref`. ProjectModules inserts from the ZPA views and resolves
-- conflicts on `zpa_module_ref`, so a row with none can be neither a source nor a target — but
-- that is a property of a statement, and statements get rewritten. The constraint is the
-- property of the table, and it is what the tests point at.
--
-- It is also written so that adopting a module later is one UPDATE: set `source` to 'ZPA' and
-- fill in the reference. Nothing here is built for that yet — which ZPA row is the right one is
-- a question only a person can answer, and what happens to the instances already hanging off
-- the local row is a second one — but the door is left open rather than nailed shut.
--
-- The previous image runs against this schema: both new columns have defaults, and every read
-- lists its columns explicitly.
--
-- +goose Up

ALTER TABLE module
    -- Where this catalogue row comes from. 'ZPA' is what the projection writes; 'LOCAL' is what
    -- the faculty enters. A CHECK rather than an enum, for the reason migration 8 gives:
    -- widening a CHECK is one statement, widening an enum fights the transaction goose runs in.
    ADD COLUMN source text NOT NULL DEFAULT 'ZPA'
        CONSTRAINT module_source_is_known CHECK (source IN ('ZPA', 'LOCAL')),

    -- What the row stands for. Two values, because there are exactly two readers: the badge in
    -- the interface, and the question "how many electives do we still need". A placeholder is
    -- otherwise an ordinary module and is planned with the same table.
    ADD COLUMN kind text NOT NULL DEFAULT 'MODULE'
        CONSTRAINT module_kind_is_known CHECK (kind IN ('MODULE', 'FWP_PLACEHOLDER')),

    -- Who entered it. The same on-the-row audit module_component and course_instance keep;
    -- SET NULL rather than CASCADE, because deactivating a person must not take their entries.
    ADD COLUMN created_by uuid REFERENCES person (id) ON DELETE SET NULL,

    -- The load-bearing one: a local row never carries a ZPA reference.
    ADD CONSTRAINT module_local_has_no_zpa_ref
        CHECK (source = 'ZPA' OR zpa_module_ref IS NULL),

    -- retired_at means "a successful import stopped mentioning it". The import has no opinion
    -- about a local row, so it can never be that. A local course nobody needs any more is
    -- `active = false`; deleting is not offered, because instances and later wishes point here.
    ADD CONSTRAINT module_local_is_never_retired
        CHECK (source = 'ZPA' OR retired_at IS NULL);

-- Two clicks on "anlegen" are one row.
--
-- Only for local rows, and on the name, because there is no other identity to hold them apart:
-- a ZPA module has its reference, and this one has what a person typed. Case-insensitive, since
-- "FWP technisch" and "FWP Technisch" are the same placeholder written twice.
CREATE UNIQUE INDEX module_local_name_idx
    ON module (home_programme_id, lower(name)) WHERE source = 'LOCAL';

-- +goose Down

DROP INDEX module_local_name_idx;

ALTER TABLE module
    DROP CONSTRAINT module_local_is_never_retired,
    DROP CONSTRAINT module_local_has_no_zpa_ref,
    DROP COLUMN created_by,
    DROP COLUMN kind,
    DROP COLUMN source;
