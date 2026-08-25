-- Migration 14: subject groups — the faculty's own way of grouping modules and people.
--
-- Migration 2 left SUBJECT_GROUP_LEAD unscoped and said where the scoped form belonged: "the
-- moment subject groups exist as rows, and the migration that creates them is where the scoped
-- grant belongs — as its own table, so this one never has to be edited". This is that
-- migration, and person_subject_group_scope below is that table.
--
-- SUBJECT GROUPS DO NOT HAVE A SEMESTER
--
-- That is the first thing to know about them and the thing every later query depends on. A
-- subject group is a statement about a *subject* — mathematics, software, technical computer
-- science — not about a plan. It is not copied between semesters, it does not know about
-- examination regulations, and it survives every change of them. Consequently the subject
-- group of anything planned is derived through the module, and never stored beside it:
--
--     wish/assignment → instance_part → course_instance → module → module_subject_group
--
-- THE CONSEQUENCE OF DERIVING IT, STATED SO THAT IT DOES NOT SURPRISE ANYBODY
--
-- Re-cutting a group — and the faculty already expects to, mathematics has been split into a
-- classical and a machine-learning group — moves modules between groups with an UPDATE. Because
-- visibility is derived rather than copied, that UPDATE **retroactively** changes who may read
-- the unpublished wishes on those modules' instances.
--
-- That is the correct behaviour: whoever is responsible now is who may look now. It is written
-- down because the alternative reading is available and wrong — a subject_group_id copied onto
-- the wish row would freeze responsibility at the moment somebody registered interest, and the
-- lead of a group that has since been split would keep reading wishes for subjects that are no
-- longer theirs. internal/store carries a test that pins the derived behaviour rather than
-- discovering it later.
--
-- FOUR THINGS A GROUP CARRIES, AND WHY THEY ARE FOUR TABLES
--
--     subject_group             the group itself
--     module_subject_group      which modules belong to it — exactly one group per module
--     person_subject_group      who is in it — a person may be in several
--     person_subject_group_scope  who leads it
--
-- Membership and leadership are deliberately not one table with a boolean. They answer
-- different questions and are granted by different acts: membership says which subjects a
-- colleague works in, leadership is a role grant and has to be revocable with the role. The
-- foreign key on person_role below is what makes the second half of that sentence true.
--
-- +goose Up

-- The group
-- ---------
--
-- Nothing here comes from the examination office; the source has no notion of a subject group.
-- These rows are entered by the faculty, like module_component, and no import ever touches them.
CREATE TABLE subject_group (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    -- MATHE, SWE, TI — what the faculty says out loud, and what belongs in a URL and in a
    -- colleague's evaluation script. Addressed by this rather than by the uuid, exactly like
    -- semester.code and programme.code.
    code text NOT NULL UNIQUE,

    -- "Mathematik (klassisch)". Required, unlike programme.title, because there is no import
    -- that could fill it in later and a group nobody can name is a group nobody will use.
    name text NOT NULL,

    -- Retired rather than deleted, like programme.active and module.retired_at. A group that
    -- has been split still has to render in last year's planning.
    active boolean NOT NULL DEFAULT true,

    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    -- Short and upper case, so the column cannot quietly become a second name field. Slightly
    -- longer than programme.code allows, because a split group needs a distinguishable suffix
    -- (MATHE-ML) and the alternative is somebody encoding it into the name.
    CONSTRAINT subject_group_code_is_short CHECK (code ~ '^[A-Z][A-Z0-9._-]{0,15}$'),
    CONSTRAINT subject_group_name_not_blank CHECK (length(btrim(name)) > 0)
);

-- Which subject group a module belongs to
-- ---------------------------------------
--
-- One group per module, expressed as a table with module_id as its primary key rather than as a
-- column on module. The 1:1 is real; the separate table is not redundancy.
--
-- module is written by the ZPA projection. A hand-maintained column inside a table an import
-- rewrites is a column somebody is afraid to fill in — the exact problem module_component was
-- created to avoid, and its migration says so: "from the people who know it and never touched by
-- the import". A separate table makes "the projection cannot reach this" mechanical instead of a
-- promise, and store.TestProjectionNeverTouchesFacultyOwnedTables can say so by name.
--
-- It also makes the operational requirement cheap. open-questions.md asks that groups be
-- splittable in service without data loss; with this shape that is an UPDATE of the affected
-- rows, not a migration.
CREATE TABLE module_subject_group (
    module_id uuid PRIMARY KEY REFERENCES module (id) ON DELETE CASCADE,

    -- RESTRICT, unlike the two grant tables below, and the asymmetry is the point. Assigning
    -- 506 modules is work somebody did by hand over weeks; losing it to a DELETE would be
    -- silent and unrecoverable. Retiring a group is active = false, which leaves this intact.
    subject_group_id uuid NOT NULL REFERENCES subject_group (id) ON DELETE RESTRICT,

    -- Who said so. Not an audit log — this is a judgement rather than an import, and the person
    -- who made it is who one asks when it looks wrong. Same reasoning as module_component.
    assigned_at timestamptz NOT NULL DEFAULT now(),
    assigned_by uuid REFERENCES person (id) ON DELETE SET NULL
);

-- "every module of this group", which is the query the wish filter and the assignment screens
-- both start from. The primary key already covers the other direction.
CREATE INDEX module_subject_group_group_idx ON module_subject_group (subject_group_id);

-- Membership
-- ----------
--
-- "Eine Person kann in mehreren Fachgruppen sein" — from the kickoff, and the reason this is a
-- table and not a column on person.
--
-- Membership is NOT a permission. It is what the wish screen is filtered by so that a colleague
-- sees their own subjects first, and it is deliberately not a gate: registering interest outside
-- one's groups stays possible, because FWP wildcards, teaching for other programmes and simply
-- moving into a new subject are all real, and the repair for the last one is joining the group
-- rather than meeting a refusal.
--
-- What it is explicitly not: a licence to read unpublished wishes. The kickoff sentence
-- "jeder in einer Fachgruppe müsste alles lesen können" is about planning data. The
-- first-come-first-served race the confidentiality rule exists to end plays out *inside* a
-- subject group — the colleague who has taught the subject for years is in it — so a membership
-- that granted wish visibility would switch the rule off precisely where it is needed. Only the
-- lead reads them; see internal/policy/wish.go and testdata/visibility_matrix.golden.
CREATE TABLE person_subject_group (
    person_id        uuid NOT NULL REFERENCES person (id) ON DELETE CASCADE,
    subject_group_id uuid NOT NULL REFERENCES subject_group (id) ON DELETE CASCADE,

    joined_at  timestamptz NOT NULL DEFAULT now(),
    granted_by uuid REFERENCES person (id) ON DELETE SET NULL,

    PRIMARY KEY (person_id, subject_group_id)
);

CREATE INDEX person_subject_group_group_idx ON person_subject_group (subject_group_id);

-- Leadership
-- ----------
--
-- The counterpart of person_programme_scope, copied deliberately rather than generalised: a
-- narrow table per scoped role, so that neither has to guess the shape of the other. That file
-- said person_subject_group_scope "arrives with the subject groups". Here it is.
--
-- WHY THIS IS A TABLE AND NOT subject_group.lead_person_id
--
-- The load-bearing line is the composite foreign key on person_role, and a column cannot express
-- it. Without it, revoking SUBJECT_GROUP_LEAD leaves the scope rows standing, and granting the
-- role again silently restores the groups somebody deliberately took away — a widening nobody
-- performed. person_role's primary key is exactly (person_id, role), so the scope cannot outlive
-- the grant it scopes.
--
-- The faculty's own sentence is "keine Fachgruppe ohne Person, die sich ihrer annimmt", and it
-- is honoured as a work list rather than as NOT NULL: a group has to be creatable before its
-- lead is decided, and a lead has to be revocable without destroying the group. "2 subject
-- groups without a lead" is a bounded, finishable task — the same shape as "14 modules without a
-- split", which is what the beta testers need in October.
--
-- Several leads per group are possible and one person may lead several groups. Neither is the
-- expected case and neither needs a rule: the shape follows from the primary key, and a
-- constraint forbidding it would be a guess about how the faculty organises itself.
CREATE TABLE person_subject_group_scope (
    person_id        uuid NOT NULL,
    role             text NOT NULL,
    subject_group_id uuid NOT NULL REFERENCES subject_group (id) ON DELETE CASCADE,

    granted_at timestamptz NOT NULL DEFAULT now(),
    granted_by uuid REFERENCES person (id) ON DELETE SET NULL,

    PRIMARY KEY (person_id, role, subject_group_id),

    FOREIGN KEY (person_id, role) REFERENCES person_role (person_id, role) ON DELETE CASCADE,

    -- One role today, widened in one statement when the next one needs a subject group. Same
    -- argument text over an enum makes everywhere else in this schema.
    CONSTRAINT person_subject_group_scope_role_is_scopable CHECK (role IN ('SUBJECT_GROUP_LEAD'))
);

CREATE INDEX person_subject_group_scope_group_idx ON person_subject_group_scope (subject_group_id);

-- An unscoped SUBJECT_GROUP_LEAD may do nothing, not everything — decided here, exactly as
-- person_programme_scope decided it for study programmes, and for the same reason.
--
-- It runs against this system's own precedent twice over: an empty token scope list and an empty
-- role narrowing both mean "unrestricted". Neither transfers. Those are mechanisms that can only
-- ever remove, so "nothing selected" has to mean "nothing removed". A subject group scope is not
-- a narrowing of the grant; it is the grant's subject. The role fills the instances of ONE
-- subject group, and the role that means all of them already exists and is called DEANS_OFFICE.
--
-- Read the other way, this migration would be the deploy on which every existing subject group
-- lead silently became faculty-wide — including, once wishes exist, faculty-wide over other
-- people's unpublished ones. What such a person meets instead is a refusal that names its own
-- repair.

-- +goose Down

DROP TABLE person_subject_group_scope;
DROP TABLE person_subject_group;
DROP TABLE module_subject_group;
DROP TABLE subject_group;
