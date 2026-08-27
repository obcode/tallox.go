---
name: planning-marks
description: Die Planung öffnet und schließt dort, wo geplant wird — zwei Marken statt einer Phase, und warum absent hier offen heißt
metadata:
  type: project
---

Entschieden und gebaut am 2026-08-28, Migration 18. Die Korrektur kam aus der Fakultät und
betrifft die tragende Struktur, nicht ein Detail.

## Was falsch war

`semester.phase` entschied, wann irgendetwas geschrieben werden durfte — **ein Wert für die ganze
Fakultät**. So arbeitet sie nicht: die Studiengänge legen ihren Bedarf zu unterschiedlichen Zeiten
fest, und jede Fachgruppe fährt ihre eigene Wunschrunde. Der Satz aus der Fakultät:

> Bedarfsplanung, Wünsche und Zuteilung sollen prinzipiell **immer** möglich sein.

## Was an ihre Stelle trat

Zwei Marken, beide dort geschaltet, wo die Zuständigkeit ohnehin liegt — niemand braucht eine neue
Rolle dafür:

| | Korn | Wer schaltet | Was es tut |
| --- | --- | --- | --- |
| `demand_completion` | Semester × **Studiengang** | Studiengangsleitung | **Ansage.** Sperrt nichts. |
| `wish_window` | Semester × **Fachgruppe** | Fachgruppenleitung | **Tür.** Auf und zu, jederzeit. |

Sie sehen sich ähnlich und sind es nicht. Die Ansage sagt „IF für SS29 steht, nach heutigem
Stand"; wer später noch Bedarf einbringt, macht sie **veraltet**, nicht falsch — dafür gibt es das
Zurücknehmen. Die Tür beendet eine Wunschrunde wirklich.

Deshalb auch zwei Formen: Zurücknehmen der Ansage **löscht die Zeile** (nicht gemeldet und
gemeldet-dann-zurückgenommen sind derselbe Zustand), das Wiederöffnen einer Tür **behält sie**
(zwei Entscheidungen, und die zweite ist sehenswert).

## Fehlende Zeile heißt **offen** — und das ist die Umkehrung des Üblichen

Überall sonst im Schema heißt abwesend nein: ein ungescopter Grant erlaubt nichts, eine unbekannte
Phase erlaubt nichts, ein unbekannter Scope-String trifft keinen Zweig. Hier heißt abwesend
**offen**, und das muss so sein.

„Prinzipiell immer möglich" ist die Regel, das Schließen ist der Eingriff. Ein fail-closed-Default
hätte den Deploy dieser Migration zu dem Moment gemacht, in dem **jede Fachgruppe der Fakultät
still keine Wünsche mehr annimmt** — eine Rechteänderung, die niemand gewählt hat, als
Nebenwirkung einer Schemaänderung.

Es scheitert auch in die reparierbare Richtung: eine offene Tür, die zu sein sollte, kostet eine
Eintragung, über die man redet. Eine geschlossene, die offen sein sollte, kostet einer Kollegin den
Glauben, das Werkzeug sei kaputt — still, bis jemand fragt.

Ein Modul **ohne Fachgruppe** hat keine Tür und bleibt offen. Dasselbe Argument.

## Was die Phase noch ist

Eine Anzeige — und **eine** harte Bedeutung: `FINAL` ist abgeschlossen, danach schreibt niemand
mehr irgendetwas. Die Schreibmatrix ist dadurch fast flach und bleibt trotzdem: ein Satz mit einem
Ort zum Nachlesen schlägt drei Bedingungen, die jemand erst finden muss.

**Zwei Zeilen bewegten sich dabei, in entgegengesetzte Richtungen:**

- **Der Bedarf schließt jetzt in `FINAL`**, wo er offen war. Sein Argument — eine späte Instanz
  ist eine Korrektur — tragen jetzt die drei Phasen davor. Bliebe er als einziger offen, hieße
  `FINAL` je nach Bildschirm etwas anderes.
- **Die Zuteilung ist von Anfang an offen**, wo sie einen Tag lang vor ihrer Phase geschlossen war
  (2026-08-27). Das Windhund-Argument war: eine Instanz, die während der Wunschrunde besetzt wird,
  ist genau das Rennen, das die Vertraulichkeitsregel beenden soll. Die Antwort der Fakultät: die
  Wunschrunde gehört der **Fachgruppe**, nicht der Fakultät — ihre Leitung öffnet und schließt sie
  und ist dieselbe Person, die danach besetzt. Ein Werkzeug, das diese beiden Schritte für sie
  ordnet, ordnet die Arbeit von jemandem, der sie ganz überblickt. Und das Rennen ist eines, das
  diese Leitung schlicht nicht laufen muss.

## Beide Marken sind öffentlich

Ungewöhnlich in diesem Code und Absicht: sie sind Tatsachen über den **Prozess**, nicht über
Menschen. Wer eine geschlossene Tür vorfindet, muss sehen können, dass sie zu ist — sonst hat er
ein Werkzeug, das Schreibvorgänge ablehnt, ohne zu sagen warum. Vertraulich ist, worüber die Marken
etwas sagen: die Wünsche und die Zuteilungen, und die haben ihre eigenen Regeln.

`WISH_WINDOW_CLOSED` ist ein eigener Code neben `WISH_PHASE_CLOSED`, weil die Reparatur eine andere
Person ist: eine geschlossene Tür ist ein Schalter der Fachgruppenleitung, ein abgeschlossenes
Semester ist das Ende des Prozesses und niemandes Schalter.

## Eine Falle, die den Compiler stumm ließ

sqlc benennt die generierte Datei nach der Query-Datei — und Go liest `planning_windows.sql.go`
als **Windows-Datei**: das GOOS-Suffix wird geprüft, bevor die `.sql.go`-Endung abgeschnitten
wird. Das Paket kompilierte, die Queries fehlten einfach, und der Fehler zeigte auf `querier.go`.
Deshalb heißt sie `planning_marks.sql`.

Siehe auch [[wishes]], [[assignments]], [[semester-and-phase]].
