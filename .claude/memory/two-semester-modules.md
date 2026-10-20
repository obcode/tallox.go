---
name: two-semester-modules
description: Module über zwei Semester — module.semester_span als Teiler, Aufteilung je Semester, je eine Instanz pro Semester; was bewusst nicht gebaut ist
metadata:
  type: project
---

Gebaut am 2026-10-20 auf `feat/two-semester-modules`, Migration 34. Die Frage kam aus Collab.
Bestätigt hat die Fakultät: „Wirtschaftsinformatik läuft im WiSe und im SoSe mit je 4 SWS
(V2 + Ü2) pro Zug.“ Im ZPA steht das als **eine** Zeile mit 8 SWS.

## Modell

- **`module.semester_span smallint` (1 oder 2).** Das ist eine Zahl und keine Flagge, weil sie der
  Teiler ist: Katalog-SWS geteilt durch `semester_span` ergibt, was eine Semester-Instanz hält.
  Höchstens 2, weil es nichts Längeres gibt und ein Tippfehler sonst teilen würde. Wie
  `project_based` gehört die Spalte der Fakultät; der Import fasst sie nie an. Ein API-Test prüft
  das über eine erneute Projektion.
- **Geplant wird je Semester eine Instanz, die Aufteilung gilt je Semester** (V2 + Ü2). Deshalb
  braucht es keine neue Instanz-Logik. Die Instanzen in WS und SS sind unabhängig, und
  Kontrolling sowie Studienjahr summieren richtig auf 8.
- Was sich ändert: `proposedComponents` rechnet mit `hours / span` (ganzzahlig), und
  `contactHoursPerSemester` ist die Vergleichszahl für die Warnung „Abweichung“ in der GUI.
  `contactHoursPerWeek` bleibt die ZPA-Zahl.
- Wer darf: dieselbe Regel wie bei der Aufteilung (`MayWriteModuleSplit`). Die Mutation
  `setModuleSemesterSpan` geht durch beide Türen. Falsche Werte → `SEMESTER_SPAN_INVALID`.

## Bewusst nicht gebaut

- **Kein Fachsemester-Automatismus.** Ob die SoSe-Instanz Fachsemester 2 ist, hängt davon ab, in
  welchem Semester der Studiengang beginnt. Sie wird aus der SPO vorbelegt wie jede andere und ist
  das Zahlenfeld der Zeile.
- **Keine Kopplung WS ↔ SS.** Nichts verhindert, nur eine der beiden Hälften zu planen. Darauf
  aufmerksam zu machen wäre ein nächster Schritt, wenn es gewünscht wird.

Verwandt: [[split-application]], [[catalogue-projection]].
