-- Migration 7: the semester code takes the shape the ZPA uses — 2026-WS, 2026-SS.
--
-- Migration 4 chose 2026W. That was a code this project made up, and it turned out to compete
-- with a name the faculty already has: the ZPA writes "WS 2026" and "SS 2026", and the
-- neighbouring exam-planning tool writes 2026-WS. A semester code appears in URLs, in export
-- filenames and in the evaluation scripts colleagues write against the API, which is precisely
-- where two spellings of the same semester cost more than the rename does today.
--
-- The term keeps two letters, as everywhere else in the faculty, and the separator is a hyphen
-- rather than the ZPA's space: a space in a URL, a filename or a shell argument has to be
-- quoted, and the thing being separated is not a phrase.
--
-- What does not change is the property every ORDER BY in the system leans on. 2026-SS sorts
-- before 2026-WS because the year still leads and S still precedes W, which is the order the
-- terms happen in — so `ORDER BY code DESC` stays chronological and the index below stays the
-- right one.
--
-- Editing migration 4 would have been the smaller diff and is not allowed: it has run, here
-- and on the host, and a migration that changes after the fact is one nobody can reason about.
--
-- +goose Up

-- The old constraint has to go first — the rows are rewritten *through* it.
ALTER TABLE semester DROP CONSTRAINT semester_code_is_year_and_term;

-- 2027S becomes 2027-SS, 2026W becomes 2026-WS. The WHERE is not decoration: it makes the
-- statement a no-op on a database that somehow already holds the new form, rather than a
-- corruption of it.
UPDATE semester
SET code = left(code, 4) || '-' || right(code, 1) || 'S'
WHERE code ~ '^[0-9]{4}[SW]$';

-- Four digits, a hyphen, and SS or WS. Enforced here and not only in Go, for the same reason
-- as before: the chronological sort is a promise made to every query in the system, and one
-- row reading "WS 2026" would quietly break it for the whole table.
ALTER TABLE semester
    ADD CONSTRAINT semester_code_is_year_and_term CHECK (code ~ '^[0-9]{4}-(SS|WS)$');

-- +goose Down

ALTER TABLE semester DROP CONSTRAINT semester_code_is_year_and_term;

UPDATE semester
SET code = left(code, 4) || left(right(code, 2), 1)
WHERE code ~ '^[0-9]{4}-(SS|WS)$';

ALTER TABLE semester
    ADD CONSTRAINT semester_code_is_year_and_term CHECK (code ~ '^[0-9]{4}[SW]$');
