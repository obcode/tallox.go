-- Migration 19: one programme's demand, met by another programme's event.
--
-- A module has one home programme and often counts in several: 249 of the 403 active modules do.
-- IT-Sicherheit und technischer Datenschutz is at home in DC and is offered by DE, GS and ID.
-- Sometimes two of those programmes hold it **once, together** — one lecture, one room, one
-- person, two programmes' students.
--
-- Until now that was two instances with two sets of parts, two assignments, and one event counted
-- twice. The faculty's own way of saying it is the shape of the fix: "echter Bedarf in DE und eine
-- Art Import in GS". Both declarations are real. The event is one, and it belongs to one of them.
--
-- WHY THIS DOES NOT LOSE THE DENOMINATOR
--
-- Migration 8 rejected cross-programme sharing, and this migration has to answer it, because the
-- sentence is still on the wall three files away:
--
--     "a part shared between a DA instance and a DC one would belong to two programmes, and the
--      import/export figure would lose its denominator."
--
-- That objection is about a *part* belonging to two programmes, and the denominator it protects is
-- the count of *declarations* — the decision paper's "zwei Studiengänge, zwei Instanzen, zwei
-- Bedarfsmeldungen, und die Import/Export-Meldung fällt als Nebenprodukt an". Nothing here creates
-- such a part:
--
--   * The guest's row stays. The demand is still declared twice, by two leads, and the difference
--     between the declarations is still the export. The denominator is not merely preserved — the
--     link below is the first datum that makes the *numerator* computable, because until now "DE
--     and GS both declared it" was indistinguishable from "DE and GS each hold their own event".
--   * Every instance_part still belongs to one instance and therefore to one programme. Nothing
--     points at two. SUM(teaching_hours) over the rows as stored is still right with no DISTINCT,
--     which is the property migration 8's flag argument was defending.
--   * An accepted guest holds no parts, so its contribution to any hours figure is zero *by
--     construction* rather than by an exclusion clause somebody has to remember. That is the same
--     reason serves_sibling_tracks is a flag and not a link table, applied one level up.
--
-- WHY THE WHOLE INSTANCE AND NOT A PART
--
-- Decided with the faculty: what is shared is the whole offering, not "the lecture together, the
-- laboratories apart". Per-part coverage across programmes would be exactly the part belonging to
-- two programmes that migration 8 refused, and it would buy a case nobody asked for.
--
-- The asymmetry that settles it is the one this schema keeps using: this can be *narrowed* to part
-- granularity later, by adding a table beside a link that already exists. It could never be
-- widened back — going from many covered parts to one covered offering would mean deciding which
-- of several rows had been the real one.
--
-- WHY BOTH SIDES HAVE TO AGREE
--
-- The lead of the programme whose demand is covered asks; the lead of the programme that holds the
-- event accepts. Each half is an ordinary demand write against that lead's own programme, so
-- neither of them needs anything in the other's — which is what makes it usable between two
-- programme leads who cannot reach each other's demand at all. The dean's office holds both.
--
-- A single column set by one side would have been a lead writing into a programme they do not
-- lead, in a schema whose entire permission model hangs off course_instance.programme_id. The
-- acceptance is therefore not a nicety; it is what keeps the write inside the writer's scope.
--
-- WHAT IS DELIBERATELY NOT HERE
--
-- The counting rule, and any statistics table. Whether GS's covered demand counts in full, not at
-- all, or proportionally for the dean's office's import/export report is a question for the
-- faculty and it is still open. Nothing here depends on the answer: the guest holds no parts, so
-- every sum is already right, and the link is recorded as data so that the rule, when it arrives,
-- is a query rather than another migration.
--
-- ROLLING BACK
--
-- Purely additive, and the previous image runs against it — it simply ignores the columns. What it
-- cannot do is *respect* them: its planDemand would give a covered cohort its parts back, and its
-- withdrawCourseInstance would report a host with guests as the opaque INSTANCE_IN_USE. Both are
-- visible and repairable, and no data is lost either way.

-- +goose Up

ALTER TABLE course_instance
    -- The instance whose teaching meets this one's demand. NULL — the ordinary case — means this
    -- instance holds its own.
    --
    -- On the guest, because the guest is the one making a statement about its own demand, and
    -- because a column holds one value, which is "at most one host" for free. The foreign key is
    -- further down: it needs three more columns to say what it has to say.
    ADD COLUMN covered_by_instance_id uuid,

    -- The host's programme, denormalised on purpose, and the foreign key below is what keeps it
    -- honest.
    --
    -- A CHECK cannot read another row, so "the host must be a *different* programme" is not
    -- expressible without the host's programme being on this row. It earns its keep twice: the
    -- demand list reads it to render "gehalten von DE3" without a self-join.
    --
    -- Why different at all: the same programme is what serves_sibling_tracks is for. Letting two
    -- mechanisms describe one case is how one of them quietly stops meaning anything.
    ADD COLUMN covered_by_programme_id uuid,

    -- Always false, and it exists to be false.
    --
    -- The foreign key ties it to the host's own is_covered, and the CHECK below forbids true. That
    -- is the no-chain rule, declarative, and in *both* directions: a host that later tries to
    -- become somebody's guest fails its own UPDATE, because its generated is_covered would move
    -- under a key that points at it.
    --
    -- Chains are refused because a chain has no owner of the parts anybody can name in one step.
    -- Every screen and every future sum would have to walk it, and one of them eventually would
    -- not.
    ADD COLUMN covered_by_is_covered boolean,

    -- The handshake, as provenance rather than as a status column.
    --
    -- covered_accepted_at IS NULL *is* "asked and not yet answered". A third column naming the
    -- state would be something these two timestamps could contradict, and then the question would
    -- be which one to believe.
    ADD COLUMN covered_requested_at timestamptz,
    ADD COLUMN covered_accepted_at timestamptz,

    -- Who asked and who agreed. SET NULL rather than CASCADE, like module_component.created_by and
    -- demand_completion.completed_by: the person can leave, the decision stays.
    --
    -- Stored and not published. DemandCompletion is the precedent — it keeps completed_by and the
    -- API offers only completedAt — and the reason is the same: this is provenance for whoever
    -- asks later, not a field a screen renders.
    ADD COLUMN covered_requested_by uuid REFERENCES person (id) ON DELETE SET NULL,
    ADD COLUMN covered_accepted_by uuid REFERENCES person (id) ON DELETE SET NULL,

    -- Generated, stored, and referenced by the foreign key below. Nothing writes it; do not look
    -- for the writer.
    ADD COLUMN is_covered boolean
        GENERATED ALWAYS AS (covered_by_instance_id IS NOT NULL) STORED;

-- The key that says four things at once
-- -------------------------------------
--
-- Read out loud: *the instance that covers this one is the same module, in the same semester, in
-- the programme named on this row, and is not itself covered by anybody.*
--
-- Four invariants as a property of the row rather than of a Go function, which matters because two
-- of them are about a row the writer is not looking at. MATCH SIMPLE does the rest: with
-- covered_by_instance_id NULL the whole key is unchecked, which is exactly the ordinary instance.
--
-- ON DELETE RESTRICT is also the withdrawal rule — a host somebody's demand depends on cannot
-- quietly disappear.
ALTER TABLE course_instance
    ADD CONSTRAINT course_instance_is_addressable_as_a_host
        UNIQUE (id, semester_id, module_id, programme_id, is_covered),
    ADD CONSTRAINT course_instance_covered_by_is_a_real_alternative
        FOREIGN KEY (covered_by_instance_id, semester_id, module_id,
                     covered_by_programme_id, covered_by_is_covered)
        REFERENCES course_instance (id, semester_id, module_id, programme_id, is_covered)
        ON DELETE RESTRICT,

    -- All four or none. Half a link is not a weaker statement, it is an unreadable one — and it is
    -- what makes "clear the coverage" a single UPDATE that cannot leave a remnant behind.
    --
    -- requested_by is not in the count: it is nullable on its own, because the person who asked
    -- may since have been removed.
    ADD CONSTRAINT course_instance_coverage_is_all_or_nothing CHECK (
        num_nonnulls(covered_by_instance_id, covered_by_programme_id,
                     covered_by_is_covered, covered_requested_at) IN (0, 4)),

    -- IS DISTINCT FROM rather than <>, so the NULL case does not evaluate to NULL and pass.
    ADD CONSTRAINT course_instance_coverage_crosses_programmes CHECK (
        covered_by_programme_id IS DISTINCT FROM programme_id),

    ADD CONSTRAINT course_instance_coverage_does_not_chain CHECK (
        covered_by_is_covered IS NOT TRUE),

    -- Accepted implies asked, and not before. Guards against a release that cleared the request
    -- and left the acceptance standing, which would read as a coverage nobody asked for.
    ADD CONSTRAINT course_instance_coverage_is_accepted_after_it_is_asked CHECK (
        covered_accepted_at IS NULL
        OR (covered_requested_at IS NOT NULL AND covered_accepted_at >= covered_requested_at));

-- "Whose demand does this instance meet" — the host's own screen, and the withdrawal check.
-- Partial, because the column is null on almost every row.
CREATE INDEX course_instance_covers_idx ON course_instance (covered_by_instance_id)
    WHERE covered_by_instance_id IS NOT NULL;

-- What the key still cannot say
-- -----------------------------
--
-- "An accepted guest holds no parts of its own." That is a cross-table cardinality statement, the
-- same kind migration 8 declined to buy with a trigger, and it is enforced in internal/store: in
-- the transaction that accepts, and on every path that adds a part. It is tested against a real
-- database rather than a fake, because a fake would pass either way.

-- +goose Down

DROP INDEX IF EXISTS course_instance_covers_idx;

ALTER TABLE course_instance
    DROP CONSTRAINT IF EXISTS course_instance_coverage_is_accepted_after_it_is_asked,
    DROP CONSTRAINT IF EXISTS course_instance_coverage_does_not_chain,
    DROP CONSTRAINT IF EXISTS course_instance_coverage_crosses_programmes,
    DROP CONSTRAINT IF EXISTS course_instance_coverage_is_all_or_nothing,
    DROP CONSTRAINT IF EXISTS course_instance_covered_by_is_a_real_alternative,
    DROP CONSTRAINT IF EXISTS course_instance_is_addressable_as_a_host,
    DROP COLUMN IF EXISTS is_covered,
    DROP COLUMN IF EXISTS covered_accepted_by,
    DROP COLUMN IF EXISTS covered_requested_by,
    DROP COLUMN IF EXISTS covered_accepted_at,
    DROP COLUMN IF EXISTS covered_requested_at,
    DROP COLUMN IF EXISTS covered_by_is_covered,
    DROP COLUMN IF EXISTS covered_by_programme_id,
    DROP COLUMN IF EXISTS covered_by_instance_id;
