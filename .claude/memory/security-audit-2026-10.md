---
name: security-audit-2026-10
description: Was das Security-Audit vom 2026-10-28 fand, was daraufhin gebaut wurde, und welche Hinweise bewusst offen bleiben
metadata:
  type: project
---

# Security-Audit 2026-10-28 (Backend)

Code-Review auf `dea4ad1`, kein Pentest. Ergebnis: keine kritischen oder hohen Befunde; das
Kernmodell (Wunsch-Filter als Guard/Filter-Paar, Scope-Durchsetzung, `@interactiveOnly`,
Layering-Regel) wurde bestätigt. Die Audit-Datei selbst liegt im privaten `tallox.dev` unter
`docs/security-audit-2026-10-28-go.md`.

**Abgearbeitet auf `fix/security-audit-2026-10` (Merge am selben Tag):**

- **M1** Fünf Bereichs-Mappings endeten mit `return err`; `%w` im Store hält den Treibertext
  (Tabelle, Constraint, SQLSTATE) in der Kette. Jetzt: `internalRefusal` als Default-Arm überall,
  `graph.PresentError` als Backstop für alles ohne `extensions.code`, Test
  `TestAStoreFailureSaysNothing` (Tabelle unter dem Handler umbenennen, beide Türen).
  Transportfehler **vor** einer Operation (kaputtes JSON) bleiben wörtlich — die können keine
  Zeile verraten.
- **M3** Body-Grenze, Complexity-Grenze, Server-Timeouts, Session-Timeouts im Pool. Die GUI-Queries
  wurden gemessen (`DemandTable` = 185); vor dem Senken wieder messen (die Messung stand in einem
  Wegwerf-Test in `bootstrap/`, Muster: `complexity.Calculate` über die Dokumente aus
  `tallox.gui/src/lib/gql/__generated__/gql.ts`).
- **M2** Mehr als ein `X-Remote-User` → 401 (`ErrAmbiguousIdentity`, Warn). Netztrennung in
  `tallox.dev`.
- **L1** ADMIN nie befristet (Domain + CHECK). **L2** Policy vor `EnsureRecorded`. **L3** Mail der
  Protected Admins getrimmt. **L5** Partnerfakultät liest Struktur, nicht Roster. **L6** Scopes eines
  abgelaufenen Grants fallen beim Re-Grant. **L4** Token-Actor wird auch im `TokenService` und
  `AssignmentService` abgewiesen; **nicht** im `DemandService`, weil
  `TestTheTokenDoorPlansAsFarAsTheBrowserDoor` dort das Gegenteil als Entscheidung festhält.
- **L7** Actions auf SHA, Tag per `env:`, gosec an (Tests und `internal/golden` ausgenommen, G115
  begründet aus).
- Playground-Default aus; toter `WishWriteContextByWishID` weg und
  `TestNoOtherQueryFileReadsWishes` hält jeden Wish-Read in `wish.sql`; `RevokeToken` ohne
  Owner weg; `markUsed` koalesziert; Scrubber redigiert `user=`/`database=`/`host=`.

**Bewusst offen (by design, nicht vergessen):**

- Roster per Token: `teachers` und `Person` über `SubjectGroup.leads/members` liefern Mail und
  Beschäftigungskategorie des ganzen Kollegiums an jedes eigene Token. Gewollt („Person carries
  identity and roles“); die Grenze zieht die Partnerfakultät, nicht der Token.
- `assignedLoad` ist per Token gesperrt, die Information aber aus veröffentlichten
  `assignments.parts.teachingHours` clientseitig bildbar. Das Feld ist gesperrt, nicht die
  Arithmetik — so steht es jetzt auch in CLAUDE.md.
- Selbst-Grant-Befristung ist per Entzug + Neu-Grant verlängerbar; der Nachtbericht listet es.
- FINAL ist reversibel (FINAL → REVIEW für DEANS_OFFICE).
- `ApplyModuleSplit` schreibt Teil-Stunden fremder Programme um — dokumentierte Entscheidung.
- Kein Proxy-Secret zwischen Caddy und Backend: drei Repos und eine Rotation im Betrieb für
  dieselbe Frage, die die Netztrennung beantwortet.

**Why:** Die drei tragenden Regeln brechen nicht als Absturz. Das Audit hat gezeigt, wo die
Regel steht und der Rand nicht: Fehlertexte, Ressourcen, der Vertrauensanker.
**How to apply:** Neuer Bereich → Default-Arm ist `internalRefusal`, nie `return err`. Neue
Tabelle mit Wünschen → nur in `wish.sql` lesen. Neuer Service, der Personaldaten schreibt →
`MayReadInteractiveOnly` im Service wiederholen, nicht nur die Direktive. Siehe
[[identity-and-auth]], [[scope-enforcement]], [[wishes]].
