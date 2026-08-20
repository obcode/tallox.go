-- Reading the module catalogue.
--
-- The shape of this file is set by one decision: a list of modules is loaded in a fixed number
-- of statements rather than one per row. Four for a filtered list — the modules, their
-- components, their offerings, and the programmes — stitched together in Go. The catalogue is
-- 506 modules and 1784 offerings, so loading what a screen needs and joining it in memory is
-- both simpler than a loader framework and faster than the query-per-field it replaces.

-- name: ListProgrammes :many
-- Every study programme, including the one with no current regulations.
--
-- Ordered by code, which is how the faculty says them and how a picker should show them.
SELECT id, code, title, active
FROM programme
ORDER BY code;

-- name: ProgrammeByCode :one
SELECT id, code, title, active
FROM programme
WHERE code = $1;

-- name: ListSpos :many
-- Every version of every programme's regulations, newest first within a programme.
--
-- Loaded whole rather than per programme: 29 rows, and every screen that shows one programme's
-- versions also has the others in a picker beside it.
SELECT s.id, s.programme_id, s.version, s.valid_from, s.primuss_id, p.code AS programme_code
FROM spo s
JOIN programme p ON p.id = s.programme_id
ORDER BY p.code, s.version DESC;

-- name: ListModules :many
-- The catalogue, filtered.
--
-- The programme filter is a union of two conditions and the second is not redundant: a module
-- counts in one of the programme's sets of regulations, OR the programme is its home. Measured
-- against the real catalogue, 26 active modules are reachable only through the second half, ten
-- of them in the largest programme — and the first thing a programme lead does is look for a
-- module they are responsible for.
--
-- The duty filter is applied through the same subquery rather than beside it, so that
-- "compulsory in this programme" cannot accidentally mean "compulsory anywhere". MIXED is
-- expressed as "has both", which is what makes it a filter rather than a label.
SELECT m.id, m.name, m.home_programme_id, m.course_type, m.frequency,
       m.contact_hours_per_week, m.credits, m.active, m.official, m.retired_at, m.zpa_module_ref
FROM module m
WHERE (sqlc.narg('programme')::text IS NULL
       OR EXISTS (SELECT 1 FROM programme hp
                   WHERE hp.id = m.home_programme_id AND hp.code = sqlc.narg('programme')::text)
       OR EXISTS (SELECT 1
                    FROM module_offering o
                    JOIN spo s ON s.id = o.spo_id
                    JOIN programme p ON p.id = s.programme_id
                   WHERE o.module_id = m.id
                     AND p.code = sqlc.narg('programme')::text
                     AND (sqlc.narg('spo')::uuid IS NULL OR s.id = sqlc.narg('spo')::uuid)))
  -- Narrowing to one version without naming a programme still means "counts in this version".
  AND (sqlc.narg('spo')::uuid IS NULL
       OR EXISTS (SELECT 1 FROM module_offering o
                   WHERE o.module_id = m.id AND o.spo_id = sqlc.narg('spo')::uuid))
  AND (sqlc.arg('any_frequency')::boolean
       OR m.frequency = ANY (sqlc.arg('frequencies')::text[]))
  AND (sqlc.narg('duty')::text IS NULL OR sqlc.narg('programme')::text IS NULL
       OR (SELECT CASE
                      WHEN bool_and(o.is_duty) THEN 'COMPULSORY'
                      WHEN bool_or(o.is_duty) THEN 'MIXED'
                      ELSE 'ELECTIVE'
                  END
             FROM module_offering o
             JOIN spo s ON s.id = o.spo_id
             JOIN programme p ON p.id = s.programme_id
            WHERE o.module_id = m.id
              AND p.code = sqlc.narg('programme')::text
              AND (sqlc.narg('spo')::uuid IS NULL OR s.id = sqlc.narg('spo')::uuid)
          ) = sqlc.narg('duty')::text)
  AND (sqlc.narg('search')::text IS NULL
       OR m.name ILIKE '%' || sqlc.narg('search')::text || '%'
       OR EXISTS (SELECT 1 FROM module_offering o
                   WHERE o.module_id = m.id
                     AND array_to_string(o.module_codes, ' ')
                         ILIKE '%' || sqlc.narg('search')::text || '%'))
  AND (sqlc.arg('include_inactive')::boolean OR (m.active AND m.retired_at IS NULL))
  AND (NOT sqlc.arg('without_components')::boolean
       OR NOT EXISTS (SELECT 1 FROM module_component c WHERE c.module_id = m.id))
-- A module with no name sorts last rather than first: the empty string would otherwise put the
-- handful of nameless ones at the top of every list in the system.
ORDER BY (m.name = ''), m.name, m.id;

-- name: ModuleByID :one
SELECT id, name, home_programme_id, course_type, frequency,
       contact_hours_per_week, credits, active, official, retired_at, zpa_module_ref
FROM module
WHERE id = $1;

-- name: ModuleComponentsFor :many
-- The splits of a set of modules, in one statement.
SELECT id, module_id, kind, teaching_hours, position
FROM module_component
WHERE module_id = ANY (sqlc.arg(module_ids)::uuid[])
ORDER BY module_id, position;

-- name: ModuleOfferingsFor :many
-- Where a set of modules counts, in one statement, joined to the regulations they count in.
--
-- Unfiltered by programme on purpose, even when the list was filtered by one. A module's
-- offerings are what "where does this count" means, and answering it with only the programme
-- somebody happened to filter by would make the same module look different depending on how it
-- was found.
SELECT o.id, o.module_id, o.spo_id, o.is_duty, o.module_codes, o.focuses,
       o.min_programme_semester,
       s.version AS spo_version, s.valid_from AS spo_valid_from, s.primuss_id AS spo_primuss_id,
       p.id AS programme_id, p.code AS programme_code, p.title AS programme_title,
       p.active AS programme_active
FROM module_offering o
JOIN spo s ON s.id = o.spo_id
JOIN programme p ON p.id = s.programme_id
WHERE o.module_id = ANY (sqlc.arg(module_ids)::uuid[])
ORDER BY o.module_id, p.code, s.version DESC;

-- name: ReplaceModuleComponents :exec
-- Clear a module's split, so the caller can write the new one in the same transaction.
--
-- Delete-then-insert rather than a diff, because the entries only mean anything together: what
-- is being replaced is one statement about a module, not a set of independent rows. Inside a
-- transaction, so nobody reads the empty moment in between.
DELETE FROM module_component WHERE module_id = $1;

-- name: InsertModuleComponent :exec
INSERT INTO module_component (module_id, kind, teaching_hours, position, created_by)
VALUES ($1, $2, $3, $4, sqlc.narg(created_by));
