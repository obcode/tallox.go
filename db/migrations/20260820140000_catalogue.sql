-- Migration 8: the catalogue, and the thing that gets planned.
--
-- The first migration of the domain beyond the semester, and it answers the question migration
-- 2 and migration 4 both declined to answer:
--
--     An instance is a module offered in one semester, for one study programme, to one parallel
--     cohort. The set of examination regulations is not part of its identity. What gets assigned
--     is not the instance but its parts.
--
-- The reasoning is in the faculty's decision paper; what belongs here is the part a reader of
-- this schema needs, and the two places where the paper was overruled by the people who do the
-- planning.
--
-- WHY THE REGULATIONS STAY OUT OF THE IDENTITY
--
-- One lecture in the winter serves every valid version of the regulations at once: students
-- under IF-2019 and under IF-2025 sit in the same room with the same person. An instance per
-- version would be the same event two to four times in every list — declared twice by the
-- programme lead, wished for twice by the lecturer, assigned twice by the subject group lead.
-- Measured against the source, this costs almost nothing: a module keeps its identity across a
-- change of regulations (IG kept all 113 modules over three versions, IF 126 of 127), so an
-- instance and the wishes hanging off it survive a new version untouched.
--
-- The price, accepted deliberately: the version is not readable off an instance. Where it is
-- needed — "does this module still count under the new regulations" — it is a query over
-- module_offering, not a column here.
--
-- WHY THE PARALLEL COHORT *IS* PART OF THE IDENTITY
--
-- This is where the decision paper was overruled, and the argument that did it is one sentence
-- from the faculty: an instance is normally assigned to one person. Software Engineering I runs
-- in two cohorts in IF because of the number of students — the faculty calls them IF1A and
-- IF1B — and each is held by a different person. If the cohort were a multiplicity inside the
-- instance, "assign IF3A to somebody" would not be a sentence this schema can express, and that
-- is the sentence the entire assignment phase is made of.
--
-- The counter-argument is real and is recorded here because it will come back: a cohort looks
-- like a multiplicity, exactly as the course type does, and the paper rejected an identity
-- carrying the course type with that argument. What settles it is asymmetry, not elegance. A
-- unique key can be widened later — an extra column with a default that reproduces today's rows
-- — and it can never be narrowed, because narrowing means deciding which of two existing rows
-- was the real one. Between a model that is too fine and a model in which the assignment phase
-- has no subject, the recoverable mistake is the one to make.
--
-- WHAT THE SOURCE DOES NOT PROVIDE, AND WHO PROVIDES IT INSTEAD
--
-- The examination office publishes one number of hours per module and a phrase describing how
-- the teaching is broken up ("SU mit Praktikum"). It does not publish the split. Measured: 457
-- of 506 modules carry `sws: '4'`, and the split into lecture and laboratory exists only as
-- prose inside the effort description, in fewer than half the modules and in inconsistent
-- wording. So the split is Tallox's own — module_component below — entered by the people who
-- know it and never touched by the import.
--
-- That number and the teaching load are different physical quantities, and this schema refuses
-- to let them share a name. A four-hour module whose instance runs one lecture and three
-- laboratory groups costs the faculty eight hours of teaching. Somebody summing the module's
-- own figure would get four, and four is a plausible-looking wrong answer.
--
--     module.contact_hours_per_week   what a STUDENT attends       (imported)
--     module_component.teaching_hours the canonical split per unit (entered here)
--     instance_part.teaching_hours    what a LECTURER is credited  (planned)
--
-- ZPA IDS ARE IMPORT KEYS
--
-- Every table that has a counterpart in the examination office's system carries a nullable,
-- unique zpa_*_ref, and nothing joins on it at request time. No foreign key points into the
-- zpa_* cache — store.TestNoDomainTableReferencesTheZpaCache reads pg_constraint and says so.
-- These tables are filled by a projection over the cache's views, which is why a module the
-- source has never heard of is possible and has to be: a programme lead must be able to plan
-- something the catalogue does not know yet.
--
-- +goose Up

-- Study programmes
-- ----------------
--
-- The unit a demand belongs to, a module is at home in, and the import/export statistics are
-- summed over. Nineteen of them today.
CREATE TABLE programme (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    -- IF, IG, DC, DA — the name the faculty says out loud, and the one the examination office
    -- publishes. Addressed by this over the wire rather than by the uuid, for the same reason
    -- semester.code is: it is short, stable, and belongs in a URL and in a colleague's script.
    code text NOT NULL UNIQUE,

    -- The long name, and the one column here the import must never write after the first time.
    --
    -- The source has no long name: its `title` field carries the code again ('IF'), so a
    -- projection that wrote title on every run would overwrite "Informatik (Bachelor)" with
    -- "IF" the night after somebody typed it. db/queries/person.sql solved the identical
    -- problem for person.name and the projection copies that discipline.
    title text NOT NULL DEFAULT '',

    -- False for a programme the examination office publishes no current regulations for.
    --
    -- One exists: six modules name a programme whose only set of regulations is a placeholder
    -- valid from 2099. Dropping those modules would lose four active ones; listing the
    -- programme as ordinary would put a programme with no students into every picker. So it is
    -- present and marked.
    active boolean NOT NULL DEFAULT true,

    zpa_programme_ref bigint UNIQUE,

    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    -- Short and upper case, so the column cannot quietly become a second name field. The dot
    -- is in the pattern because the faculty already runs MUC.DAI.
    CONSTRAINT programme_code_is_short CHECK (code ~ '^[A-Z][A-Z0-9.]{0,9}$'),
    CONSTRAINT programme_code_not_blank CHECK (length(code) > 0)
);

-- Sets of examination regulations
-- -------------------------------
--
-- Present as rows for three reasons and none of them is that an instance points at one — it
-- deliberately does not. module_offering needs a target that is not the cache; the optional
-- filter needs a labelled, ordered list per programme; and this is the only path from an
-- offering to the programme it counts in.
CREATE TABLE spo (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    programme_id uuid NOT NULL REFERENCES programme (id) ON DELETE CASCADE,

    -- The year, as the examination office numbers them: 2019, 2023, 2025.
    version integer NOT NULL,

    valid_from date,

    -- The identifier the campus management system uses, e.g. 07-IF-2025 — and empty while the
    -- version is still being entered. That emptiness is the only reliable signal that a set of
    -- regulations is not finished yet, which matters because an unfinished one holds a fraction
    -- of its eventual modules and would otherwise look like a catalogue that lost most of them.
    primuss_id text,

    zpa_spo_ref bigint UNIQUE,

    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    UNIQUE (programme_id, version)
);

CREATE INDEX spo_programme_version_idx ON spo (programme_id, version DESC);

-- Modules
-- -------
--
-- The catalogue entry. Stable across changes of regulations, which is what makes it safe for a
-- wish and an assignment to hang off something derived from it.
CREATE TABLE module (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Every module is planned by exactly one programme. The source is single-valued about this
    -- and the faculty is explicit: a programme should not plan modules that are at home
    -- somewhere else. Where a module also counts elsewhere, that is module_offering's job.
    --
    -- RESTRICT rather than CASCADE: removing a programme must not silently take its catalogue.
    home_programme_id uuid NOT NULL REFERENCES programme (id) ON DELETE RESTRICT,

    -- May be empty, and nineteen modules are.
    --
    -- The source's module objects carry no name field at all: the name exists only inside the
    -- nested object of an association row, so a module that appears in no set of regulations
    -- has none anywhere. Six of those nineteen are active and five are owned by the faculty's
    -- largest programme. Skipping them would mean a programme lead searching for a module they
    -- are responsible for and not finding it, which costs more than an ugly row.
    name text NOT NULL DEFAULT '',

    -- How the teaching is broken up, as an enum plus the phrase it was derived from.
    --
    -- The pair is the pattern for every imported vocabulary here. The enum is what queries and
    -- the API use; the raw phrase is what makes an unrecognised sixth value visible as a report
    -- line rather than as a silent fall to the default.
    course_type text NOT NULL DEFAULT 'DEPENDS_ON_SUBJECT',
    course_type_source text NOT NULL DEFAULT '',

    -- How often it runs. The one imported field that is genuinely useful as a filter: a
    -- summer-only module is not a candidate for a winter semester's demand, and eighty-nine of
    -- them are.
    frequency text NOT NULL DEFAULT 'UNKNOWN',
    frequency_source text NOT NULL DEFAULT '',

    -- Contact hours a STUDENT attends per week, as the catalogue states them.
    --
    -- Not teaching load, and not the sum of the parts of an instance. See the header.
    contact_hours_per_week integer,

    credits integer,

    -- The examination office's own flags. 101 modules are inactive and 22 unofficial; both are
    -- kept and filtered rather than hidden, because "what did this programme offer in 2024" is
    -- a question worth being able to answer.
    active boolean NOT NULL DEFAULT true,
    official boolean NOT NULL DEFAULT true,

    -- When a successful import stopped mentioning it. Distinct from active = false, which is
    -- the source's own flag on a module it still publishes.
    --
    -- Modules are never deleted. A delete here would cascade towards instances and eventually
    -- towards wishes, which are the records this system exists to keep.
    retired_at timestamptz,

    zpa_module_ref bigint UNIQUE,

    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    -- Three homes for each of these lists — this constraint, internal/domain, and the GraphQL
    -- enum — and they cannot import one another. store.TestDatabaseAndDomainAgreeOnFrequencies
    -- and its neighbours compare them pairwise, the same way the roles and phases are kept in
    -- step. A CHECK rather than a PostgreSQL enum because widening a CHECK is one statement and
    -- widening an enum fights the transaction goose runs it in.
    CONSTRAINT module_frequency_is_known CHECK (frequency IN (
        'EVERY_SEMESTER',
        'EVERY_WINTER_SEMESTER',
        'EVERY_SUMMER_SEMESTER',
        'ALTERNATING_WITHIN_SUBJECT_GROUP',
        'ON_ANNOUNCEMENT',
        'UNKNOWN'
    )),
    CONSTRAINT module_course_type_is_known CHECK (course_type IN (
        'SU_WITH_LAB',
        'SU_WITH_EXERCISE',
        'SEMINAR',
        'LAB',
        'SU',
        'EXERCISE',
        'PROJECT',
        'SELF_STUDY',
        'DEPENDS_ON_SUBJECT'
    )),
    CONSTRAINT module_hours_are_plausible CHECK (
        contact_hours_per_week IS NULL OR contact_hours_per_week BETWEEN 0 AND 30
    ),
    CONSTRAINT module_credits_are_plausible CHECK (
        credits IS NULL OR credits BETWEEN 0 AND 60
    )
);

CREATE INDEX module_home_programme_idx ON module (home_programme_id);

-- No trigram index on name, and that is a measurement rather than an omission: the catalogue is
-- 506 rows. A sequential scan with ILIKE is a fraction of a millisecond, and pg_trgm would be an
-- extension to install and keep installed for nothing.

-- The split of a module into teachable units
-- ------------------------------------------
--
-- Tallox's own, and the reason it exists is that the examination office does not publish it.
-- It publishes one figure — four hours — and a phrase. Whether those four hours are two of
-- lecture plus two of laboratory, or three plus one, is knowledge the faculty has and the
-- source does not.
--
-- Entered once per module and then stable; nothing about it changes when a new set of
-- regulations lands. The projection never writes here — this is the first table in the schema
-- that is purely Tallox's, and keeping the import out of it is what makes it safe to rely on.
--
-- It is a precondition rather than a decoration: an instance cannot be created for a module
-- that has none, because the parts of the instance are made from these rows and a plan whose
-- hours nobody has stated is not a plan. That refusal is also the work list — "your programme
-- has fourteen modules without a split" is a bounded, finishable task, which is what the beta
-- testers need in October rather than an open form.
CREATE TABLE module_component (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    module_id uuid NOT NULL REFERENCES module (id) ON DELETE CASCADE,

    kind text NOT NULL,

    -- Hours per week for ONE unit of this kind. A module with two hours of laboratory has one
    -- row saying two, however many parallel groups an instance later runs — the multiplicity
    -- belongs to the instance, not here.
    teaching_hours numeric(4, 2) NOT NULL,

    position integer NOT NULL DEFAULT 0,

    -- Who stated it. Not an audit log, which is a separate thing; this is here because the
    -- figure is a judgement rather than an import, and the person who made it is who one asks
    -- when it looks wrong.
    created_by uuid REFERENCES person (id) ON DELETE SET NULL,

    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    UNIQUE (module_id, position),

    CONSTRAINT module_component_kind_is_known CHECK (kind IN (
        'LECTURE', 'LAB', 'EXERCISE', 'SEMINAR', 'PROJECT', 'OTHER'
    )),
    CONSTRAINT module_component_hours_are_plausible CHECK (
        teaching_hours > 0 AND teaching_hours <= 20
    )
);

CREATE INDEX module_component_module_idx ON module_component (module_id, position);

-- Deliberately no constraint that the components sum to contact_hours_per_week.
--
-- Twelve modules carry zero hours in the source and several carry figures that do not match
-- what is actually taught. A CHECK would make exactly those modules unplannable, and the
-- unplannable ones would be the ones whose data is worst — precisely where a human needs to be
-- able to enter the truth. The comparison is made and shown as a warning next to both numbers;
-- disagreeing with the examination office is allowed and visible.

-- Where a module counts
-- ---------------------
--
-- One row per module per set of regulations. Filled only by the projection, never by hand: this
-- is a statement about somebody else's examination regulations, and a hand-editable copy would
-- be a second version of them that nobody in the faculty is authorised to keep. A programme
-- lead who needs a module that is not in their regulations creates the *instance*, which points
-- at the programme directly and needs no offering.
--
-- WHY THIS GRAIN
--
-- The source's association rows are module x regulations x catalogue slot — 3272 of them. The
-- decision paper proposed folding them to module x programme. Measured over the whole
-- catalogue, that fold loses information and this one does not:
--
--     (module, regulations)  2386 pairs, 0 disagree about compulsory-or-elective
--     (module, programme)    1076 pairs, 4 disagree
--
-- Compulsory-or-elective is functionally determined here and not one level up. What folding the
-- catalogue slots does lose — the specialisation and the code variants — is display material,
-- and both fold into arrays without a decision being made about them.
CREATE TABLE module_offering (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    module_id uuid NOT NULL REFERENCES module (id) ON DELETE CASCADE,
    spo_id uuid NOT NULL REFERENCES spo (id) ON DELETE CASCADE,

    -- Compulsory or elective, from the catalogue slot. This is what a programme lead means by
    -- "the compulsory and the elective catalogue", and it is the only real boolean the source
    -- publishes.
    is_duty boolean NOT NULL,

    -- Display only, never a key, and plural because it genuinely is.
    --
    -- The code belongs to the module-regulations-slot triple rather than to the module: 85
    -- module/regulations pairs carry more than one because the specialisation is inside the
    -- code, one module carries eight across the catalogue, and a fifth of the source rows carry
    -- none at all. This is the local shape of the identifier that became a primary key in the
    -- sibling project and could never be corrected afterwards.
    module_codes text[] NOT NULL DEFAULT '{}',

    -- The specialisations whose catalogue this module sits in. Reachable only through the
    -- catalogue slot; the regulations' own list of them is filled in two cases out of 29.
    focuses text[] NOT NULL DEFAULT '{}',

    -- The earliest programme semester a student may take it in. A floor, not a cohort year:
    -- nearly half the source rows say 1, which for an elective means "no restriction".
    -- course_instance.programme_semester is the other thing and is named differently on purpose.
    min_programme_semester integer,

    -- How many source rows folded into this one, so the projection report can say so and a
    -- surprising number is visible rather than smoothed away.
    source_rows integer NOT NULL DEFAULT 0,

    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    -- No zpa_msba_ref. An offering is a fold of one to four source rows, and a unique reference
    -- to an arbitrarily chosen one of them would look authoritative while being a coin toss.
    -- Its identity is the pair below, and both halves carry their own reference.
    UNIQUE (module_id, spo_id)
);

-- The reverse direction: "every module of this programme's regulations". The unique constraint
-- already covers module_id.
CREATE INDEX module_offering_spo_idx ON module_offering (spo_id);

-- Course instances
-- ----------------
--
-- What gets planned. One module, offered in one semester, for one programme, to one cohort.
CREATE TABLE course_instance (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    -- RESTRICT on all three, and it is the same argument each time: a semester, a module or a
    -- programme disappearing must not silently take a semester's planning with it. Retiring is
    -- a flag on those rows, never a delete.
    semester_id uuid NOT NULL REFERENCES semester (id) ON DELETE RESTRICT,
    module_id uuid NOT NULL REFERENCES module (id) ON DELETE RESTRICT,

    -- Whose demand this is. The import/export figures the dean's office needs are the sum over
    -- this column, and it is the reason the programme is in the identity at all: a module that
    -- two programmes offer together is two declarations, by two leads, and the difference
    -- between them is the export.
    programme_id uuid NOT NULL REFERENCES programme (id) ON DELETE RESTRICT,

    -- The parallel cohort — the A in IF3A. Empty when the module runs as a single cohort, which
    -- is the ordinary case.
    --
    -- The letter only. The label the faculty reads is the programme's code, the programme
    -- semester and this, assembled where the people who read it are; storing the assembled
    -- string would denormalise two facts and go stale on the third.
    track text NOT NULL DEFAULT '',

    -- Which cohort year this is for — the 3 in IF3A. A planning decision, not a catalogue fact.
    --
    -- Seeded from the regulations when the instance is created and editable afterwards, rather
    -- than derived on every read. Derived it would be a *set* rather than a number: 23 of the
    -- 1076 module/programme pairs disagree across versions of the regulations, and "IF3/4A" is
    -- not a label anybody uses. Worse, it would change retroactively when a new version lands,
    -- renaming a cohort that has already been taught.
    programme_semester integer,

    created_by uuid REFERENCES person (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    -- The identity, and the header of this migration is the argument for every column in it.
    -- Note what is absent: the set of regulations. One instance serves all valid versions.
    UNIQUE (semester_id, module_id, programme_id, track),

    CONSTRAINT course_instance_track_is_a_label CHECK (track ~ '^[A-Z0-9]{0,3}$'),
    CONSTRAINT course_instance_programme_semester_is_plausible CHECK (
        programme_semester IS NULL OR programme_semester BETWEEN 1 AND 12
    )
);

-- "What has to be offered in this semester for this programme" — the demand list, and the query
-- every screen in the planning starts from.
CREATE INDEX course_instance_demand_idx ON course_instance (semester_id, programme_id);

-- The sibling cohorts of one module, which is what a shared part below has to find.
CREATE INDEX course_instance_cohort_idx ON course_instance (semester_id, module_id, programme_id);

-- Instance parts
-- --------------
--
-- The assignable unit. A wish and an assignment will hang off one of these, never off the
-- instance: the faculty's own sentence is "one holds the lecture, another the laboratory".
--
-- Made from the module's components when the instance is created, with multiplicity — a module
-- whose split is one lecture and one laboratory typically becomes one lecture part and two or
-- three laboratory parts, because the groups are what the room and the supervision limit.
CREATE TABLE instance_part (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    -- CASCADE, unlike everything pointing at course_instance from outside: a withdrawn instance
    -- has no parts, and withdrawal is already refused once anything hangs off them.
    course_instance_id uuid NOT NULL REFERENCES course_instance (id) ON DELETE CASCADE,

    kind text NOT NULL,
    position integer NOT NULL DEFAULT 0,

    -- What a LECTURER is credited with for holding this part. Not the module's own figure.
    --
    -- Nullable, because an instance can be declared before the hours are settled — the demand
    -- deadline comes before the detail does, and refusing the declaration until every part has a
    -- number would push the whole thing outside the tool.
    teaching_hours numeric(4, 2),

    -- This part is held once and serves the other cohorts of the same module as well.
    --
    -- The case: one person gives the lecture for IF3A and IF3B, it happens once, and its two
    -- hours must be counted once. Expressed as a flag rather than as a link table between parts
    -- and instances, and the difference matters more than it looks:
    --
    --   * summing teaching hours stays SUM over the rows as stored, each exactly once. Through a
    --     link table it would need SUM over DISTINCT part_id — which somebody writes correctly
    --     the first time and wrongly the second, and the wrong answer looks reasonable.
    --   * "delete the part when its last link goes" is not something a foreign key can say, so a
    --     link table buys a trigger or an invariant enforced in Go.
    --   * a part shared between a DA instance and a DC one would belong to two programmes, and
    --     the import/export figure would lose its denominator. Sharing bounded to the cohorts of
    --     one module keeps every part in exactly one programme.
    --
    -- Only meaningful when the instance has siblings; harmless and false otherwise. The sibling
    -- instances render it as borrowed, through a join, and copying a cohort deliberately does
    -- not copy it — it already serves the new one.
    serves_sibling_tracks boolean NOT NULL DEFAULT false,

    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT instance_part_kind_is_known CHECK (kind IN (
        'LECTURE', 'LAB', 'EXERCISE', 'SEMINAR', 'PROJECT', 'OTHER'
    )),
    CONSTRAINT instance_part_hours_are_plausible CHECK (
        teaching_hours IS NULL OR (teaching_hours > 0 AND teaching_hours <= 20)
    )
);

-- No UNIQUE on (course_instance_id, position): reordering parts would otherwise need a dance
-- through a temporary value for a list that is three rows long.
CREATE INDEX instance_part_instance_idx ON instance_part (course_instance_id, position);

-- Programme-scoped grants
-- -----------------------
--
-- Migration 2 left the role grant unscoped and said where the scoped form belonged: "the
-- migration that creates them ... as its own table, so this one never has to be edited". This
-- is that migration, and this is that table.
--
-- A narrow table rather than a general one with a nullable column per dimension. A subject group
-- lead is scoped to a subject group, which is not a row yet; a general table would have to guess
-- the shape of that scope today, and guessing is what this migration exists not to do.
-- person_subject_group_scope arrives with the subject groups.
CREATE TABLE person_programme_scope (
    person_id uuid NOT NULL,
    role text NOT NULL,
    programme_id uuid NOT NULL REFERENCES programme (id) ON DELETE CASCADE,

    granted_at timestamptz NOT NULL DEFAULT now(),
    granted_by uuid REFERENCES person (id) ON DELETE SET NULL,

    PRIMARY KEY (person_id, role, programme_id),

    -- The load-bearing constraint, and the quiet failure it prevents is a security one.
    --
    -- Without it, revoking PROGRAMME_LEAD leaves these rows standing, and granting the role
    -- again silently restores the programmes somebody deliberately took away — a widening
    -- nobody performed. person_role's primary key is exactly (person_id, role), so the scope
    -- cannot outlive the grant it scopes.
    FOREIGN KEY (person_id, role) REFERENCES person_role (person_id, role) ON DELETE CASCADE,

    -- One role today. Widened in one statement when the next scoped role arrives, which is the
    -- same argument person_role's own CHECK makes for text over an enum.
    CONSTRAINT person_programme_scope_role_is_scopable CHECK (role IN ('PROGRAMME_LEAD'))
);

CREATE INDEX person_programme_scope_programme_idx ON person_programme_scope (programme_id);

-- An unscoped PROGRAMME_LEAD may plan nothing, and that reading is decided here rather than
-- inferred later.
--
-- It runs against this system's own precedent — an empty token scope list and an empty role
-- narrowing both mean "unrestricted" — and the precedent does not transfer. Those two are
-- mechanisms that can only ever remove, so "nothing selected" has to mean "nothing removed".
-- A programme scope is not a narrowing of the grant; it is the grant's subject. The role
-- declares the demand of ONE programme, and a role that means all of them already exists and is
-- called DEANS_OFFICE.
--
-- Reading it the other way would make the deploy of this migration the moment every existing
-- programme lead silently became faculty-wide — the widening direction, which nothing in this
-- system takes. The refusal a lead meets instead names its own repair.

-- The projection's report
-- -----------------------
--
-- The catalogue tables above are filled from the zpa_* cache rather than joined to it. That
-- projection has to make nine decisions about an untidy source — three modules whose home
-- programme is the string "None", six that name a programme with only a placeholder set of
-- regulations, nineteen with no name, 665 associations pointing at regulations the source no
-- longer publishes — and every one of them has to be visible afterwards. A projection that
-- silently drops rows is indistinguishable from a catalogue that never had them.
--
-- Named with the zpa_ prefix on purpose. store.TestNoDomainTableReferencesTheZpaCache is a
-- prefix test, and a foreign key from an unprefixed table to zpa_sync_run would trip it. The
-- prefix keeps that test mechanical without weakening it, and it is honest: this is a record of
-- the import, exactly like zpa_sync_run is.
CREATE TABLE zpa_catalogue_projection (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    -- The import run that triggered it, or NULL when somebody asked for a projection on its own
    -- — which is a thing to want, because the projection rules change and a changed rule has to
    -- be applicable to data already held without reaching into another institution's system.
    run_id uuid REFERENCES zpa_sync_run (id) ON DELETE CASCADE,

    started_at timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz,
    status text NOT NULL DEFAULT 'RUNNING',

    programmes_written integer NOT NULL DEFAULT 0,
    modules_written integer NOT NULL DEFAULT 0,
    offerings_written integer NOT NULL DEFAULT 0,
    offerings_removed integer NOT NULL DEFAULT 0,

    error text,

    CONSTRAINT zpa_catalogue_projection_status_is_known CHECK (
        status IN ('RUNNING', 'SUCCEEDED', 'FAILED')
    ),
    CONSTRAINT zpa_catalogue_projection_finished_when_done CHECK (
        (status = 'RUNNING') = (finished_at IS NULL)
    )
);

CREATE INDEX zpa_catalogue_projection_started_idx ON zpa_catalogue_projection (started_at DESC);

-- One kind of finding, with a count and a few examples.
--
-- Counts and samples rather than a row per affected object: the interesting number is "665
-- associations across 12 sets of regulations", and a reader who wants to chase one needs a
-- handful of identifiers, not all of them.
CREATE TABLE zpa_catalogue_projection_note (
    projection_id uuid NOT NULL REFERENCES zpa_catalogue_projection (id) ON DELETE CASCADE,
    code text NOT NULL,
    count integer NOT NULL,

    -- Examination office identifiers, as text, capped where they are written.
    sample text[] NOT NULL DEFAULT '{}',

    PRIMARY KEY (projection_id, code),

    CONSTRAINT zpa_catalogue_projection_note_code_is_known CHECK (code IN (
        -- Skipped: the home programme is mandatory and the source says "None".
        'MODULE_WITHOUT_HOME_PROGRAMME',
        -- Skipped: a programme code this schema cannot store. Every real one is two to four
        -- upper-case letters, so this is defence rather than expectation — but a code that
        -- failed the constraint would otherwise abort the whole projection, and take the
        -- modules of every other programme with it.
        'PROGRAMME_CODE_MALFORMED',
        -- Kept, programme marked inactive: no set of regulations the source still publishes.
        'PROGRAMME_WITHOUT_REGULATIONS',
        -- Kept with an empty name: the source has no name field and no association to borrow from.
        'MODULE_WITHOUT_NAME',
        -- Kept, flagged: the examination office retired it.
        'MODULE_INACTIVE',
        -- Not projected: the association points at regulations the source no longer returns.
        'ASSOCIATION_WITH_UNKNOWN_REGULATIONS',
        -- Mapped to UNKNOWN: a phrase this version does not recognise. Never a silent default.
        'FREQUENCY_UNMAPPED',
        'COURSE_TYPE_UNMAPPED',
        -- Folded with min(): the source rows of one pair disagree.
        'MIN_SEMESTER_CONFLICT',
        -- Must be zero. The grain of module_offering rests on it, so it fails loudly if it ever
        -- is not — measured today at 0 of 2386 pairs.
        'DUTY_CONFLICT'
    )),
    CONSTRAINT zpa_catalogue_projection_note_count_is_positive CHECK (count > 0)
);

-- +goose Down

DROP TABLE zpa_catalogue_projection_note;
DROP TABLE zpa_catalogue_projection;
DROP TABLE person_programme_scope;
DROP TABLE instance_part;
DROP TABLE course_instance;
DROP TABLE module_offering;
DROP TABLE module_component;
DROP TABLE module;
DROP TABLE spo;
DROP TABLE programme;
