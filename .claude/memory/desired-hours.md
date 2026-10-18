---
name: desired-hours
description: Wunsch-SWS — eigene Zeile je Person und Semester, Leserecht über die Fachgruppen der Person, eigene Zeile in der Schreibmatrix für die offene Sperrfrage
metadata:
  type: project
---

Migration 31 (`20261018120000_desired_teaching_hours.sql`), 2026-10-18. Aus dem Kickoff: „wie
viele SWS jemand halten will“, und ab einem Punkt im Prozess nicht mehr änderbar.

**Am WishService, nicht als eigener Service.** Gehört zur Aussage „das möchte ich halten, und so
viel davon“, wird auf der Wunschseite eingetragen, und spart die Verdrahtung. Der Store ist
`store.Wishes`, das Interface `domain.DesiredHoursStore` ist in `WishStore` eingebettet.

**Nur selbst, keine Zeile heißt „nicht angegeben“** — anders als `0` (Forschungssemester).
`numeric(4,1)`, 0–40, in halben Stunden; die Domain sagt es in einem Satz, bevor der CHECK greift.

**Leserecht: die Kompetenz-Regel, zur Person gedreht.** `policy.DesiredHoursVisibility`: eigene
immer (beide Türen), interaktiv zusätzlich Fachgruppenleitungen **der Fachgruppen, in denen die
Person Mitglied ist** (`AssignmentScope`), und das Dekanat. Mitgliedschaft wird zur Abfragezeit
gelesen — Umhängen ändert, wer liest. Keine Studiengangsleitung (sie erreicht Instanzen, keine
Personen), kein ADMIN, nie veröffentlicht. `TestEveryDesiredHoursReadIsFiltered` liest die
Query-Datei, kein COUNT/SUM.

**Die Sperre ist offen.** `WriteAreaDesiredHours` hat die Form der Wünsche (offen bis `FINAL`, in
beide Richtungen). Die Antwort von der Klausurtagung (Vorschlag: mit Schließen des Wunschfensters)
ist eine Änderung an dieser Zeile — und am `wish_window`-Check, falls es das Fenster wird; dann
braucht die Person eine Fachgruppe, und Personen ohne Fachgruppe müssen eine Antwort bekommen.

GraphQL: `myDesiredTeachingHours`, `desiredTeachingHours(semester, person)`,
`setDesiredTeachingHours(semester, hours, note)` — `hours: null` nimmt zurück. Codes
`DESIRED_HOURS_OUT_OF_RANGE`, `DESIRED_HOURS_PHASE_CLOSED`, `SEMESTER_REQUIRED`. Scope `WISHES`.

Eine zugeteilte Last je Person gibt es (noch) nicht — die Zuteilungsseite zeigt die Wunsch-SWS
daher nur als Hinweis am Kandidaten. Verwandt: [[wishes]], [[competences]].
