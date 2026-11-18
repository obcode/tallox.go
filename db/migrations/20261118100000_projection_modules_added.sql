-- Migration: a projection records the modules it brought into the catalogue.
--
-- A run's appeared/changed/disappeared compare the fetch with the cache. They cannot say what
-- reached the catalogue: a module fetched by the nightly run is not new again in the morning, and
-- a module the cache already held arrives in the catalogue with the first projection that runs
-- after it — which is not the run that fetched it if that run was only PARTIAL. On 2026-11-18 a
-- manual run reported "keine Änderungen" while a new module arrived, and nothing in the report
-- could show which of the two had happened.
--
-- modules_added counts the imported modules this projection inserted; added_modules keeps up to
-- twenty of them as "<programme code> · <name>", the answer to "is the module I asked for here".
-- Additive: the previous image neither reads nor writes either column, and the defaults are what
-- an older projection honestly is — nothing recorded.
--
-- +goose Up

ALTER TABLE zpa_catalogue_projection
    ADD COLUMN modules_added integer NOT NULL DEFAULT 0,
    ADD COLUMN added_modules text[] NOT NULL DEFAULT '{}';

-- +goose Down

ALTER TABLE zpa_catalogue_projection
    DROP COLUMN added_modules,
    DROP COLUMN modules_added;
