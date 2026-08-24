-- Migration 13: the access log — who reached this installation, through which door, and what
-- they asked for.
--
-- The audit log has been promised in this repository since migration 2. `person_role.granted_by`
-- points at it, `@interactiveOnly` names it among the things a token never reaches, and
-- `policy.Narrow` justifies keeping `NarrowedFrom` partly with it. This is the table.
--
-- It records reads as well as writes, which makes it an access log rather than only an audit
-- log. The reason is the door construction itself: `sso.hm.edu` lets the whole university knock,
-- and only a row in `person` lets somebody in. Every one of those refusals is currently a single
-- Info line in a container log that nobody reads.
--
-- # The rule this schema exists to protect
--
-- ADMIN is deliberately NOT on the exception list of the wish visibility rule — running the
-- system is a different job from planning with it. An access log that stored arguments would
-- hand ADMIN that exception back through the side door: `wish(id: …)` with its argument, or a
-- variables blob, is a copy of the confidential data with none of the policy attached.
--
-- So this table stores the operation name and the ROOT FIELD NAMES, and nothing else about what
-- was asked. No variables, no query document, no response. That is the same allow-list reasoning
-- as internal/obs/scrub.go, for the same reason: a field nobody anticipated must be absent
-- rather than present-and-unreviewed. "Person X called myWishes" is harmless; "person X called
-- wish(id: 7f3…)" is not, and the difference is a column that does not exist here.
--
-- The corollary is that adding such a column later is not a small change. It needs the argument
-- above answered, not just a migration.
--
-- # Why the columns are denormalised
--
-- actor_mail, roles and narrowed_from record what was true AT THE TIME. A log that resolved
-- them through a join would answer "what may this person do today", which is a different
-- question and the wrong one — the whole point of reading a log is that the present has moved
-- on. actor_id stays beside them for the queries that do want today's person.
--
-- # Retention
--
-- Rows are deleted after 90 days by the nightly -access-report run. The constant lives in
-- internal/domain with its reasoning; it is not a configuration key, so it cannot drift between
-- installations. This is employee activity data, and a retention period that is decided per
-- installation is a retention period that nobody can state.

-- +goose Up

CREATE TABLE access_log (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    at            timestamptz NOT NULL DEFAULT now(),

    -- The person row, when there was one. NULL for a refused sign-in — which is precisely the
    -- case where somebody knocked and this installation did not know them.
    --
    -- People are deactivated rather than deleted here, so this reference ordinarily survives.
    -- ON DELETE SET NULL rather than RESTRICT anyway: a log entry must never be the reason a
    -- repair on the host cannot proceed, and actor_mail below keeps the entry readable.
    actor_id      uuid REFERENCES person (id) ON DELETE SET NULL,

    -- The identity as asserted, whether or not it resolved. citext for the same reason the
    -- person table uses it: the proxy's capitalisation is not part of the identity.
    actor_mail    citext,

    -- INTERACTIVE or TOKEN. The third factor of the invariant, recorded, so that "was this a
    -- person in a browser or a script" is answerable months later without inference.
    door          text NOT NULL,

    -- Which token, when it was the token door. The token's public half is its primary key and
    -- is shown in the token list, so this both names the token and cannot reveal it: the secret
    -- half is not in that column and not in this one.
    --
    -- Tokens are revoked rather than deleted, so this reference ordinarily survives revocation —
    -- which is the point, because "what did the token we revoked in October actually do" is the
    -- question one asks after revoking it. ON DELETE SET NULL for the reason actor_id has it.
    token_id      text REFERENCES personal_access_token (token_id) ON DELETE SET NULL,

    -- The EFFECTIVE roles: what the request was judged by. Empty for a refusal and for an
    -- anonymous caller.
    roles         text[] NOT NULL DEFAULT '{}',

    -- The roles as held, when and only when the caller asked to be narrowed. NULL otherwise —
    -- so "was this person acting narrowed" is a NULL check rather than a set comparison against
    -- grants that have since changed.
    narrowed_from text[],

    -- The operation name from the document. Client-supplied, so it is a label and never a key;
    -- capped, because it arrives from outside.
    operation     text,

    -- The root fields, and see the header: this is as specific as this table gets about what
    -- was asked for.
    fields        text[] NOT NULL DEFAULT '{}',

    -- Whether the operation was data-changing. Derived from the operation type rather than from
    -- the field names, exactly as graph/scope.go decides it — so "show me every change" is one
    -- predicate and not a list of field names somebody has to keep up to date.
    mutation      boolean NOT NULL DEFAULT false,

    -- How it ended. REFUSED_AUTH is written by the auth middleware and is the only outcome that
    -- can appear without a person row.
    outcome       text NOT NULL,

    -- The extensions.code of the first error, when there was one: INSUFFICIENT_SCOPE,
    -- INTERACTIVE_ONLY, TOKEN_NOT_FOUND. The code and never the message — the German sentences
    -- get reworded, and a log that stored them would be a second place they have to match.
    error_code    text,

    duration_ms   integer,

    -- Where from. A stolen credential is recognised by where it is used and by nothing else,
    -- which is the only reason this column is worth its privacy cost — and it falls under the
    -- same 90 days as the rest of the row.
    source_ip     inet,

    CONSTRAINT access_log_door_is_known CHECK (door IN ('INTERACTIVE', 'TOKEN')),
    CONSTRAINT access_log_outcome_is_known
        CHECK (outcome IN ('OK', 'ERROR', 'REFUSED_AUTH', 'REFUSED_SCOPE', 'REFUSED_INTERACTIVE')),
    -- A refused sign-in never carries roles: it was refused before there were any. This catches
    -- the write that fills the row in from a half-built actor.
    CONSTRAINT access_log_refused_auth_has_no_roles
        CHECK (outcome <> 'REFUSED_AUTH' OR cardinality(roles) = 0)
);

-- The three reads: the page, one person's history, and everything that was not OK.
CREATE INDEX access_log_recent_idx ON access_log (at DESC);
CREATE INDEX access_log_actor_idx ON access_log (actor_id, at DESC);
CREATE INDEX access_log_refused_idx ON access_log (at DESC) WHERE outcome <> 'OK';

-- +goose Down

DROP TABLE access_log;
