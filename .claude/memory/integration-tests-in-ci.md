---
name: integration-tests-in-ci
description: "./internal/store/... sind zwei Test-Binaries — zwei Container, ein Ryuk, und daraus wird ein sterbender Postmaster"
metadata:
  type: project
---

**`go test ./internal/store/...` baut zwei Test-Binaries** — `internal/store` und, seit es
`storetest_test.go` gibt, `internal/store/storetest`. `go test` lässt Pakete parallel laufen,
also startet jedes seinen eigenen PostgreSQL über `storetest.startContainer`. Der Kommentar
dort („at most once per test binary") stimmt weiterhin; die stillschweigende Annahme, dass es
nur ein Binary gibt, stimmt seit dem 2026-08-20 nicht mehr.

**Ryuk ist ein Singleton pro Testcontainers-Session.** Zwei gleichzeitig startende Binaries
streiten sich darum, und das äußert sich in zwei ganz verschieden aussehenden Fehlern:

- `Reaper handshake failed: read ack: … connection reset by peer` beim Start, oder
- mitten in der Suite `FATAL: terminating connection due to unexpected postmaster exit`
  (SQLSTATE 57P01), danach `connection refused` — und rund 60 rote Tests in `internal/store`,
  die eine Minute vorher noch grün waren.

Der zweite Fall ist der teure: er sieht aus wie ein Datenbank- oder Migrationsproblem und ist
keines. **Wer in `internal/store` plötzlich massenhaft rote Tests sieht, sucht zuerst nach
`postmaster exit` im Log**, nicht nach der letzten Migration.

**Der Fix im CI-Job `test-from-scratch`:** `go test -count=1 -p 1 -v ./internal/store/...`
plus `TESTCONTAINERS_RYUK_DISABLED: "true"` im Job-`env`. `-p 1` serialisiert die beiden
Binaries, und der Runner wird nach dem Job ohnehin weggeworfen, also muss dort nichts gereapt
werden. **Lokal bleibt Ryuk an** — außerhalb der CI überlebt der Container sonst den
Testprozess.

Wer ein weiteres Paket unter `internal/store/` mit eigenen Tests anlegt, ändert daran nichts
mehr; `-p 1` deckt beliebig viele ab, kostet aber je Paket eine Container-Startzeit.

Warum diese Fehlschläge drei Wochen lang jeden Release verhindert haben, ohne dass es
auffiel, steht in `ci-feature-gates` im privaten Repo.
