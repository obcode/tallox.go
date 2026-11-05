---
name: local-teachers
description: Lehrpersonen von Hand anlegen, bis das ZPA sie liefert — gleiche Tabelle, Übernahme über die Adresse in der Nacht, @fullReach, kein Löschen, und die eine Lücke
metadata:
  type: project
---

Gebaut am 2026-11-05 (Migration `20261105230000_local_teachers.sql`), Schritt 2 des Anliegens
„Lehrbeauftragte planbar“. Zuerst nur der Server; die Oberfläche folgt mit eigenem Schritt.

## Was es ist

`createLocalTeacher` / `changeLocalTeacher` legen eine `teacher`-Zeile mit `source = 'LOCAL'` an,
ohne `zpa_teacher_ref`. `Teacher.enteredByHand` zeigt das an. Alles, was auf `teacher` zeigt
(Zuteilung, Kompetenz, „davon Lehrbeauftragte“), funktioniert damit unverändert — dieselbe
Begründung wie bei [[local-modules]].

## Übernahme durch den Import

`AdoptLocalTeachers` läuft **vor** `ProjectTeachers`: gleiche Adresse heißt gleicher Mensch, also
bekommt die lokale Zeile die ZPA-Referenz und `source = 'ZPA'` in einem Statement
(`teacher_source_matches_ref` hält beides zusammen). Das Upsert findet sie dann über die Referenz,
überschreibt Namen und Flags mit denen des ZPA und **behält die id** — Zuteilungen und Kompetenzen
bleiben. Ohne diesen Schritt würde das Upsert an `teacher.mail UNIQUE` scheitern und die ganze
nächtliche Projektion mit ihm. Berichtet als `LOCAL_TEACHER_ADOPTED` (Ereignis dieses Laufs,
kein Zustandszähler wie die anderen Befunde; Stichprobe = ZPA-ids, keine Adressen).

**Bewusst offene Lücke:** steht eine Person schon im ZPA und *ändert* ihre Adresse auf die einer
lokalen Zeile, übernimmt der Import nicht (`NOT EXISTS` auf die Referenz), und das Upsert scheitert
am Unique-Index. Das ist dieselbe Fehlerklasse wie zwei ZPA-Lehrende, die Adressen tauschen, und
das Zusammenführen hieße Zuteilungen umhängen — Sache eines Menschen.

## Entscheidungen

- **Wer:** `policy.MayEnterTeacher` = Fachgruppenleitung (irgendeiner Gruppe, die Person ist ja in
  keiner) oder Dekanat, mit voller Reichweite. Jede Leitung darf jede lokale Zeile korrigieren.
- **`@fullReach` statt `@interactiveOnly`** (der Plan vom 2026-10-29 sagte interactiveOnly): seit
  dem 2026-10-28 steht dort nur noch Token-, Personen-/Rollenverwaltung und Zugriffsprotokoll,
  und eine Lehrperson anzulegen vergibt keinen Zugang. `TEACHER_MAIL_TAKEN` verrät aber, ob es zu
  einer Adresse ein Konto gibt — deshalb nicht für normale Tokens.
- **Eindeutig gegen `teacher` und `person`**: ein Konto ohne Lehrenden-Zeile ist schon als Person
  zuteilbar; eine Zeile daneben wäre derselbe Mensch zweimal und teilte jede Summe. Ausnahme beim
  Ändern: hat die eigene Adresse inzwischen ein Konto, ist das die Verknüpfung, keine Kollision.
  Ein Code für beide Fälle, nie der Constraint im Klartext.
- **Kein Löschen**, auch nicht ohne Zuteilung: „hat eine Zuteilung“ wäre vor der Veröffentlichung
  eine vertrauliche Aussage. Stattdessen `active: false`.
- **`created_by` ohne Fremdschlüssel**: `TestProjectingTeachersAdmitsNobody` verbietet jeden FK von
  `teacher` auf `person`, weil die Verknüpfung die Adresse ist.
- Namen als Nachname/Vorname, ohne Komma (das trennt in `short_name`); `PlainName` dreht sie.
