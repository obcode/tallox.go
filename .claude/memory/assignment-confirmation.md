---
name: assignment-confirmation
description: Bestätigung der Zuteilung — ein Prüftermin je Semester, „passt so“ je Person, Zustand aus einem Fingerabdruck abgeleitet statt gespeichert
metadata:
  type: project
---

Gebaut am 2026-10-29 (Server-Teil). Die Fakultät wollte, dass alle Lehrenden vor der Übernahme ins
ZPA eine Woche Zeit bekommen, ihre Zuteilungen zu prüfen — **ein Termin für die ganze Planung**,
nicht je Studiengang.

Entschieden mit Olli:

- **Ein Prüftermin je Semester** (`semester.assignment_confirmation_due`), gesetzt vom Dekanat,
  `@fullReach`, bis `FINAL`. Sperrt nichts, wie „Review fertig“ ([[planning-marks]]).
- **Unabhängig von der Veröffentlichung** der Zuteilungen: die eigenen sieht man ohnehin.
- **Je Person**, alle eigenen Zuteilungen des Semesters auf einmal (`confirmMyAssignments`), durch
  jede Tür — eigene Daten.
- E-Mail an die Zugeteilten: später, eigener Schritt (kein SMTP in Go, siehe Zugriffsprotokoll).

**Der Zustand wird nie gespeichert.** Die Zeile hält einen md5 über Teile und SWS der Person
(View `assignment_fingerprint`); `CONFIRMED`/`STALE`/`OPEN` entsteht beim Lesen aus dem Vergleich.
Dadurch wird eine Bestätigung von selbst veraltet, wenn jemand einen Teil umhängt, SWS korrigiert
oder einen Teil entfernt — kein Schreibpfad muss daran denken. Dieselbe View für Schreiben und
Vergleichen, sonst wären es zwei Digests.

**Fremde Bestätigungen** (`assignmentConfirmations`, `@fullReach`) nur für Personen, von denen der
Aufrufer mindestens eine Zuteilung lesen darf — die Personen-IDs kommen aus der gefilterten
Zuteilungsliste, die SQL-Abfrage selbst filtert nicht. Bewusst in Kauf genommen: wer nur einen Teil
der Lehre einer Person sieht, erfährt, ob sie *insgesamt* bestätigt hat (genau das ist der Zweck);
Modul, Teil und SWS nennt die Bestätigung nicht.

Keine Freitext-Begründung („passt nicht, weil …“) — das wäre eine Notiz zu einer Person, siehe
Regel 2; Widerspruch geht wie bisher an die Fachgruppenleitung.
