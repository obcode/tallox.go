---
name: programme-degree
description: Bachelor/Master am Studiengang — von Hand gepflegt, NULL statt geraten, Feld-Resolver statt Spalte in zwanzig Queries
metadata:
  type: project
---

Seit 2026-10-22 (`db/migrations/20261022100000_programme_degree.sql`) hat der Studiengang
`programme.degree` ∈ `BACHELOR` | `MASTER` | NULL, dazu `degree_set_at`/`degree_set_by`.
Ausgelöst hat das eine Bitte aus Collab: Unter der Fachgruppe WI stehen Bachelor- und
Mastermodule gemischt, besetzt werden sie aber getrennt.

**Warum es von Hand gepflegt wird:** Das ZPA liefert am Programm nur `{id, title}`, und `title`
ist wieder der Code. Die Kürzel (IF, IB, IC, IN …) verraten den Abschluss nicht. `-M-` in
Modulcodes ist nicht dokumentiert, und die Codes sind ohnehin nur zur Anzeige da. Es ist also
Fakultätswissen wie die Modul-Zerlegung. Die Projektion schreibt die Spalte nie, das pinnt
`TestProjectionDoesNotTouchTheDegree`.

**NULL statt geraten.** Keine Migration füllt Werte vor, denn ein geratener Abschluss sähe
gepflegt aus. Vgl. `20261016160000_remove_guessed_other_parts.sql`. NULL trifft in keinem
Filter zu, auch nicht in „alle außer“. Die GUI nennt die betroffenen Studiengänge und wer sie
pflegt.

**Wer setzt ihn:** `policy.MaySetProgrammeDegree` = `MayAdministerSemesters`, also nur das
Dekanat, über beide Türen. ADMIN darf es nicht. Die Studiengangsleitungen dürfen es auch nicht,
denn der Abschluss entscheidet, was die gefilterten Ansichten anderer zeigen. Die Funktion
steht für sich, aus demselben Grund wie `MaySetProgrammeQuota`.

**Filter:**
- `ModuleFilter.degree` hat dieselben zwei Hälften wie `programme`: Heimat ODER Angebot in
  einem Studiengang dieses Abschlusses. Ein Modul in Bachelor und Master trifft beide.
- `courseInstances(degree:)` filtert auf `ci.programme_id`.

**Die Falle, die vermieden wurde: das stille null.** Ein `domain.Programme` wird an ~20
Stellen im Store gebaut, oft nur mit Code/Title/Active (Wünsche, Zuteilungen, Coverage-Host,
Planungsmarken …). Ein `Degree`-Feld am Struct wäre dort leer geblieben und hätte als „nicht
gepflegt“ gelogen. Deshalb gilt:
- `Programme.degree` ist ein **Feld-Resolver** (`gqlgen.yml`, `resolver: true`) über die
  Lookup-Tabelle `ListProgrammeDegrees`.
- Die Tabelle wird **einmal pro Operation** geladen. Dafür sorgt `graph.CacheProgrammeDegrees`
  (AroundOperations, `graph/degree.go`).
- `setProgrammeDegree` verwirft den Cache nach dem Schreiben (`forgetProgrammeDegrees`).
  Ohne das liest die zweite von zwei Mutationen in einer Operation den alten Wert;
  `TestTheDegreeIsReadAfterTheWriteInOneOperation` fängt das.
- `domain.Programme` hat **kein** `Degree`-Feld. Wer eins einführt, baut die Falle wieder ein.

Verwandt: [[programme-planning-status]], [[sws-controlling]]
