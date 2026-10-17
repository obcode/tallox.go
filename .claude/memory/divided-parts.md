---
name: divided-parts
description: Teile aufteilen und an andere Fakultäten abgeben — Stück statt Anteil, divided_from_id wegen der Gruppenzahl, taught_by als Freitext, Zuteilungsregel
metadata:
  type: project
---

Gebaut am 2026-10-17 auf `feat/divided-parts`, Migration 29. Anlass: Collab-Anforderung zur
Zuteilung. Ein Modul mit 4 SWS Vorlesung und 4 SWS Praktikum, bei dem wir je 2 SWS halten und
eine andere Fakultät die anderen 2 + 2. Laut Anforderung „absolute Ausnahme, UI nicht
überfrachten“.

## Aufteilen heißt Teil schneiden, nicht Zuteilung teilen

Die Entscheidung „genau eine Person pro Teil“ aus [[assignments]] (Migration 17) bleibt.
`divideInstancePart(id, teachingHours)` schneidet ein **Stück** derselben Art ab, und der Teil
behält den Rest. Gerechnet wird in SQL mit `numeric` (`AddInstancePartHours`), damit die
Instanzsumme auf den Cent gleich bleibt. Auch ein Stück kann man weiter aufteilen. Das neue Stück
zeigt dann trotzdem auf den **ungeteilten** Teil, es entstehen also keine Ketten.
`rejoinInstancePart` gibt die Stunden zurück. Solange jemand das Stück hält, wird das abgelehnt
(`PART_ASSIGNED`).

**`divided_from_id` ist nötig, nicht nur nett:** Die Gruppenzahl der Bedarfsplanung ist die Zahl
der Praktikumsteile. Ohne die Markierung würde ein geteiltes Praktikum beim nächsten Speichern
als zweite Gruppe zählen. `adjustGroups` überspringt Stücke. Eine Gruppe, die Stücke hat, wird
nicht entfernt, sondern als `PART_DIVIDED` gemeldet.

Den zusammengesetzten FK `(divided_from_id, course_instance_id, kind)` gibt es mit **NO
ACTION**, nicht RESTRICT. Das Zurückziehen einer Instanz (Cascade) und `MoveInstancePartsTo`
bewegen Teil und Stück in einem Statement.

## Andere Fakultät: `instance_part.taught_by`

Das ist Freitext („FK 04“), getrimmt, 1–80 Zeichen. Bewusst kein Fakultätskatalog, denn
`teacher.faculty` ist lückenhaft und hier geht es um einen Teil, den niemand von uns hält. Was
so ein Teil auslöst:
- **Nicht besetzbar.** `FillInstancePart` ist jetzt `INSERT … SELECT … WHERE taught_by IS NULL
  FOR SHARE`. `SetInstancePartTaughtBy` nimmt `FOR UPDATE` (`LockInstancePart`) und prüft
  danach in einem *neuen* Statement, ob eine Zuteilung existiert. Erst damit ist das Rennen in
  beide Richtungen zu. `TestHandingAwayAndFillingAtOnceNeverBothSucceed` schlägt ohne das
  `FOR SHARE` sofort fehl (gegengeprüft). Der FK-Check der Zuteilung nimmt nur `FOR KEY SHARE`
  und reicht deshalb nicht.
- **Nicht in der Last.** `TeachingLoadByProgramme` filtert `taught_by IS NULL` und weist
  `external_hours` daneben aus (`ProgrammeTeachingLoad.externalHours`). Siehe
  [[sws-controlling]].
- **Nicht wünschbar**, aber nur, wenn **alle** Teile der Instanz fremd sind
  (`INSTANCE_TAUGHT_ELSEWHERE`, über `WishWriteContext.taught_elsewhere`). Gewünscht wird die
  Instanz, siehe [[wishes]]. Bei „teils fremd“ ist der Wunsch normal möglich, und die Notiz sagt,
  welche Hälfte.

## Wer darf

`policy.MayDivideInstancePart` = Zuteilungsregel, wie `MayWriteInstanceFormat`. Rückfrage am
2026-10-17: Die Fachgruppenleitung soll das selbst können, denn bei ihr landet die Absprache mit
der anderen Fakultät. Die Instanzsumme ändert sich durchs Aufteilen nicht, deshalb nicht
`MayWriteDemand`. Alle drei Mutationen sind `@interactiveOnly`, weil sie `PART_ASSIGNED`
antworten können (`TestHandingAStaffedPartAwayTellsNobodySomethingNew`).

## Was abgelehnt wird (`PART_DIVIDED`)

Abgelehnt werden das Entfernen eines Teils mit Stücken, ein Art-Wechsel (das fängt der FK ab),
und `shareInstancePartAcrossTracks`, wenn auf einer Seite geteilt oder fremd ist. Ebenso
`splitInstancePartAcrossTracks` bei einer geteilten, zugübergreifenden Vorlesung. Einen
zugübergreifenden Teil **aufteilen** geht dagegen: Das Stück ist dann auch zugübergreifend.
`unshareBeforeHandover` summiert die Stücke zurück, bevor ein Geschwister-Zug seine eigene
Vorlesung bekommt.

Kopie und Duplikat übernehmen Stücke und `taught_by` (`copyParts`, zwei Durchläufe wegen der
neuen IDs). Die Absprache mit der anderen Fakultät wiederholt sich meistens.

## Mehrfach-Einträge in der Modul-Aufteilung (2026-10-17, gleicher Tag)

„Vorlesung 2 + Vorlesung 2 + Praktikum 2 + Praktikum 2“ ist als Modul-Aufteilung jetzt erlaubt.
Bis dahin galt `validComponents` mit „jede Art nur einmal“. Den Wunsch hat der Nutzer bei einem
Modul mit 8 SWS geäußert. Die Bedeutung ist: **jede Instanz hält den Teil schon aufgeteilt.**
`partsFromSplit` in `internal/store/demand.go` macht aus dem ersten Eintrag einer Art den Teil
und aus jedem weiteren ein Stück davon (`divided_from_id`). Das gilt für alle drei Wege, auf
denen Teile aus der Aufteilung entstehen: Anlegen, `createForPlan` und das Ende einer Deckung.

`PracticalKindOf` summiert die Stunden einer Art. Eine zusätzliche Gruppe ist deshalb ein ganzes
Praktikum (4 SWS) und nicht ungeteilt. Den Stepper in der GUI (`plannedHours`) rechnet das schon
so.

Abgewogen wurde: Die Sperre stammte aus dem Betatest, damals ließ sich ein Eintrag nicht
entfernen (inzwischen geht das mit leer oder 0). Ein versehentliches „2 VL + 2 VL“ statt
„2 VL + 2 PR“ fällt jetzt nur noch über die Warnung zur Abweichung von den ZPA-SWS auf. Der
frühere Test `TestASplitNamesEachKindOnce` heißt jetzt `TestASplitMayNameAKindTwice`.

## Wo es liegt

- `db/migrations/20261017100000_divided_and_external_parts.sql`
- `internal/store/demand.go`: `DivideInstancePart`, `RejoinInstancePart`,
  `SetInstancePartTaughtBy`, `copyParts`
- `internal/domain/demandservice.go`: `DividePart`, `RejoinPart`, `SetPartTaughtBy`,
  `dividablePart`
- Tests: `internal/store/divided_parts_test.go`, `bootstrap/divided_parts_test.go`
