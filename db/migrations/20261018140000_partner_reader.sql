-- Migration 32: a role for the partner faculties.
--
-- FK10, MUC.DAI, MUC.HEALTH and DPH follow the planning of the study programmes they share with
-- this faculty. Decided 2026-10-18: a role of its own, PARTNER_READER, scoped to study programmes
-- through person_programme_scope exactly as PROGRAMME_LEAD is — the scope table is keyed by role
-- already, so nothing else changes in the schema. What the role means is internal/policy's
-- partner.go: held alone it reads less than an ordinary account and writes nothing.
--
-- The constraint is the list policy.AllRoles() returns, and store.TestDatabaseAndPolicyAgreeOnRoles
-- compares the two.

-- +goose Up

ALTER TABLE person_role DROP CONSTRAINT person_role_role_known;
ALTER TABLE person_role ADD CONSTRAINT person_role_role_known CHECK (role IN (
    'LECTURER',
    'SUBJECT_GROUP_LEAD',
    'PROGRAMME_LEAD',
    'DEANS_OFFICE',
    'PARTNER_READER',
    'ADMIN'
));

-- The scope table admits the roles that are scoped to programmes. One more, the same table.
ALTER TABLE person_programme_scope DROP CONSTRAINT person_programme_scope_role_is_scopable;
ALTER TABLE person_programme_scope ADD CONSTRAINT person_programme_scope_role_is_scopable
    CHECK (role IN ('PROGRAMME_LEAD', 'PARTNER_READER'));

-- +goose Down

-- A grant the old binary cannot read would be a grant of nothing to it; removing it says so.
DELETE FROM person_programme_scope WHERE role = 'PARTNER_READER';
DELETE FROM person_role WHERE role = 'PARTNER_READER';
ALTER TABLE person_programme_scope DROP CONSTRAINT person_programme_scope_role_is_scopable;
ALTER TABLE person_programme_scope ADD CONSTRAINT person_programme_scope_role_is_scopable
    CHECK (role IN ('PROGRAMME_LEAD'));
ALTER TABLE person_role DROP CONSTRAINT person_role_role_known;
ALTER TABLE person_role ADD CONSTRAINT person_role_role_known CHECK (role IN (
    'LECTURER',
    'SUBJECT_GROUP_LEAD',
    'PROGRAMME_LEAD',
    'DEANS_OFFICE',
    'ADMIN'
));
