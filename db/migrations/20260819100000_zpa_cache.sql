-- Migration 5: the cache of the examination office's module master data.
--
-- This one does NOT wait on the instance primary key, and that is worth saying first because
-- migrations 2 and 4 both say the opposite about themselves. It mirrors somebody else's ids
-- into tables that nothing points at, so it freezes no decision about what an instance is. It
-- is in fact the cheapest way to answer that question: six weeks of nightly snapshots plus a
-- change log is direct evidence for "does a module's identifier survive a change of
-- examination regulations", which is exactly what the open question asks.
--
-- THE RULE THIS SCHEMA EXISTS TO PROTECT
--
-- A ZPA id is an import key, never an identity. When the domain tables arrive, a module row
-- will have its own uuid and a nullable column saying "this came from ZPA object N" — and
-- nothing will join on that column at request time. The sibling project made a foreign
-- identifier its primary key and every table downstream inherited its quirks until correcting
-- one stopped being possible.
--
-- The interface makes the case by itself. Its module_code — the obvious candidate for a
-- natural key — is not one: it belongs to the (module, regulations, basket) triple rather than
-- to the module, 264 of 487 modules carry more than one, one carries eight, and 21 % of the
-- association rows carry none at all.
--
-- internal/store enforces this: TestNoDomainTableReferencesTheZpaCache reads pg_constraint and
-- fails on any foreign key into zpa_object from outside this prefix. The rule that matters
-- most is the one that cannot be broken by a hurried afternoon.
--
-- The corollary, and it is a feature: because nothing points in, this cache may be truncated
-- and rebuilt at any time without touching a single planning row.
--
-- WHY THE PAYLOAD STAYS UNTYPED
--
-- One table per endpoint with typed columns was the obvious alternative. Against it: their
-- shape has already changed once and will again, so every change would become a migration with
-- the three-release column dance — paid for a cache of a database this faculty neither owns
-- nor influences. Worse, a typed mirror silently discards what it did not model, so the first
-- question about an unmapped field is answered with "we never knew", irrecoverably.
--
-- The spike makes that concrete: a typed mirror written last week would have had a name column
-- on the module (their module objects carry no name at all — it exists only inside the nested
-- object of an association row) and an integer for the credits (everything arrives as a
-- string, and the booleans arrive as the Python words "True" and "False").
--
-- A typed projection out of this table, by contrast, can be dropped and rebuilt by a migration
-- from data already held. So: land it whole now, project later, and only where a feature needs
-- one.

-- +goose Up

-- pgcrypto for digest(). gen_random_uuid() has been in core since 13, which is why migration 1
-- could do without it; sha256 cannot.
--
-- Same non-atomicity as citext there — the existence check and the catalog insert are separate
-- steps, and CI migrates many schemas in parallel against a database created seconds earlier.
--
-- +goose StatementBegin
DO $$
BEGIN
    CREATE EXTENSION IF NOT EXISTS pgcrypto WITH SCHEMA public;
EXCEPTION
    WHEN unique_violation OR duplicate_object THEN
        NULL;
END
$$;
-- +goose StatementEnd

-- One object as it arrived, with its history.
CREATE TABLE zpa_object (
    -- A surrogate key even though (kind, zpa_id) is unique, and here the reason is the whole
    -- point of the table rather than a habit: zpa_id is somebody else's primary key. Making it
    -- ours is the mistake this cache exists to avoid, in miniature. With a surrogate, the
    -- change log's foreign key does not carry a ZPA id and the discipline is structural rather
    -- than remembered.
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    -- The internal vocabulary, not the endpoint path: MODULE, not module_info. What a kind is
    -- called on the wire is a fact about their HTTP surface and lives in internal/zpa; this
    -- constraint can never be edited once released, so it must not depend on a URL somebody
    -- else may rename.
    kind            text NOT NULL,

    -- Their primary key. bigint rather than text: every id they publish is an integer, and a
    -- numeric column sorts and indexes the way one expects. It arrives as a JSON string —
    -- their interface types nothing — and the client parses it.
    zpa_id          bigint NOT NULL,

    -- The object, whole and unread.
    --
    -- jsonb rather than text, and that choice is what makes the hash below mean anything: jsonb
    -- normalises key order and whitespace, so "the hash changed" means the content changed and
    -- not that their serialiser reordered a map.
    payload         jsonb NOT NULL,

    -- Generated and stored, so it cannot drift from the payload and no Go code can forget it.
    -- Computed over the canonical form the database holds, not over the bytes that arrived.
    content_hash    text GENERATED ALWAYS AS (encode(digest(payload::text, 'sha256'), 'hex')) STORED,

    -- A human name when the payload offers one, NULL when it does not.
    --
    -- Modules currently have none — the name lives only in the nested object of an association
    -- row — so this is NULL for them until that is fixed at the source. Shown next to an id,
    -- never matched on.
    label           text,

    first_seen_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at    timestamptz NOT NULL DEFAULT now(),
    -- When the payload last differed from the one before it. Untouched by a sync that finds
    -- the object unchanged, which is what makes "what actually moved" answerable months later.
    last_changed_at timestamptz NOT NULL DEFAULT now(),

    -- Set when a *successful* fetch of this kind did not mention the object; cleared if it
    -- comes back. Rows are never deleted: a disappearance is a fact worth keeping, and
    -- reappearance is common enough (an object edited out of one catalogue and back into
    -- another) that losing first_seen_at to it would be a shame.
    --
    -- "Successful" is doing real work in that sentence. It is why the client treats an empty
    -- result as an error and why a partial run only retires the kinds it actually fetched:
    -- without both, one bad night would retire the entire catalogue, with a change-log entry
    -- per module to make it look deliberate.
    gone_at         timestamptz,

    CONSTRAINT zpa_object_kind_is_known CHECK (kind IN ('MODULE', 'BASKET', 'MSBA', 'SPO')),
    CONSTRAINT zpa_object_identity UNIQUE (kind, zpa_id)
);

-- The lookup every sync does, once per kind: what do we currently hold that is still present.
CREATE INDEX zpa_object_present_idx ON zpa_object (kind, zpa_id) WHERE gone_at IS NULL;

-- One run of the import.
CREATE TABLE zpa_sync_run (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    -- SCHEDULE for the nightly job, MANUAL for somebody pressing the button.
    trigger     text NOT NULL,

    -- Who pressed it; NULL for the scheduled run. People are deactivated rather than deleted
    -- here, so this reference is safe.
    started_by  uuid REFERENCES person (id),

    started_at  timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz,

    -- The row is written as RUNNING at the start rather than on completion. That is what makes
    -- a crashed run visible — a RUNNING row from two days ago — instead of invisible, which is
    -- what a write-on-completion design gives you: no row at all, indistinguishable from a job
    -- that was never scheduled.
    status      text NOT NULL DEFAULT 'RUNNING',

    fetched     integer NOT NULL DEFAULT 0,
    appeared    integer NOT NULL DEFAULT 0,
    changed     integer NOT NULL DEFAULT 0,
    disappeared integer NOT NULL DEFAULT 0,

    -- The failure, in the words the operator needs. Never the response body: theirs is HTML
    -- outside the VPN and can carry hostnames.
    error       text,

    CONSTRAINT zpa_sync_run_trigger_is_known CHECK (trigger IN ('SCHEDULE', 'MANUAL')),
    CONSTRAINT zpa_sync_run_status_is_known
        CHECK (status IN ('RUNNING', 'SUCCEEDED', 'PARTIAL', 'FAILED')),
    -- Finished exactly when it is not running. Cheap, and it catches the write that sets one
    -- of the two and forgets the other — which is the bug that makes a dashboard show a run
    -- that has been going for three weeks.
    CONSTRAINT zpa_sync_run_finished_when_done
        CHECK ((status = 'RUNNING') = (finished_at IS NULL))
);

-- "When did it last succeed" and the recent list, which is the whole read pattern.
CREATE INDEX zpa_sync_run_recent_idx ON zpa_sync_run (started_at DESC);

-- What happened per endpoint within one run.
--
-- Its own table rather than a summary column, because PARTIAL is only useful if it can say
-- WHICH endpoint failed — and because a jsonb summary would be the one place in this design
-- storing data we produce ourselves as untyped, which is the exact opposite of the argument
-- for the payloads.
CREATE TABLE zpa_sync_run_kind (
    run_id  uuid NOT NULL REFERENCES zpa_sync_run (id) ON DELETE CASCADE,
    kind    text NOT NULL,
    status  text NOT NULL,
    fetched integer NOT NULL DEFAULT 0,
    error   text,

    PRIMARY KEY (run_id, kind),
    CONSTRAINT zpa_sync_run_kind_kind_is_known CHECK (kind IN ('MODULE', 'BASKET', 'MSBA', 'SPO')),
    CONSTRAINT zpa_sync_run_kind_status_is_known CHECK (status IN ('SUCCEEDED', 'FAILED'))
);

-- What a run changed. The report, organised by the question people actually ask.
CREATE TABLE zpa_change (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id         uuid NOT NULL REFERENCES zpa_sync_run (id) ON DELETE CASCADE,
    object_id      uuid NOT NULL REFERENCES zpa_object (id) ON DELETE CASCADE,

    -- Denormalised on purpose. This is read by run and rendered as a list, and a report that
    -- needs a join to say what it is about is a report nobody reads. They are also a record of
    -- what the object was called at the time, which the object row no longer is.
    kind           text NOT NULL,
    zpa_id         bigint NOT NULL,
    label          text,

    change         text NOT NULL,

    -- NULL for an appearance and for a disappearance respectively.
    --
    -- Keeping the before-state doubles the bytes and is worth it: without it a change record
    -- becomes unreadable the moment the object changes again, and a durable answer six months
    -- later is the entire purpose. Order of magnitude for the whole cache: a few thousand
    -- objects at a few KB, plus small nightly deltas — tens of megabytes, riding along in the
    -- nightly pg_dump.
    payload_before jsonb,
    payload_after  jsonb,

    -- The top-level keys that differ. This is what turns "the hash changed" into a sentence:
    -- "bei vier Modulen haben sich SWS und Modulverantwortliche geändert". Top-level only —
    -- a deep diff is a bigger feature than the question being asked, and their payloads nest
    -- at most one level.
    changed_keys   text[] NOT NULL DEFAULT '{}',

    detected_at    timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT zpa_change_is_known
        CHECK (change IN ('APPEARED', 'CHANGED', 'DISAPPEARED', 'REAPPEARED'))
);

CREATE INDEX zpa_change_run_idx ON zpa_change (run_id, kind, zpa_id);

-- +goose Down

DROP TABLE zpa_change;
DROP TABLE zpa_sync_run_kind;
DROP TABLE zpa_sync_run;
DROP TABLE zpa_object;

-- pgcrypto is deliberately left installed, for the reason migration 1 leaves citext: other
-- schemas in the same database may be using it, and a Down that can take out a colleague's
-- schema is worse than one that leaves a harmless extension behind.
