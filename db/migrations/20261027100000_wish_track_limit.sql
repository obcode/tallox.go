-- Migration: "one of the cohorts, I do not mind which".
--
-- Asked for from the faculty on 2026-10-27: a module offered in two cohorts of a programme, IF4A
-- and IF4B, and somebody who would take one of them but not both and does not mind which. Until
-- now that sentence lived in the note of one of the two wishes — readable by a person, invisible
-- to the sum of hours on the wish screen and easy to miss on the assignment screen.
--
-- WHAT IT IS
--
-- An upper bound on the wishes one person has registered for the cohorts of one module in one
-- programme in one semester: "of these, at most this many". The wishes themselves stay what they
-- were — one per cohort, each with its own priority — because a wish still points at an instance
-- (migration 16), and "A gerne, B notfalls, höchstens einer" is a sentence the bound can say and a
-- single "any cohort" wish could not. A bound also says "two of three", which a flag does not.
--
-- Keyed by (semester, module, programme) and not by an instance, because it is about the siblings
-- together, and a cohort merged into another (handOverWishes) must not take the bound with it.
--
-- WHO SEES IT
--
-- Whoever sees the wishes it qualifies, and only through them: no query reads this table except
-- by joining it onto a wish row that has already passed the visibility filter. It is part of
-- "who wants what", like the priority, and not the owner's own words like the note — after
-- publication it is what tells colleagues that somebody wishing for both cohorts takes only one.
--
-- ROLLBACK
--
-- A new table nobody else references. The previous image does not know it and is not disturbed by
-- it; bounds set in the meantime come back when rolling forward.

-- +goose Up

CREATE TABLE wish_track_limit (
    person_id uuid NOT NULL REFERENCES person (id) ON DELETE CASCADE,
    semester_id uuid NOT NULL REFERENCES semester (id) ON DELETE CASCADE,
    -- CASCADE on both: a bound on an offering that no longer exists means nothing. The wishes it
    -- qualified hold their instances with RESTRICT, so this never removes one that is still read.
    module_id uuid NOT NULL REFERENCES module (id) ON DELETE CASCADE,
    programme_id uuid NOT NULL REFERENCES programme (id) ON DELETE CASCADE,
    max_tracks smallint NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (person_id, semester_id, module_id, programme_id),
    -- Zero is not "none of them" — that is withdrawing the wishes. Nine is more cohorts than any
    -- programme here has ever run in parallel.
    CONSTRAINT wish_track_limit_in_range CHECK (max_tracks BETWEEN 1 AND 9)
);

-- +goose Down

DROP TABLE wish_track_limit;
