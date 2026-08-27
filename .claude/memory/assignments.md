---
name: assignments
description: Die Zuteilung — person oder teacher, zwei Achsen zum Schreiben, Compare-and-Set, und drei Funde beim Bauen
metadata:
  type: project
---

Gebaut am 2026-08-27 auf `feat/assignment`, Migration 17. Der dritte Prozessschritt: der Bedarf
sagt, was angeboten werden muss, die Wünsche sagen, wer möchte, und das hier sagt, wer hält.

Entschieden **bevor** die erste Migrationszeile geschrieben war — der Wunsch wurde einen Tag nach
dem Bau umgebaut (Migration 16), und das sollte sich nicht wiederholen.

## Worauf eine Zuteilung zeigt

**Auf den Instanz-Teil**, eine Ebene unter dem Wunsch. Migration 16 hatte die Arbeitsteilung schon
aufgeschrieben, einen Tag bevor es etwas gab, worauf sie zeigen konnte:

    wish       → die Instanz.  Was jemand anbietet zu unterrichten.
    assignment → der Teil.     Wer die Vorlesung hält und wer das Praktikum.

`UNIQUE (instance_part_id)` — genau eine Person je Teil. Wer teilen will, teilt den *Teil*;
Praktikumsgruppen sind ohnehin eigene Teile, und für die Vorlesung gibt es
`splitInstancePartAcrossTracks`. Anteile an einer Stundenzahl müssten sich bei jedem Schreiben auf
die SWS des Teils summieren, und eine Summe, die still aufhört zu stimmen, ist genau die
plausibel aussehende falsche Zahl aus [[instance-identity]].

## person **oder** teacher, genau eins

Zwei nullable Fremdschlüssel, `CHECK (num_nonnulls(person_id, teacher_id) = 1)`.

Wer hier lehrt, hat nicht notwendig ein Konto und muss es nicht bekommen: sechs der 257
ZPA-Lehrenden tragen Adressen, die `sso.hm.edu` nie behaupten wird, drei gar keine. Die
Alternative — für jede:n eine `person`-Zeile — ist genau das, was [[teachers-are-not-users]]
ablehnt: `person` ist die Zugangsliste.

**Kanonisiert wird auf dem Schreibweg:** wird ein `teacher` zugeteilt, der über die Mailadresse ein
Konto hat, wird das **Konto** gespeichert. Sonst säße dieselbe Kollegin je nach Auswahlliste unter
zwei Identitäten in der Tabelle, und „meine Zuteilungen" fände die Hälfte.

Folge, die man kennen muss: eine Zeile ohne Konto ist **niemandes „eigene"**. Sie wird über
Zuständigkeit oder nach der Veröffentlichung gelesen, wie jede andere.

## Zwei Achsen zum Schreiben, und was das nach sich zieht

`MayWriteAssignment` = Phase ∧ (Fachgruppe des Moduls ∨ Studiengang der Instanz). Eine
**Vereinigung**, kein Schnitt.

Das ersetzt den Satz „a programme lead declares instances and does not fill them", der in
`assignment_matrix.golden` stand. Entschieden hat es das Modul **ohne Fachgruppe**: mit den
Fachgruppen allein wäre es das Dekanat oder niemand, und davon hat der Katalog viele, solange er
sortiert wird.

**Der Preis: zwei Rollen dürfen dieselbe Zeile schreiben.** Deshalb ist der Schreibweg
Compare-and-Set und nicht Read-then-Write:

- `setAssignment(..., replacing: ID)` nennt die Zuteilung, die man gesehen hat.
- **Fehlt sie, heißt das „nur wenn frei"** — `INSERT … ON CONFLICT DO NOTHING`. Ein Aufruf, der
  nichts sagt, kann nichts überschreiben. Das ist die sichere Richtung als Default.
- Ist sie weggewandert: `ASSIGNMENT_MOVED_ON`, dasselbe Muster wie `AdvanceSemesterPhase`.

## Die Phasen: zu **vor** der Zuteilungsphase

Die einzige Zeile der Schreibmatrix, die **früh** schließt statt spät. `DEMAND_PLANNING` und
`WISHES` sind zu, weil eine Instanz, die während der Wunschphase schon besetzt ist, genau das
Windhundverfahren ist, das die Vertraulichkeitsregel beenden soll — und diesmal hätte das Werkzeug
es inszeniert.

`FINAL` ist **offen**, anders als beim Wunsch. Eine Krankheitsvertretung im November ändert die
Lehre; ein Wunsch danach änderte nur das Protokoll.

## Vertraulich bis zur eigenen Veröffentlichung

`semester.assignments_published_at`, unabhängig von `wishes_published_at` **in beide Richtungen**.
Der Regelsatz ist wörtlich der Wunsch-Satz mit einem ausgetauschten Substantiv, absichtlich —
zwei gleich klingende Regeln kann man sich gleichzeitig merken.

Der Zweck ist ein anderer und klingt schwächer, ist es aber nicht: ein halbfertiger Plan, dem alle
zusehen, erzeugt Fragen zu Entscheidungen, die niemand getroffen hat — und die Fachgruppenleitung
bereitet ihn dann in einer Tabelle woanders vor und trägt das Ergebnis ein. Das ist der Zustand,
den Tallox ablösen soll, durch die Vordertür.

Gerendert in `assignment_visibility_matrix.golden` (Rolle × Tür × Zuständigkeit × veröffentlicht).
Die Phase ist **keine** Dimension davon — das behauptet
`TestAssignmentVisibilityDoesNotDependOnThePhase`, damit die weggelassene Spalte keine Lüge wird.

## Drei Dinge, die beim Bauen auffielen

**Schreiben muss browser-only sein.** `PART_ALREADY_ASSIGNED` sagt, dass ein Teil besetzt ist —
und über ein Token schrumpft die Leseregel auf „nur eigene". Ein Skript hätte Teil für Teil
durchprobiert und die Besetzung eines ganzen Semesters aus den Ablehnungen gelernt, ohne
Login-Ereignis und ohne eine Zeile zu lesen. Dieselbe Form wie das `planDemand(dryRun:)`-Orakel
aus [[go/wishes]], nur diesmal geschlossen, bevor es sie gab. Lesen bleibt durch beide Türen offen,
verengt durch die Policy statt durch die Tür.

**Löschen musste erst lesen.** `Clear` prüfte zuerst das Schreibrecht — und `ErrNotYourSubject`
hätte jedem, der eine id hält, bestätigt, dass es diese vertrauliche Zuteilung gibt. Jetzt liest
der Service zuerst durch den Sichtbarkeitsfilter: wer sie nicht hätte sehen können, hört „gibt es
nicht". Die beiden `…WriteContext`-Queries tragen deshalb bewusst **keinen** Filter — was sie
ungefährlich macht, ist nicht eine `WHERE`-Klausel, sondern die Reihenfolge.

**Der Löschpfad auf `instance_part` ist wieder scharf.** Migration 16 hatte den eingehenden
RESTRICT *entfernt* — Umschneiden darf nicht an bloßem Interesse scheitern. Migration 17 setzt ihn
zurück, weil eine Zuteilung kein Interesse ist. Neuer Code `PART_ASSIGNED`; kein Kontraktbruch,
weil seit Migration 16 nichts auf einen Teil zeigte und der Zweig unerreichbar war.
Das Zurückziehen einer **Instanz** behält die opake Meldung: dort können zwei verschiedene Dinge
hängen, und welches davon wäre mehr, als der Aufrufer wissen darf.

## Wo es liegt

| Was | Wo |
| --- | --- |
| Migration | `db/migrations/20260827100000_assignments.sql` |
| Regel (Guard + Filter) | `internal/policy/assignment.go` |
| Schreibmatrix | `internal/policy/write.go`, `testdata/write_matrix.golden` |
| Sichtbarkeits-Folie | `internal/policy/testdata/assignment_visibility_matrix.golden` |
| Store | `internal/store/assignments.go`, `db/queries/assignment.sql` |
| Service | `internal/domain/assignmentservice.go` |
| API (4 Wurzelfelder, Area `PLANNING`, Schreiben `@interactiveOnly`) | `graph/assignment.graphqls` |

Siehe auch [[wishes]], [[subject-groups]], [[semester-and-phase]].
