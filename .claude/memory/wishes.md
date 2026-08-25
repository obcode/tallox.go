---
name: wishes
description: Die Wunschtabelle — nur selbst, kein COUNT, die zwei Geltungsbereiche, und das Orakel, das dabei aufging
metadata:
  type: project
---

Migration 15 (`db/migrations/20260825110000_wishes.sql`), 2026-08-25. Die Regel stand seit dem
31.07. in `internal/policy`; das hier ist das Erste, worauf sie zeigt.

Ein Wunsch ist das Interesse **einer** Person an **einem** `instance_part` — nicht an der Instanz:
„eine hält die Vorlesung, eine andere das Praktikum".

## Nur selbst — und was daran hängt

Kein `entered_by`. Ein fremdeingetragener Wunsch ist keine Interessensbekundung, sondern die
Meinung anderer über jemanden; dafür gibt es die Zuteilung.

Die umkehrbare Richtung, für den Fall dass es jemand revidiert: bei „nur selbst" ist die Herkunft
jeder Zeile bekannt (`entered_by = person_id`), das Nachrüsten also ein definierter Backfill.
Andersherum nicht — sobald fremdeingetragene Zeilen existieren, unterscheidet sie nachher nichts
mehr von eigenen.

**Konsequenz, die man kennen muss:** dadurch darf die `UNIQUE`-Verletzung im Klartext gemeldet
werden. Nur die Eigentümerin kann sie über ihre eigene Zeile auslösen, also verrät „du hast dich
hier schon eingetragen" nichts. **Der Satz hängt an der Regel, nicht an der Tabelle** — käme
Fremdeintrag je, muss die Meldung im selben Commit generisch werden.

## Es gibt kein COUNT

`db/queries/wish.sql` enthält keins, und `store.TestEveryWishQueryIsFiltered` liest die Datei und
besteht darauf. Wer eine Zahl will, zählt die Zeilen, die er sehen durfte. Im Schema ebenso: kein
`wishCount`, kein `hasWishes`, kein Weg von `InstancePart` zu seinen Wünschen —
`TestNoFieldCountsWishes` prüft das per Introspection.

## Zwei Geltungsbereiche, orthogonal

`UnpublishedWishScope` gibt beide zurück: Studiengang **der Instanz** (nie der der Person) und
Fachgruppe **des Moduls**. Eine Fachgruppe reicht über Studiengänge, ein Studiengang über
Fachgruppen — man sieht eine Zeile über eine der Achsen oder über keine. Beide werden abgeleitet,
nichts steht auf der Wunschzeile; siehe [[subject-groups]] für die rückwirkende Folge.

`RoleSet.Plans()` ist **gelöscht**, nicht nur umgangen: ein fakultätsweites Prädikat namens
„plant" ist das, wonach die nächste Regel greift.

## Drei Dinge, die beim Bauen auffielen

**`INSTANCE_IN_USE` funktionierte nicht.** Unerreichbarer Code seit er geschrieben wurde, also
konnte es niemand wissen: `ON DELETE RESTRICT` wirft **SQLSTATE 23001**, nicht das 23503, auf das
`isForeignKeyViolation` prüfte — und RESTRICT ist genau das, was die Wunschtabelle benutzt. Der
Fehler war kein verpasster Refusal, sondern ein Leck: die Treibermeldung nennt den Constraint
`wish_instance_part_id_fkey`. (NO ACTION prüft am Statement-Ende → 23503, RESTRICT sofort → 23001.)

**Das dryRun-Orakel.** `planDemand(dryRun: true)` ist folgenlos und meldet `INSTANCE_IN_USE` je
Zug — über ein PAT also „welche Instanzen sind bewünscht" in einem Aufruf, ohne Login-Ereignis.
Deshalb sind **alle** Mutationen in `demand.graphqls` jetzt `@interactiveOnly`, dateiweise und
nicht nur die vier, die den Refusal werfen können (`TestEveryDemandMutationRefusesAToken` liest
die Herkunftsdatei aus der geparsten Feldposition). Interaktiv ist es kein Leck, und das ist eine
Invariante und kein Zufall: `MayWriteDemand` und `UnpublishedWishScope` schneiden beide mit
`PlanningScope`. `TestInstanceInUseTellsNobodySomethingNew` behauptet das und zählt seine
Auslöser mit, damit er nicht leerläuft.

**Kontraktbruch:** ein PAT kann keinen Bedarf mehr schreiben. Als `feat:` committet, Bruchstelle
im Body, Major-Bump ist eine Frage fürs nächste Release — siehe [[release-versioning]].

## Die Phase: offen bis das Semester abgeschlossen ist

**Entschieden mit der Fakultät am 2026-08-25, und es ersetzt die engere Lesart, die die Tabelle
einen Tag lang trug** („nur in der Wunschphase"). Gewünscht ist: solange das Semester nicht
abgeschlossen ist, also solange die Zuteilung nicht erfolgt ist — das sind
`DEMAND_PLANNING`, `WISHES` und `ASSIGNMENT`.

Dasselbe Argument wie beim Bedarf eine Zeile darüber: wer im März sagt, er nähme doch die zweite
Praktikumsgruppe, korrigiert etwas — und eine Korrektur, die das Werkzeug verweigert, passiert
trotzdem, nur per Mail. Danach ist die Liste im Werkzeug die falsche. Was die Zuteilung schützt,
ist die Zuteilung selbst und nicht eine geschlossene Phase.

`FINAL` ist zu und ist damit die **einzige geschlossene Zelle der ganzen Schreibmatrix**: ein
abgeschlossenes Semester ist das Protokoll dessen, was die Fakultät getan hat.

Nebeneffekt vom Bau: „Zelle sagt niemand" und „Zelle fehlt" mussten unterscheidbar werden —
`policy.Decided`, statt sich auf nil-vs-leer zu verlassen.

Siehe auch [[identity-and-auth]], [[semester-and-phase]].
