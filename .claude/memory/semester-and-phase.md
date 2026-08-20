---
name: semester-and-phase
description: Die erste Domänen-Tabelle — Identität, Nachbarschaftsregel, Compare-and-Set, und wer schalten darf
metadata:
  type: project
---

Gebaut am 2026-08-02 auf `feat/semester-and-phase`. Die erste Tabelle der Fachlichkeit und die
Wirbelsäule für alles Weitere: Bedarf, Wünsche, Zuteilung und Statistik hängen an einem
Semester.

**Warum ausgerechnet das zuerst:** es ist der einzige Fachschritt ohne offene Frage. Modul und
Instanz hängen am Instanz-Primärschlüssel, der laut [[open-questions]] blockierend ungeklärt
ist — und eine freigegebene Migration wird nicht mehr geändert. `internal/policy` hatte den
Kopf schon (`Phase`, Reihenfolge, `SemesterState`, mit dem Wunsch-Filter mitgeschrieben und
getestet); dieser Schritt war der Körper.

## Identität: uuid als PK, `code` als natürlicher Schlüssel

`code` ist `2026-WS` / `2026-SS` — vier Ziffern, Bindestrich, Term; das Jahr ist das, in dem
das Semester **beginnt**. Wintersemester 2026/27 heißt also `2026-WS`.

Ursprünglich war es `2026W`, geändert am 2026-08-20 (Migration 7). Grund ist nicht Ästhetik:
das ZPA schreibt „WS 2026", Plexams schreibt `2026-WS`, und ein selbst erfundenes drittes
Format kostet in jedem Export, jeder URL und jedem Auswertungsskript einer Kollegin eine
Übersetzung. Der Bindestrich statt des ZPA-Leerzeichens, weil ein Leerzeichen in URL,
Dateiname und Shell-Argument gequotet werden muss.

**Nicht repariert wird die ZPA-Schreibweise selbst:** `WS 2026` wird abgelehnt statt
umgestellt. Sie ist nur eindeutig, wenn man weiß, dass die Jahreszahl den *Beginn* meint — und
ein falscher Rateversuch legt ein Semester ein Jahr daneben an, das aussieht, als hätte es
jemand so gewollt. Klein- und Leerzeichen werden getrimmt (`  2026-ws  ` geht), mehr nicht.

- **Sortiert als reiner Text chronologisch**, weil das Jahr führt und SS vor WS kommt — was auch
  die Reihenfolge ist, in der die Semester stattfinden. Darauf verlässt sich inzwischen jedes
  `ORDER BY`, deshalb steht das Format als CHECK-Constraint in der Migration und nicht nur als
  Regex in Go.
- **Trotzdem uuid als Primärschlüssel.** Genau die Lektion aus dem Schwesterprojekt: dort wurde
  ein natürlicher Identifikator zum PK, und jede nachgelagerte Tabelle hat seine Eigenheiten
  geerbt.
- Die deutschen Namen („Sommersemester 2027") macht die GUI, in `$lib/semester.ts`. Der
  Zweijahres-Fall ist die Stelle, an der eine naive Formatierung „Wintersemester 2026" ausgibt.

## Niemand legt ein Semester an

Geändert am 2026-08-20. `createSemester` gibt es **nicht mehr**, und das ist die Entscheidung,
nicht eine Lücke: ein Semester ist ein Name für einen Zeitraum und ist da, so wie der nächste
März da ist. Angelegt wird eine **Entscheidung** darüber — Phase umschalten, Wünsche
veröffentlichen —, und die erste Entscheidung ist es, die die Zeile entstehen lässt
(`EnsureSemester`, ein einziges `INSERT … ON CONFLICT DO UPDATE`).

Die Zeile ist also **das Protokoll der Entscheidungen**, nicht die Existenz. Kein Zeile heißt
deshalb weder „fehlt" noch „nicht gefunden", sondern: es hat noch niemand etwas entschieden —
und das ist exakt `DEMAND_PLANNING` mit vertraulichen Wünschen. `domain.untouched(code)` schreibt
diese Voreinstellungen einmal hin, damit sie mit den DB-Defaults sichtbar dieselben sind.

**Was die Liste zeigt:** das Kalenderfenster (2 Semester zurück, 6 voraus) **plus** alles, wozu
es schon eine Zeile gibt. Beide Hälften sind nötig — nur die Zeilen ergäben auf einer frischen
Installation eine leere Seite ohne Einstieg, nur das Fenster ließe einen Plan für fünf Jahre
voraus lautlos verschwinden.

**Der Kalender entscheidet nur, was angeboten wird, nie eine Phase.** Das ist die Trennlinie,
die die Regel „Phase wird geschaltet, nicht ausgerechnet" heil lässt: eine um zwei Wochen
falsche Semestergrenze verschiebt einen Eintrag in einer Liste, nie eine Frist.

**Adressiert wird über den `code`, nicht über die uuid.** Der Code ist der Name, den das
Semester in der Fakultät hat und der ein Skript überlebt; die uuid bleibt Primärschlüssel für
die Tabellen, die später darauf zeigen, geht aber nicht mehr über die Leitung.

**Grenze ±10 Jahre** (`SEMESTER_OUT_OF_RANGE`). Nicht weil der Kalender endlich wäre, sondern
weil sich hier nichts zurücknehmen lässt: es gibt kein Löschen und kein Un-Veröffentlichen, und
ein vertippter Jahrgang in einem Skript stünde sonst für immer in der Planung der Fakultät.

**Was das für die Studiengangsleitung heißt:** sie kann für jedes Semester in Reichweite planen,
ohne dass jemand es vorher „öffnet". Es gibt kein Recht aufs Anlegen mehr, weil es keinen Akt
des Anlegens mehr gibt — `MayAdministerSemesters` betrifft nur noch die Phase.

## Ein Schritt zur Zeit, in beide Richtungen

`policy.Phase.MayMoveTo` erlaubt nur Nachbarn. **Rückwärts ist Absicht:** einen Plan wieder
aufzumachen ist in einer Fakultät normal, und es hier zu verbieten verhindert es nicht, sondern
verlagert es aus dem Werkzeug heraus. Auf einer nur per VPN erreichbaren Kiste darf „jemand hat
zu früh auf FINAL geklickt" kein Problem sein, dessen Lösung psql auf dem Host ist.

`Phase.Neighbours()` ist aus `MayMoveTo` abgeleitet und wird als `reachablePhases` ausgeliefert —
die GUI rendert ihre Knöpfe daraus und hat **keine eigene Nachbarschaftsregel**.

## Compare-and-Set, nicht Read-then-Write

`AdvanceSemesterPhase` hat `WHERE id = $1 AND phase = $3`. Zwei Leute aus dem Dekanat, die
gleichzeitig klicken, sind genau die Situation, in der ein Phasenwechsel passiert; ohne das
zweite Prädikat schriebe der zweite Aufruf ASSIGNMENT über das WISHES des ersten — eine
übersprungene Phase, die hinterher aussieht, als hätte sie jemand gewählt. Test mit zwei
Goroutinen: genau ein Gewinner, und der Verlierer bekommt `ErrPhaseMovedOn`.

Kein Zeilen-Ergebnis heißt entweder „Semester weg" oder „Phase weggewandert" — unterschieden
über eine zweite Query, aber nur auf dem Fehlerpfad. Die GUI braucht den Unterschied: das eine
ist ein toter Link, das andere eine veraltete Seite („bitte neu laden").

## Wer darf was

| | lesen | Phase schalten | veröffentlichen |
| --- | --- | --- | --- |
| angemeldet | ✓ | | |
| DEANS_OFFICE | ✓ | ✓ | ✓ (nur Browser) |

Anlegen steht nicht mehr in der Tabelle, weil es den Vorgang nicht mehr gibt.

- **Lesen darf jede:r Angemeldete.** Die Phase ist die Antwort auf „darf ich schon Wünsche
  eintragen" — sie zu verstecken ergäbe ein Werkzeug, das Schreibzugriffe ablehnt, ohne zu sagen
  warum.
- **ADMIN darf nicht schalten.** Dieselbe Linie wie bei den Wünschen: Betreiben ≠ Planen. Wer es
  wirklich muss, gibt sich sichtbar und befristet DEANS_OFFICE.
- **PROGRAMME_LEAD auch nicht.** Eine Studiengangsleitung meldet Bedarf *innerhalb* einer Phase
  an; ließe man sie die Wunschphase beenden, entschiede ein Studiengang den Fakultätstakt.
- **`publishWishes` ist als einziges `@interactiveOnly`** — nicht weil es eine stärkere Rolle
  bräuchte (es ist dieselbe), sondern weil es als einziges **nicht rückholbar** ist und der
  Moment ist, in dem die Vertraulichkeitsregel aufhört zu gelten. Idempotent, und es behält den
  **ersten** Zeitstempel.

## `phase` und `wishes_published_at` bleiben unabhängig

Keine Constraint verbindet sie, weder in der Datenbank noch in der Policy. Die Wunschphase kann
enden, ohne zu veröffentlichen (späte Einträge stoppen, während die Planung läuft), und
veröffentlicht werden kann während der Zuteilung. Ein CHECK würde eines von beidem unmöglich
machen, und es ist heute nicht wissbar, welches die Fakultät zuerst braucht. Je ein Test dafür
in `internal/policy` und `internal/store`.

## Fallstricke

- **`r.Semesters` im Resolver kollidiert** mit der generierten `Semesters`-Methode auf
  `queryResolver`. Das Feld heißt deshalb `Planning`. Die Fehlermeldung („func hat kein Feld
  `List`") braucht eine Minute, bis man sie liest.
- **Der Verb-Check im Schema-Test** verlangt READ auf Query- und WRITE auf Mutation-Feldern —
  `advanceSemesterPhase` als READ zu annotieren wäre kein Loch (die Strukturregel überschreibt
  es), aber die Annotation ist das, woran Kolleg:innen ablesen, welchen Scope ihr Token braucht.
- Die GUI-Fixtures brauchten eine **Dekanats-Persona** (`fuenf`), damit der E2E-Lauf überhaupt
  jemanden hat, der schalten darf.

Siehe [[scope-enforcement]] — `PLANNING` ist die erste Scope-Fläche, für die ein verengtes Token
etwas Sinnvolles ausdrückt.
