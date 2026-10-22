---
name: desired-hours
description: Wunsch-SWS — Bereich min/max je Person und Semester, eingefroren und veröffentlicht durch das Dekanat, Notiz bleibt vertraulich, Übersicht des Kollegiums
metadata:
  type: project
---

Migration 31 (`20261018120000_desired_teaching_hours.sql`, 2026-10-18), erweitert mit Migration
`20261022120000_desired_hours_range.sql` (2026-10-22). Kickoff: „wie viele SWS jemand halten
will“, ab einem Punkt nicht mehr änderbar. Am 2026-10-22 hat die Dekanin per Mail entschieden,
wie, und zwar **statt der Deputat-Ampel**: alle tragen modulbehaftete Lehre ohne
Abschlussarbeiten als Min/Max ein. Zum Stichtag friert das Dekanat ein, die FKL prüft auf
Plausibilität und klärt Ausreißer bilateral. Danach wird der Stand **für alle** sichtbar, „damit
alle sehen, ob sie wem was wegnehmen“.

**Am WishService, nicht als eigener Service.** Store `store.Wishes`, Interface
`domain.DesiredHoursStore` in `WishStore` eingebettet.

**Bereich als *eine* neue Spalte.** `hours` bleibt der Höchstwert, `min_hours` (nullable) ist der
Mindestwert. Das vorherige Image schreibt weiter nur `hours`, und seine Zeilen lesen sich als
min = max. Gelesen wird `LEAST(COALESCE(min_hours, hours), hours)`. Einen CHECK über beide Spalten
gibt es nicht (das alte Image würde daran scheitern), die Domain lehnt min > max ab
(`DESIRED_HOURS_RANGE`). Ein Ende allein ergibt einen Einzelwert, beide `null` nehmen den Wert
zurück. Keine Zeile heißt „nicht angegeben“ und ist etwas anderes als `0`.

**Drei Zustände, zwei Marken an `semester`:** `desired_hours_frozen_at/_by` und
`desired_hours_published_at/_by`. Beide sind idempotent, der erste Zeitstempel gilt, und es gibt
kein Zurück. Ein CHECK sorgt dafür, dass nichts veröffentlicht ist, ohne eingefroren zu sein.
Beide Mutationen (`freezeDesiredHours`, `publishDesiredHours`) sind `@interactiveOnly` und nur
für `DEANS_OFFICE`. Kein Kalender, kein Phasenbezug. Die Schreibmatrix-Zeile
`WriteAreaDesiredHours` bleibt bis FINAL offen, das Einfrieren prüft `MayStateDesiredHours`
zusätzlich.
- Offen: eigene Zeile, beide Türen.
- Eingefroren: nur `correctDesiredTeachingHours` (Dekanat, interaktiv, nicht in FINAL) für jede
  Person. Es setzt `corrected_by`. Schreibt die Person selbst, wird `corrected_by` wieder NULL.
  Die Abweisung kommt bewusst aus der Domain als `FORBIDDEN` und nicht über `@interactiveOnly`,
  weil dessen `null` hier „zurückgenommen“ hieße.
- Veröffentlicht: der Bereich ist für alle lesbar, durch beide Türen (wie veröffentlichte
  Wünsche), nur nicht für PartnerOnly.

**Die Notiz wird nie veröffentlicht.** Sie hat einen eigenen Filter
(`DesiredHoursNoteVisibility`, die Regel von vorher: selbst, Leitungen der Fachgruppen der
Person, Dekanat, interaktiv). In SQL ist das das zweite Prädikat `note_*` per `CROSS JOIN
LATERAL`. Darf jemand die Notiz nicht lesen, sind Notiz und `correctedBy` der leere String.
`TestEveryDesiredHoursReadIsFiltered` verlangt beide Prädikate.

**Übersicht `desiredHoursOverview(semester)`**: aktive Personen mit LECTURER oder
Fachgruppen-Mitgliedschaft (beides ist ohnehin lesbar), dazu Fachgruppen und ein `LEFT JOIN` auf
die Wunsch-SWS **mit Filter im ON**. „Nicht sichtbar“ und „nicht angegeben“ sind dort beide
`stated: false`, so entsteht kein Hat-angegeben-Flag. `assignedLoad` daneben läuft durch die
eigene Regel, siehe [[assigned-load]].

Codes: `DESIRED_HOURS_OUT_OF_RANGE`, `DESIRED_HOURS_RANGE`, `DESIRED_HOURS_PHASE_CLOSED`,
`DESIRED_HOURS_FROZEN`, `DESIRED_HOURS_NOT_FROZEN`, `PERSON_NOT_FOUND` (Korrektur für ein
unbekanntes Konto), `SEMESTER_REQUIRED`.

Noch offen für die Klausurtagung: ob Studiengangsleitungen vor der Veröffentlichung lesen.
Verwandt: [[wishes]], [[competences]], [[assigned-load]].
