---
name: programme-review
description: Phase REVIEW zwischen Zuteilung und FINAL, und die Marke „Review fertig" — warum bis Semesterende alles offen bleibt
metadata:
  type: project
---

Auf Rückmeldung aus der Fakultät (2026-10-16): Die Studiengangsleitungen verantworten gegenüber dem
Dekanat das SWS-Controlling, und das lässt sich erst nach der Zuteilung beurteilen. Drei LBA
gefunden heißt voller Bedarf. Keine gefunden, und die Leitung übernimmt drei Züge selbst, heißt
zusammengelegte Vorlesungen und weniger Gruppen.

- **Phase `REVIEW`** zwischen `ASSIGNMENT` und `FINAL` (Migration 25, CHECK erweitert). In der
  Schreibmatrix wie `ASSIGNMENT`: Bedarf, Zuteilung und Wünsche offen. „Bis zum Schluss alles
  veränderbar" war die ausdrückliche Antwort.
- **`FINAL` heißt jetzt „Semester vorbei"**, nicht „fertig geplant". Das Dekanat setzt es am
  Semesterende; bis dahin läuft `REVIEW`. Damit ist die Krankheitsvertretung vor Semesterbeginn
  eine Review und keine Wiederöffnung.
- **Marke `review_completion`** je (Semester, Studiengang), Zwilling von `demand_completion`:
  eine Ansage, die nichts sperrt, öffentlich lesbar, setzen darf die Leitung des Studiengangs und
  das Dekanat (`policy.MayAnnounceReviewComplete` = `MayPlanProgramme`), in jeder Phase.
  Eigene Tabelle statt Spalte an `demand_completion`: zwei Aussagen zu verschiedenen Zeitpunkten.
  Domain-Typ `ReviewCompletion = DemandCompletion` (gleiche Form).
- Down-Migration mappt `REVIEW` → `ASSIGNMENT`.
- Bekannte Kante: eine Vorlesung zusammenlegen, deren Geschwister-Teil schon besetzt ist, gibt
  `PART_ASSIGNED` — erst die Zuteilung lösen. Stunden eines besetzten Teils ändern geht ohne
  Rückfrage; die gutgeschriebenen SWS der Person ändern sich still mit.

Tests: `bootstrap/review_test.go`, Matrix-Golden in `internal/policy/testdata/`.

Verwandt: [[planning-marks]], [[sws-controlling]], [[assignments]]
