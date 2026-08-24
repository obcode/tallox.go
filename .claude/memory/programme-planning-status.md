---
name: programme-planning-status
description: Welche Studiengänge die Fakultät plant — warum es sich nicht aus dem ZPA ableiten lässt, gemessen, und was daraus folgt
metadata:
  type: project
---

Migration 12 (`db/migrations/20260824170000_programme_planning_status.sql`), 2026-08-24.
`programme.planning_status` ∈ `PLANNED` | `NOT_OURS` | `DISCONTINUED`.

## Warum es sich nicht ableiten lässt — die Messung

Die Frage kam als „kannst Du die aus den ZPA-Daten erkennen?". Antwort: **nein**, und das ist
kein Bauchgefühl, sondern am echten Bestand nachgesehen:

| | Studiengang | neueste SPO | aktive Module |
| --- | --- | ---: | ---: |
| geplant | **IB** | **2010** | 3 |
| geplant | **IN** | **2010** | 31 |
| geplant | GN | 2017 | 5 |
| ausgelaufen | IS | 2017 | **13** |
| ausgelaufen | IC | 2019 | 6 |
| ausgelaufen | IT | 2020 | 3 |
| nicht unserer | ZD | 1994 | 1 |
| nicht unserer | GAST | 2019 | 1 |
| nicht unserer | RS | 2020 | 0 |

- **Jede Schwelle auf dem SPO-Alter wirft IB und IN weg** — die beiden geplanten mit der
  ältesten SPO liegen vor allen sieben nicht geplanten außer ZD.
- **Die Modulzahl sagt nichts:** IS ist ausgelaufen und hat mehr aktive Module als GN, GS und WD
  zusammen jeweils.
- **`active` des ZPA ist bei allen bis auf WA wahr.**
- **Das Programm-Objekt in einer SPO trägt `{id, title}`** — kein Enddatum, keine Fakultät, keinen
  Nachfolger. `title` ist das Kürzel noch einmal.

Also eine Entscheidung, und die Spalte ist ihr Protokoll. Es ist auch die Form, die die Frage
behält: es kommen Studiengänge auf **beiden** Seiten der Linie dazu.

## Drei Werte statt eines Booleans

`NOT_OURS` und `DISCONTINUED` tun heute dasselbe und heißen Verschiedenes. Der eine ist der
Studiengang von jemand anderem, der andere unserer mit aufgezeichnetem Bedarf und Studierenden,
die noch fertig werden. Am Tag, an dem jemand „was haben wir in IC angeboten" fragt, brauchen die
beiden verschiedene Antworten — ein Boolean hätte die Unterscheidung für ein Byte weggeworfen.

## Warum `PLANNED` der Default ist

Der restriktive Default an anderer Stelle dieses Schemas („eine neue Person hält keine Rolle",
„eine ungescopte Studiengangsleitung darf nichts") gilt **Berechtigungen** — und ein Recht, das
auf „ja" steht, ist ein Loch. Das hier ist keins: in einem Picker zu stehen gewährt nichts, und
wer planen darf, steht weiter in `person_programme_scope`.

Entschieden hat der Preis der beiden Fehler. Ein Studiengang, der auftaucht und nicht sollte, ist
ein Klick. Ein Studiengang, der still fehlt, ist eine Rückfrage („warum kann ich meinen nicht
planen?"), deren Antwort von dem Bildschirm aus unsichtbar ist, auf dem sie gestellt wird.

## Was daraus folgt, und wo

- **`programmes`** liefert die geplanten; `includeUnplanned: true` alle. Verhaltensänderung für
  Skripte, die den Default lesen.
- **Jeder Bedarfs-Schreibpfad** und `createLocalModule` lehnen mit `PROGRAMME_NOT_PLANNED` ab —
  ausdrücklich **nicht** `NOT_YOUR_PROGRAMME`: die Person leitet womöglich einen Studiengang, und
  die Reparatur ist keine Rolle, sondern eine Entscheidung über den Studiengang.
- **`setPersonProgrammes`** lehnt die Zuordnung ab: ein Grant, der nie benutzbar wäre. Die Prüfung
  steht im Store, weil das die einzige Stelle ist, die die Zeile liest — ein zweites Nachschlagen
  wäre eine zweite Antwort auf dieselbe Frage.
- **Lesen bleibt in jede Richtung offen.** `ProgrammeByCode` filtert nicht, und der Bedarf eines
  ausgelaufenen Studiengangs ist das Protokoll dessen, was die Fakultät getan hat.

`setProgrammePlanningStatus` gehört dem Dekanat (`MayAdministerSemesters`), durch beide Türen —
rückholbar und gewöhnliche Prozessarbeit, dieselbe Linie wie beim Planungssemester. ADMIN steht
nicht darauf: Betreiben ≠ Planen.

Siehe [[semester-and-phase]], [[local-modules]].
