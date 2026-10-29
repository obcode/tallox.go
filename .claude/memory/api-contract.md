---
name: api-contract
description: schema.graphql als veröffentlichter API-Vertrag (2026-10-29) — warum gerendert statt zusammengeklebt, die Bruch-Prüfung mit Trailer statt Label, Release-Anhang, und was für die GUI noch aussteht
metadata:
  type: project
---

Gebaut am 2026-10-29 auf `feat/issue-46`, nach einem Plan, den Oliver mit vier Antworten
freigegeben hat: graphql-inspector, von Anfang an scharf (fail), GUI pinnt ein **Tag**,
Bestätigung per Commit-Trailer.

## Was es gibt

- `schema.graphql` im Repo-Wurzelverzeichnis, erzeugt von `go generate ./...` (zweites
  `//go:generate` in `graph/resolver.go`, nach gqlgen) über `internal/sdl/cmd/sdlgen`.
- `internal/sdl.Render(sdl.Served())` druckt das `*ast.Schema`, das gqlgen tatsächlich
  ausführt, mit dem gqlparser-Formatter. **Gerendert, nicht aus den `.graphqls` zusammengeklebt**:
  so kann die Datei keinen Server beschreiben, den es nicht gibt, und der Test vergleicht Bytes.
  Der Formatter druckt Direktiven-*Verwendungen* — genau das, was Introspection weglässt.
- Tests in `internal/sdl`: Datei == gerendertes Schema; Datei lädt allein und übersteht den
  Rundlauf durch den Parser; jede `@scope`/`@interactiveOnly`/`@fullReach`-Verwendung des
  Servers steht in der Datei am selben Feld.
- CI-Job `schema-diff` (`.github/scripts/schema-diff.sh`): graphql-inspector, gepinnt, gegen
  den Abzweig von `main` (auf `main` gegen den ersten Elternteil). Bruch → rot, außer ein
  Commit im Bereich trägt `API-Break: <Grund>`. Release und GUI-Dispatch warten auf den Job.
- `docker.yml`, Job `attach-schema`: hängt `schema.graphql` an jedes veröffentlichte Release.

## Stolperstellen

- Der Formatter rückt Leerzeilen in Beschreibungen ein. Der pre-commit-Hook
  `trailing-whitespace` entfernt das — und schon ist die committete Datei „veraltet“ und der
  Test rot. `Render` entfernt reine Einrückungszeilen deshalb selbst.
- Ohne `schema.graphql` an der Basis (der erste Branch) meldet der Job nur einen Hinweis.
- **Trailer statt Label**, weil hier ohne Merge-Requests gearbeitet wird. Ein bestätigter Bruch
  ist **kein** Major-Bump: Server und GUI teilen den Major, das bleibt Absprache mit Oliver.

## Offen

- `tallox.gui`: `update-schema` holt die Datei aus einem gepinnten `tallox.go`-Tag statt per
  Introspection vom laufenden Server; geht erst, wenn ein Release die Datei trägt.
- Hilfe/`/api-doku` in der GUI: Verweis auf die Datei als Ort, an dem Tür, Bereich und
  Token-Art je Feld stehen.
- Mock-Server (aus dem Vorschlag) ist ausdrücklich ein späterer Schritt.
