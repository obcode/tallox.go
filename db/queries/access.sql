-- The access log: one row per operation, plus the sign-ins that were refused before there was
-- an operation at all.
--
-- The theme of this file is that every read is an aggregate or a bounded page. An audit trail
-- is the one table where "select everything and filter in Go" is not merely slow but wrong:
-- the row limit is what keeps a support question from pulling a term's worth of colleagues'
-- movements into a process that then logs its own memory usage.

-- name: RecordAccess :exec
-- Append one entry. Called on the request path, best-effort — see graph.RecordAccess.
--
-- Nothing here is read back. The caller has no use for the id, and returning one would cost a
-- round trip on every single request for a value nobody wants.
INSERT INTO access_log (
    actor_id, actor_mail, door, token_id, roles, narrowed_from,
    operation, fields, mutation, outcome, error_code, duration_ms, source_ip
) VALUES (
    sqlc.narg('actor_id'), sqlc.narg('actor_mail'), sqlc.arg('door'), sqlc.narg('token_id'),
    sqlc.arg('roles'), sqlc.narg('narrowed_from'), sqlc.narg('operation'), sqlc.arg('fields'),
    sqlc.arg('mutation'), sqlc.arg('outcome'), sqlc.narg('error_code'),
    sqlc.narg('duration_ms'), sqlc.narg('source_ip')
);

-- name: AccessLogEntries :many
-- One page of the log, newest first.
--
-- Keyset pagination on (at, id) rather than OFFSET: the table grows at the head while somebody
-- is reading it, and OFFSET would silently skip or repeat rows exactly when the log is busiest —
-- which is when somebody is most likely to be reading it because something happened.
SELECT a.id, a.at, a.actor_id, a.actor_mail, a.door, a.token_id, a.roles, a.narrowed_from,
       a.operation, a.fields, a.mutation, a.outcome, a.error_code, a.duration_ms, a.source_ip,
       p.name AS actor_name
FROM access_log a
LEFT JOIN person p ON p.id = a.actor_id
WHERE (sqlc.narg('actor_id')::uuid IS NULL OR a.actor_id = sqlc.narg('actor_id')::uuid)
  AND (sqlc.narg('mail')::text IS NULL
       OR a.actor_mail ILIKE '%' || sqlc.narg('mail')::text || '%')
  AND (sqlc.narg('door')::text IS NULL OR a.door = sqlc.narg('door')::text)
  AND (NOT sqlc.arg('only_refused')::boolean OR a.outcome <> 'OK')
  AND (NOT sqlc.arg('only_mutations')::boolean OR a.mutation)
  AND (sqlc.narg('from')::timestamptz IS NULL OR a.at >= sqlc.narg('from')::timestamptz)
  AND (sqlc.narg('until')::timestamptz IS NULL OR a.at < sqlc.narg('until')::timestamptz)
  -- The cursor. Both halves, because two entries can share a microsecond and a cursor on the
  -- timestamp alone would drop whichever of them landed second.
  AND (sqlc.narg('before_at')::timestamptz IS NULL
       OR (a.at, a.id) < (sqlc.narg('before_at')::timestamptz, sqlc.arg('before_id')::uuid))
ORDER BY a.at DESC, a.id DESC
LIMIT sqlc.arg('lim');

-- name: AccessLogCounts :one
-- The headline figures for one window, in one pass.
--
-- One query rather than six, because the nightly report and the page both want all of them and
-- six round trips to count the same rows six times is how a report becomes something one is
-- reluctant to run.
SELECT
    count(*)::bigint                                                    AS total,
    count(*) FILTER (WHERE door = 'INTERACTIVE')::bigint                AS interactive,
    count(*) FILTER (WHERE door = 'TOKEN')::bigint                      AS via_token,
    count(*) FILTER (WHERE mutation)::bigint                            AS mutations,
    count(*) FILTER (WHERE outcome = 'ERROR')::bigint                   AS errors,
    count(*) FILTER (WHERE outcome = 'REFUSED_AUTH')::bigint            AS refused_auth,
    count(*) FILTER (WHERE outcome = 'REFUSED_SCOPE')::bigint           AS refused_scope,
    count(*) FILTER (WHERE outcome = 'REFUSED_INTERACTIVE')::bigint     AS refused_interactive,
    count(DISTINCT actor_id)::bigint                                    AS people
FROM access_log
WHERE at >= sqlc.arg('from')::timestamptz
  AND at < sqlc.arg('until')::timestamptz;

-- name: AccessLogByRole :many
-- How much happened under each role, for one window.
--
-- Over the EFFECTIVE roles, so a narrowed session counts under what it was narrowed to. That is
-- the honest reading: the request was judged by those roles, whatever the person holds.
SELECT role::text AS role, count(*)::bigint AS operations
FROM access_log, unnest(roles) AS role
WHERE at >= sqlc.arg('from')::timestamptz
  AND at < sqlc.arg('until')::timestamptz
GROUP BY role
ORDER BY operations DESC, role;

-- name: AccessLogRefusedSignIns :many
-- Who was turned away, and why, for one window.
--
-- Grouped rather than listed: somebody whose account has no person row will retry, and twelve
-- identical lines say nothing that one line with a count does not. This is the part of the
-- nightly report that names people, and it names them because being turned away is the event.
SELECT actor_mail::text AS mail,
       COALESCE(error_code, '')::text AS reason,
       door,
       count(*)::bigint AS attempts,
       max(at)::timestamptz AS last_at
FROM access_log
WHERE outcome = 'REFUSED_AUTH'
  AND at >= sqlc.arg('from')::timestamptz
  AND at < sqlc.arg('until')::timestamptz
  AND actor_mail IS NOT NULL
GROUP BY actor_mail, error_code, door
ORDER BY last_at DESC;

-- name: AccessLogMutations :many
-- Every change made in one window, by whom and to what — grouped by person and root field.
--
-- The field name is as specific as this gets, and deliberately so: see the note in the
-- migration. "setPersonRoles, three times, by X" is a report; the arguments would be a copy of
-- the data with none of the policy on it.
SELECT COALESCE(a.actor_mail, '')::text AS mail,
       field::text AS field,
       count(*)::bigint AS calls,
       max(a.at)::timestamptz AS last_at
FROM access_log a, unnest(a.fields) AS field
WHERE a.mutation
  AND a.outcome = 'OK'
  AND a.at >= sqlc.arg('from')::timestamptz
  AND a.at < sqlc.arg('until')::timestamptz
GROUP BY a.actor_mail, field
ORDER BY last_at DESC;

-- name: PruneAccessLog :one
-- Delete what is older than the cutoff and say how much that was.
--
-- Returning the count is not decoration: it goes into the nightly report, so a prune that has
-- silently stopped working shows up as a number that stops moving rather than as a table that
-- quietly grows for a year.
WITH deleted AS (
    DELETE FROM access_log WHERE at < sqlc.arg('cutoff')::timestamptz RETURNING 1
)
SELECT count(*)::bigint AS deleted FROM deleted;
