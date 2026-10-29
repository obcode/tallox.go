---
name: instance-format
description: Block/semesterbegleitend und Englisch je Zug — Override gegen den ZPA-Default, Schreibregel der Zuteilung, Mitnahmepfade
metadata:
  type: project
---

Seit 2026-10-16 (Migration 28) trägt `course_instance` zwei Angaben, angefragt aus der
Zuteilung über Confluence:

- `delivery` — `WEEKLY` (Default) oder `BLOCK`, je Zug für alle Teile. Eine einzelne
  Praktikumsgruppe im Block bleibt Notiz. Entscheidung: am Zug, nicht am Teil.
- `taught_in_english` — **nullable Override**. `NULL` heißt „wie der Modulkatalog sagt“:
  `module.teaching_language`, projiziert aus ZPA `std_lang` (case-folded, `DE`/`EN`/Wörter),
  ohne Angabe Deutsch. Unbekannte Werte → `LANGUAGE_UNMAPPED` im Projektionsbericht.
  `UpdateCourseInstanceFormat` speichert per `NULLIF` einen Wert gleich dem Katalog als `NULL`.
  Ein früher gesetzter Override bleibt aber Override, auch wenn der Katalog später gleichzieht.
  Der Nutzer wollte ausdrücklich den ZPA-Default statt fest Deutsch.

**Schreibregel ist die der Zuteilung**, nicht die des Bedarfs: `policy.MayWriteInstanceFormat`
delegiert an `MayWriteAssignment` (Fachgruppenleitung ∪ Studiengangsleitung ∪ Dekanat, bis
`FINAL`). Grund: gefragt hat die Fachgruppenleitung, die den Zug besetzt. Die Mutation
`setCourseInstanceFormat` liegt im `DemandService` (`SetFormat`), weil der die Instanz liest
und zurückgibt, und wirft die Assignment-Sentinels (`NOT_YOUR_SUBJECT`,
`ASSIGNMENT_PHASE_CLOSED`). `@interactiveOnly` wie alle Planungsschreibwege.
`TestInstanceFormatFollowsTheAssignmentRule` hält die Gleichheit fest.

**Mitnahme** (siehe [[instance-coverage]] für warum das zählt): Duplikat, ein beim Planen
hinzukommender Zug (vom Geschwister, wie die Notiz) und Kopie aus dem Vorsemester tragen den
gespeicherten Override weiter, nicht den effektiven Wert. Neu deklariert → Default.
Test: `TestHowACohortIsTaughtFollowsItsCohorts`.

Gemessen am 2026-10-23 an den Produktionsdaten: `std_lang` ist nur `DE` (485) oder `EN` (24), die Projektion stimmt damit überein (ein Modul ohne Angabe → `NULL`). Nichts zu tun. Früher stand hier: echte `std_lang`-Werte waren lokal nicht messbar (ZPA-Cache leer). Nach dem ersten
Produktiv-Sync den Bericht auf `LANGUAGE_UNMAPPED` prüfen und `languagePhrases` erweitern.

**CiE (Courses in English)** seit 2026-10-28: eigene Spalte `course_instance.cie` (boolean,
Default false), **nicht** dasselbe wie `taught_in_english` — eine ausdrücklich gesetzte Listung,
kein Override gegen einen Katalogwert. Je Zug, eigene Mutation `setCourseInstanceCiE`, gleiche
Schreibregel wie das Format (`DemandService.formatWritable`). Mitnahme wie das Format (Duplikat,
Geschwister beim Planen, Kopie), Test `TestTheCiEListingFollowsItsCohorts`. Gewünscht ist noch die
Bedingung „nur setzbar, wenn das Modulhandbuch Englisch als mögliche Sprache nennt“ — gemessen
am 2026-10-29, siehe unten.

**ZPA `languages` ist die *weitere* mögliche Sprache, nicht die Liste aller** (gemessen am
2026-10-29 an den Produktionsdaten, für die CiE-Bedingung aus Issue #41):

| `languages` | `std_lang` | Module |
| --- | --- | ---: |
| `EN` | `DE` | 295 |
| NULL | `DE` | 190 |
| `DE` | `EN` | 23 |
| NULL | `EN` | 1 |

Nie gleich `std_lang`, nie eine Liste. Also: Englisch möglich ⇔ `std_lang = EN` ∨ `languages`
enthält `EN`. Wer nur `languages = 'EN'` prüft, verliert die 24 englischsprachigen Module.
Unbekannte Werte (Liste, Wörter) gehören wie bei `std_lang` in den Projektionsbericht.

**Sprachregel und CiE-Erweiterung (2026-10-29, Issue #41, zweiter Schritt):**

- `module.other_language` (+ `_source`), projiziert aus ZPA `languages` mit derselben
  `languagePhrases`-Tabelle; `LANGUAGE_UNMAPPED` zählt jetzt beide Felder, ein Modul einmal.
- `domain.Module.EnglishPossible()` = `teaching_language = EN ∨ other_language = EN`, **lokale Module
  immer** (kein Modulhandbuch; ein FWP-Platzhalter „gern auf Englisch“ ist genau der Fall).
  GraphQL `Module.englishPossible`.
- Englisch (`setCourseInstanceFormat`), CiE (`setCourseInstanceCiE`) und CiE am Wunsch nur, wo
  `EnglishPossible`, sonst `ENGLISH_NOT_OFFERED`. **Nur der Wechsel nach Englisch** wird abgelehnt:
  ein Zug, der schon Englisch/CiE ist, behält das beim Speichern des Formats — sonst würde eine alte
  Entscheidung jede andere Änderung blockieren. Zurücknehmen geht immer.
- `policy.MayWriteInstanceCiE` = Format-Regel ∨ (hält einen Teil des Zugs ∧ LECTURER ∧ nicht FINAL,
  über die Wunsch-Zeile der Phasenmatrix). „Hält“ liest `TeachesCourseInstance`: eigener Teil oder
  eine mit `serves_sibling_tracks` geteilte Vorlesung des Parallelzugs. Das Format bleibt bei den
  Leitungen.
- `wish.cie`: Angebot, kein Kriterium der Zuteilung und keine Listung. Sichtbar wie `priority`
  (also auch nach der Veröffentlichung), weil es Teil des Angebots ist, nicht eigene Worte.
  `setWish(cie: Boolean! = false)` — wie die Notiz bei jedem Aufruf geschrieben.
- Fixture: `languages` steht jetzt auf `EN` (Deutsch, Englisch möglich), der häufigste echte Fall;
  `DE` bei `std_lang = DE` kommt in den echten Daten nie vor.
- Tests: `TestProjectionMapsTheOtherLanguage`, `TestWhoTeachesACohort`,
  `TestTheCiEListingFollowsTheFormatOrTheTeaching`, `TestWhereEnglishIsPossible`, und in
  `bootstrap/instance_language_rule_test.go` die drei Regeln durch alle Türen.
