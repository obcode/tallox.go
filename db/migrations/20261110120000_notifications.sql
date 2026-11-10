-- Migration: mail — subscriptions to what the planning makes official, and announcements.
--
-- Decided 2026-11-10. Three rules shape every table here:
--
--   Only what is official. Nothing in the planning writes an event. The dispatcher compares the
--   state the planning already keeps — a release, a door, a publication timestamp, a phase, an
--   instance in a released demand — with what it has announced (notification_announced,
--   announced_instance), and announces the difference once it has stood for a few minutes
--   (domain.NotificationSettle). A release clicked by mistake and withdrawn in that time was
--   never there as far as any inbox is concerned; a rolled-back act left no state to compare.
--   And it covers every way into the planning — GUI, token, a copy of last year's demand —
--   without one hook per write path that the next write path forgets.
--
--   Always opt-in. notification_subscription holds only rows a person made for themselves. No
--   migration, import or default ever writes one.
--
--   A mail is an export. Events carry ids, not text; the dispatcher renders them from facts every
--   account may read, and a person's own rows only for that person. Mail goes to person.mail —
--   the address the login asserts — and to nobody else.
--
-- Events, deliveries and queued mail are pruned after 90 days (domain.NotificationRetention),
-- like the access log. Announcements are kept: they are official post, and their archive is what
-- a recipient reads them again from.

-- +goose Up

CREATE TABLE IF NOT EXISTS notification_subscription (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    person_id uuid NOT NULL REFERENCES person (id) ON DELETE CASCADE,
    kind text NOT NULL,
    -- A code rather than a reference: nobody creates a semester, and "tell me about 2028-SS" is
    -- a sensible thing to say before anything has been decided about it.
    semester_code text,
    module_id uuid REFERENCES module (id) ON DELETE CASCADE,
    programme_id uuid REFERENCES programme (id) ON DELETE CASCADE,
    subject_group_id uuid REFERENCES subject_group (id) ON DELETE CASCADE,
    target_set text,
    delivery text NOT NULL DEFAULT 'IMMEDIATE',
    created_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT notification_subscription_kind_is_known CHECK (kind IN (
        'MODULE_OFFERED', 'MODULE_WITHDRAWN', 'DEMAND_RELEASED', 'WISH_WINDOW_CHANGED',
        'WISHES_PUBLISHED', 'ASSIGNMENTS_PUBLISHED', 'CONFIRMATION_DUE', 'DESIRED_HOURS_FROZEN',
        'DESIRED_HOURS_PUBLISHED', 'PHASE_ADVANCED', 'REVIEW_COMPLETED', 'ADVANCE_WISH_RESOLVED'
    )),
    CONSTRAINT notification_subscription_target_set_is_known CHECK (target_set IN (
        'MY_ADVANCE_WISHES', 'MY_WISHES', 'MY_ASSIGNMENTS', 'MY_COMPETENCES',
        'MY_SUBJECT_GROUPS', 'MY_PROGRAMMES'
    )),
    CONSTRAINT notification_subscription_delivery_is_known CHECK (delivery IN ('IMMEDIATE', 'DAILY')),
    CONSTRAINT notification_subscription_semester_is_year_and_term
        CHECK (semester_code ~ '^[0-9]{4}-(SS|WS)$'),
    -- At most one target. None means every event of the kind.
    CONSTRAINT notification_subscription_has_at_most_one_target
        CHECK (num_nonnulls(module_id, programme_id, subject_group_id, target_set) <= 1),
    -- Subscribing twice is the same subscription.
    CONSTRAINT notification_subscription_is_unique UNIQUE NULLS NOT DISTINCT
        (person_id, kind, semester_code, module_id, programme_id, subject_group_id, target_set)
);

CREATE INDEX IF NOT EXISTS notification_subscription_kind_idx
    ON notification_subscription (kind);

CREATE TABLE IF NOT EXISTS notification_settings (
    person_id uuid PRIMARY KEY REFERENCES person (id) ON DELETE CASCADE,
    -- No subscription mail until the end of this day. Events in that time are not sent later.
    paused_until date
);

-- One official act that the dispatcher has noticed, waiting to be fanned out to subscribers.
--
-- The columns are what subscriptions match on, written at the moment it was noticed: the module,
-- the programme and the subject group *then*. A module moved to another subject group afterwards
-- was offered under the group it had.
CREATE TABLE IF NOT EXISTS notification_event (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kind text NOT NULL,
    semester_id uuid REFERENCES semester (id) ON DELETE CASCADE,
    module_id uuid REFERENCES module (id) ON DELETE CASCADE,
    programme_id uuid REFERENCES programme (id) ON DELETE CASCADE,
    subject_group_id uuid REFERENCES subject_group (id) ON DELETE CASCADE,
    -- No reference: MODULE_WITHDRAWN is about an instance that no longer exists.
    course_instance_id uuid,
    -- For an event about one person's own rows (an advance wish resolved, a confirmation still
    -- open): only that person is ever told.
    person_id uuid REFERENCES person (id) ON DELETE CASCADE,
    -- What the kind needs beyond the ids: OPEN/SHUT, the new phase, CONVERTED/NOT_OFFERED, the
    -- cohort of an instance, a due date.
    detail text NOT NULL DEFAULT '',
    occurred_at timestamptz NOT NULL DEFAULT now(),
    fanned_out_at timestamptz,

    CONSTRAINT notification_event_kind_is_known CHECK (kind IN (
        'MODULE_OFFERED', 'MODULE_WITHDRAWN', 'DEMAND_RELEASED', 'WISH_WINDOW_CHANGED',
        'WISHES_PUBLISHED', 'ASSIGNMENTS_PUBLISHED', 'CONFIRMATION_DUE', 'DESIRED_HOURS_FROZEN',
        'DESIRED_HOURS_PUBLISHED', 'PHASE_ADVANCED', 'REVIEW_COMPLETED', 'ADVANCE_WISH_RESOLVED'
    ))
);

CREATE INDEX IF NOT EXISTS notification_event_pending_idx
    ON notification_event (occurred_at) WHERE fanned_out_at IS NULL;

-- What has been announced, as one key per fact and the value it had: 'WISH_WINDOW:<semester>:
-- <group>' = 'SHUT', 'PHASE:<semester>' = 'ASSIGNMENT', 'DEMAND_RELEASED:<semester>:<programme>'.
-- The dispatcher announces a fact whose current value differs from the one stored here, and
-- stores the new one in the same transaction as the event.
--
-- A release withdrawn and given again keeps its row: the second release is not news to anybody
-- who heard of the first, and a withdrawal is "let me check again", not a cancellation.
CREATE TABLE IF NOT EXISTS notification_announced (
    key text PRIMARY KEY,
    value text NOT NULL DEFAULT '',
    announced_at timestamptz NOT NULL DEFAULT now()
);

-- Which instances have been announced as offered, with what they were — so that one that has
-- since been deleted can still be announced as withdrawn, to the people who heard it was on.
CREATE TABLE IF NOT EXISTS announced_instance (
    course_instance_id uuid PRIMARY KEY,
    semester_id uuid NOT NULL REFERENCES semester (id) ON DELETE CASCADE,
    module_id uuid NOT NULL REFERENCES module (id) ON DELETE CASCADE,
    programme_id uuid NOT NULL REFERENCES programme (id) ON DELETE CASCADE,
    track text NOT NULL DEFAULT '',
    announced_at timestamptz NOT NULL DEFAULT now()
);

-- One mail, queued. Rendered when queued, sent by the dispatcher.
--
-- No address column: the address is read from person.mail when it is sent, from an active
-- account only. A colleague whose login address changes gets the mail at the new one; one who
-- was deactivated in the meantime gets nothing.
CREATE TABLE IF NOT EXISTS mail_outbox (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    person_id uuid NOT NULL REFERENCES person (id) ON DELETE CASCADE,
    category text NOT NULL,
    -- Replies go here: the sender of an announcement. Empty for a notification.
    reply_to text NOT NULL DEFAULT '',
    subject text NOT NULL,
    html text NOT NULL,
    text_body text NOT NULL,
    state text NOT NULL DEFAULT 'PENDING',
    attempts integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    sent_at timestamptz,
    last_error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT mail_outbox_category_is_known CHECK (category IN ('NOTIFICATION', 'ANNOUNCEMENT')),
    CONSTRAINT mail_outbox_state_is_known CHECK (state IN ('PENDING', 'SENT', 'FAILED', 'DROPPED'))
);

CREATE INDEX IF NOT EXISTS mail_outbox_pending_idx
    ON mail_outbox (next_attempt_at) WHERE state = 'PENDING';

-- Who is to be told about which event, and in which mail they were.
--
-- One row per event and person, however many of their subscriptions matched: an event reaches
-- a person once. `delivery` is IMMEDIATE when any matching subscription is.
CREATE TABLE IF NOT EXISTS notification_delivery (
    event_id uuid NOT NULL REFERENCES notification_event (id) ON DELETE CASCADE,
    person_id uuid NOT NULL REFERENCES person (id) ON DELETE CASCADE,
    -- One of the subscriptions that matched, for the line "you receive this because …".
    subscription_id uuid REFERENCES notification_subscription (id) ON DELETE SET NULL,
    delivery text NOT NULL,
    mail_id uuid REFERENCES mail_outbox (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (event_id, person_id),
    CONSTRAINT notification_delivery_delivery_is_known CHECK (delivery IN ('IMMEDIATE', 'DAILY'))
);

CREATE INDEX IF NOT EXISTS notification_delivery_unsent_idx
    ON notification_delivery (person_id) WHERE mail_id IS NULL;

CREATE TABLE IF NOT EXISTS announcement (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    sender_id uuid NOT NULL REFERENCES person (id) ON DELETE RESTRICT,
    audience_kind text NOT NULL,
    subject_group_id uuid REFERENCES subject_group (id) ON DELETE RESTRICT,
    programme_id uuid REFERENCES programme (id) ON DELETE RESTRICT,
    semester_id uuid REFERENCES semester (id) ON DELETE RESTRICT,
    subject text NOT NULL,
    body text NOT NULL,
    recipient_count integer NOT NULL,
    sent_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT announcement_audience_is_known CHECK (audience_kind IN (
        'SUBJECT_GROUP', 'PROGRAMME', 'COLLEAGUES', 'LEADS', 'ALL_ACCOUNTS'
    )),
    CONSTRAINT announcement_subject_is_short CHECK (length(subject) BETWEEN 1 AND 200),
    CONSTRAINT announcement_body_is_bounded CHECK (length(body) BETWEEN 1 AND 20000)
);

CREATE INDEX IF NOT EXISTS announcement_sender_idx ON announcement (sender_id, sent_at);

CREATE TABLE IF NOT EXISTS announcement_recipient (
    announcement_id uuid NOT NULL REFERENCES announcement (id) ON DELETE CASCADE,
    person_id uuid NOT NULL REFERENCES person (id) ON DELETE CASCADE,
    PRIMARY KEY (announcement_id, person_id)
);

CREATE INDEX IF NOT EXISTS announcement_recipient_person_idx
    ON announcement_recipient (person_id);

-- What stands today counts as announced: nobody has subscribed yet, and the first run after the
-- deploy should not turn three years of planning into events that match nothing.
INSERT INTO announced_instance (course_instance_id, semester_id, module_id, programme_id, track)
SELECT ci.id, ci.semester_id, ci.module_id, ci.programme_id, ci.track
  FROM course_instance ci
  JOIN demand_completion dc ON dc.semester_id = ci.semester_id AND dc.programme_id = ci.programme_id
ON CONFLICT DO NOTHING;

INSERT INTO notification_announced (key, value)
SELECT 'DEMAND_RELEASED:' || semester_id || ':' || programme_id, '' FROM demand_completion
UNION ALL
SELECT 'REVIEW_COMPLETED:' || semester_id || ':' || programme_id, '' FROM review_completion
UNION ALL
SELECT 'WISH_WINDOW:' || semester_id || ':' || subject_group_id,
       CASE WHEN open THEN 'OPEN' ELSE 'SHUT' END FROM wish_window
UNION ALL
SELECT 'PHASE:' || id, phase FROM semester
UNION ALL
SELECT 'WISHES_PUBLISHED:' || id, '' FROM semester WHERE wishes_published_at IS NOT NULL
UNION ALL
SELECT 'ASSIGNMENTS_PUBLISHED:' || id, '' FROM semester WHERE assignments_published_at IS NOT NULL
UNION ALL
SELECT 'DESIRED_HOURS_FROZEN:' || id, '' FROM semester WHERE desired_hours_frozen_at IS NOT NULL
UNION ALL
SELECT 'DESIRED_HOURS_PUBLISHED:' || id, '' FROM semester WHERE desired_hours_published_at IS NOT NULL
UNION ALL
SELECT 'CONFIRMATION_DUE:' || id, assignment_confirmation_due::text
  FROM semester WHERE assignment_confirmation_due IS NOT NULL
UNION ALL
SELECT 'ADVANCE_WISH:' || aw.id, 'CONVERTED' FROM advance_wish aw WHERE aw.converted_at IS NOT NULL
UNION ALL
SELECT 'ADVANCE_WISH:' || aw.id, 'NOT_OFFERED'
  FROM advance_wish aw
  JOIN semester s ON s.code = aw.semester_code
  JOIN demand_completion dc ON dc.semester_id = s.id AND dc.programme_id = aw.programme_id
 WHERE aw.converted_at IS NULL
ON CONFLICT DO NOTHING;

-- +goose Down

DROP TABLE IF EXISTS announcement_recipient;
DROP TABLE IF EXISTS announcement;
DROP TABLE IF EXISTS notification_delivery;
DROP TABLE IF EXISTS mail_outbox;
DROP TABLE IF EXISTS announced_instance;
DROP TABLE IF EXISTS notification_announced;
DROP TABLE IF EXISTS notification_event;
DROP TABLE IF EXISTS notification_settings;
DROP TABLE IF EXISTS notification_subscription;
