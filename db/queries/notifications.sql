-- Mail: subscriptions, the comparison that notices what became official, the fan-out to
-- subscribers, and the outbox. See migration 20261110120000 for the three rules.
--
-- THE DETECTION QUERIES
--
-- Each compares one kind of state the planning keeps with what has been announced
-- (notification_announced, announced_instance), records the new value and writes an event — in
-- one statement, so that a fact is announced once however often the dispatcher runs. `cutoff` is
-- now minus domain.NotificationSettle: a state younger than that has not stood long enough to be
-- told, and one undone in that time is never seen at all.
--
-- None of them reads a wish or an assignment of anybody but the person an event is about.

-- name: NotificationSubscriptionsOf :many
SELECT s.id, s.person_id, s.kind, s.semester_code, s.target_set, s.delivery, s.created_at,
       s.module_id, m.name AS module_name, hp.code AS module_programme_code,
       s.programme_id, p.code AS programme_code, p.title AS programme_title,
       s.subject_group_id, g.code AS subject_group_code, g.name AS subject_group_name,
       g.active AS subject_group_active
FROM notification_subscription s
LEFT JOIN module m ON m.id = s.module_id
LEFT JOIN programme hp ON hp.id = m.home_programme_id
LEFT JOIN programme p ON p.id = s.programme_id
LEFT JOIN subject_group g ON g.id = s.subject_group_id
WHERE s.person_id = sqlc.arg(person_id)::uuid
ORDER BY s.created_at DESC, s.id;

-- name: SubscribeNotification :one
-- Subscribing to what one already follows is that subscription, with the delivery now asked for.
INSERT INTO notification_subscription
    (person_id, kind, semester_code, module_id, programme_id, subject_group_id, target_set, delivery)
VALUES (sqlc.arg(person_id)::uuid, sqlc.arg(kind)::text, sqlc.narg(semester_code)::text,
        sqlc.narg(module_id)::uuid, sqlc.narg(programme_id)::uuid,
        sqlc.narg(subject_group_id)::uuid, sqlc.narg(target_set)::text, sqlc.arg(delivery)::text)
ON CONFLICT ON CONSTRAINT notification_subscription_is_unique
DO UPDATE SET delivery = EXCLUDED.delivery
RETURNING id;

-- name: SetNotificationDelivery :execrows
UPDATE notification_subscription SET delivery = sqlc.arg(delivery)::text
 WHERE id = sqlc.arg(id)::uuid AND person_id = sqlc.arg(person_id)::uuid;

-- name: Unsubscribe :execrows
DELETE FROM notification_subscription
 WHERE id = sqlc.arg(id)::uuid AND person_id = sqlc.arg(person_id)::uuid;

-- name: NotificationSubscriptionCount :one
SELECT count(*)::int FROM notification_subscription WHERE person_id = sqlc.arg(person_id)::uuid;

-- name: NotificationSettingsOf :one
SELECT paused_until FROM notification_settings WHERE person_id = sqlc.arg(person_id)::uuid;

-- name: SetNotificationPausedUntil :exec
INSERT INTO notification_settings (person_id, paused_until)
VALUES (sqlc.arg(person_id)::uuid, sqlc.narg(paused_until)::date)
ON CONFLICT (person_id) DO UPDATE SET paused_until = EXCLUDED.paused_until;

-- name: ModuleExists :one
SELECT EXISTS (SELECT 1 FROM module WHERE id = sqlc.arg(id)::uuid);

-- name: SubjectGroupExists :one
SELECT EXISTS (SELECT 1 FROM subject_group WHERE id = sqlc.arg(id)::uuid);

-- name: LockNotificationDispatch :one
-- One dispatcher at a time, across replicas: the detection queries are idempotent, but two of
-- them interleaving could each see the other's fact as not yet announced.
--
-- Keyed by the schema, because an advisory lock is database-wide: the integration tests run one
-- schema each, in parallel, in one database, and a constant key had them skip each other's ticks.
SELECT pg_try_advisory_xact_lock(hashtext('tallox-notifications:' || current_schema()));

-- name: DetectDemandReleases :execrows
WITH due AS (
    SELECT dc.semester_id, dc.programme_id
      FROM demand_completion dc
     WHERE dc.completed_at <= sqlc.arg(cutoff)::timestamptz
       AND NOT EXISTS (SELECT 1 FROM notification_announced na
                        WHERE na.key = 'DEMAND_RELEASED:' || dc.semester_id || ':' || dc.programme_id)
), marked AS (
    INSERT INTO notification_announced (key)
    SELECT 'DEMAND_RELEASED:' || semester_id || ':' || programme_id FROM due
    ON CONFLICT DO NOTHING
)
INSERT INTO notification_event (kind, semester_id, programme_id)
SELECT 'DEMAND_RELEASED', semester_id, programme_id FROM due;

-- name: DetectReviewCompletions :execrows
WITH due AS (
    SELECT rc.semester_id, rc.programme_id
      FROM review_completion rc
     WHERE rc.completed_at <= sqlc.arg(cutoff)::timestamptz
       AND NOT EXISTS (SELECT 1 FROM notification_announced na
                        WHERE na.key = 'REVIEW_COMPLETED:' || rc.semester_id || ':' || rc.programme_id)
), marked AS (
    INSERT INTO notification_announced (key)
    SELECT 'REVIEW_COMPLETED:' || semester_id || ':' || programme_id FROM due
    ON CONFLICT DO NOTHING
)
INSERT INTO notification_event (kind, semester_id, programme_id)
SELECT 'REVIEW_COMPLETED', semester_id, programme_id FROM due;

-- name: DetectWishWindows :execrows
-- A door compared with the last state announced, where nothing announced reads as open — the same
-- reading an absent wish_window row has. Shut and opened again within the settle time compares
-- equal and says nothing.
WITH current AS (
    SELECT ww.semester_id, ww.subject_group_id,
           CASE WHEN ww.open THEN 'OPEN' ELSE 'SHUT' END AS state,
           'WISH_WINDOW:' || ww.semester_id || ':' || ww.subject_group_id AS key
      FROM wish_window ww
      JOIN semester s ON s.id = ww.semester_id
     WHERE ww.changed_at <= sqlc.arg(cutoff)::timestamptz AND s.phase <> 'FINAL'
), due AS (
    SELECT c.* FROM current c
      LEFT JOIN notification_announced na ON na.key = c.key
     WHERE c.state <> COALESCE(na.value, 'OPEN')
), marked AS (
    INSERT INTO notification_announced (key, value)
    SELECT key, state FROM due
    ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, announced_at = now()
)
INSERT INTO notification_event (kind, semester_id, subject_group_id, detail)
SELECT 'WISH_WINDOW_CHANGED', semester_id, subject_group_id, state FROM due;

-- name: DetectPhases :execrows
-- A semester nobody has announced a phase for started in the first one.
WITH due AS (
    SELECT s.id, s.phase, 'PHASE:' || s.id AS key
      FROM semester s
      LEFT JOIN notification_announced na ON na.key = 'PHASE:' || s.id
     WHERE s.updated_at <= sqlc.arg(cutoff)::timestamptz
       AND s.phase <> COALESCE(na.value, 'DEMAND_PLANNING')
), marked AS (
    INSERT INTO notification_announced (key, value)
    SELECT key, phase FROM due
    ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, announced_at = now()
)
INSERT INTO notification_event (kind, semester_id, detail)
SELECT 'PHASE_ADVANCED', id, phase FROM due;

-- name: DetectPublications :execrows
-- The four irreversible acts of the dean's office. No settle time: there is no taking them back.
WITH stated AS (
    SELECT 'WISHES_PUBLISHED' AS kind, id FROM semester WHERE wishes_published_at IS NOT NULL
    UNION ALL
    SELECT 'ASSIGNMENTS_PUBLISHED', id FROM semester WHERE assignments_published_at IS NOT NULL
    UNION ALL
    SELECT 'DESIRED_HOURS_FROZEN', id FROM semester WHERE desired_hours_frozen_at IS NOT NULL
    UNION ALL
    SELECT 'DESIRED_HOURS_PUBLISHED', id FROM semester WHERE desired_hours_published_at IS NOT NULL
), due AS (
    SELECT st.kind, st.id FROM stated st
     WHERE NOT EXISTS (SELECT 1 FROM notification_announced na WHERE na.key = st.kind || ':' || st.id)
), marked AS (
    INSERT INTO notification_announced (key) SELECT kind || ':' || id FROM due
    ON CONFLICT DO NOTHING
)
INSERT INTO notification_event (kind, semester_id)
SELECT kind, id FROM due;

-- name: DetectConfirmationDues :execrows
-- The due date set, or moved. Cleared says nothing.
WITH due AS (
    SELECT s.id, s.assignment_confirmation_due::text AS due_date, 'CONFIRMATION_DUE:' || s.id AS key
      FROM semester s
      LEFT JOIN notification_announced na ON na.key = 'CONFIRMATION_DUE:' || s.id
     WHERE s.assignment_confirmation_due IS NOT NULL
       AND s.updated_at <= sqlc.arg(cutoff)::timestamptz
       AND s.phase <> 'FINAL'
       AND s.assignment_confirmation_due::text IS DISTINCT FROM na.value
), marked AS (
    INSERT INTO notification_announced (key, value)
    SELECT key, due_date FROM due
    ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, announced_at = now()
)
INSERT INTO notification_event (kind, semester_id, detail)
SELECT 'CONFIRMATION_DUE', id, due_date FROM due;

-- name: DetectConfirmationReminders :execrows
-- Three days before the date, once, to each account holding something in the semester whose
-- confirmation is missing or out of date — an event about that person's own rows, so only they
-- can ever be told.
WITH open_confirmations AS (
    SELECT s.id AS semester_id, s.assignment_confirmation_due::text AS due_date, f.person_id
      FROM semester s
      JOIN assignment_fingerprint f ON f.semester_id = s.id
      LEFT JOIN assignment_confirmation c ON c.semester_id = s.id AND c.person_id = f.person_id
     WHERE s.assignment_confirmation_due IS NOT NULL
       AND s.phase <> 'FINAL'
       AND sqlc.arg(today)::date BETWEEN s.assignment_confirmation_due - 3 AND s.assignment_confirmation_due
       AND (c.fingerprint IS NULL OR c.fingerprint <> f.fingerprint)
), due AS (
    SELECT oc.*, 'CONFIRMATION_REMINDER:' || oc.semester_id || ':' || oc.due_date || ':' || oc.person_id AS key
      FROM open_confirmations oc
     WHERE NOT EXISTS (SELECT 1 FROM notification_announced na
                        WHERE na.key = 'CONFIRMATION_REMINDER:' || oc.semester_id || ':' || oc.due_date
                                       || ':' || oc.person_id)
), marked AS (
    INSERT INTO notification_announced (key) SELECT key FROM due ON CONFLICT DO NOTHING
)
INSERT INTO notification_event (kind, semester_id, person_id, detail)
SELECT 'CONFIRMATION_DUE', semester_id, person_id, 'REMINDER:' || due_date FROM due;

-- name: DetectOfferedInstances :execrows
-- An instance in a released demand, once both have stood for the settle time. Declared and
-- deleted again in that time, it was never offered; declared after the release, it is offered
-- when it has stood. The subject group is the module's at this moment.
WITH due AS (
    SELECT ci.id, ci.semester_id, ci.module_id, ci.programme_id, ci.track, msg.subject_group_id
      FROM course_instance ci
      JOIN demand_completion dc ON dc.semester_id = ci.semester_id AND dc.programme_id = ci.programme_id
      JOIN semester s ON s.id = ci.semester_id
      LEFT JOIN module_subject_group msg ON msg.module_id = ci.module_id
     WHERE ci.created_at <= sqlc.arg(cutoff)::timestamptz
       AND dc.completed_at <= sqlc.arg(cutoff)::timestamptz
       AND s.phase <> 'FINAL'
       AND NOT EXISTS (SELECT 1 FROM announced_instance ai WHERE ai.course_instance_id = ci.id)
), marked AS (
    INSERT INTO announced_instance (course_instance_id, semester_id, module_id, programme_id, track)
    SELECT id, semester_id, module_id, programme_id, track FROM due
    ON CONFLICT DO NOTHING
)
INSERT INTO notification_event
    (kind, semester_id, module_id, programme_id, subject_group_id, course_instance_id, detail)
SELECT 'MODULE_OFFERED', semester_id, module_id, programme_id, subject_group_id, id, track FROM due;

-- name: DetectWithdrawnInstances :execrows
-- An announced instance that no longer exists, told to whoever heard it was on.
WITH gone AS (
    DELETE FROM announced_instance ai
     WHERE NOT EXISTS (SELECT 1 FROM course_instance ci WHERE ci.id = ai.course_instance_id)
    RETURNING ai.course_instance_id, ai.semester_id, ai.module_id, ai.programme_id, ai.track
)
INSERT INTO notification_event
    (kind, semester_id, module_id, programme_id, subject_group_id, course_instance_id, detail)
SELECT 'MODULE_WITHDRAWN', g.semester_id, g.module_id, g.programme_id, msg.subject_group_id,
       g.course_instance_id, g.track
  FROM gone g
  LEFT JOIN module_subject_group msg ON msg.module_id = g.module_id;

-- name: DetectAdvanceWishes :execrows
-- An advance wish resolved by its programme's release: converted into wishes, or not offered
-- because the released demand has no cohort of the module. NOT_OFFERED can still become CONVERTED
-- when a cohort is declared later, and is told again then.
WITH current AS (
    SELECT aw.id, aw.person_id, aw.module_id, aw.programme_id, s.id AS semester_id,
           CASE WHEN aw.converted_at IS NOT NULL THEN 'CONVERTED' ELSE 'NOT_OFFERED' END AS state
      FROM advance_wish aw
      JOIN semester s ON s.code = aw.semester_code
      JOIN demand_completion dc ON dc.semester_id = s.id AND dc.programme_id = aw.programme_id
     WHERE dc.completed_at <= sqlc.arg(cutoff)::timestamptz
       AND s.phase <> 'FINAL'
       AND (aw.converted_at IS NOT NULL
            OR NOT EXISTS (SELECT 1 FROM course_instance ci
                            WHERE ci.semester_id = s.id AND ci.programme_id = aw.programme_id
                              AND ci.module_id = aw.module_id))
), due AS (
    SELECT c.*, 'ADVANCE_WISH:' || c.id AS key
      FROM current c
      LEFT JOIN notification_announced na ON na.key = 'ADVANCE_WISH:' || c.id
     WHERE c.state IS DISTINCT FROM na.value
), marked AS (
    INSERT INTO notification_announced (key, value)
    SELECT key, state FROM due
    ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, announced_at = now()
)
INSERT INTO notification_event
    (kind, semester_id, module_id, programme_id, subject_group_id, person_id, detail)
SELECT 'ADVANCE_WISH_RESOLVED', d.semester_id, d.module_id, d.programme_id, msg.subject_group_id,
       d.person_id, d.state
  FROM due d
  LEFT JOIN module_subject_group msg ON msg.module_id = d.module_id;

-- name: PendingNotificationEvents :many
SELECT id FROM notification_event WHERE fanned_out_at IS NULL ORDER BY occurred_at FOR UPDATE;

-- name: FanOutNotificationEvents :execrows
-- Who is to be told: every active, unpaused subscriber whose subscription matches, once per
-- event however many of their subscriptions do. A target set is resolved against the
-- subscriber's own rows and nobody else's; an event about one person reaches only that person.
INSERT INTO notification_delivery (event_id, person_id, subscription_id, delivery)
SELECT DISTINCT ON (e.id, s.person_id) e.id, s.person_id, s.id, s.delivery
  FROM notification_event e
  JOIN notification_subscription s ON s.kind = e.kind
  JOIN person p ON p.id = s.person_id AND p.active
  LEFT JOIN semester sem ON sem.id = e.semester_id
  LEFT JOIN notification_settings ns ON ns.person_id = s.person_id
 WHERE e.id = ANY(sqlc.arg(event_ids)::uuid[])
   AND (ns.paused_until IS NULL OR ns.paused_until < sqlc.arg(today)::date)
   AND (s.semester_code IS NULL OR s.semester_code = sem.code)
   AND (s.module_id IS NULL OR s.module_id = e.module_id)
   AND (s.programme_id IS NULL OR s.programme_id = e.programme_id)
   AND (s.subject_group_id IS NULL OR s.subject_group_id = e.subject_group_id)
   AND (e.person_id IS NULL OR e.person_id = s.person_id)
   AND (s.target_set IS NULL
        OR (s.target_set = 'MY_ADVANCE_WISHES' AND EXISTS (
                SELECT 1 FROM advance_wish aw
                 WHERE aw.person_id = s.person_id AND aw.module_id = e.module_id))
        OR (s.target_set = 'MY_WISHES' AND EXISTS (
                SELECT 1 FROM wish w
                  JOIN course_instance ci ON ci.id = w.course_instance_id
                  LEFT JOIN module_subject_group msg ON msg.module_id = ci.module_id
                 WHERE w.person_id = s.person_id AND ci.semester_id = e.semester_id
                   AND (e.subject_group_id IS NULL OR msg.subject_group_id = e.subject_group_id)))
        OR (s.target_set = 'MY_ASSIGNMENTS' AND EXISTS (
                SELECT 1 FROM assignment a
                  JOIN instance_part ip ON ip.id = a.instance_part_id
                  JOIN course_instance ci ON ci.id = ip.course_instance_id
                 WHERE a.person_id = s.person_id AND ci.semester_id = e.semester_id))
        OR (s.target_set = 'MY_COMPETENCES' AND EXISTS (
                SELECT 1 FROM competence c
                 WHERE c.person_id = s.person_id AND c.module_id = e.module_id))
        OR (s.target_set = 'MY_SUBJECT_GROUPS' AND EXISTS (
                SELECT 1 FROM person_subject_group psg
                 WHERE psg.person_id = s.person_id AND psg.subject_group_id = e.subject_group_id))
        OR (s.target_set = 'MY_PROGRAMMES' AND EXISTS (
                SELECT 1 FROM person_role_scope prs
                 WHERE prs.person_id = s.person_id AND prs.role = 'PROGRAMME_LEAD'
                   AND prs.programme_id = e.programme_id)))
 ORDER BY e.id, s.person_id, (s.delivery = 'IMMEDIATE') DESC, s.created_at
ON CONFLICT DO NOTHING;

-- name: MarkNotificationEventsFannedOut :exec
UPDATE notification_event SET fanned_out_at = now() WHERE id = ANY(sqlc.arg(event_ids)::uuid[]);

-- name: DueNotificationDeliveries :many
-- What is to be sent now, with everything a mail says about it: immediate deliveries always,
-- daily ones that arrived before the last morning slot. Ordered so that one person's rows are
-- together.
SELECT d.event_id, d.person_id, d.subscription_id, d.delivery,
       p.name AS person_name,
       e.kind, e.detail, e.occurred_at, e.course_instance_id,
       COALESCE(sem.code, '')::text AS semester_code,
       e.module_id, COALESCE(m.name, '')::text AS module_name,
       COALESCE(hp.code, '')::text AS module_programme_code,
       e.programme_id, COALESCE(pr.code, '')::text AS programme_code,
       COALESCE(pr.title, '')::text AS programme_title,
       e.subject_group_id, COALESCE(g.code, '')::text AS subject_group_code,
       COALESCE(g.name, '')::text AS subject_group_name,
       s.kind AS sub_kind, s.target_set AS sub_target_set, s.semester_code AS sub_semester_code,
       COALESCE(sm.name, '')::text AS sub_module_name,
       COALESCE(sp.code, '')::text AS sub_programme_code,
       COALESCE(sg.name, '')::text AS sub_subject_group_name
  FROM notification_delivery d
  JOIN notification_event e ON e.id = d.event_id
  JOIN person p ON p.id = d.person_id
  LEFT JOIN semester sem ON sem.id = e.semester_id
  LEFT JOIN module m ON m.id = e.module_id
  LEFT JOIN programme hp ON hp.id = m.home_programme_id
  LEFT JOIN programme pr ON pr.id = e.programme_id
  LEFT JOIN subject_group g ON g.id = e.subject_group_id
  LEFT JOIN notification_subscription s ON s.id = d.subscription_id
  LEFT JOIN module sm ON sm.id = s.module_id
  LEFT JOIN programme sp ON sp.id = s.programme_id
  LEFT JOIN subject_group sg ON sg.id = s.subject_group_id
 WHERE d.mail_id IS NULL
   AND (d.delivery = 'IMMEDIATE' OR d.created_at < sqlc.arg(daily_cutoff)::timestamptz)
 ORDER BY d.person_id, e.kind, sem.code, pr.code, m.name, e.occurred_at
 LIMIT 5000;

-- name: QueueMail :one
INSERT INTO mail_outbox (person_id, category, reply_to, subject, html, text_body)
VALUES (sqlc.arg(person_id)::uuid, sqlc.arg(category)::text, sqlc.arg(reply_to)::text,
        sqlc.arg(subject)::text, sqlc.arg(html)::text, sqlc.arg(text_body)::text)
RETURNING id;

-- name: LinkDeliveriesToMail :exec
UPDATE notification_delivery SET mail_id = sqlc.arg(mail_id)::uuid
 WHERE person_id = sqlc.arg(person_id)::uuid AND event_id = ANY(sqlc.arg(event_ids)::uuid[])
   AND mail_id IS NULL;

-- name: DropMailToInactive :execrows
-- A mail queued for somebody who has since been deactivated is not sent.
UPDATE mail_outbox o SET state = 'DROPPED', last_error = 'inactive'
  FROM person p
 WHERE p.id = o.person_id AND o.state = 'PENDING' AND NOT p.active;

-- name: ClaimMail :many
-- The next mails to send, leased for ten minutes so that no other sender takes them while this
-- one talks to the server. The address is read from the account now — never from anywhere else.
WITH next AS (
    SELECT o.id
      FROM mail_outbox o
      JOIN person p ON p.id = o.person_id AND p.active
     WHERE o.state = 'PENDING' AND o.next_attempt_at <= sqlc.arg(now)::timestamptz
     ORDER BY o.created_at
     LIMIT sqlc.arg(max_rows)::int
       FOR UPDATE OF o SKIP LOCKED
)
UPDATE mail_outbox o
   SET next_attempt_at = sqlc.arg(now)::timestamptz + interval '10 minutes'
  FROM next, person p
 WHERE o.id = next.id AND p.id = o.person_id
RETURNING o.id, o.category, o.reply_to, o.subject, o.html, o.text_body, o.attempts,
          p.mail::text AS address, p.name AS person_name;

-- name: MarkMailSent :exec
UPDATE mail_outbox SET state = 'SENT', sent_at = now(), attempts = attempts + 1, last_error = ''
 WHERE id = sqlc.arg(id)::uuid;

-- name: MarkMailFailed :exec
-- Retried until the attempts run out, then FAILED — which the nightly report counts.
UPDATE mail_outbox
   SET attempts = attempts + 1,
       last_error = left(sqlc.arg(last_error)::text, 500),
       next_attempt_at = sqlc.arg(next_attempt_at)::timestamptz,
       state = CASE WHEN attempts + 1 >= sqlc.arg(max_attempts)::int THEN 'FAILED' ELSE 'PENDING' END
 WHERE id = sqlc.arg(id)::uuid;

-- name: PruneNotifications :exec
WITH old_events AS (
    DELETE FROM notification_event WHERE occurred_at < sqlc.arg(before)::timestamptz
)
DELETE FROM mail_outbox WHERE created_at < sqlc.arg(before)::timestamptz AND state <> 'PENDING';

-- name: MailCounts :one
-- For the nightly report and the smoke test: figures, never contents.
SELECT count(*) FILTER (WHERE state = 'SENT' AND sent_at >= sqlc.arg(since)::timestamptz)::int AS sent,
       count(*) FILTER (WHERE state = 'FAILED' AND created_at >= sqlc.arg(since)::timestamptz)::int AS failed,
       count(*) FILTER (WHERE state = 'PENDING')::int AS pending,
       count(*) FILTER (WHERE category = 'ANNOUNCEMENT' AND created_at >= sqlc.arg(since)::timestamptz)::int AS announcement_mails,
       COALESCE(max(sent_at), 'epoch'::timestamptz)::timestamptz AS last_sent_at
  FROM mail_outbox;
