# tallox.go

GraphQL backend for **Tallox** (from *Teacher Allocations*), the teaching-assignment
planning system (*Einsatzplanung*) of faculty 07 at Hochschule München.

> **Status: early construction.** Tooling, CI, identity and authorization are in place;
> the planning domain — modules, instances, wishes — is being built. See
> [CLAUDE.md](CLAUDE.md) for the architecture.

## What it does

Faculty 07 plans, every semester, which courses are offered and who teaches them. Tallox
models that process:

1. **Bedarfsplanung** — study-programme leads declare which course instances are needed.
2. **Wunschphase** — lecturers register interest in instances. Those entries stay invisible
   to everyone else until a publication milestone.
3. **Zuteilung** — subject-group leads assign instances to people.
4. **Auswertung** — the dean's office evaluates import/export between programmes and
   faculties.

Courses are planned as **instances**, not modules: a module can run twice in a semester, and
a lecture and its lab can be taught by different people. The assignable unit is the
*instance part*.

## Two ways in

The GraphQL API is a product, not just a contract with the web UI.

| Path | Authentication | For |
| --- | --- | --- |
| `POST /query` | `X-Remote-User`, injected by the auth proxy after OIDC | browser and server-side rendering |
| `POST /api/graphql` | `Authorization: Bearer tallox_…` | scripts and evaluations |

Both mount the **same** GraphQL handler and the same authorization model:

> effective permission = (what the role allows) ∩ (what the token's scopes grant) ∩ (what the access kind allows)

A token can never do more than its owner. Personnel data (the Deputat/overtime traffic light),
other people's unpublished wishes, and token management itself are not reachable with a token
at all.

Create a token with the `createPersonalAccessToken` mutation — through the browser door, not
with a token: token management is `@interactiveOnly`, so a leaked token cannot mint its
successors. The plaintext comes back once and is never stored; the server keeps a SHA-256.
Default lifetime 90 days, 365 at most. The web UI for this is not built yet.

```console
$ curl -sS https://<host>/api/graphql \
    -H "Authorization: Bearer $TALLOX_TOKEN" \
    -H 'Content-Type: application/json' \
    -d '{"query":"{ me { mail name roles } }"}'
```

### The contract: `schema.graphql`

[`schema.graphql`](schema.graphql) is the whole API in one file, generated from the server and
committed. It is the schema introspection returns, plus what introspection cannot say: which
fields carry `@scope`, `@interactiveOnly` and `@fullReach`, i.e. which door, scope and token kind
a field needs. Every release has the file attached, so a script can pin the contract of exactly
the version it runs against and generate its types without a running server. Introspection stays
on for editors and exploration.

The contract changes additively: add a field, mark the old one `@deprecated(reason: …)`, remove
it in a later release. CI lists every change against `main` and fails on a breaking one unless a
commit acknowledges it (see [Development](#development)).

## Stack

Go · [gqlgen](https://gqlgen.com) · PostgreSQL 18 · [sqlc](https://sqlc.dev) ·
[goose](https://github.com/pressly/goose) · chi · zerolog · viper

No ORM: SQL is written by hand in `db/queries/`, sqlc turns it into typed Go.

## Development

Everything runs in the DevContainer from the `tallox.dev` repo — Go, Node, PostgreSQL and all
tooling, one *Reopen in Container* away.

```bash
go build ./...
go test ./...
golangci-lint-v2 run
go generate ./...                                     # gqlgen, then schema.graphql
sqlc generate                                         # db/queries/*.sql -> Go
goose -dir db/migrations postgres "$TALLOX_DB_URL" up
gowatch                                               # live-reloading server on :8080
```

**API changes start with the contract.** The first commit of a branch that changes the API
touches only `graph/*.graphqls` and the regenerated `schema.graphql`, so the diff of
`schema.graphql` can be reviewed as the contract before any resolver exists. The `schema-diff`
CI job reports what changed against `main`. A breaking change fails it unless a commit message
in the branch carries a trailer

```text
API-Break: <why, and what a client has to change>
```

and acknowledging a break is not releasing a major: server and GUI share their major version,
and raising it is decided together with the maintainer.

The server needs `TALLOX_DB_URL` and applies the embedded migrations at startup.

Everything else comes from `tallox.yaml` (in `.` or `$HOME`, or `-config <path>`), with
explicitly-set flags winning over it. An unknown key in that file is a startup failure rather
than a shrug: a file that documents a setting the program ignores is worse than no file.

`auth.protectedadmins` is the list of people who must be able to administer this installation
whatever the database says. It is reconciled at every start — created, reactivated, granted
ADMIN as needed, and never revoked. It is what makes a fresh database usable at all, and it is
the way back in after an administrator has been removed by accident.

Locally `-auth-mode=dev` injects a development user on the browser path, so the GraphQL
playground at `/` works without a login. `gowatch` passes that flag (see `gowatch.yml`) —
without it the local server runs in proxy mode, nothing sets `X-Remote-User`, and every
request is anonymous while the process looks perfectly healthy. That user holds every role; to see what a colleague
with one role sees, seed a person and send `X-Remote-User` yourself. The **token path stays
real** even in dev — it needs an actual PAT from the local database, which keeps the
production code path exercised daily instead of discovered in October.

## Layout

```
bootstrap/          entrypoint: flags, config, wiring, router, shutdown
graph/              gqlgen schema and resolvers (one .graphqls per domain)
schema.graphql      the published contract, generated from graph/ — do not edit
internal/sdl/       renders schema.graphql from the served schema
internal/principal/ the authenticated actor in the context
internal/auth/      proxy-header and bearer-token authenticators
internal/policy/    visibility and phase rules — pure, no I/O
internal/domain/    business logic
internal/store/     the only package that touches the database
db/migrations/      goose
db/queries/         sqlc
```

A CI test asserts that no package outside `internal/store` imports `pgx`. That is what makes
"every read goes through the policy" true by construction rather than by discipline.

## License

BSD 3-Clause. See [LICENSE](LICENSE).
