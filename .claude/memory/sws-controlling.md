---
name: sws-controlling
description: teachingLoad — Lehr-SWS je Studiengang, der die Teile hält (seit 2026-10-16, vorher je Heimat); warum Lehr- statt Modul-SWS; Kontingent, Saldo, Studienjahr; Filter foreignHome
metadata:
  type: project
---

Gebaut am 2026-10-15 auf Rückmeldung aus der Fakultät: das einzige, was gegenüber der
Excel-Lösung fehlte. Dort schlug ein eigenes Sheet die SWS eines Moduls seinem
**Heimatstudiengang** zu, und daran legten die Studiengangsleitungen fest, wie viele FWP sich ein
Studiengang leisten kann. „Pragmatisch, nicht optimal, aber alle konnten damit leben."

## Zuordnung: wer die Teile hält, zahlt (2026-10-16)

**Ersetzt die ursprüngliche Zuordnung nach Heimatstudiengang.** Rückmeldung aus Collab: Im Excel
hatte z. B. Analysis eine IF- und eine DC-Variante. Im ZPA ist es *ein* Modul (Heimat IF, auch in
den SPOs von DC, IC, GS), also zahlte IF auch die Analysis, die DC selbst plant. „DC will nicht,
dass IF die DC-Analysis mitzahlt."

- Die SWS zählen beim **Studiengang der Instanz, die die Teile hält**. Separat geplant → der
  Studiengang selbst. Gedeckt (an die Veranstaltung eines anderen gehängt, 0 Teile) → der Gastgeber.
- Die Excel-„Variante" ist damit die separat geplante Instanz, ohne Pflegetabelle. Bewusst
  verworfen: eine Mitheimat je Modul (etwa 50 Module allein IF∩DC wären zu pflegen).
- Folge: Ein fremdes Wahlfach, das ein Studiengang mit eigener Gruppe plant, zahlt er selbst.
- `shares` sind jetzt nach **Heimat der Module** aufgeteilt; `ownHours` = eigene Module,
  `ForeignHours()` = Import. SQL `TeachingLoadByProgramme`.
- API: `ProgrammeTeachingLoad.programme` (vorher `homeProgramme`), `TeachingLoadShare.homeProgramme`
  (vorher `programme`), `ProgrammeStudyYearLoad.programme`. Kontraktbruch einen Tag nach dem Bau,
  als `feat:` committet; Frage nach dem gemeinsamen Major gestellt.

## Was gebaut ist (Stand 2026-10-15, Zuordnung siehe oben)

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
- Gedeckter Bedarf steht mit 0 SWS und als Instanz in `instanceCount` beim Gast; die Teile und
  damit die Kosten liegen beim Gastgeber. Nichts zählt doppelt.
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
- `ProgrammeTeachingLoad.yearlyQuota`, `semesterQuota`, `balance` (= semesterQuota −
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

**Semester-Soll je Term (2026-10-16, Rückmeldung zum Plan):** im **WiSe** die Hälfte des
Kontingents, weil das SoSe noch kommt; im **SoSe** Kontingent − Lehr-SWS des WiSe desselben
Studienjahres, weil das verbraucht ist. Der SoSe-Saldo ist damit der Rest des Studienjahres.
`TeachingLoad.SpentInWinter` trägt das WiSe; nil im WiSe.

## Studienjahr (2026-10-16)

`studyYearLoad(studyYear)`: je Heimatstudiengang Kontingent, WiSe, SoSe, Rest
(`domain.StudyYearLoad`). Das ist die Hauptsicht der Studiengangsleitungen: „ist noch Luft für ein
FWP". **SoSe durchspielen** heißt: den SoSe-Bedarf früh als echten Bedarf anlegen — bewusst kein
eigenes Planspiel (entschieden 2026-10-16). Er ist öffentlich lesbar, und Wünsche darauf sind wie
immer möglich (Wunschfenster der Fachgruppe); die Fakultät wollte das so lassen.

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

Tests: `internal/store/teaching_load_test.go` (echte PostgreSQL, sieben Fälle),
`internal/store/programme_quota_test.go` (Upsert, Studienjahr, CHECK, Fremd-Filter),
`bootstrap/controlling_test.go` (beide Türen, Scope, ungültiges Semester),
`bootstrap/programme_quota_test.go` (wer schreibt, Verengung, Saldo durch beide Türen).

Verwandt: [[instance-coverage]], [[local-modules]], [[programme-planning-status]]
