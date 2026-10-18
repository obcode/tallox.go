-- Migration 33: which modules are project-based courses (PBLV).
--
-- A process rule from the kickoff: PBLVs are given out last, "damit sie nicht als Lückenfüller
-- weggeschnappt werden". The examination office does not say which modules they are — course_type
-- PROJECT is a different thing, a module taught as a project — so the faculty marks them, once
-- per module, like the split. The import never touches the column.
--
-- Only the mark. Whether "last" is a hard gate or a warning, and until when, is open
-- (docs/rueckfragen-2026-10.md in the private repo); the interface sorts them last and says why.

-- +goose Up

ALTER TABLE module
    ADD COLUMN project_based boolean NOT NULL DEFAULT false;

-- +goose Down

ALTER TABLE module
    DROP COLUMN IF EXISTS project_based;
