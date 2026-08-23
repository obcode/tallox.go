# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this
repository. The workspace-level file at `/workspace/CLAUDE.md` (from the private `tallox.dev`
repo) applies as well and covers the domain glossary, the cross-repo workflow and the git
conventions.

## Overview

`tallox.go` is the GraphQL backend for **Tallox** (from *Teacher Allocations*), the
teaching-assignment planning system (*Einsatzplanung*) of faculty 07 at Hochschule München. It models the planning process
defined in FKR 387/378: study-programme leads declare which course instances are needed,
lecturers register interest, subject-group leads assign, and the dean's office evaluates.

It is the backend for `tallox.gui` **and a first-class API**: colleagues use Personal Access
Tokens to write their own evaluations. Every rule the GUI appears to enforce is enforced
here, because the token path bypasses the GUI entirely.

**Everything in this repository is written in English** — identifiers, enum values, comments,
commit messages, documentation. The domain is German (Fachgruppe, Deputat, Wunsch), and the
translation happens once, in `tallox.gui`, where the terms are read by the people who use
them. The only German in the Go code is the user-facing strings the GUI shows: policy
`Reason` texts, validation messages, and the authentication refusals in `internal/auth`.

The mapping between the two vocabularies is written down once, in the doc comment on
`policy.Role`: LECTURER = Dozent:in, SUBJECT_GROUP_LEAD = Fachgruppenleitung,
PROGRAMME_LEAD = Studiengangsleitung, DEANS_OFFICE = Dekanat.

**This repository is public.** No hostnames, no operational detail, no names of colleagues in
fixtures, tests, comments or commit messages. Test data is invented (`prof.eins@example.org`),
not anonymised-real.

## Build, Test, Lint

```bash
go build ./...
go test ./...                      # integration tests need $TALLOX_TEST_DB_URL
go test ./internal/policy/ -run TestVisibilityMatrix
go test ./internal/policy/ -update-golden   # re-record the matrix, then READ the diff
go test -race -shuffle=on ./...    # what CI runs
TALLOX_TEST_DB=container go test ./internal/store/...   # needs a Docker socket
go vet ./...
golangci-lint-v2 run               # note the -v2 binary name
go generate ./...                  # gqlgen, after editing graph/*.graphqls
sqlc generate                      # after editing db/queries/*.sql
goose -dir db/migrations postgres "$TALLOX_DB_URL" up
go run . -migrate-status              # what is applied, what this binary would apply
```

Pre-commit hooks run gofmt, go vet, golangci-lint-v2 and gitleaks. Run `pre-commit install`
once. The gitleaks config carries a rule for the `tallox_` token format — the realistic
leak path is a colleague's evaluation script, not the server.

## Architecture

```
main.go                    version, time.Local = Europe/Berlin, bootstrap.Serve()
bootstrap/                 flags, viper config, wiring, chi router, graceful shutdown
graph/                     gqlgen: *.graphqls (follow-schema) + *.resolvers.go — thin
internal/buildinfo/        the ldflags version stamp. Shared by main, /healthz and `buildInfo`.
internal/principal/        the authenticated Actor in the context. stdlib + uuid only.
internal/auth/             two authenticators, one middleware, the PAT format
internal/policy/           visibility, phase and administration rules; role narrowing.
                           Pure: no DB, no HTTP, no GraphQL.
internal/domain/           business logic — token lifetimes, validation. No I/O of its own.
internal/store/            the ONLY owner of pgxpool. sqlc-generated queries.
internal/store/storetest/  integration harness: a migrated schema per test
db/                        embedded SQL assets (db.Migrations)
db/migrations/*.sql        goose
db/queries/*.sql           sqlc
internal/testdata/         invented fixtures — the cast of personas
internal/graphqltest/      drives the real handler through both doors
internal/golden/           committed renderings, `-update-golden` to re-record
```

**Enforced by a CI test: no package outside `internal/store` imports `pgx`/`pgxpool`.**
That makes "resolvers cannot reach the database" mechanically true rather than aspirational,
and it means every future CSV/PDF export necessarily goes through the policy. This is a
deliberate response to how the sibling project's business-logic package grew — starting with
the boundary costs almost nothing, retrofitting it is a multi-month effort.

Resolvers are thin and delegate to `internal/domain`. Hand-written model types live in
`graph/model/` with both `json:` (GraphQL) and `db:` tags and are autobound by gqlgen.

Generated, do not edit by hand: `graph/generated/`, `graph/model/models_gen.go`,
`internal/store/*.sql.go`.

## Auth: two doors, one authorization model

The backend does **not** authenticate interactively. In production it sits behind Caddy →
oauth2-proxy → OIDC (`sso.hm.edu`), which injects a trusted `X-Remote-User`. Separately, it
authenticates Personal Access Tokens itself on a distinct path.

```
/query, /download/*   ← browser + SvelteKit SSR, X-Remote-User (proxy-verified)
/api/graphql          ← Personal Access Token, Bearer ONLY, no fallback
```

The same gqlgen handler is mounted on both, so there is no second schema and no second
`AroundOperations` to forget.

**The invariant, tested once:**

> effective permission = (what the Role allows) ∩ (what the Scopes grant) ∩ (what the Kind allows)

A token can never exceed its owner's role; demoting a user instantly demotes their tokens.

`auth.mode` is `dev` | `proxy` | `off-token`, not a boolean (`-auth-mode`, default `proxy`).
In `dev` the browser path injects a development user **but the token path stays real** — that
way the production code path is exercised daily instead of discovered in October. Sending
`X-Remote-User` by hand still goes through the ordinary lookup, which is how one checks what a
single role actually sees. `off-token` does not mount `/api/graphql` at all: an emergency stop
that removes the route leaves no code path that could be wrong about whether it is engaged.

`gowatch` passes `-auth-mode=dev` for the local loop; without it the server would run in
proxy mode behind no proxy, and every request would be anonymous while every process looks
healthy.

The server reads `TALLOX_DB_URL` from the environment and applies the embedded migrations at
startup. Without a database it refuses to start — it cannot authenticate anybody.

`-migrate-status` reports what is applied and what this binary would apply, then exits without
touching anything.

`auth.protectedadmins` in `tallox.yaml` solves two problems with one mechanism. The first is
the first-boot deadlock: both doors need a person row, and handing out a Personal Access Token
is itself something only a signed-in administrator can do, so a fresh database is locked from
the outside with the key on the inside. The second is an administrator removing another one by
accident — on an installation reachable only through a VPN, "restart the container" is a much
better recovery than psql on the host.

It is reconciled at **every** start and is **additive only**: listed people are created,
reactivated and granted ADMIN as needed, and nothing is ever revoked. A list that could revoke
would turn a deleted line or a typo into a silent mass demotion found at the next restart.
Deciding it in the authenticator instead does not work: an actor with no person row has no id,
and the id is what `granted_by` references, what a token belongs to, and what the audit log
resolves.

**Migrations are not undone by a rollback**: pinning an older image tag
leaves the newer schema in place, so every migration has to be one the previous image can run
against — add a column in one release, stop reading the old one in the next, drop it in a
third.

**A configuration key is forward-only for the same reason.** `UnmarshalExact` means a file
carrying a key the binary does not know refuses to start — so ship the image that reads a key
before the key appears in anyone's `tallox.yaml`, and do not roll back past that image once it
has. Two things make this worse than it sounds: the failure is *deferred* to the next restart
rather than landing on the edit, and rollback is the documented recovery for everything else
here. `-check-config` reads the file, reports what it configures and exits — without a
database, unlike `-migrate-status`, because "will this image start with this file" is asked
exactly when the database may be the thing that is broken.

### Scopes

Schema-driven via a `@scope(area:, verb:)` directive, evaluated in `AroundOperations`
(`graph.EnforceScopes`, decided by `policy.ScopesAllow`). A root field without `@scope` is
treated as `ADMIN:WRITE` (fail-closed) **and fails `TestEveryRootFieldDeclaresAScope`**.

Areas are `PUBLIC`, `PROFILE`, `TOKENS`, `ADMIN`; verbs `READ` and `WRITE`. New areas arrive
with the fields that need them, not in advance — an area with no field behind it is a promise
in an enum that colleagues can read via introspection.

Four things carry this design:

1. **`skip_runtime: true` in `gqlgen.yml`.** Unlike `@interactiveOnly`, `@scope` is *not* a
   generated directive call. An operation asking for three root fields and permitted two must
   execute neither, and the structural rule below has no field to hang off. So the schema
   declares it and `graph/scope.go` reads it out of the field definitions.
2. Its default direction is fail-closed. The sibling project's is fail-open, which makes its
   newest endpoints its least protected ones — backwards, because a new endpoint is the one
   nobody has reviewed.
3. Its root-field walk ignores inline fragments and fragment spreads, so
   `mutation { ... on Mutation { … } }` yields an empty field list and the write check falls
   through to "not data-changing". Here: recurse into fragments **and** set the structural
   rule (`Mutation ∨ Subscription ⇒ WRITE`) independently of the walk, so neither half alone
   is load-bearing.
4. **An empty scope list means unrestricted**, within the owner's roles. Same argument as
   `policy.Narrow`: the mechanism can only ever remove, so "nothing selected" has to mean
   "nothing removed". Reading it as "nothing permitted" would make the empty set the most
   restrictive value of a column whose default is empty, and every existing token would have
   died on the deploy that shipped it.

Consequence of (4): **the minting path is where scoping becomes real.**
`createPersonalAccessToken` cannot choose scopes yet, so every token in existence carries an
empty list and the term does not bite outside tests. Introspection is exempt — it is what makes
editor completion and codegen work for colleagues, and it is deliberately on in production.

### `@interactiveOnly`

A real gqlgen field directive, so *generated* code calls it and no resolver can forget it. On
nullable fields it returns `null` rather than erroring the whole operation, so scripts stay
useful and the schema documents the boundary itself. Implemented in `graph/directives.go`,
decided in `policy.MayReadInteractiveOnly`, wired in `bootstrap` — gqlgen fails closed on a
directive with no implementation, so forgetting the wiring breaks the field loudly rather
than serving it.

Refusals travel as `*gqlerror.Error` with an `extensions.code` (`INTERACTIVE_ONLY`,
`TOKEN_NOT_FOUND`, …). The GUI branches on the code; the German sentence is the part that
gets reworded, and matching prose across a repository boundary breaks the first time somebody
improves a message.

Not reachable via token: the Deputat/overtime traffic light (personnel data), unpublished
wishes of other people, free-text notes about people, **token management itself** (otherwise a
leaked token mints its successors), user administration, the audit log.

Belt and braces: keep personnel data in its own root fields rather than hanging it off
`Person`, so there is no traversal path in the first place.

## Policy: the rule that carries the project

Wishes are confidential until published. Visible iff owner ∨ (a planning role or the dean's
office, **and** an interactive session) ∨ `semester.wishes_published_at IS NOT NULL`. The
purpose is to end the first-come-first-served race — a leak here is political damage, not a
bug.

Two decisions inside that rule are decisions rather than mechanics, and both are visible in
the golden matrix:

- A **Personal Access Token never reads somebody else's unpublished wish**, not even for a
  planner. A token is long-lived, sits in a script, and decouples "who saw this" from any
  login event. Own entries stay readable through both doors — that is their own data.
- **ADMIN is not on the exception list.** Running the system is a different job from planning
  with it; an administrator who needs to look is granted DEANS_OFFICE, visibly.

`internal/policy` is pure and holds the rule in **two consistent forms**:

- `CanSee…(actor, state, record) bool` — guard, for records already in hand
- `…Visibility(actor, state) …Filter` — query parameters, so the predicate runs in the
  database and indexes apply

A property test over the full cartesian product asserts the two agree. That drift is the
realistic way this design fails.

**Three leak channels row filtering does not cover:**

1. **Aggregates.** "3 colleagues already registered interest" leaks the information
   completely without a single name. Before publication: no counts, no has-wishes flags, no
   sorting by them, no heat-map colouring. Counts go through the same filter.
2. **Error messages.** A verbatim `UNIQUE` violation reveals that someone else already
   registered. Map DB errors generically on the write path.
3. **Exports.** Every CSV/PDF/ICS handler goes through `internal/store`.

`TestVisibilityMatrix` renders the full matrix to
`internal/policy/testdata/visibility_matrix.golden`. Every policy change becomes a reviewable
diff — and that file is the slide for the faculty retreat when someone asks who sees what
when. Keep it committed and keep it readable.

The semester phase lives in the database and is advanced by an audited mutation, **never
derived from the calendar**.

### Administration, and the two things that protect it

`policy.MayAdministerPeople` is ADMIN **and** an interactive session. Administration is
`@interactiveOnly` because granting somebody a role from a long-lived token in a script would
decouple "who did this" from any sign-in, and the act it decouples is the granting of access
itself.

**The last active administrator cannot be demoted or deactivated.** Both are the same refusal
(`LAST_ADMIN`), because they have the same consequence. It is a transaction that locks the
ADMIN grant rows before reading them: two administrators removing each other simultaneously is
write skew — both checks are individually correct, and the outcome is an installation that has
to be repaired from the host. The guard is in `internal/store` for the same reason the wish
filter is: it is a statement about rows, and a version of it in a service layer passes its unit
test while the shipped code races.

**Roles can be narrowed, never widened.** `policy.Narrow` is `held ∩ selected`, which is the
whole security argument: an intersection cannot add, so the selection may travel as an ordinary
untrusted header (`X-Tallox-Assume-Roles`, browser door only) and nothing downstream has to
establish where it came from. The obvious version — "let an administrator preview any role" —
would hand ADMIN a two-click route into unpublished wishes, which is precisely the decision the
policy makes deliberately in the other direction. To preview what a lecturer sees, hold
LECTURER. `TestNarrowCanOnlyEverRemove` asserts the property exhaustively.

`Actor.Roles` is the *effective* set, so narrowing reaches every rule automatically rather than
the ones somebody remembered. `Actor.NarrowedFrom` is for the banner and the audit log, and for
no rule.

There is **no DEVELOPER role and there will not be one**. `diagnoseAccess` answers the support
question — why can my colleague not see this — with decisions and never with content, and an
administrator who genuinely has to look grants themselves DEANS_OFFICE visibly and with an
expiry (`person_role.expires_at`, at most 30 days). A role that sees everything is a line in the
golden matrix that has to be defended to the colleagues the confidentiality rule protects.

## Configuration

viper reads a single file `tallox.yaml` (in `.` or `$HOME`, or `-config <path>`), plus
`TALLOX_DB_URL` from the environment. Secrets stay in the file, never in the database.

Precedence is file, then **explicitly set** flags — `flag.Visit`, so that `-playground=false`
is distinguishable from "the flag defaulted to false". Without that distinction every flag
default silently overrides the file and the file is decorative, which stays invisible until
somebody sets a value that happens to equal a default.

`UnmarshalExact`: an unknown key is a **startup failure**. Every key the file may carry is
read by something, and the blocks that are planned but not wired (ZPA, SMTP) are commented out
in `deploy/tallox.yaml.example` rather than declared — so the file cannot document a setting
the program ignores.

Rule of thumb: YAML holds bootstrap values and secrets. Everything semester-scoped and
user-editable (semester config, milestones, phases) lives in PostgreSQL and is edited through
the GUI.

Note `server.introspection` defaults to **on**, even in production — unlike the sibling
project. The API is a product here; introspection is what makes editor completion, codegen
and schema exploration work for the colleagues writing evaluations. Only the playground is
disabled in production.

## Conventions

- **Logging:** `zerolog` — `log.Error().Err(err).Str("field", v).Msg("cannot do x")`.
  `log.Fatal()` only in `bootstrap/`.
- **Error reporting:** with `SENTRY_DSN` set, every `log.Error()` goes to a GlitchTip
  instance via [internal/obs](internal/obs/) — a `sentryzerolog` writer hung beside the
  console writer, so no call site changes. **Read [internal/obs/scrub.go](internal/obs/scrub.go)
  before adding anything that reports:** the writer turns *every* zerolog field into a Sentry
  tag, so tags, headers and contexts are **allow-lists** — a field nobody anticipated is
  dropped, not sent, and mail addresses in free text are redacted. `obs.SkipField` on a log
  line keeps it out of the report. Issues are grouped by the **`caller` field**, not by stack
  trace (every error runs through one writer and would otherwise fold into a single issue);
  `zerolog.CallerMarshalFunc` is `obs.RepoRelativeCaller` so the grouping key does not depend
  on where the binary was built. Started in `setupReporting` *before* `LoadConfig`, so a
  configuration file that will not load is reported too. Empty DSN = no reporting, which is
  the local default; `-check-config` says which it is. Live check:
  `SENTRY_SMOKE_DSN=... go test ./internal/obs/ -run TestLiveIngest -v`.
  The package is the counterpart of `plexams.go/obs`; keep the two in step.
- **Timezone:** `main.go` forces `time.Local = Europe/Berlin`. Milestone and phase
  calculations depend on it.
- **Errors:** plain `error` wrapped with `fmt.Errorf("...: %w", err)`. No custom hierarchy.
  "Not found" is `(nil, nil)`.
- **Tests:** stdlib `testing`, table-driven with `t.Run`. No testify, no mock library — seams
  are narrow hand-written interfaces (see `auth.UserLookup`, `auth.TokenLookup`).
  Integration tests use `$TALLOX_TEST_DB_URL`, get their own schema and drop it in `t.Cleanup`.
  CI sets `TALLOX_TEST_DB_REQUIRED=1` so a skipped integration test is a failure rather than
  silently green.
- **Migrations:** goose, embedded, applied at startup. Steps must be idempotent; never edit
  or reorder a released migration. The startup path (`MigrateUpDSN`) holds a PostgreSQL
  advisory lock so that two starting replicas take turns; `Migrate` stays unlocked because the
  test harness migrates a private schema per test and the lock is database-wide. Note that a
  concurrency test starting from an *empty* schema proves nothing — goose serialises those by
  itself on creating its version table.
- Version is injected via ldflags into `main.version/commit/date`.

## Testing

No step, feature or fix is finished without tests. The rules this system has to get right are
the kind whose violation is a political problem rather than a bug report, so a regression has
to turn CI red before anyone notices it in the GUI.

**The harnesses, and what each is for**

| Package | Use it for |
| --- | --- |
| `internal/store/storetest` | A real, migrated PostgreSQL schema per test. `storetest.New(t)`. |
| `internal/graphqltest` | Driving the real handler as a principal, through one or both doors. |
| `internal/testdata` | The cast: `Eins` owns the record, `Zwei` must not see it, `Vier` plans. |
| `internal/golden` | Rendering a whole rule as a reviewable file. |

`storetest.New(t)` creates a schema, migrates it, drops it in `t.Cleanup` — so tests may call
`t.Parallel()`, and a crashed run leaves nothing behind. It takes the database from
`$TALLOX_TEST_DB_URL` when that is reachable and otherwise starts one via **Testcontainers**;
`$TALLOX_TEST_DB` (`auto` | `env` | `container`) pins the choice. The DevContainer has no
Docker socket, so the container path only runs in CI — which is exactly why CI runs it, in a
job of its own, against a database with no history.

**Do not mock the database.** The wish filter is a `WHERE` clause, "no aggregates before
publication" is what `COUNT` does with that clause, and the generic write-path error exists
because PostgreSQL raises SQLSTATE 23505. A fake store passes all three while the shipped
query leaks.

**Every rule gets tested through both doors.** `graphqltest.EachDoor` runs the assertion once
per mount for the same principal. The realistic failure is not a wrong answer — it is a rule
someone adds for the browser and forgets on the token path. Where the doors are *supposed* to
differ (`@interactiveOnly`), assert per door explicitly rather than quietly covering one.

**Test the leak channels, not just the rows.** For any visibility rule, that means a case for
the count as well as the list, and `graphqltest.AssertNoLeak(t, msg, append(graphqltest.DatabaseNoise(), testdata.Mails(testdata.Others(owner))...)...)`
on the write path.

**Migrations are tested in three directions** (`internal/store/migrate_test.go`): they apply
from nothing, applying twice is a no-op, and Down undoes Up. The rollback path is only ever
exercised on the day it is needed, unless CI exercises it on a Tuesday.

**CI gate** (`.github/workflows/ci.yml`): lint · generated-code drift · `go test -race
-shuffle=on` with coverage · the same integration tests on a from-scratch Testcontainers
database · govulncheck. End-to-end lives in `tallox.gui`, where the auth proxy exists to be
tested.
