-- Migration: lecturers confirm their assignments before they go to the examination office.
--
-- The faculty asked for one date per semester, set by the dean's office, by which everybody who
-- teaches checks what they have been given — a week before the plan is entered into the
-- examination office's system. Until then a colleague learned about a mistake when the timetable
-- office asked.
--
-- Two halves, and they are not the same kind of thing:
--
--   semester.assignment_confirmation_due   a date, a decision of the dean's office. It may be set,
--                                          moved and cleared; it blocks nothing.
--   assignment_confirmation                one row per person who said "passt so", with a
--                                          fingerprint of what they confirmed.
--
-- The state a screen shows — confirmed, open, out of date — is never stored. It is derived on
-- read by comparing the stored fingerprint with the person's current assignments, so a change
-- that nobody remembered to report (a part handed to somebody else, an hour figure corrected, a
-- part removed) makes a confirmation out of date by itself. A stored status would be a second
-- copy of the assignments that every writer had to keep in step, and the one that forgot would
-- leave a colleague's "confirmed" standing over a plan they never saw.
--
-- No free text. "Passt nicht, weil …" would be a note about a person, which is decided
-- deliberately and not in passing; disagreement goes to the subject group lead as before.

-- +goose Up
ALTER TABLE semester
    ADD COLUMN IF NOT EXISTS assignment_confirmation_due date,
    ADD COLUMN IF NOT EXISTS assignment_confirmation_due_by uuid
        REFERENCES person (id) ON DELETE SET NULL;

CREATE TABLE IF NOT EXISTS assignment_confirmation (
    semester_id uuid NOT NULL REFERENCES semester (id) ON DELETE CASCADE,
    -- An account. Somebody without one cannot sign in to confirm, and their assignments are
    -- nobody's "own" (migration 17); for them the arrangement with the subject group lead stays.
    person_id uuid NOT NULL REFERENCES person (id) ON DELETE CASCADE,
    confirmed_at timestamptz NOT NULL DEFAULT now(),
    -- What was confirmed: a digest of the person's parts and their hours in this semester, as
    -- computed by the query that compares it (AssignmentFingerprints). Opaque on purpose — it is
    -- compared, never read.
    fingerprint text NOT NULL,
    PRIMARY KEY (semester_id, person_id)
);

-- What a person holds in a semester, reduced to one comparable value: their parts and the hours
-- each is worth to them. A view rather than the expression in two queries, because confirming and
-- comparing must compute it identically — a digest written twice is two digests the day somebody
-- edits one of them, and every confirmation would turn stale overnight, or never.
--
-- Hours are part of it because that is what the colleague confirms: a lecture cut from 4 to 2
-- hours is a different teaching load, even though it is still the same part. A note on the
-- assignment is not: it records the decision and is not something the holder signs off.
CREATE OR REPLACE VIEW assignment_fingerprint AS
SELECT ci.semester_id,
       a.person_id,
       md5(string_agg(ip.id::text || '=' || COALESCE(ip.teaching_hours::text, ''), ','
                      ORDER BY ip.id)) AS fingerprint
FROM assignment a
JOIN instance_part ip ON ip.id = a.instance_part_id
JOIN course_instance ci ON ci.id = ip.course_instance_id
WHERE a.person_id IS NOT NULL
GROUP BY ci.semester_id, a.person_id;

-- +goose Down
DROP VIEW IF EXISTS assignment_fingerprint;
DROP TABLE IF EXISTS assignment_confirmation;
ALTER TABLE semester
    DROP COLUMN IF EXISTS assignment_confirmation_due_by,
    DROP COLUMN IF EXISTS assignment_confirmation_due;
