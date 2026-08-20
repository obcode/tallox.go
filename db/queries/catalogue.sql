-- The module catalogue, projected out of the cached payloads.
--
-- The theme of this file is that the projection is a fold, not a copy. The source publishes one
-- row per module per set of regulations per catalogue slot; what planning asks about is a
-- module, a programme and whether the module is compulsory there. Every statement below turns
-- the first into the second, and every one of them is written so that running it twice changes
-- nothing.
--
-- Two rules hold throughout. Nothing here joins a domain row to the cache at request time — the
-- zpa_*_ref columns are matched once, in these statements, and never again. And nothing here
-- writes a module's `responsible` field into a domain table: it is a colleague's mail address,
-- and this repository is public.

-- name: ProjectProgrammes :execrows
-- Every programme the source knows, from either direction.
--
-- Two directions, because the source disagrees with itself about which programmes exist. Nineteen
-- appear in the regulations endpoint; nineteen are named as the home of a module; and they are
-- not the same nineteen. One programme has regulations and not a single module, another has six
-- modules and no regulations at all. Deriving the list from either side alone loses the other's
-- odd one out — and losing the second costs four active modules, because a module's home
-- programme is mandatory.
--
-- `active` is exactly "the regulations endpoint knows it". A programme nobody publishes
-- regulations for is not an error and not a normal choice either: its modules stay planable and
-- it stays out of the pickers.
--
-- `title` is written on insert only — never in the DO UPDATE. The source has no long name (its
-- `title` field carries the code again), so the column is for a human, and a projection that
-- refreshed it would overwrite what they typed on the next nightly run.
WITH known AS (
    SELECT programme AS code, min(programme_id) AS ref
    FROM zpa_spo_v
    WHERE present AND programme IS NOT NULL
    GROUP BY programme

    UNION

    SELECT home_programme AS code, NULL::bigint AS ref
    FROM zpa_module_v
    WHERE present
      AND home_programme IS NOT NULL
      AND home_programme NOT IN (
          SELECT programme FROM zpa_spo_v WHERE present AND programme IS NOT NULL
      )
)
INSERT INTO programme (code, active, zpa_programme_ref)
SELECT code, ref IS NOT NULL, ref
FROM known
-- Defence rather than expectation: every real code is two to four upper-case letters. Without
-- the filter, one malformed code would abort the whole projection and take every other
-- programme's modules with it. What is filtered out is counted by MalformedProgrammeCodes below.
WHERE code ~ '^[A-Z][A-Z0-9.]{0,9}$'
ON CONFLICT (code) DO UPDATE
SET active            = EXCLUDED.active,
    -- COALESCE rather than assignment: a programme first seen through a module's owner has no
    -- reference, and a later run that learns its regulations should fill the gap without a
    -- run in the other order erasing it.
    zpa_programme_ref = COALESCE(EXCLUDED.zpa_programme_ref, programme.zpa_programme_ref),
    updated_at        = now();

-- name: MalformedProgrammeCodes :many
-- The codes ProjectProgrammes had to leave out, so the report can name them.
SELECT DISTINCT code FROM (
    SELECT programme AS code FROM zpa_spo_v WHERE present AND programme IS NOT NULL
    UNION
    SELECT home_programme AS code FROM zpa_module_v WHERE present AND home_programme IS NOT NULL
) c
WHERE code !~ '^[A-Z][A-Z0-9.]{0,9}$'
ORDER BY code;

-- name: ProjectSpos :execrows
-- One row per version of one programme's regulations.
--
-- Only the ones the endpoint returns. The 12 the association rows reference and the endpoint
-- does not are deliberately not synthesised from the copy embedded in those rows: that copy has
-- a version and a date but no programme, and a set of regulations belonging to no programme is
-- the same pathology as a module whose owner is the string "None", one level up.
INSERT INTO spo (programme_id, version, valid_from, primuss_id, zpa_spo_ref)
SELECT p.id, s.version, s.valid_from, s.primuss_id, s.spo_id
FROM zpa_spo_v s
JOIN programme p ON p.code = s.programme
WHERE s.present AND s.version IS NOT NULL
ON CONFLICT (programme_id, version) DO UPDATE
SET valid_from  = EXCLUDED.valid_from,
    primuss_id  = EXCLUDED.primuss_id,
    zpa_spo_ref = EXCLUDED.zpa_spo_ref,
    updated_at  = now();

-- name: ProjectModules :execrows
-- The catalogue itself.
--
-- The two vocabularies arrive as German prose and are translated through a mapping passed in
-- from internal/domain, rather than through a CASE expression written here. The mapping already
-- has three homes that cannot import one another; a fourth inside a query would be the one
-- nobody updates, in the place where being wrong is silent.
--
-- An unrecognised phrase becomes the safe value and is counted by UnmappedVocabulary below. It
-- must never be only the safe value: UNKNOWN is what the term filter treats as "show it
-- anyway", so a phrase that quietly fell to it would hide nothing and explain nothing.
--
-- Modules whose home programme is absent are dropped by the inner join, which is the whole
-- mechanism — the column is NOT NULL by decision, and zpa_text() has already turned the
-- source's "None" into a real NULL. WithoutHomeProgramme below counts what fell out.
WITH frequency_map AS (
    SELECT p.phrase, v.value
    FROM unnest(sqlc.arg(frequency_phrases)::text[]) WITH ORDINALITY AS p (phrase, n)
    JOIN unnest(sqlc.arg(frequency_values)::text[]) WITH ORDINALITY AS v (value, n) ON v.n = p.n
), course_type_map AS (
    SELECT p.phrase, v.value
    FROM unnest(sqlc.arg(course_type_phrases)::text[]) WITH ORDINALITY AS p (phrase, n)
    JOIN unnest(sqlc.arg(course_type_values)::text[]) WITH ORDINALITY AS v (value, n) ON v.n = p.n
)
INSERT INTO module (home_programme_id, name, course_type, course_type_source,
                    frequency, frequency_source, contact_hours_per_week, credits,
                    active, official, retired_at, zpa_module_ref)
SELECT p.id,
       COALESCE(m.name, ''),
       COALESCE(ct.value, 'DEPENDS_ON_SUBJECT'),
       COALESCE(m.course_type, ''),
       COALESCE(fr.value, 'UNKNOWN'),
       COALESCE(m.frequency, ''),
       m.sws,
       m.credits,
       COALESCE(m.active, true),
       COALESCE(m.official, true),
       CASE WHEN NOT m.present THEN now() END,
       m.module_id
FROM zpa_module_v m
JOIN programme p ON p.code = m.home_programme
LEFT JOIN frequency_map fr ON fr.phrase = m.frequency
LEFT JOIN course_type_map ct ON ct.phrase = m.course_type
ON CONFLICT (zpa_module_ref) DO UPDATE
SET home_programme_id      = EXCLUDED.home_programme_id,
    name                   = EXCLUDED.name,
    course_type            = EXCLUDED.course_type,
    course_type_source     = EXCLUDED.course_type_source,
    frequency              = EXCLUDED.frequency,
    frequency_source       = EXCLUDED.frequency_source,
    contact_hours_per_week = EXCLUDED.contact_hours_per_week,
    credits                = EXCLUDED.credits,
    active                 = EXCLUDED.active,
    official               = EXCLUDED.official,
    -- Set when the source stops mentioning it, cleared when it comes back. Never a DELETE: a
    -- module is what an instance, and eventually a wish, points at.
    retired_at             = EXCLUDED.retired_at,
    updated_at             = now();

-- name: ProjectModuleOfferings :execrows
-- Where each module counts, folded to one row per module per set of regulations.
--
-- The fold is the point. A module sits in up to four catalogue slots of one version — the
-- ordinary catalogue and the specialisations — and the slots differ in the module code and
-- sometimes in the earliest semester. Measured over the whole catalogue they never differ in
-- compulsory-or-elective, which is what makes this grain safe and one grain coarser unsafe.
--
-- bool_and rather than bool_or, so that if the assumption ever breaks the result is the weaker
-- claim rather than the stronger one. DutyConflicts below reports it either way.
WITH folded AS (
    SELECT m.module_id,
           m.spo_id,
           bool_and(b.is_duty) AS is_duty,
           -- DISTINCT and NULL-free: a fifth of the source rows carry no code, and the ones
           -- that do repeat it across versions.
           array_remove(array_agg(DISTINCT m.module_code), NULL) AS module_codes,
           array_remove(array_agg(DISTINCT b.focus_short), NULL) AS focuses,
           min(m.min_programme_semester) AS min_programme_semester,
           count(*)::integer AS source_rows
    FROM zpa_msba_v m
    JOIN zpa_basket_v b ON b.basket_id = m.basket_id
    WHERE m.present AND m.module_id IS NOT NULL AND m.spo_id IS NOT NULL
    GROUP BY m.module_id, m.spo_id
)
INSERT INTO module_offering (module_id, spo_id, is_duty, module_codes, focuses,
                             min_programme_semester, source_rows)
SELECT mo.id, s.id, f.is_duty, f.module_codes, f.focuses, f.min_programme_semester, f.source_rows
FROM folded f
-- Both joins are inner, and each one drops a documented case: an association whose module was
-- skipped for having no home programme, and an association pointing at regulations the source
-- no longer returns. The second is 665 real rows and is reported by
-- AssociationsWithUnknownRegulations.
JOIN module mo ON mo.zpa_module_ref = f.module_id
JOIN spo s ON s.zpa_spo_ref = f.spo_id
ON CONFLICT (module_id, spo_id) DO UPDATE
SET is_duty                = EXCLUDED.is_duty,
    module_codes           = EXCLUDED.module_codes,
    focuses                = EXCLUDED.focuses,
    min_programme_semester = EXCLUDED.min_programme_semester,
    source_rows            = EXCLUDED.source_rows,
    updated_at             = now();

-- name: DeleteStaleModuleOfferings :execrows
-- Offerings the source no longer supports.
--
-- The only catalogue table anything is deleted from, and it is safe for one reason that is
-- asserted by a test rather than assumed: nothing references an offering. A module keeps its
-- row and gains retired_at; a programme keeps its row and loses `active`; an offering is a
-- claim about somebody else's regulations, and when they stop making it the claim goes.
DELETE FROM module_offering o
WHERE NOT EXISTS (
    SELECT 1
    FROM zpa_msba_v m
    JOIN module mo ON mo.zpa_module_ref = m.module_id
    JOIN spo s ON s.zpa_spo_ref = m.spo_id
    WHERE m.present AND mo.id = o.module_id AND s.id = o.spo_id
);

-- The report
-- ----------
--
-- Nine things the projection decides about untidy input, each counted with a handful of
-- examples. Counts and samples rather than a row per object: the useful sentence is "665
-- associations across 12 sets of regulations", and whoever wants to chase one needs a few
-- identifiers, not all of them.
--
-- Every one of these runs inside the same transaction as the statements above, against the same
-- snapshot — so the report describes the projection that happened rather than the cache as it
-- looked a moment later.

-- name: CountModulesWithoutHomeProgramme :one
-- Skipped entirely. The source writes the string "None" and zpa_text() has already turned it
-- into a NULL, which is the difference between "absent" and "a programme called None".
SELECT count(*)::integer AS count,
       (array_agg(module_id::text ORDER BY module_id))[1:20]::text[] AS sample
FROM zpa_module_v
WHERE present AND home_programme IS NULL;

-- name: CountProgrammesWithoutRegulations :one
-- Projected and marked inactive. Their modules stay planable, which is the point: the real one
-- has six modules behind it and four of them are still active.
SELECT count(*)::integer AS count,
       (array_agg(code ORDER BY code))[1:20]::text[] AS sample
FROM programme
WHERE NOT active;

-- name: CountModulesWithoutName :one
-- Projected with an empty name, because the alternative is worse.
--
-- The source's module objects carry no name field at all; the name is borrowed from an
-- association row, so a module in no set of regulations has none anywhere. Skipping them would
-- mean a programme lead searching for a module they are responsible for and not finding it.
SELECT count(*)::integer AS count,
       (array_agg(zpa_module_ref::text ORDER BY zpa_module_ref))[1:20]::text[] AS sample
FROM module
WHERE name = '' AND retired_at IS NULL;

-- name: CountInactiveModules :one
-- Projected with the flag, never hidden. "What did this programme offer in 2024" is a question
-- worth being able to answer.
SELECT count(*)::integer AS count,
       (array_agg(zpa_module_ref::text ORDER BY zpa_module_ref))[1:20]::text[] AS sample
FROM module
WHERE NOT active;

-- name: CountAssociationsWithUnknownRegulations :one
-- Not projected, and the largest of the nine: 665 real rows over 12 sets of regulations the
-- endpoint stopped returning — the historical ones and a placeholder dated 2099.
--
-- Grouped by regulations in the sample rather than by association, because the useful thing to
-- see is which twelve, not which six hundred.
SELECT count(*)::integer AS count,
       (SELECT array_agg(spo_id::text ORDER BY spo_id)
          FROM (SELECT DISTINCT s.spo_id
                  FROM zpa_msba_v s
                 WHERE s.present AND s.spo_id IS NOT NULL
                   AND NOT EXISTS (
                       SELECT 1 FROM zpa_spo_v v WHERE v.present AND v.spo_id = s.spo_id)
               ) unknown
       )::text[] AS sample
FROM zpa_msba_v m
WHERE m.present AND m.spo_id IS NOT NULL
  AND NOT EXISTS (SELECT 1 FROM zpa_spo_v v WHERE v.present AND v.spo_id = m.spo_id);

-- name: CountUnmappedFrequencies :one
-- Became UNKNOWN. An empty phrase is not counted: absent is a state the source is entitled to
-- be in, and eleven modules are in it — burying the genuinely new phrases under them would
-- defeat the purpose of the line.
SELECT count(*)::integer AS count,
       (array_agg(DISTINCT frequency_source))[1:20]::text[] AS sample
FROM module
WHERE frequency = 'UNKNOWN' AND frequency_source <> '';

-- name: CountUnmappedCourseTypes :one
-- Became DEPENDS_ON_SUBJECT. Same reasoning, and the same exclusion of the empty phrase —
-- except that the source really does write "je nach Fach", which maps to the same value and is
-- therefore excluded here too, or 23 modules would be reported as a problem every night.
SELECT count(*)::integer AS count,
       (array_agg(DISTINCT course_type_source))[1:20]::text[] AS sample
FROM module
WHERE course_type = 'DEPENDS_ON_SUBJECT'
  AND course_type_source <> ''
  AND course_type_source <> sqlc.arg(depends_on_subject_phrase)::text;

-- name: CountMinSemesterConflicts :one
-- Folded with min(). Two real pairs are in this state, and both are a module sitting in an
-- ordinary catalogue slot and a specialisation of the same version that disagree about the
-- earliest semester a student may take it.
-- The count is of pairs and the sample is of modules, deliberately: one module can be in this
-- state in two versions of the regulations at once, and printing its identifier twice would
-- read as two different modules.
WITH conflicting AS (
    SELECT m.module_id
    FROM zpa_msba_v m
    WHERE m.present AND m.module_id IS NOT NULL AND m.spo_id IS NOT NULL
    GROUP BY m.module_id, m.spo_id
    HAVING count(DISTINCT m.min_programme_semester) > 1
)
SELECT count(*)::integer AS count,
       (SELECT array_agg(module_id::text ORDER BY module_id)
          FROM (SELECT DISTINCT module_id FROM conflicting ORDER BY module_id LIMIT 20) named
       )::text[] AS sample
FROM conflicting;

-- name: CountDutyConflicts :one
-- Must be zero, and is the one line here that is an alarm rather than a note.
--
-- The grain of module_offering rests on compulsory-or-elective being determined by (module,
-- regulations) — measured at 0 conflicts over 2386 pairs. If the source ever contradicts that,
-- the fold silently picks one answer, and this is what says so.
WITH conflicting AS (
    SELECT m.module_id
    FROM zpa_msba_v m
    JOIN zpa_basket_v b ON b.basket_id = m.basket_id
    WHERE m.present AND m.module_id IS NOT NULL AND m.spo_id IS NOT NULL
    GROUP BY m.module_id, m.spo_id
    HAVING count(DISTINCT b.is_duty) > 1
)
SELECT count(*)::integer AS count,
       (SELECT array_agg(module_id::text ORDER BY module_id)
          FROM (SELECT DISTINCT module_id FROM conflicting ORDER BY module_id LIMIT 20) named
       )::text[] AS sample
FROM conflicting;

-- The projection's own bookkeeping
-- --------------------------------

-- name: StartCatalogueProjection :one
-- Written before the first statement, like the sync run it mirrors: a projection that crashed
-- leaves a row somebody can see rather than no row at all.
INSERT INTO zpa_catalogue_projection (run_id)
VALUES (sqlc.narg(run_id))
RETURNING id, run_id, started_at, finished_at, status,
          programmes_written, modules_written, offerings_written, offerings_removed, error;

-- name: FinishCatalogueProjection :one
UPDATE zpa_catalogue_projection
SET status             = sqlc.arg(status),
    finished_at        = now(),
    programmes_written = sqlc.arg(programmes_written),
    modules_written    = sqlc.arg(modules_written),
    offerings_written  = sqlc.arg(offerings_written),
    offerings_removed  = sqlc.arg(offerings_removed),
    error              = sqlc.narg(error)
WHERE id = sqlc.arg(id)
RETURNING id, run_id, started_at, finished_at, status,
          programmes_written, modules_written, offerings_written, offerings_removed, error;

-- name: RecordCatalogueProjectionNote :exec
-- Only ever called with a positive count — a note saying "nothing happened" is noise in a
-- report whose whole value is that every line means something.
INSERT INTO zpa_catalogue_projection_note (projection_id, code, count, sample)
VALUES ($1, $2, $3, $4)
ON CONFLICT (projection_id, code) DO UPDATE
SET count = EXCLUDED.count, sample = EXCLUDED.sample;

-- name: LatestCatalogueProjections :many
SELECT id, run_id, started_at, finished_at, status,
       programmes_written, modules_written, offerings_written, offerings_removed, error
FROM zpa_catalogue_projection
ORDER BY started_at DESC
LIMIT $1;

-- name: CatalogueProjectionNotes :many
SELECT code, count, sample
FROM zpa_catalogue_projection_note
WHERE projection_id = $1
ORDER BY code;
