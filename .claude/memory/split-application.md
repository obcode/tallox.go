---
name: split-application
description: Aufteilung auf bestehende Instanzen übertragen — eigener Schritt mit Vorschau, alle Programme, blind ohne Entfernen, was bleibt und warum
metadata:
  type: project
---

Gebaut am 2026-10-20 auf `feat/apply-module-split`. Anlass war eine Collab-Meldung: Bestehende
Instanzen übernahmen eine geänderte SWS-Aufteilung nicht (drei Module im SoSe 2027). Das war
**kein Defekt einer Stelle, sondern eine fehlende Funktion**. Teile entstehen aus der Aufteilung
beim Anlegen, Kopieren und Duplizieren und gehören danach der Instanz. `setModuleComponents` hat
nie eine Instanz angefasst. Die Bedarfsseite rechnete aber mit der neuen Aufteilung mal Gruppen
(`plannedHours`), das Controlling mit den alten Teilen. So standen zwei plausible Zahlen für
denselben Zug da.

## Entscheidungen (mit dem Nutzer)

- **Eigener Schritt mit Vorschau**, nicht automatisch beim Speichern. Grund: Der Schritt kann von
  Hand geänderte Stunden überschreiben. Weil die alte Aufteilung nach dem Speichern weg ist, kann
  er sie nicht von gezielten Änderungen unterscheiden. Die Vorschau (`dryRun`) zeigt es vorher.
- **Alle Instanzen des Moduls**, über alle Studiengänge, außer in `FINAL`. Dürfen darf es, wer die
  Aufteilung schreiben darf (`MayWriteModuleSplit`). Eine Instanz eines anderen Studiengangs mit
  der alten Aufteilung ist dieselbe falsche Zahl.

## Was der Schritt tut (`store.ApplyModuleSplit`)

Pro Instanz in drei Durchgängen:

1. **Arten, die die Aufteilung noch nennt:** Ist die Gruppe gleich gestückelt, ändern sich nur die
   Stunden, auch an besetzten Teilen (die Person hält, was das Modul jetzt verlangt). Nennt die
   Aufteilung die Art jetzt doppelt und die Gruppe ist ungeteilt, wird sie geteilt wie in
   `partsFromSplit`. Ist sie anders gestückelt, bleibt sie und wird als `PART_DIVIDED` gemeldet.
2. **Arten, die sie nicht mehr nennt:** werden entfernt, und zwar im Savepoint. Besetzt →
   `PART_ASSIGNED`. Gestückelt oder fremd gehalten → `PART_DIVIDED`.
3. **Arten, die fehlen:** werden ergänzt. Bei der praktischen Art entstehen so viele Gruppen, wie
   von der ersetzten praktischen Art entfernt wurden (Praktikum → Übung behält drei Gruppen).
   Blieb eine Gruppe, weil sie besetzt ist, wird nicht verdoppelt. Was ein Geschwisterzug geteilt
   hält, wird nicht ergänzt.

Danach werden die Positionen neu nummeriert (`RenumberInstanceParts`, Vorlesung zuerst), wie in
Migration 27. Gedeckte Kohorten werden übersprungen, sie halten nichts.

## Die Leckstelle: blind heißt „nichts entfernen“

Eine Studiengangsleitung des Heimatstudiengangs erreicht auch die Instanzen anderer Studiengänge,
darf deren unveröffentlichte Zuteilungen aber nicht lesen. Hieße die Antwort dort `PART_ASSIGNED`,
wäre sie die Zuteilung ohne Namen. Deshalb gilt für Instanzen, bei denen
`CanSeeAssignment(actor, semester, {Programm, Fachgruppe, ohne Person})` falsch ist: **Es wird gar
nichts entfernt**, ob besetzt oder nicht, und die Antwort ist immer
`SPLIT_LEFT_TO_PROGRAMME`. Stunden ändern und Ergänzen hängen nicht an Zuteilungen und laufen
weiter. `TestApplyingASplitBlindTellsNothingAboutAssignments` prüft, dass ein besetzter und ein
freier Zug dieselbe Antwort bekommen. Zusätzlich ist die Mutation `@interactiveOnly`, wie jede, die
`PART_ASSIGNED` antworten kann.

## Wo es liegt

- `internal/domain/splitapplication.go` (Service, Typen, `ErrSplitLeftToProgramme`)
- `internal/store/splitapplication.go`, Queries `InstancesForSplit`, `RenumberInstanceParts`
- `graph/catalogue.graphqls`: `applyModuleSplitToInstances(moduleId, dryRun)` →
  `ModuleSplitApplication`
- Tests: `internal/store/splitapplication_test.go`, `bootstrap/splitapplication_test.go`

## Offen

Der Gruppen-Stepper in `adjustGroups` zählt nur Gruppen der *aktuellen* praktischen Art. Nach
einem Artwechsel legt er neue Gruppen neben die alten, solange niemand die Aufteilung überträgt.
Verwandt: [[divided-parts]], [[instance-coverage]], [[assignments]].
