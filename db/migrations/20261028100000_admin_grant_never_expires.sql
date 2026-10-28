-- Migration: an ADMIN grant never expires.
--
-- Found by the security audit of 2026-10-28 (L1). The last-administrator guard counts every
-- unexpired ADMIN grant, and a temporary one counts until the minute it runs out: the only
-- administrator could grant somebody ADMIN until tomorrow, step down today, and the guard would
-- pass — leaving an installation with nobody tomorrow, repairable only from the host.
--
-- The role whose point is to let people back in is therefore granted without a date, or not at
-- all. The domain refuses it first (ADMIN_GRANT_MUST_NOT_EXPIRE); this constraint is what makes
-- the refusal hold for psql and for any later caller of GrantRole too.
--
-- Rows that had already run out are removed first: they granted nothing any more, so removing
-- them changes nobody's rights. A grant that is still live with a date would fail the ALTER and
-- stop the server from starting — deliberately. Deciding whether that person keeps ADMIN for
-- good or loses it now is a human decision, not a migration's.

-- +goose Up
DELETE FROM person_role
WHERE role = 'ADMIN' AND expires_at IS NOT NULL AND expires_at <= now();

ALTER TABLE person_role
    ADD CONSTRAINT person_role_admin_never_expires
    CHECK (role <> 'ADMIN' OR expires_at IS NULL);

-- +goose Down
ALTER TABLE person_role DROP CONSTRAINT person_role_admin_never_expires;
