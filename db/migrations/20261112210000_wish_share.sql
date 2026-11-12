-- Migration: a wish may state the owner's share of the cohort.
--
-- A wish has always meant the whole cohort. Some cohorts are divided between two people — one
-- teaches the discrete-mathematics half of a lecture, another the theory half — and a wish for the
-- whole of it states more teaching hours than its owner intends to hold. The note could say so,
-- but nothing could add it up. Decided on 2026-11-12: the share is stated in whole teaching hours
-- (SWS); a percentage waits for a concrete need.
--
-- NULL is the whole cohort, which is every existing row and the ordinary wish. The CHECK holds
-- only the one-column half of the rule; the upper bound is the cohort's hours, which live on its
-- parts and change with them, so the domain checks it when the wish is written
-- (WISH_SHARE_OUT_OF_RANGE). Additive: the previous image neither reads nor writes the column,
-- and its upsert leaves it as it is.
--
-- +goose Up

ALTER TABLE wish
    ADD COLUMN share_hours smallint,
    ADD CONSTRAINT wish_share_hours_positive CHECK (share_hours > 0);

-- +goose Down

ALTER TABLE wish
    DROP CONSTRAINT wish_share_hours_positive,
    DROP COLUMN share_hours;
