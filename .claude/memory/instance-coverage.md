# Deckung: der Bedarf eines Studiengangs, gehalten von einem anderen

Gebaut am 2026-08-27, Migration 19. Der Fall: ein Modul ist in DC zu Hause, wird von DE, GS und ID
genutzt, und **DE und GS halten es als eine einzige Lehrveranstaltung gemeinsam**. Vorher waren das
zwei Instanzen mit je eigenen Teilen — zwei Zuteilungen und die doppelte Last für eine
Veranstaltung, die einmal stattfindet.

## Die Form

Der Gast behält seine Bedarfsmeldung und hält **keine Teile**; der Gastgeber hält die
Veranstaltung. Der Gast leiht sich dessen Teile zur Anzeige — dieselbe Konstruktion wie
`serves_sibling_tracks` für Züge, eine Ebene höher, aber mit einem **expliziten Verweis** statt
eines abgeleiteten Joins: „DE und GS teilen sich das, ID nicht" ist eine Entscheidung, keine
Ableitung.

## Warum das die Ablehnung aus Migration 8 nicht verletzt

Migration 8, `db/queries/demand.sql` und die Entscheidungsvorlage lehnen
studiengangsübergreifendes Teilen dreimal mit demselben Satz ab: *„a part shared between a DA
instance and a DC one would belong to two programmes, and the import/export figure would lose its
denominator."*

Der Einwand gilt einem **Teil, der zwei Studiengängen gehört**. Der Nenner, den er schützt, ist die
**Zahl der Bedarfsmeldungen**, nicht die Zahl der Teile. Hier bleibt beides heil:

- Die GS-Zeile bleibt stehen, der Bedarf wird weiter zweimal gemeldet. **Der Nenner bleibt nicht
  nur erhalten — der Verweis ist das erste Datum, das den Zähler berechenbar macht**, denn bis
  dahin war „DE und GS haben beide gemeldet" nicht unterscheidbar von „beide halten ihre eigene".
- Jeder `instance_part` gehört weiter genau einem Studiengang. `SUM` über die Zeilen bleibt ohne
  `DISTINCT` richtig.
- Der Beitrag des Gastes ist **konstruktiv** null (keine Zeilen), nicht durch eine Ausschlussklausel.

## Ein Fremdschlüssel, vier Invarianten

```sql
FOREIGN KEY (covered_by_instance_id, semester_id, module_id,
             covered_by_programme_id, covered_by_is_covered)
REFERENCES course_instance (id, semester_id, module_id, programme_id, is_covered)
```

Laut gelesen: *dasselbe Modul, dasselbe Semester, der hier genannte Studiengang, und nicht selbst
gedeckt.* Dazu `is_covered` als `GENERATED ALWAYS ... STORED`.

Zwei Dinge daran sind vor dem Schreiben empirisch geprüft worden und lohnen die Erinnerung:

- **Die Ketten-Regel gilt in beide Richtungen.** Ein Gastgeber, der später selbst Gast werden will,
  scheitert am UPDATE — sein generiertes `is_covered` würde unter einem Schlüssel wandern, der auf
  ihn zeigt. Ein Guard in Go hätte genau diese Richtung verfehlt.
- **`covered_by_programme_id` ist absichtlich denormalisiert.** Ein CHECK kann keine fremde Zeile
  lesen, „anderer Studiengang" ist ohne diese Spalte nicht ausdrückbar. Der FK hält sie ehrlich:
  eine gelogene Angabe wird abgewiesen.

**Die SQLSTATEs unterscheiden sich und das ist tragend:** der Studiengangs-CHECK wirft 23514, der
FK 23503, RESTRICT 23001. Go kann die Fälle daran *nicht* auseinanderhalten und liest deshalb den
Gastgeber vorher, um einen Satz sagen zu können. Der Schlüssel ist, was die Prüfungen wahr macht.

## Der Handschlag, und warum er zweiseitig ist

Die Berechtigung des ganzen Bedarfs hängt an `course_instance.programme_id`. Eine einseitig
gesetzte Spalte wäre eine Leitung, die eine Tatsache in einen fremden Studiengang schreibt —
„deine Veranstaltung bedient jetzt auch meine Studierenden" ist ein Anspruch auf fremde Lehre.

```
RequestCoverage  writable(guest)  — mein Bedarf wird anderswo gedeckt
AcceptCoverage   writable(host)   — ja, ich halte sie auch für euch
ReleaseCoverage  eine von beiden  — es ist vorbei
```

**Keine neue Policy-Funktion, keine neue Zelle in der Matrix** — beide goldenen Matrizen sind
unverändert, und genau das ist die Zusicherung. Zwei Leitungen, die je einen Studiengang schreiben
dürfen, drücken zusammen etwas aus, das keine allein schreiben könnte.

Fragen ändert **nichts**. Deshalb darf gefragt werden, indem man auf eine Instanz zeigt, die man
gar nicht schreiben dürfte.

## Was still schiefgegangen wäre

Die eigentliche Arbeit war nicht der Verweis, sondern die Pfade, die einer teillosen Instanz wieder
Lehre geben:

| Stelle | Ohne Guard |
| --- | --- |
| `adjustGroups` | **die schlimmste** — `planDemand` schickt `groups: n`, der Gast hält 0 Praktika, es fügt `n` ein |
| `AddInstancePart` | Teil zurück, Last doppelt |
| `DuplicateCourseInstance` | Kopie ohne Teile *und* ohne Verweis — die Zeile, die der Mechanismus verhindern soll |
| `sharedKindsOf` | liest die (leeren) Teile eines Gastes mit |
| `DeleteCourseInstance` | Gastgeber löschen → RESTRICT → das opake `ErrInstanceInUse` |

`adjustGroups` **meldet** statt zu überspringen: jemand hat einen Stepper bewegt und hat eine
Antwort verdient.

## Zwei Entscheidungen, die leicht andersherum ausgefallen wären

- **Kopieren trägt die Anfrage, nie die Zusage.** Die Gastgeber-Leitung hat für *jenes* Semester
  zugesagt. Fehlt der Gastgeber im Zielsemester, wird der Gast **ohne Teile** kopiert und das
  gezählt (`CoverageNotPossible`) — Teile aus der Modul-Zerlegung zu bauen hieße, per Knopfdruck
  Lehre zu erfinden.
- **Lösen stellt die Gruppenzahl nicht wieder her.** Die Zerlegung nennt eine Einheit je Art; drei
  Praktikumsgruppen waren eine Planungsentscheidung, die mit den Teilen ging.

## Die Wunschregel, festgenagelt statt angenommen

`db/queries/wish.sql` filtert `own_or_scoped` über zwei Zweige, und sie reichen hier verschieden
weit: `prog.id` ist der Studiengang **der Instanz des Wunsches**, `msg.subject_group_id` hängt am
**Modul**. Gastgeber und Gast sind zwei Instanzen eines Moduls, also:

- Die **Fachgruppenleitung** — die Rolle, die den Teil tatsächlich besetzt — sieht beide Seiten.
- Eine **Studiengangsleitung**, die nicht zugleich Fachgruppenleitung ist, sieht die
  unveröffentlichten Wünsche des anderen Studiengangs **nicht**.

Die Poolung braucht deshalb **keine neue Query** — eine zweite wäre eine zweite Stelle, an der die
Vertraulichkeitsregel vergessen werden kann. Ob der zweite Punkt so bleiben soll, ist eine Frage an
die Fakultät; `TestACoveredCohortsWishesReachWhoeverFillsThePart` ist das Protokoll, nicht die
Zustimmung.

## Offen

**Die Zählweise für die Import/Export-Statistik.** Es gibt bis heute keine Statistik-Query und
keine Statistik-Tabelle. Der Gast hat null Teile, also stimmt jede Summe schon; der Verweis liegt
als Datum vor, also lässt sich jede spätere Regel darauf ausdrücken — auch eine anteilige. Zu
klären: zählt der gedeckte Bedarf voll, gar nicht, oder anteilig?

Verwandt: [[assignments]], [[wishes]], [[local-modules]], [[planning-marks]]

## Vom Sonderfall zum Regelfall (2026-08-28, Migration 20)

Einen Tag nach Migration 19 hat die Fakultät die Voreinstellung umgedreht: **eine Kohorte, die
neben der Meldung eines anderen Studiengangs für dasselbe Modul entsteht, wird sofort mit ihr
zusammen gehalten.** Getrennt zu planen ist das, was man ausdrücklich wählt.

### Der Handschlag stand an der falschen Stelle, nicht zu viel

Er ist **nicht** verschwunden, sondern enger geschnitten — und das ist die eigentliche Erkenntnis:

| Weg | Akt | Regel |
| --- | --- | --- |
| **Planen** (Häkchen, `declareCourseInstance`) | eine **neue** Kohorte, die noch nichts hat | sofort gekoppelt |
| **Nachträglich** (`requestInstanceCoverage`) | eine **bestehende** Kohorte mit Teilen und womöglich Zuteilungen wird aufgelöst | Handschlag bleibt |

Die Begründung für die einseitige Kopplung steht schon in Migration 19, gegen sie selbst
gerichtet: *„the instance they point at is not changed by it — not its parts, not its assignments,
not what it costs."* Beim Koppeln einer **frischen** Kohorte wird ausschließlich in der Zeile des
Gastes geschrieben. Der Handschlag hat dort keine Datenwirkung abgesichert, sondern eine
Wirklichkeit — fremde Studierende im Hörsaal —, und die ist jetzt der Normalfall.

Weil kein Contract-Bruch erlaubt war, blieben `acceptInstanceCoverage` und beide Zeitstempel. Das
ist kein Ballast: der manuelle Weg braucht sie weiterhin. **Der automatische Pfad setzt
`covered_requested_at` und `covered_accepted_at` im selben Moment.**

### Der Index, der zwei Lasten trägt

```sql
CREATE UNIQUE INDEX course_instance_one_covered_cohort_per_programme
    ON course_instance (semester_id, module_id, programme_id)
    WHERE covered_by_instance_id IS NOT NULL;
```

Er sagt: eine gedeckte Kohorte ist die *ganze* Teilnahme eines Studiengangs an dem Modul. Und er
macht die Beförderung sicher — weil damit alle Gäste eines Gastgebers in verschiedenen
Studiengängen liegen, kann das Umhängen nie auf den Studiengang des Nachfolgers treffen. Ohne ihn
bräuchte die Beförderung einen Sonderweg für gleichnamige Mit-Gäste.

`coupleIfHostExists` prüft deshalb **vorher**, ob der Studiengang schon eine gedeckte Kohorte hat,
und legt sonst mit eigenen Teilen an. Sonst käme der Index als roher Unique-Verstoß aus dem
Speichern.

### Beförderung statt Verweigerung

Der Rückzug eines Gastgebers wird nicht mehr abgelehnt: der **längstgedeckte Gast** übernimmt.
Die Teile werden **umgehängt, nicht neu gebaut** — deshalb überlebt die Zuteilung, die an der
Teil-Id hängt. Neu bauen würde sie verlieren und den Rückzug obendrein ablehnen, sobald jemand die
Veranstaltung hält (`assignment … ON DELETE RESTRICT`).

**Das schreibt in einen fremden Studiengang, und das ist nicht mit „bleibt im eigenen Scope" zu
rechtfertigen.** Die Rechtfertigung ist Erhaltung: von vier möglichen Ausgängen verlieren drei eine
Tatsache. Nicht zurückgekauft wird die Gruppenzahl — sie kommt so an, wie der abgebende
Studiengang sie geplant hat.

### Zwei Fallen, die Geld gekostet hätten

**`serves_sibling_tracks` wandert mit.** Das Flag heißt „für die anderen Kohorten *meines*
Studiengangs". Zieht die Zeile in einen anderen Studiengang um, heißt es dort weiter dasselbe —
der abgebende Studiengang verlöre still seine Vorlesung, der aufnehmende bekäme eine geschenkt.
Die Beförderung löst das Teilen deshalb **vor** dem Umhängen auf.

**`SplitInstancePartAcrossTracks` hatte denselben Fehler schon.** Die Schleife legt jedem
Geschwisterzug ohne Teil einen an — ohne Deckungsprüfung. Betroffen ist *nicht* ein Gast aus einem
anderen Studiengang (`SiblingInstanceIDs` joint auf denselben Studiengang), sondern ein **zweiter
Zug desselben Studiengangs**, der anderswo gehalten wird — was der Index ausdrücklich erlaubt.
Mein erster Test dafür war deshalb vakuum und musste umgebaut werden.

**Merksatz: einen Deckungstest immer einmal ohne den Fix laufen lassen.** Zweimal in dieser Sitzung
war ein Test grün, der nichts prüfte.

### Ein Gastgeber mit Wünschen bleibt unrückziehbar

`wish.course_instance_id` ist `ON DELETE RESTRICT`. Die Beförderung rettet ihn nicht: sie liegt in
derselben Transaktion, rollt also mit zurück. Die Ablehnung bleibt das opake `ErrInstanceInUse` —
eine Vorabprüfung wäre das Wunsch-Orakel, das `db/queries/wish.sql` nicht haben will. **Die GUI
darf in der Rückzugs-Bestätigung deshalb nicht versprechen, dass jemand übernimmt.**

### Zurückgestellt

`covered_since` statt der vier Spalten, und `acceptInstanceCoverage` entfernen — beides erst, wenn
ein Breaking Change erlaubt ist. Solange der manuelle Weg den Handschlag behält, ist ohnehin
nichts davon überflüssig.
