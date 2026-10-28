---
name: full-reach-tokens
description: Planungs-Tokens mit voller Reichweite (2026-10-28) — warum Lesen und Schreiben nur zusammen aufgehen, was browser-only bleibt, und die Stolperstellen beim Bauen
metadata:
  type: project
---

Gebaut am 2026-10-28 auf `feat/full-reach-tokens`. Auslöser: eine Studiengangsleitung wollte
über die API planen, auch mit Claude. Der Auftrag lautete „Token-Zugriff maximieren, nur bei
absolut kritischen Dingen einschränken“.

## Der Kern: Lesen und Schreiben gehen nur zusammen auf

Gesperrt waren die Planungs-Mutationen nie, weil Schreiben gefährlich wäre. Über ein Token fiel
die **Lese**-Reichweite auf „nur Eigenes“, und die Absagen `INSTANCE_IN_USE`,
`PART_ALREADY_ASSIGNED`, `PART_ASSIGNED` hätten genau das verraten, was das Token nicht lesen
durfte. Nur die Mutationen zu öffnen, hätte ein Orakel gebaut. Deshalb gilt: **ein Token bekommt
die Mutation genau dann, wenn es die Zeilen lesen darf.** Ein normales Token bekommt beides
nicht, ein Planungs-Token beides.

## Entscheidungen

- **Opt-in je Token:** `createPersonalAccessToken(fullReach: true)`, beim Anlegen fest wie die
  Scopes. Bestehende Tokens sind unverändert. Die Alternative „jedes Token wie der Browser“ hätte
  jedem unbeschränkten Bestandstoken still die vertraulichen Wünsche gegeben.
- **Höchstens 90 Tage:** Domain-Absage (`ErrFullReachLifetime`) und ein CHECK mit 91 Tagen. Der
  Tag Puffer ist da, weil `expires_at` aus der Uhr der App kommt und `created_at` aus der Uhr der
  DB.
- **Browser-only bleiben nur:** Token-Verwaltung, Personen- und Rollenverwaltung (einschließlich
  `teacherAccounts`/`setTeacherAdmitted`, das ist Zugangskontrolle) und das Zugriffsprotokoll.
  `graph.TestOnlyAccessStaysInteractiveOnly` hält die Liste in beide Richtungen fest.
- **Ausdrücklich mit geöffnet** (so entschieden): unumkehrbare Akte (publishWishes,
  publishAssignments, freeze/publishDesiredHours), Personaldaten anderer (desiredHoursOverview,
  assignedLoad, Korrektur der Wunsch-SWS), ZPA-Betrieb und Fachgruppen-Verwaltung **einschließlich
  `setSubjectGroupLeads`**. Letzteres ist faktisch eine Rechtevergabe (wer Wünsche der Gruppe
  liest) und war Teil der Entscheidung, also nicht übersehen. `MayAdministerSubjectGroups`
  trennt das von `MayAdministerPeople`.
- **Kein neues Log-Feld:** `access_log.token_id` + die unveränderliche Spalte am Token reichen.
  Der Nachtbericht hat den Abschnitt „PLANUNGS-TOKENS MIT VOLLER REICHWEITE“ und macht den Bericht
  „berichtenswert“, solange eines gilt.

## Wo was liegt

`principal.Actor.FullReach` + `ActsWithFullReach()` · `policy.MayActWithFullReach` (ersetzt
`a.Interactive()` in allen Reichweiten-Regeln) · `@fullReach` in `graph/directives.go`, Code
`FULL_REACH_REQUIRED` · `domain.ErrFullReachRequired` für Aufrufer ohne Resolver ·
`Session.fullReach`, `PersonalAccessToken.fullReach`.

## Stolperstellen

- **Neue Codes für alte Absagen.** Ein normales Token bekommt auf den wieder geöffneten Feldern
  jetzt `FULL_REACH_REQUIRED` statt `INTERACTIVE_ONLY`. Das Verhalten ist gleich, der Code
  nicht. Ein Dutzend bootstrap-Tests musste umgestellt werden; Skripte im Kollegium, die auf den
  alten Code prüfen, auch.
- **`outcomeOf` im Zugriffslog** kannte nur `INTERACTIVE_ONLY`. Ohne Anpassung wären die neuen
  Absagen als `ERROR` gezählt worden, ein stiller Rückschritt im Nachtbericht. Beide Codes landen
  jetzt in `REFUSED_INTERACTIVE`, der Code steht in der Spalte daneben.
- **Die Schreibmatrizen beschrifteten die Tür über `actor.Kind`**, also stand bei Planungs-Tokens
  „token“. Jetzt `doorLabel(actor)`. Gegenprüfung nach `-update-golden`: jede `full reach`-Zeile
  muss textgleich mit ihrer `interactive`-Zeile sein. Ein Skript reicht, Augenmaß nicht.
- **Nach einer Änderung an `*.graphqls` immer `go generate`**, bevor man `graph/`-Tests liest.
  Sonst prüfen sie das alte eingebettete Schema und melden Phantome.
- Gegenprobe der neuen bootstrap-Tests: `ActsWithFullReach` auf `Interactive()` zurückgedreht →
  alle rot; `MayReadInteractiveOnly` auf Full Reach geöffnet → der Zugangs-Test rot.

Siehe [[scope-enforcement]], [[wishes]], [[assignments]], [[access-log]].
