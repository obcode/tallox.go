-- Migration 30: which catalogue slot a module counts in — the basket's name.
--
-- Asked for in the SWS controlling: a programme lead of DC wants to see which modules of the
-- basket "WPF Anwendungen des maschinellen Lernens" are offered this semester and what they
-- cost, not just "elective". The name was in the cache all along (zpa_msba_v.basket_name,
-- zpa_basket_v.name) and the projection threw it away, because migration 6 only needed
-- compulsory-or-elective out of the slot.
--
-- PLURAL, AND DISPLAY ONLY
--
-- module_offering folds the up to four slots a module sits in within one version of the
-- regulations — the ordinary catalogue and the specialisations — into one row. Their names fold
-- the same way module_codes and focuses do: an array, DISTINCT and NULL-free, never a key. A
-- basket is the examination office's grouping of somebody else's regulations, and nothing here
-- references it.
--
-- BACKFILLED HERE, NOT AT THE NEXT IMPORT
--
-- The projection rewrites the column on every run, but the next run is up to a night away and
-- the column would read empty until then — which looks exactly like "this module is in no
-- basket". The backfill is the projection's own fold, against the same views.

-- +goose Up

ALTER TABLE module_offering
    ADD COLUMN baskets text[] NOT NULL DEFAULT '{}';

UPDATE module_offering o
SET baskets = f.baskets
FROM (
    SELECT mo.id AS module_id,
           s.id AS spo_id,
           array_remove(array_agg(DISTINCT b.name ORDER BY b.name), NULL) AS baskets
    FROM zpa_msba_v m
    JOIN zpa_basket_v b ON b.basket_id = m.basket_id
    JOIN module mo ON mo.zpa_module_ref = m.module_id
    JOIN spo s ON s.zpa_spo_ref = m.spo_id
    WHERE m.present
    GROUP BY mo.id, s.id
) f
WHERE o.module_id = f.module_id AND o.spo_id = f.spo_id;

-- +goose Down

ALTER TABLE module_offering
    DROP COLUMN IF EXISTS baskets;
