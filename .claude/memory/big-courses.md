---
name: big-courses
description: Übersicht „Große Fächer“ (FKR-Beschluss) — was groß heißt, Soll/Ist, wer alle Profs sieht, warum Studiengangsleitungen hier mehr sehen als beim eingeplanten SWS
metadata:
  type: project
---

Gebaut am 2026-11-23 (`bigCourseOverview(studyYear)`, `db/queries/big_courses.sql`,
`policy.BigCourseVisibility`, `domain.BigCourseOverview`). Regel des Fakultätsrats: ab 20 SWS im
Jahresmittel mindestens zwei große Pflicht- oder Korbfächer je Studienjahr, ab 10 SWS eines.

**Entscheidungen mit der Fakultät (Issue im Tracker):**
- SWS = Lehr-SWS aus der **Zuteilung**, nicht Deputat; Mittel aus WiSe und folgendem SoSe.
- Nur Profs laut ZPA: keine Honorarprofs, keine Lehrbeauftragten. Gelistet, wer Kollege ist
  (Konto mit LECTURER oder Fachgruppe) oder im Studienjahr etwas hält — so erscheinen Profs ohne Konto.
- Ein Fach = (Modul, Studiengang, Semester) über alle Züge. Zweimal gehalten zählt zweimal.
- **Groß** = Pflicht im Studiengang oder in einem Korb, der nicht FWP ist (Korbname enthält das
  Wort „FWP“ → FWP; FWP-Platzhalter nie), **und** entweder ≥ 6 SWS selbst gehalten (auch über
  beide Züge) oder ganze Kohorte (ein Zug, oder Vorlesung `serves_sibling_tracks`) und die Person
  hält die Vorlesung. Nur Praktikum zählt nie über die Kohorte.
- `provisional`, solange eines der beiden Semester vor `ASSIGNMENT` steht („vorläufig“).
- Hinweis, keine Sperre.

**Sichtbarkeit:** Dekanat sowie Fachgruppen- und Studiengangsleitungen **mit Scope** und voller
Reichweite sehen alle Profs, schon während der Planung. Bewusst weiter als [[assigned-load]]
(dort keine Studiengangsleitung vor Veröffentlichung): die Regel ist Fakultätspflicht, Olli hat
„alle“ entschieden. Ungescopte Leitung, normales Token, alle anderen: nur die eigene Zeile.
Kein `@fullReach`, weil die eigene Zeile durch jede Tür lesbar bleibt.

Offen/zu prüfen: wie FWP-Körbe im echten ZPA heißen (lokal kein Cache); `teacher.faculty` wird
nicht gefiltert. Tests: `internal/store/bigcourses_test.go`, `bootstrap/bigcourses_test.go`,
`internal/policy/bigcourses_test.go`, `internal/domain/bigcourses_test.go`.
