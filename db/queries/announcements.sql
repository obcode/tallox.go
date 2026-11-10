-- Announcements: who an audience reaches, and the archive.
--
-- The recipients are active accounts and nobody else — never a teacher without one, never an
-- address from anywhere but person.mail. Who may ask for which audience is decided in
-- internal/policy before any of these runs.

-- name: AnnouncementRecipients :many
-- One query for every kind, so that "an active account" is written once.
--
-- PROGRAMME is whoever holds a part of one of the programme's instances in the semester. Who
-- wished for one is the other half, and reads the wish table — so it is in wish.sql
-- (WishersOfProgramme), under the filter every read of that table carries.
SELECT p.id
  FROM person p
 WHERE p.active
   AND CASE sqlc.arg(kind)::text
       WHEN 'SUBJECT_GROUP' THEN EXISTS (
            SELECT 1 FROM person_subject_group psg
             WHERE psg.person_id = p.id AND psg.subject_group_id = sqlc.narg(subject_group_id)::uuid)
       WHEN 'PROGRAMME' THEN EXISTS (
            SELECT 1 FROM assignment a
              JOIN instance_part ip ON ip.id = a.instance_part_id
              JOIN course_instance ci ON ci.id = ip.course_instance_id
             WHERE a.person_id = p.id AND ci.programme_id = sqlc.narg(programme_id)::uuid
               AND ci.semester_id = sqlc.narg(semester_id)::uuid)
       WHEN 'COLLEAGUES' THEN EXISTS (
            SELECT 1 FROM person_role r
             WHERE r.person_id = p.id
               AND r.role IN ('LECTURER', 'SUBJECT_GROUP_LEAD', 'PROGRAMME_LEAD', 'DEANS_OFFICE')
               AND (r.expires_at IS NULL OR r.expires_at > now()))
       WHEN 'LEADS' THEN EXISTS (
            SELECT 1 FROM person_role r
             WHERE r.person_id = p.id
               AND r.role IN ('SUBJECT_GROUP_LEAD', 'PROGRAMME_LEAD', 'DEANS_OFFICE')
               AND (r.expires_at IS NULL OR r.expires_at > now()))
       WHEN 'ALL_ACCOUNTS' THEN true
       ELSE false
       END
 ORDER BY p.id;

-- name: AnnouncementsSentSince :one
SELECT count(*)::int FROM announcement
 WHERE sender_id = sqlc.arg(sender_id)::uuid AND sent_at >= sqlc.arg(since)::timestamptz;

-- name: CreateAnnouncement :one
INSERT INTO announcement
    (sender_id, audience_kind, subject_group_id, programme_id, semester_id, subject, body, recipient_count)
VALUES (sqlc.arg(sender_id)::uuid, sqlc.arg(audience_kind)::text, sqlc.narg(subject_group_id)::uuid,
        sqlc.narg(programme_id)::uuid, sqlc.narg(semester_id)::uuid, sqlc.arg(subject)::text,
        sqlc.arg(body)::text, sqlc.arg(recipient_count)::int)
RETURNING id, sent_at;

-- name: AddAnnouncementRecipients :exec
INSERT INTO announcement_recipient (announcement_id, person_id)
SELECT sqlc.arg(announcement_id)::uuid, unnest(sqlc.arg(person_ids)::uuid[])
ON CONFLICT DO NOTHING;

-- name: QueueAnnouncementMails :exec
-- One mail per recipient, the same text for each: an announcement names nobody but its sender.
INSERT INTO mail_outbox (person_id, category, reply_to, subject, html, text_body)
SELECT unnest(sqlc.arg(person_ids)::uuid[]), 'ANNOUNCEMENT', sqlc.arg(reply_to)::text,
       sqlc.arg(subject)::text, sqlc.arg(html)::text, sqlc.arg(text_body)::text;

-- name: AnnouncementsOf :many
-- What one person sent or received, newest first.
SELECT a.id, a.audience_kind, a.subject, a.body, a.recipient_count, a.sent_at,
       a.sender_id, sp.mail::text AS sender_mail, sp.name AS sender_name, sp.active AS sender_active,
       a.subject_group_id, COALESCE(g.code, '')::text AS subject_group_code,
       COALESCE(g.name, '')::text AS subject_group_name, COALESCE(g.active, false) AS subject_group_active,
       a.programme_id, COALESCE(pr.code, '')::text AS programme_code,
       COALESCE(pr.title, '')::text AS programme_title,
       COALESCE(sem.code, '')::text AS semester_code
  FROM announcement a
  JOIN person sp ON sp.id = a.sender_id
  LEFT JOIN subject_group g ON g.id = a.subject_group_id
  LEFT JOIN programme pr ON pr.id = a.programme_id
  LEFT JOIN semester sem ON sem.id = a.semester_id
 WHERE a.sender_id = sqlc.arg(person_id)::uuid
    OR EXISTS (SELECT 1 FROM announcement_recipient r
                WHERE r.announcement_id = a.id AND r.person_id = sqlc.arg(person_id)::uuid)
 ORDER BY a.sent_at DESC, a.id
 LIMIT 500;

-- name: AnnouncementSubjectGroups :many
SELECT id, code, name, active FROM subject_group WHERE active ORDER BY code;

-- name: AnnouncementProgrammes :many
-- The programmes this faculty plans: an audience only exists where there is a demand.
SELECT id, code, title, active FROM programme WHERE planning_status = 'PLANNED' ORDER BY code;

-- name: SubjectGroupRefByID :one
SELECT id, code, name, active FROM subject_group WHERE id = sqlc.arg(id)::uuid;

-- name: ProgrammeRefByCode :one
SELECT id, code, title, active FROM programme WHERE code = sqlc.arg(code)::text;

-- name: PlanningSemesterCodeForAnnouncements :one
SELECT COALESCE((SELECT s.code FROM semester s WHERE s.is_planning_semester), '')::text;
