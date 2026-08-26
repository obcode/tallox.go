---
name: wishes
description: Die Wunschtabelle — worauf ein Wunsch zeigt (und warum das einen Tag später anders war), nur selbst, kein COUNT, die zwei Geltungsbereiche, das Orakel
metadata:
  type: project
---

Migration 15 (`db/migrations/20260825110000_wishes.sql`), 2026-08-25. Die Regel stand seit dem
31.07. in `internal/policy`; das hier ist das Erste, worauf sie zeigt.

## Worauf ein Wunsch zeigt: auf die **Instanz**

Migration 15 hat es einen Tag lang andersherum gehabt, mit dem Argument „eine hält die Vorlesung,
eine andere das Praktikum" — also sei der Instanz-**Teil** die zuweisbare Einheit und damit auch
die Einheit des Interesses. Die erste Hälfte stimmt weiter, die zweite nicht. Migration 16
(`20260826100000_wish_on_instance.sql`, 2026-08-26) korrigiert das.

Ausgelöst hat es die Confluence-Tabelle, in der die Fakultät bis jetzt geplant hat: eine Zeile je
Modul und Studiengang, eine Spalte je Zug, eine Spalte „Besetzungsmöglichkeiten". Das ist die
Granularität, in der Leute denken — „ich würde Softwareentwicklung II machen", nicht „ich würde
die zweite Praktikumsgruppe von IF2B machen". Auf Teil-Ebene ist dieselbe Tabelle acht Zeilen und
acht Formulare je Modul.

    Wunsch    → die Instanz. Was jemand anbietet zu halten.
    Zuteilung → der Teil.    Wer die Vorlesung hält und wer das Praktikum.

Der Sonderfall, für den die alte Lesart gebaut war, verschwindet nicht — er wandert in die
`note` und dann in die Zuteilung, wo er eine Absprache zwischen mehreren ist statt einer Angabe,
die eine Person allein macht.

**Zwei Folgen im Schema, die man sonst als Fehler liest:**

- `INSTANCE_IN_USE` hängt jetzt am eigenen Fremdschlüssel des Wunsches — ein Schritt statt zwei.
- Einen **Teil** einer bewünschten Instanz zu entfernen ist wieder erlaubt. Teile sind das
  Neu-Zerschneiden einer Instanz (dritte Praktikumsgruppe, geteilte Vorlesung), und niemand hat
  sich auf einen Teil eingetragen. Die Instanz selbst zurückzuziehen bleibt verweigert.

**Migration 16 bricht bewusst die Rollback-Regel.** Das vorige Image liest `wish.instance_part_id`
und kann ohne einen Teil keinen Wunsch schreiben — ein Wunsch auf eine Instanz ist für es nicht
darstellbar, auch nicht mit einer nullable gelassenen Spalte. Vertretbar genau hier: die Tabelle
war einen Tag alt, enthielt Probeeinträge, kein Semester hatte `DEMAND_PLANNING` verlassen. Down
ist geschrieben und getestet, ein Fehldeploy geht also runter-migrieren und *dann* Tag zurück.

`TestMigrationSixteenMovesWishesOntoTheirInstance` fährt den Backfill gegen Zeilen in der alten
Form: `MigrateDownTo` auf 15, Zeilen schreiben, hoch. Die stärkste Priorität überlebt, und die
Notizen der eingesammelten Zeilen wandern in die verbleibende — ein Schemawechsel, der still
löscht, was jemand getippt hat, fällt Monaten später auf, und zwar dieser Person.

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
`wishCount`, kein `hasWishes`, kein Weg von `CourseInstance` zu seinen Wünschen —
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
Der Constraint heißt seit Migration 16 anders und hängt an der Instanz; die Prüfung auf beide
SQLSTATEs bleibt.

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

Dasselbe Argument wie beim Bedarf eine Zeile darüber: wer im März sagt, er nähme doch den zweiten
Zug, korrigiert etwas — und eine Korrektur, die das Werkzeug verweigert, passiert
trotzdem, nur per Mail. Danach ist die Liste im Werkzeug die falsche. Was die Zuteilung schützt,
ist die Zuteilung selbst und nicht eine geschlossene Phase.

`FINAL` ist zu und ist damit die **einzige geschlossene Zelle der ganzen Schreibmatrix**: ein
abgeschlossenes Semester ist das Protokoll dessen, was die Fakultät getan hat.

Nebeneffekt vom Bau: „Zelle sagt niemand" und „Zelle fehlt" mussten unterscheidbar werden —
`policy.Decided`, statt sich auf nil-vs-leer zu verlassen.

Siehe auch [[identity-and-auth]], [[semester-and-phase]].
