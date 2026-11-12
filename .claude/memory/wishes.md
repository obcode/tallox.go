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
  **Seit 2026-10-23 nicht mehr beim Zusammenlegen:** fällt ein Zug weg und bleibt das Modul im
  Studiengang angeboten, wandern seine Wünsche auf einen Geschwisterzug (`handOverWishes` in
  `internal/store/demand.go`, beide Wege: `planDemand` und `withdrawCourseInstance`). Doppelter
  Wunsch → stärkere Priorität, beide Notizen, Regel aus Migration 16. Ausgelöst von einer
  Collab-Meldung: nach der Wunschrunde ließ sich der Bedarf nicht mehr zuschneiden, obwohl die
  Phasenmatrix offen war — gesperrt hat der FK, nicht die Phase. Kein Bericht über umgehängte
  Wünsche (das wäre der Count); das Modul ganz streichen und besetzte Züge lehnen weiter ab.
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

## Notiz und Zeitstempel bleiben nach der Veröffentlichung vertraulich (2026-10-25)

Oliver, bis das Plenum anders entscheidet: Die Veröffentlichung zeigt, **wer was will** (Person,
Instanz, Stufe). Die Notiz und `createdAt`/`updatedAt` liest weiter nur, wer den Wunsch vor der
Veröffentlichung lesen durfte (`policy.CanReadWishDetails` = `CanSeeWish` mit unveröffentlichtem
Zustand): die Person selbst, die zuständigen Leitungen im Browser, das Dekanat. Für alle anderen
ist die Notiz `""`, und die Zeitstempel sind `null`. Die Reihenfolge der Eintragungen ist genau die
Windhund-Information. Das ist die vorsichtige Richtung: Lockern geht später, eine veröffentlichte
Notiz holt niemand zurück. Umgesetzt als Guard über die schon gefilterten Zeilen
(`withoutDetailsFor` in `domain/wish.go`), die Query liefert dafür `subject_group_id` mit.
Kontraktbruch: `createdAt`/`updatedAt` sind jetzt nullable.

## „Einen der Züge, egal welcher“: die Obergrenze (2026-10-27)

Collab-Wunsch: Software-Architektur mit Zug A und B, jemand übernimmt einen, egal welchen. Oliver
hat zwischen drei Formen entschieden — Obergrenze je Zeile, Schalter „egal welcher Zug“, nur
Notiz — und die **Obergrenze** genommen: man wünscht sich weiter jeden Zug einzeln mit eigener
Stufe, und dazu „höchstens N davon“. Ein Schalter hätte genau einen Zug gemeint und die Zuteilung
von Zug B den Wunsch auf Zug A mitlesen lassen; die Obergrenze sagt auch „2 von 3“ und
„A gerne, B notfalls, höchstens einer“.

- Tabelle `wish_track_limit`, Schlüssel (Person, Semester, Modul, Studiengang) — **nicht** an der
  Instanz, damit sie beim Zusammenlegen von Zügen (`handOverWishes`) nicht mitwandert.
- **Gelesen nur als Spalte der Wunschzeile** (`Wish.trackLimit`, LEFT JOIN in beiden gefilterten
  Queries). Es gibt keine Query, die die Tabelle allein liest — damit ist sie sichtbar genau dort,
  wo der Wunsch ist. Entscheidung Oliver: nach der Veröffentlichung **wie die Stufe sichtbar, nicht
  wie die Notiz** — sie gehört zu „wer will was“ und sagt dem Kollegium, dass zwei Eintragungen ein
  Angebot für einen Zug sind.
- `setWishTrackLimit(courseInstanceId, maxTracks)`: benannt über irgendeinen der Züge, `null` hebt
  auf, 1–9 (CHECK + `WISH_TRACK_LIMIT_INVALID`). Phase und Wunschrunde wie `setWish`. Ohne eigenen
  Wunsch auf einen der Züge `WISH_TRACK_LIMIT_WITHOUT_WISHES` — eine Grenze auf nichts würde
  ungelesen warten und Monate später einen neuen Wunsch einschränken. Aus demselben Grund räumt
  `WithdrawWish` danach eigene Grenzen ohne Wunsch weg (`PruneOwnWishTrackLimits`).
- Die Ablehnung liest nur eigene Wünsche; `TestABoundIsRefusedWhereAWishWouldBe` prüft, dass sie
  gleich lautet, ob jemand anderes eingetragen ist oder nicht.

## Nachtrag 2026-10-28

„Über ein Token nur Eigenes“ gilt jetzt nur noch für **normale** Tokens. Ein Planungs-Token mit
voller Reichweite liest, was die Person im Browser liest. Siehe [[full-reach-tokens]].

## Reihenfolge der Schreibregel (Stand 2026-10-31)

Phase (`FINAL` zu) → Freigabe des Studiengangs der Instanz (`WISHES_NOT_RELEASED`, abwesend =
zu) → Wunschrunde der Fachgruppe (`WISH_WINDOW_CLOSED`, abwesend = offen) → `taught_elsewhere`.
Gilt für Eintragen, Obergrenze und Zurückziehen gleich. Siehe [[planning-marks]].

## Vormerkungen (`advance_wish`, 2026-11-01)

Anregung aus dem Kollegium nach der Freigabe-Regel: eine private Planung „was ich die nächsten Jahre
halte“ ins Werkzeug legen, damit (a) die Fachgruppenleitung sie sieht und (b) man erfährt, wenn man
nicht gebraucht wird. Ausdrücklich ohne Anspruch. Oliver hat entschieden:

- **Korn Semester × Modul × Studiengang**, kein Zug; optional `max_tracks`. Semester als **Code,
  ohne FK** — eine Vormerkung ist keine Entscheidung über das Semester, also entsteht keine Zeile
  (`TestNotingDownAnAdvanceWishRecordsNoSemester`).
- **Übernahme bei der Freigabe** (`AnnounceDemandComplete`) und bei jeder neuen Instanz in einem schon
  freigegebenen Studiengang (`CreateCourseInstance`, `DuplicateCourseInstance`, `PlanDemand` inkl.
  dryRun vor dem Rollback, `CopyDemand`) — `convertPendingAdvanceWishes`, eine Query mit
  data-modifying CTEs, in derselben Transaktion. Wunsch auf **jeden** Zug, `max_tracks` →
  `wish_track_limit` (nur falls keine da), bestehender Wunsch: stärkere Stufe, Notizen mit „ · “.
  Nicht in `FINAL`, nicht auf ganz fremd gelehrte Züge. **Einmal:** `converted_at` gesetzt, ein danach
  zurückgezogener Wunsch kommt beim nächsten Freigeben nicht wieder.
- **Wunschrunde wird bei der Übernahme nicht gefragt.** Begründung: Eintragen ist ab Freigabe
  verweigert (`ADVANCE_WISH_RELEASED`), also liegt jede Vormerkung vor jeder Runde auf diesem Bedarf.
- **Status abgeleitet, nicht gespeichert:** `CONVERTED` (converted_at) · `NOT_OFFERED` (freigegeben,
  keine Instanz des Moduls — das ist die „Benachrichtigung“, im Werkzeug, keine Mail) · `PENDING`.
- **Sichtbarkeit = Wunschregel**, mit dem genannten Studiengang statt dem der Instanz:
  `policy.WishVisibility`/`CanReadWishDetails` unverändert wiederverwendet, daher keine neue
  Golden-Matrix. `db/queries/advance_wish.sql` trägt denselben Filter, kein COUNT;
  `TestEveryWishQueryIsFiltered` liest beide Dateien. Die Antwort von Freigabe und `planDemand`
  ist gleich, ob Vormerkungen existieren oder nicht (`TestTheReleaseSaysNothingAboutAdvanceWishes`).
- Ändern nur solange `PENDING` (`ADVANCE_WISH_CONVERTED`), Zurückziehen immer; die entstandenen
  Wünsche bleiben.
- **Auch nach der Freigabe (2026-11-12, Olli am 2026-11-10):** ein freigegebener Bedarf kann noch
  wachsen, und die Studiengangsleitung will sehen, wer ein Wahlfach anbieten würde. Verweigert
  (`ADVANCE_WISH_RELEASED`, Code unverändert) nur noch, wenn der freigegebene Bedarf **eine Instanz
  des Moduls** hat — dann ist der Wunsch das Richtige. Sonst angenommen, Status `NOT_OFFERED`,
  Spalte `noted_after_release` (nur beim Insert gesetzt; Ändern macht eine frühe nicht zur späten).
- **Damit gilt die Wunschrunde doch, aber nur für späte:** `ConvertAdvanceWishes` übernimmt eine
  `noted_after_release` nur, solange die Runde der Fachgruppe des Moduls offen ist (abwesend =
  offen). Sonst bliebe die geschlossene Runde umgehbar: vormerken, warten, bis das Modul in den
  Bedarf kommt. `SetWishWindow(open=true)` ruft die Übernahme für alle Studiengänge des Semesters
  (`programme_id` NULL). Frühe Vormerkungen fragen die Runde weiter nicht.
- Eine späte bekommt **keine** `ADVANCE_WISH_RESOLVED`-Mail „nicht angeboten“ — das wusste ihr
  Autor beim Eintragen (`DetectAdvanceWishes`).

## Anteil am Zug (`share_hours`, 2026-11-12)

Collab-Meldung #37: zwei teilen sich eine Vorlesung (je Zug die Hälfte), und ein Wunsch auf den
ganzen Zug behauptet mehr SWS, als man halten will. Olli: im Normalfall wünscht man den ganzen Zug,
**zusätzlich** kann man einen Anteil angeben — vorerst **nur ganze SWS**, keine Prozentangabe,
solange es keine konkrete Anforderung gibt.

- Spalte `wish.share_hours smallint` (nullable, CHECK > 0), Migration `20261112210000_wish_share.sql`,
  additiv. `NULL` = ganzer Zug, also jede bestehende Zeile.
- Obergrenze in der Domain (`domain.WishShare`): 1 bis SWS des Zuges aufgerundet, sonst
  `WISH_SHARE_OUT_OF_RANGE`; auch für einen Zug ohne angegebene SWS. Ein Anteil ≥ den SWS wird als
  `NULL` gespeichert — „4 von 4“ und „alles“ sind ein Wunsch. Geprüft nur beim Schreiben: die SWS
  hängen an den Teilen und ändern sich mit ihnen, ein Leser kappt.
- `setWish(shareHours:)` wie `note`: bei jedem Aufruf geschrieben, weglassen = ganzer Zug.
- **Sichtbar wie die Stufe**, nicht wie die Notiz (Olli, 2026-11-12): gehört zu „wer will was“.
  Keine eigene Policy, keine neue Golden-Matrix; Spalte einer schon gefilterten Zeile.
- Sagt „wie viel“, nie „welcher Teil“ — das bleibt in der Notiz und in der Zuteilung
  („abtrennen“). Die Regel „Wunsch → Instanz, Zuteilung → Teil“ oben gilt unverändert.
- Zusammenlegen (`handOverWishes`): die überlebende Zeile bekommt den größeren Anteil, `NULL`
  (ganzer Zug) gewinnt (`MergeCollidingWishShares`). Vormerkungen übernehmen keinen Anteil.
