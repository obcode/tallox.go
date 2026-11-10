---
name: independent-work
description: Abschlussarbeiten, Praxissemester, Bachelorseminar (ZPA „selbständiges Arbeiten“) werden nicht geplant — Bedarf, Kompetenzen, Vormerkungen lehnen sie ab
metadata:
  type: project
---

Seit 2026-11-10 (Anliegen #54): ein Modul mit `course_type = 'SELF_STUDY'` ist
`independentWork` und wird **nicht geplant**. Das sind am echten Bestand genau zwölf Module:
Bachelor- und Masterarbeiten, die praktischen Studiensemester, das Bachelorseminar — alle Pflicht,
keines vor einer Kohorte gehalten. Vorher ließen sie sich als Kompetenz wählen und zählten auf die
Mindestzahl an Pflichtfächern.

- Die Lehrform entscheidet allein (`domain.CourseType.IndependentWork`), nicht der Name.
- `Plannable()` ist dafür immer false, auch mit Aufteilung. Geprüft wird in `statedComponents`,
  durch das jeder Pfad läuft, der Teile baut: `MODULE_NOT_PLANNED`.
- Bestehende Kohorten bleiben, die Semesterkopie lässt sie liegen und zählt sie (`notPlanned`).
- Kompetenzen (eigene und für Lehrende) und Vormerkungen: abgelehnt mit demselben Code; die
  Pflichtfach-Zählungen und „kann niemand“ lassen sie aus (SQL `course_type <> 'SELF_STUDY'`).
- Bestehende Kompetenz-Einträge bleiben lesbar und zurückziehbar.

- Auch `ModuleRef.independentWork` (Module einer Fachgruppe, Vormerkungen) trägt die Markierung,
  weil die Kompetenz-Seiten ihre Auswahl aus `mySubjectGroups.modules` bauen; ohne das Feld konnte
  die GUI dort nichts ausblenden.

Die GUI blendet sie in den Auswahllisten aus.
Siehe [[catalogue-projection]], [[competences]], [[desired-hours]].
