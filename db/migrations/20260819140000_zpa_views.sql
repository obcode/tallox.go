-- Migration 6: the structure inside the cached payloads, as views.
--
-- Migration 5 lands each object whole and reads exactly one scalar out of it. That is right for
-- what it is — a landing layer that survives the source changing shape, and that can answer
-- "what changed" about fields nobody modelled. It is not enough on its own: the relationships
-- between the four kinds, and the handful of scalars planning will actually ask about, sat
-- inside jsonb where every future query would have written `(payload->'module'->>'id')::bigint`
-- and would have had to know by itself that `active` is the string 'True' and that `owner` can
-- be the string 'None'. Twelve places, one of them wrong.
--
-- WHY VIEWS AND NOT MIRROR TABLES
--
-- Mirror tables with real foreign keys were the obvious alternative, and measuring the source
-- ruled them out. Of the three references an association row carries:
--
--     msba.module -> module_info      0 dangling
--     msba.basket -> basket_info      0 dangling
--     msba.spo    -> spo_info        12 SPOs dangling, 665 of 3272 rows (20 %)
--
-- spo_info returns 29 sets of regulations; the association rows reference 41. The missing ones
-- are historical (2007 to 2017) plus one that reads as a placeholder (version 2099). A foreign
-- key would therefore reject a fifth of the real data — the import would fail on a Tuesday for
-- being correct. In the other direction there are 19 modules and 7 baskets that no association
-- mentions. Referential integrity is the source's business, and a cache has to be able to hold
-- their truth including the untidy parts.
--
-- Three more reasons, and the first is the one that makes the rest affordable:
--
--   * Size makes performance a non-question. 3861 objects, 4.9 MB in total. Any query here is
--     a sequential scan of a few milliseconds. An expression index on zpa_object can be added
--     the day something needs one, without changing a line of this.
--   * A view costs almost nothing to change: CREATE OR REPLACE, no data movement, no
--     three-release column dance. That is precisely the property the untyped landing table was
--     chosen for, and this is it being used.
--   * sqlc reads views out of these migrations exactly like tables, so the queries above them
--     are typed.
--
-- The rule that nothing outside the zpa_ prefix references the cache is untouched: views carry
-- no constraints, and TestNoDomainTableReferencesTheZpaCache looks at foreign keys.
--
-- +goose Up

-- The coercions, in one place each.
--
-- Guarded rather than plain casts, and that is the important part. The source types nothing:
-- numbers, dates and booleans all arrive as text, and one malformed value in one row would make
-- a plain `::int` throw for the whole view — turning a single bad record into a cache nobody
-- can read. NULL is the honest answer to "this did not parse", and the payload is still there
-- for whoever needs to see what actually arrived.
--
-- IMMUTABLE so they may appear in an index expression later; STRICT so NULL in is NULL out.

-- +goose StatementBegin
CREATE FUNCTION zpa_int(value text) RETURNS integer
    LANGUAGE sql IMMUTABLE STRICT AS $$
    SELECT CASE WHEN value ~ '^-?[0-9]+$' THEN value::integer END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION zpa_bigint(value text) RETURNS bigint
    LANGUAGE sql IMMUTABLE STRICT AS $$
    SELECT CASE WHEN value ~ '^-?[0-9]+$' THEN value::bigint END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION zpa_date(value text) RETURNS date
    LANGUAGE sql IMMUTABLE STRICT AS $$
    SELECT CASE WHEN value ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}$' THEN value::date END
$$;
-- +goose StatementEnd

-- The source writes Python's words for the truth values, as text.
-- +goose StatementBegin
CREATE FUNCTION zpa_bool(value text) RETURNS boolean
    LANGUAGE sql IMMUTABLE STRICT AS $$
    SELECT CASE WHEN value = 'True' THEN true WHEN value = 'False' THEN false END
$$;
-- +goose StatementEnd

-- Empty and the four-letter word "None" both mean absent.
--
-- Three modules carry `owner: 'None'` — a Python None that came through as text, which is a
-- null wearing the costume of a value. Without this, a query for "modules whose home programme
-- is None" would return three rows and look like a real programme.
-- +goose StatementBegin
CREATE FUNCTION zpa_text(value text) RETURNS text
    LANGUAGE sql IMMUTABLE STRICT AS $$
    SELECT NULLIF(NULLIF(btrim(value), ''), 'None')
$$;
-- +goose StatementEnd

-- One version of one programme's examination regulations.
CREATE VIEW zpa_spo_v AS
SELECT o.zpa_id                                   AS spo_id,
       zpa_int(o.payload ->> 'version')           AS version,
       zpa_date(o.payload ->> 'valid_from')       AS valid_from,
       zpa_text(o.payload ->> 'primuss_id')       AS primuss_id,
       zpa_bigint(o.payload -> 'program' ->> 'id') AS programme_id,
       zpa_text(o.payload -> 'program' ->> 'title') AS programme,
       o.gone_at IS NULL                          AS present,
       o.last_changed_at
FROM zpa_object o
WHERE o.kind = 'SPO';

-- A catalogue slot: compulsory or elective, optionally inside a specialisation.
--
-- is_duty is the distinction between the compulsory and the elective catalogue, which is what
-- the study programme leads are actually declaring when they plan demand.
CREATE VIEW zpa_basket_v AS
SELECT o.zpa_id                                            AS basket_id,
       zpa_text(o.payload ->> 'basket')                    AS name,
       (o.payload ->> 'is_duty')::boolean                  AS is_duty,
       zpa_bigint(o.payload -> 'schwerpunkt' ->> 'sp_id')  AS focus_id,
       zpa_text(o.payload -> 'schwerpunkt' ->> 'sp_short') AS focus_short,
       zpa_text(o.payload -> 'schwerpunkt' ->> 'sp_title') AS focus,
       o.gone_at IS NULL                                   AS present,
       o.last_changed_at
FROM zpa_object o
WHERE o.kind = 'BASKET';

-- The association: this module counts in these regulations, in this catalogue slot.
--
-- The row where the module code lives, and the reason it is not an identifier: it belongs to
-- the triple rather than to the module, 264 of 487 modules carry more than one, one carries
-- eight, and 688 of these rows carry none.
--
-- spo_version and spo_valid_from are read from the association's own nested copy rather than
-- joined from zpa_spo_v, and that is not redundancy. spo_info omits 12 sets of regulations that
-- these rows reference, so the join would lose 665 of them; the embedded copy is the only place
-- that information exists for those. Where both are present they never disagree — checked
-- against the whole catalogue, zero conflicts.
CREATE VIEW zpa_msba_v AS
SELECT o.zpa_id                                          AS msba_id,
       zpa_bigint(o.payload -> 'module' ->> 'id')        AS module_id,
       zpa_text(o.payload -> 'module' ->> 'name')        AS module_name,
       zpa_bigint(o.payload -> 'spo' ->> 'id')           AS spo_id,
       zpa_int(o.payload -> 'spo' ->> 'version')         AS spo_version,
       zpa_date(o.payload -> 'spo' ->> 'valid_from')     AS spo_valid_from,
       zpa_bigint(o.payload -> 'basket' ->> 'id')        AS basket_id,
       zpa_text(o.payload -> 'basket' ->> 'name')        AS basket_name,
       zpa_text(o.payload ->> 'module_code')             AS module_code,
       zpa_int(o.payload ->> 'min_program_semester')     AS min_programme_semester,
       jsonb_array_length(COALESCE(o.payload -> 'exam_types', '[]'::jsonb)) AS exam_type_count,
       o.gone_at IS NULL                                 AS present,
       o.last_changed_at
FROM zpa_object o
WHERE o.kind = 'MSBA';

-- A module in the catalogue.
--
-- The prose stays in the payload and is deliberately not projected: the descriptions, learning
-- objectives, literature and effort are about 1.8 kB per module, they are display text, and
-- nothing will ever filter on them. Columns for them would be most of the bytes and none of the
-- value.
--
-- `name` is derived here rather than stored, and it has to be: the module objects carry no name
-- field at all — the name exists only inside the nested object of an association row, and the
-- importer cannot fill it in because it fetches each kind independently and modules come before
-- associations. Deriving it in a view is the cheapest correct place, and it is the clearest
-- illustration of why this layer is views rather than mirror tables: the rule lives once, costs
-- nothing to change, and needed no re-import when it turned out to be wrong.
--
-- NULL for the 19 modules no association mentions. o.label wins where it exists, so the day the
-- source grows a real name field the client picks it up and this falls back automatically.
CREATE VIEW zpa_module_v AS
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
-- DISTINCT ON with an explicit order rather than a correlated subquery: one pass, and the row
-- it picks is the same one every time. A module appearing in nine programmes has nine names to
-- choose from and they are the same string.
LEFT JOIN (
    SELECT DISTINCT ON (module_id) module_id, module_name
    FROM zpa_msba_v
    WHERE module_name IS NOT NULL
    ORDER BY module_id, msba_id
) n ON n.module_id = o.zpa_id
WHERE o.kind = 'MODULE';

-- The views deliberately do NOT filter out retired objects; they expose `present` instead.
--
-- A view that hid them would make "which modules did this programme offer in 2024" unanswerable
-- from the same place that answers "which does it offer now", and the whole reason rows are
-- retired rather than deleted is that the first question is worth answering.

-- +goose Down

DROP VIEW zpa_module_v;
DROP VIEW zpa_msba_v;
DROP VIEW zpa_basket_v;
DROP VIEW zpa_spo_v;

DROP FUNCTION zpa_text(text);
DROP FUNCTION zpa_bool(text);
DROP FUNCTION zpa_date(text);
DROP FUNCTION zpa_bigint(text);
DROP FUNCTION zpa_int(text);
