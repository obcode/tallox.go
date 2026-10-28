-- Migration: a Personal Access Token can be minted with full reach.
--
-- Until now a token was always narrower than its owner's browser session: other people's
-- unpublished wishes, assignments and competences collapsed to "your own" through it, and every
-- mutation whose refusal would answer a question about those rows was browser-only. Programme
-- leads asked to plan through the API — from scripts and from assistants — and planning is not
-- possible on a view that is missing the rows the plan is about.
--
-- full_reach is the explicit opt-in: such a token reaches what its owner reaches in a browser,
-- except token management, person and role administration and the access log, which stay
-- browser-only for every credential. It is chosen at minting and never changed, like scopes.
--
-- The ceiling is the safety half of the bargain. The domain refuses more than 90 days
-- (ErrLifetimeOutOfRange); the constraint is the backstop for psql and any later caller, and it
-- carries a day of slack because expires_at is computed from the application's clock and
-- created_at from the database's, and those two never agree to the millisecond.

-- +goose Up
ALTER TABLE personal_access_token
    ADD COLUMN IF NOT EXISTS full_reach boolean NOT NULL DEFAULT false;

ALTER TABLE personal_access_token
    ADD CONSTRAINT personal_access_token_full_reach_is_short_lived
    CHECK (NOT full_reach OR expires_at <= created_at + interval '91 days');

-- +goose Down
ALTER TABLE personal_access_token DROP CONSTRAINT personal_access_token_full_reach_is_short_lived;
ALTER TABLE personal_access_token DROP COLUMN full_reach;
