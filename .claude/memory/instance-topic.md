---
name: instance-topic
description: Sammelmodule wie das WI-Seminar — ein Zug je Seminar, das Thema an der Instanz, Schreibregel Bedarf ∪ Zuteilung, Mitnahme nur bei der Semesterkopie
metadata:
  type: project
---

Gebaut am 2026-10-20 auf `feat/seminar-topics`, Migration `20261020120000_instance_topic.sql`.
Anlass war eine Collab-Anfrage: Im ZPA gibt es für die WI-Seminare nur das Sammelmodul „Seminar
Wirtschaftsinformatik“, tatsächlich laufen je Semester bis zu 14 Seminare mit eigenem Titel.

## Entscheidung: ein Zug je Seminar, kein lokales Modul je Seminar

Vorgeschlagen waren (1) ein lokales Modul je Seminar und (2) eine Instanz mit n Seminar-Teilen.
Gebaut wurde (3): **Das Sammelmodul bekommt einen Zug je Seminar, und jeder Zug ein `topic`.**
Das ist dieselbe Form wie bei den drei FWP-Platzhaltern ([[local-modules]]).

- Gegen (2): **Gewünscht wird die Instanz, nicht der Teil** ([[wishes]]). Mit einer
  Sammel-Instanz könnte sich niemand auf „Design Thinking“ eintragen.
- Gegen (1): Der ZPA-Bezug geht verloren, jedes Seminar braucht eine eigene Zerlegung, und der
  Katalog wächst jedes Semester.

Dafür ist `MaxTracksPerModule` von 8 auf **26** gestiegen (das Alphabet, `trackLetters` in der
GUI vergibt einen Buchstaben). Die DB hatte kein Limit, nur den Muster-CHECK `^[A-Z0-9]{0,3}$`.

## Spalte und CHECK

`course_instance.topic text NULL`, `course_instance_topic_is_a_heading`: getrimmt, 1–120
Zeichen. So hat „kein Thema“ genau eine Schreibweise (NULL). `normaliseTopic` macht aus leer
NULL. Auf dem Draht steht `topic: String`, null statt "" (`topicModel`).

## Schreibregel: Bedarf ∪ Zuteilung

`policy.MayWriteInstanceTopic = MayWriteDemand ∨ MayWriteAssignment`. Rückfrage am 2026-10-20:
Die Studiengangsleitung benennt die Seminare vorab, damit man sich darauf wünschen kann, und die
Fachgruppenleitung korrigiert bei der Besetzung. Die Ablehnungen sind die der Zuteilung
(`NOT_YOUR_SUBJECT`, `ASSIGNMENT_PHASE_CLOSED`), wie bei `setCourseInstanceFormat`. Dazu kommt
`TOPIC_TOO_LONG`. `@interactiveOnly` wie alle Planungsschreibwege.
`TestTheTopicOfACohortFollowsTheDemandOrTheAssignment` hält die Vereinigung fest.

## Mitnahme — anders als beim Format

| Weg | Format ([[instance-format]]) | Thema |
| --- | --- | --- |
| Kopie aus dem Vorsemester | ja | **ja** |
| Duplikat | ja | **nein**, ein zweites Seminar ist ein anderes |
| hinzukommender Zug beim Planen | ja | **nein** |
| Umbenennen des Zugs | bleibt | bleibt |

## Wo es überall mitläuft

Die Wunsch- und Zuteilungszeilen bauen ihre Instanz aus eigenen Spalten (`wish.sql`,
`assignment.sql`), nicht über die Bedarfsabfrage. `ci.topic` steht deshalb in **sieben**
SELECTs. Fehlt es in einem, liest jemand in „Meine Wünsche“ nur „Seminar, Zug C“. Die Tests
`TestAWishCarriesTheTopicOfItsCohort` und `TestAnAssignmentCarriesTheTopicOfItsCohort` decken
das ab.
