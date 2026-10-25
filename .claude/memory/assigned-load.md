---
name: assigned-load
description: Eingeplante SWS je Person und Fachgruppe — die einzige erlaubte Summe über Zuteilungen vor der Veröffentlichung, und warum die Leitung sie über Fachgruppengrenzen sieht
metadata:
  type: project
---

Gebaut am 2026-10-22, zusammen mit dem Wunsch-SWS-Bereich ([[desired-hours]]). Anlass war der
Nutzer: Die Fachgruppenleitung soll während der Planung sehen, wie viel jemand schon eingeplant
ist, „jemand kann ja in mehreren Fachgruppen sein und in einer anderen schon ziemlich verplant
sein“.

**Was gezählt wird:** `SUM(instance_part.teaching_hours)` über Zuteilungen mit `person_id` im
Semester, gruppiert nach Person und der Fachgruppe des Moduls (`module_subject_group`, höchstens
eine je Modul, die Teilsummen sind also disjunkt). Module ohne Fachgruppe kommen als eigene Zeile
mit `subjectGroup: null` zuletzt. Ein Teil mit `taught_by` ist nicht besetzbar, eine Vorlesung
für beide Züge ist ein Teil: jede Stunde zählt einmal. Lehrende ohne Konto erscheinen nicht, weil
sie keine Fachgruppe haben.

**Die Ausnahme, mit dem Nutzer entschieden:** Vor `assignments_published_at` sieht eine
Fachgruppenleitung für Mitglieder **ihrer** Fachgruppen die Summe über **alle** Fachgruppen,
**aufgeteilt je Fachgruppe**. Zuerst war „nur die Gesamtsumme“ empfohlen, der Nutzer hat sich
für die Aufteilung entschieden, damit die Leitung weiß, mit welcher anderen Leitung sie reden
muss. Module, Instanzen und Teile bleiben unsichtbar.

**Die Form der Regel kommt von der Person, nicht von der Instanz**:
`policy.AssignedLoadVisibility` ist `DesiredHoursNoteVisibility` plus „veröffentlicht → alle“.
Das heißt: selbst (beide Türen), Leitungen der Fachgruppen der Person (interaktiv), Dekanat, und
nach der Veröffentlichung alle **im Browser**. Über ein Token nur die eigene, in jedem Zustand
(2026-10-25, Oliver: „nur Browser“): die veröffentlichte Zuteilung ist zeilenweise öffentlich,
aber die Summen des ganzen Kollegiums in einem Skriptaufruf sind Personaldaten in Masse.
**Keine Studiengangsleitung.**
`AssignedLoadFilter` ist ein Alias auf `DesiredHoursFilter`.

**Eine eigene Query-Datei** `db/queries/assigned_load.sql` mit dem Personen-Prädikat und eigenem
Guard (`TestEveryAssignedLoadQueryIsFiltered`). `assignment.sql` und
`TestEveryAssignmentQueryIsFiltered` (kein COUNT/SUM) bleiben unberührt. Wer dort eine Summe
ergänzen will, baut ein Leck.

GraphQL: `assignedTeachingHours(semester, person)` (Scope PLANNING:READ) und
`DesiredHoursOverviewRow.assignedLoad`. Der Resolver der Übersicht verbindet die beiden Listen über
die Person. Tests: `internal/store/assignedload_test.go`, `bootstrap/assignedload_test.go`.
Verwandt: [[assignments]], [[desired-hours]].
