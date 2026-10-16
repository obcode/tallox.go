---
name: sws-controlling
description: teachingLoad — Lehr-SWS je Heimatstudiengang, wie das alte Excel-Sheet; warum Lehr- statt Modul-SWS; Kontingent je Studienjahr und Saldo; Filter foreignHome
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

## Kontingent und Saldo (2026-10-16)

Zweite Rückmeldung aus der Fakultät: im alten Sheet stand je Studiengang ein **Kontingent** — die
„verfügbare Lehrleistung inkl. Abschlussarbeiten für das Studienjahr" aus der
Kapazitätsrechnung — und das Delta war Kontingent − (WiSe + SoSe). Tallox plant ein Semester, also:

- Tabelle `programme_quota` (Migration 24), Schlüssel `(programme_id, study_year)`, **ohne FK auf
  `semester`** — ein Kontingent ist keine Entscheidung über ein Semester und darf keine
  Semesterzeile erzeugen.
- **Studienjahr** heißt nach dem Jahr seines Winters: `2026` = 2026-WS + 2027-SS
  (`domain.StudyYear`).
- `ProgrammeTeachingLoad.yearlyQuota`, `semesterQuota` (= Hälfte), `balance` (= semesterQuota −
  teachingHours). **`null` ohne Kontingent, nicht 0** — 0 hieße „nichts zu verteilen". Gerechnet
  im Backend, damit ein PAT-Skript dieselbe Zahl bekommt wie die GUI.
- Negativ: der Studiengang lebt über seinen Verhältnissen. Positiv: Raum für Wahlkatalog oder
  Querfinanzierung (z. B. Bachelor → Master).
- Pflege: `setProgrammeQuota` (null = zurücknehmen), nur `DEANS_OFFICE`
  (`policy.MaySetProgrammeQuota`), beide Türen. Nicht die Studiengangsleitung — die Linie, an der
  ein Studiengang gemessen wird, darf der Gemessene nicht verschieben. Lesen: `programmeQuotas`,
  jede:r mit Konto.
- `teachingHours` enthält die FWP-Platzhalter — das Delta liest sich also gegen dieselbe Summe, die
  die Seite als Lehr-SWS zeigt.

## Fremdmodule

`courseInstances(..., foreignHome: true)` — nur Instanzen von Modulen, deren Heimat ein anderer
Studiengang ist (`m.home_programme_id <> ci.programme_id`, in SQL). Die Sicht der
Studiengangsleitung „was nehme ich von anderen". Gedeckte Kohorten bleiben drin (0 SWS).

## Offen

- **Körbe** (z. B. „WPF Anwendungen des maschinellen Lernens" für DC): welche Module eines Korbs
  werden angeboten, welche nicht. Der Korbname steht im ZPA-Cache (`zpa_msba_v.basket_name`), die
  Projektion (`ProjectModuleOfferings`) wirft ihn weg. Eigenes Vorhaben.
- Module anderer **Fakultäten** wirken finanziell erst ab einer Mindestzahl Studierender je Modul.
  Es gibt keine Fakultätsspalte an `programme`; ungeklärt.
- Der Blick ein Semester weiter (die zweite Hälfte des Studienjahres) — bewusst nicht.

Tests: `internal/store/teaching_load_test.go` (echte PostgreSQL, sieben Fälle),
`internal/store/programme_quota_test.go` (Upsert, Studienjahr, CHECK, Fremd-Filter),
`bootstrap/controlling_test.go` (beide Türen, Scope, ungültiges Semester),
`bootstrap/programme_quota_test.go` (wer schreibt, Verengung, Saldo durch beide Türen).

Verwandt: [[instance-coverage]], [[local-modules]], [[programme-planning-status]]
