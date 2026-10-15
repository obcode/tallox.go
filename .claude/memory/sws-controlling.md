---
name: sws-controlling
description: teachingLoad — Lehr-SWS je Heimatstudiengang, wie das alte Excel-Sheet; warum Lehr- statt Modul-SWS, und was noch offen ist (Soll/Kontingent)
metadata:
  type: project
---

Gebaut am 2026-10-15 auf Rückmeldung aus der Fakultät: das einzige, was gegenüber der
Excel-Lösung fehlte. Dort schlug ein eigenes Sheet die SWS eines Moduls seinem
**Heimatstudiengang** zu, und daran legten die Studiengangsleitungen fest, wie viele FWP sich ein
Studiengang leisten kann. „Pragmatisch, nicht optimal, aber alle konnten damit leben."

## Was gebaut ist

- Query `teachingLoad(semester)` (`graph/controlling.graphqls`), `@scope(PLANNING, READ)`, durch
  **beide Türen** — eine Summe über nicht vertrauliche Zeilen, und genau das, wofür die PAT-API da
  ist. Domain `DemandService.TeachingLoad`, Store `Demand.TeachingLoad`, SQL
  `TeachingLoadByHomeProgramme` in `db/queries/demand.sql`.
- Je Heimatstudiengang: Summe, davon eigen, Aufteilung nach Studiengang der Instanz (`shares` —
  die Differenz ist die Import/Export-Zahl), Anzahl Instanzen, FWP-Platzhalter (Anzahl/SWS),
  `partsWithoutHours`.
- **Jeder geplante Studiengang steht drin, auch bei null** — der, der noch nicht angefangen hat, ist
  die Zeile, die man sucht. Andere nur, wenn eins ihrer Module hier gelehrt wird.

## Entscheidungen

- **Lehr-SWS, nicht Modul-SWS.** Summe über `instance_part.teaching_hours`, dieselbe Regel wie
  `CourseInstance.TeachingHours()`. Ein 4-SWS-Modul mit drei Praktikumsgruppen kostet 8, nicht 4
  ([[instance-coverage]], und die drei SWS-Größen im Workspace-Glossar). Ob die Excel Modul-SWS
  zählte, ist nicht geklärt — falls ja, weichen die Zahlen bewusst ab.
- `SUM` ohne `DISTINCT` ist richtig: geteilte Vorlesung = ein Teil, gedeckte Kohorte = keine Teile.
- **Die Heimat-Zuordnung beantwortet die offene Zählfrage aus [[instance-coverage]] nebenbei**: die
  Teile liegen beim Gastgeber, das Modul hat eine Heimat, nichts zählt doppelt. Für die
  Import/Export-Sicht (nach Studiengang der Instanz) bleibt sie offen; der gedeckte Bedarf steht
  dort mit 0 SWS und als Instanz in `instanceCount`.
- Ein Teil ohne Stunden zählt 0 und wird **gezählt** (`partsWithoutHours`), damit eine unfertige
  Zerlegung nicht wie ein leichtes Semester aussieht.

## Offen

Erste Stufe ist nur die Ist-Übersicht. Kein Soll, kein Kontingent, keine FWP-Zielzahl je
Studiengang — das kommt erst, wenn klar ist, wie das alte Sheet daraus „wie viele FWP" gemacht
hat. Ebenso offen: ob Module fremder Fakultäten irgendwo mitzählen.

Tests: `internal/store/teaching_load_test.go` (echte PostgreSQL, sieben Fälle),
`bootstrap/controlling_test.go` (beide Türen, Scope, ungültiges Semester).

Verwandt: [[instance-coverage]], [[local-modules]], [[programme-planning-status]]
