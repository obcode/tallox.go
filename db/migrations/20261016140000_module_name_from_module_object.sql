-- Migration 26: a module's name comes from the module object.
--
-- The view assumed the module objects carry no name and borrowed one from an association row.
-- They do carry one: the field is called `module`, not `name`, and nobody looked for it under
-- that key. The consequence was every module that no association mentions showing up as
-- nameless in the catalogue, although the examination office names it — a programme lead
-- looking at "module without a name" next to a name in the source has every reason to
-- distrust the rest of the page.
--
-- The module's own field wins; the association's copy stays as the fallback, so a module the
-- source really sends without one is still named wherever it is associated. o.label is no
-- longer consulted: for a module it is now read from the same field at sync time, and reading
-- the payload here instead means the fix needs no new sync, only the next projection.
--
-- CREATE OR REPLACE: same columns, same order, same types, so nothing that reads the view
-- notices, and the previous image runs against it unchanged.

-- +goose Up
CREATE OR REPLACE VIEW zpa_module_v AS
SELECT o.zpa_id                                    AS module_id,
       COALESCE(zpa_text(o.payload ->> 'module'), n.module_name) AS name,
       zpa_text(o.payload ->> 'owner')             AS home_programme,
       zpa_text(o.payload ->> 'course_type')       AS course_type,
       zpa_int(o.payload ->> 'sws')                AS sws,
       zpa_int(o.payload ->> 'credits')            AS credits,
       zpa_bool(o.payload ->> 'active')            AS active,
       zpa_bool(o.payload ->> 'official')          AS official,
       zpa_bool(o.payload ->> 'repeater_exam')     AS repeater_exam,
       zpa_text(o.payload ->> 'responsible')       AS responsible,
       zpa_text(o.payload ->> 'frequency')         AS frequency,
       zpa_text(o.payload ->> 'languages')         AS languages,
       zpa_text(o.payload ->> 'std_lang')          AS default_language,
       o.gone_at IS NULL                           AS present,
       o.last_changed_at
FROM zpa_object o
LEFT JOIN (
    SELECT DISTINCT ON (module_id) module_id, module_name
    FROM zpa_msba_v
    WHERE module_name IS NOT NULL
    ORDER BY module_id, msba_id
) n ON n.module_id = o.zpa_id
WHERE o.kind = 'MODULE';

-- +goose Down
CREATE OR REPLACE VIEW zpa_module_v AS
SELECT o.zpa_id                                    AS module_id,
       COALESCE(o.label, n.module_name)            AS name,
       zpa_text(o.payload ->> 'owner')             AS home_programme,
       zpa_text(o.payload ->> 'course_type')       AS course_type,
       zpa_int(o.payload ->> 'sws')                AS sws,
       zpa_int(o.payload ->> 'credits')            AS credits,
       zpa_bool(o.payload ->> 'active')            AS active,
       zpa_bool(o.payload ->> 'official')          AS official,
       zpa_bool(o.payload ->> 'repeater_exam')     AS repeater_exam,
       zpa_text(o.payload ->> 'responsible')       AS responsible,
       zpa_text(o.payload ->> 'frequency')         AS frequency,
       zpa_text(o.payload ->> 'languages')         AS languages,
       zpa_text(o.payload ->> 'std_lang')          AS default_language,
       o.gone_at IS NULL                           AS present,
       o.last_changed_at
FROM zpa_object o
LEFT JOIN (
    SELECT DISTINCT ON (module_id) module_id, module_name
    FROM zpa_msba_v
    WHERE module_name IS NOT NULL
    ORDER BY module_id, msba_id
) n ON n.module_id = o.zpa_id
WHERE o.kind = 'MODULE';
