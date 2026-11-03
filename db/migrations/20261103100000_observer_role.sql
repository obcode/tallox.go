-- Migration: a role for observers.
--
-- Somebody who follows the whole planning without taking part in it — building a neighbouring
-- system, say — gets OBSERVER. Decided 2026-11-03. What the role means is internal/policy's
-- observer.go: held alone it reads everything that is not confidential, no other people's
-- statements, and writes nothing. It is on no exception list of the confidentiality rule.
--
-- Unscoped, so person_programme_scope is untouched.
--
-- An OBSERVER grant always ends. The domain refuses one without a date first
-- (OBSERVER_GRANT_MUST_EXPIRE); this constraint makes that hold for psql and any later caller of
-- GrantRole as well. The mirror image of person_role_admin_never_expires.
--
-- The role list is the one policy.AllRoles() returns, and store.TestDatabaseAndPolicyAgreeOnRoles
-- compares the two.

-- +goose Up

ALTER TABLE person_role DROP CONSTRAINT person_role_role_known;
ALTER TABLE person_role ADD CONSTRAINT person_role_role_known CHECK (role IN (
    'LECTURER',
    'SUBJECT_GROUP_LEAD',
    'PROGRAMME_LEAD',
    'DEANS_OFFICE',
    'PARTNER_READER',
    'OBSERVER',
    'ADMIN'
));

ALTER TABLE person_role
    ADD CONSTRAINT person_role_observer_expires
    CHECK (role <> 'OBSERVER' OR expires_at IS NOT NULL);

-- +goose Down

-- A grant the old binary cannot read would be a grant of nothing to it; removing it says so.
ALTER TABLE person_role DROP CONSTRAINT person_role_observer_expires;
DELETE FROM person_role WHERE role = 'OBSERVER';
ALTER TABLE person_role DROP CONSTRAINT person_role_role_known;
ALTER TABLE person_role ADD CONSTRAINT person_role_role_known CHECK (role IN (
    'LECTURER',
    'SUBJECT_GROUP_LEAD',
    'PROGRAMME_LEAD',
    'DEANS_OFFICE',
    'PARTNER_READER',
    'ADMIN'
));
