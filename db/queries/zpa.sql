-- The cache of the examination office's module master data, its runs and its changes.
--
-- The theme of this file is that a sync never deletes and never overwrites blindly. Every
-- write says what it expects to find, so that the difference between "unchanged", "changed",
-- "new" and "gone" is decided by the database against the row it actually holds, rather than
-- by Go against a row it read a moment ago.

-- name: UpsertZPAObject :one
-- One object, written whole.
--
-- What kind of change this was — new, changed, or unchanged — is decided by the caller against
-- the hashes ZPAObjectStateByKind already returned, not here. Deciding it in SQL was the first
-- attempt and it was worse: it needed a subquery in RETURNING whose visibility rules are
-- subtle, to answer a question the caller had already answered for free.
--
-- last_changed_at moves only when the content really differs, and that comparison is against
-- the generated content_hash — so it is the canonical jsonb form being compared, not whatever
-- key order arrived. A sync that changes nothing leaves every timestamp but last_seen_at
-- alone, which is what makes "what actually moved, and when" answerable months later.
INSERT INTO zpa_object (kind, zpa_id, payload, label)
VALUES ($1, $2, $3, $4)
ON CONFLICT (kind, zpa_id) DO UPDATE
SET payload         = EXCLUDED.payload,
    label           = EXCLUDED.label,
    last_seen_at    = now(),
    last_changed_at = CASE
                          WHEN zpa_object.content_hash IS DISTINCT FROM
                               encode(digest(EXCLUDED.payload::text, 'sha256'), 'hex')
                              THEN now()
                          ELSE zpa_object.last_changed_at
                      END,
    -- A returning object is present again. first_seen_at is deliberately untouched: the day we
    -- first saw it is a fact, and overwriting it would lose the only evidence it had ever been
    -- away.
    gone_at         = NULL
RETURNING id, content_hash, first_seen_at, last_changed_at;

-- name: ZPAObjectStateByKind :many
-- What is currently held for one kind: enough to decide the diff without reading the payloads.
--
-- The payload itself is fetched only for the objects that turn out to have changed, which
-- keeps a nightly run that changes nothing from moving 2.7 MB through the wire for no reason.
SELECT zpa_id, content_hash, (gone_at IS NOT NULL)::boolean AS is_gone
FROM zpa_object
WHERE kind = $1;

-- name: ZPAObjectPayload :one
SELECT id, payload, label
FROM zpa_object
WHERE kind = $1 AND zpa_id = $2;

-- name: RetireMissingZPAObjects :many
-- Mark everything of one kind that a successful fetch did not mention.
--
-- Only ever called after a fetch that succeeded and returned something — the client refuses an
-- empty result for exactly this reason. Rows are marked, never deleted.
UPDATE zpa_object
SET gone_at = now()
WHERE kind = $1
  AND gone_at IS NULL
  AND NOT (zpa_id = ANY (sqlc.arg(present)::bigint[]))
RETURNING id, zpa_id, label, payload;

-- name: StartZPASyncRun :one
-- Written before the first fetch, not after the last one. A run that crashes then leaves a
-- RUNNING row somebody can see, rather than no row at all — which is indistinguishable from a
-- job that was never scheduled, and is how "the import stopped three weeks ago" happens.
INSERT INTO zpa_sync_run (trigger, started_by)
VALUES ($1, $2)
RETURNING id, trigger, started_by, started_at, finished_at, status,
          fetched, appeared, changed, disappeared, error;

-- name: FinishZPASyncRun :one
UPDATE zpa_sync_run
SET status      = $2,
    finished_at = now(),
    fetched     = $3,
    appeared    = $4,
    changed     = $5,
    disappeared = $6,
    error       = $7
WHERE id = $1
RETURNING id, trigger, started_by, started_at, finished_at, status,
          fetched, appeared, changed, disappeared, error;

-- name: RecordZPASyncRunKind :exec
INSERT INTO zpa_sync_run_kind (run_id, kind, status, fetched, error)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (run_id, kind) DO UPDATE
SET status = EXCLUDED.status, fetched = EXCLUDED.fetched, error = EXCLUDED.error;

-- name: RecordZPAChange :exec
INSERT INTO zpa_change (run_id, object_id, kind, zpa_id, label, change,
                        payload_before, payload_after, changed_keys)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: ZPASyncRuns :many
SELECT r.id, r.trigger, r.started_by, p.name AS started_by_name, r.started_at,
       r.finished_at, r.status, r.fetched, r.appeared, r.changed, r.disappeared, r.error
FROM zpa_sync_run r
LEFT JOIN person p ON p.id = r.started_by
ORDER BY r.started_at DESC
LIMIT $1;

-- name: ZPASyncRunByID :one
SELECT r.id, r.trigger, r.started_by, p.name AS started_by_name, r.started_at,
       r.finished_at, r.status, r.fetched, r.appeared, r.changed, r.disappeared, r.error
FROM zpa_sync_run r
LEFT JOIN person p ON p.id = r.started_by
WHERE r.id = $1;

-- name: LastSuccessfulZPASyncRun :one
-- The one number the interface shows largest, and the one the deploy smoke check asserts is
-- recent. PARTIAL counts: it fetched something, and a run that got three of four endpoints is
-- not the silence this is watching for.
SELECT r.id, r.trigger, r.started_by, p.name AS started_by_name, r.started_at,
       r.finished_at, r.status, r.fetched, r.appeared, r.changed, r.disappeared, r.error
FROM zpa_sync_run r
LEFT JOIN person p ON p.id = r.started_by
WHERE r.status IN ('SUCCEEDED', 'PARTIAL')
ORDER BY r.started_at DESC
LIMIT 1;

-- name: ZPASyncRunKinds :many
SELECT run_id, kind, status, fetched, error
FROM zpa_sync_run_kind
WHERE run_id = $1
ORDER BY kind;

-- name: ZPAChangesByRun :many
SELECT id, run_id, object_id, kind, zpa_id, label, change, changed_keys, detected_at
FROM zpa_change
WHERE run_id = $1
ORDER BY kind, zpa_id;

-- name: FailAbandonedZPASyncRuns :many
-- Called at startup, beside the protected-admin reconciliation.
--
-- A process that dies mid-sync leaves a RUNNING row forever, and forever is long enough that
-- the interface would show a run in progress that nothing is progressing. The cutoff is passed
-- in rather than hard-coded here so the caller's reasoning about it stays in Go.
UPDATE zpa_sync_run
SET status      = 'FAILED',
    finished_at = now(),
    error       = 'der Prozess wurde beendet, bevor der Lauf fertig war'
WHERE status = 'RUNNING'
  AND started_at < sqlc.arg(older_than)::timestamptz
RETURNING id;
