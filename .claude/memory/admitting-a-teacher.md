---
name: admitting-a-teacher
description: Wie aus einer ZPA-Lehrperson ein Tallox-Konto wird — warum LECTURER dabei mitkommt, warum die Transaktion im Store liegt, warum es ein Root-Feld ist
metadata:
  type: project
---

Gebaut am 2026-08-22 auf `feat/teacher-accounts`, für den Verwaltungsbildschirm
`/verwaltung/personen`: ZPA-Lehrende auflisten, per Schalter zu Nutzer:innen machen, Rollen in
derselben Zeile vergeben.

## Die Oberfläche

| Feld | Was es ist |
| --- | --- |
| `Query.teacherAccounts` | alle `teacher`-Zeilen (außer zurückgezogenen) mit dem Konto dahinter oder `null` |
| `Mutation.setTeacherAdmitted(teacherId, admitted)` | der Schalter, beide Richtungen |
| `Person.active` | neu, additiv — vorher konnte niemand ein deaktiviertes Konto von einem aktiven unterscheiden |

Beides `@interactiveOnly @scope(area: ADMIN, …)`. Keine Filterargumente: 257 Zeilen hinter
einem Admin-Login, gefiltert wird in der GUI.

## Drei Entscheidungen, die man dem Code nicht ansieht

**Zulassen vergibt LECTURER** (`domain.AdmissionRole`). Überall sonst hier gilt: ein neues
Konto hat *keine* Rolle, ausdrücklich, damit „wer darf was" eine Liste ist, die jemand
geschrieben hat. Die Zulassung aus der Lehrendenliste des Prüfungsamts ist die Ausnahme und
eigentlich keine: in dieser Liste zu stehen *ist* die Aussage, die LECTURER macht. Dazu ist es
die kleinste Rolle — Modulkatalog (den das ZPA ohnehin veröffentlicht) und die eigenen Daten,
nichts über andere. Ein Konto ohne jede Rolle wäre schlimmer als eine Vorgabe, die niemand
gewählt hat: es kann sich anmelden und sieht nichts, was wie ein Defekt aussieht.
`createPerson` bleibt rollenlos — das Dekanatssekretariat ist keine Dozentin.

**Die Transaktion liegt im Store, die Rollenentscheidung in der Domäne.**
`store.AdmitTeacher(ctx, teacherID, role, grantedBy)` macht `EnsurePerson` → `SetPersonActive`
→ `GrantRole` unter einer Transaktion; `domain.PeopleService.SetTeacherAdmitted` nennt die
Rolle beim Aufruf. Der Grund für die Transaktion ist nicht Ordnungsliebe: `EnsurePerson` ist
ein Upsert, und damit ist ein Doppelklick — oder eine zweite Administration auf derselben
Liste — ein zweites No-op statt einer `UNIQUE`-Verletzung, die als `INTERNAL` beim Aufrufer
landet. Ein Bildschirm, der bei jedem Klick speichert, wird doppelt geklickt.
`TestTwoAdministratorsAdmittingTheSameTeacherAtOnce` hält das fest.

**Der Name kommt schlicht ins Konto**, also ohne akademische Titel: `domain.PlainName` dreht die
ZPA-Schreibweise `Nachname, Vorname` um. `person.name` ist die eine Stelle, die später ohne
`sortName` daneben gelesen wird — `me` beantwortet sie direkt —, also muss sie beim Schreiben schon
richtig sein. Migration 21 hat die Konten davor nachgezogen, aber nur die, deren Name Zeichen für
Zeichen `teacher.full_name` war. Begründung im geteilten Memory unter `name-register`.

**Es ist ein Root-Feld und kein `Teacher.person`.** `graph/scope.go` liest `@scope` nur an
Root-Feldern — an einem verschachtelten Feld würde es stillschweigend ignoriert. Und `Teacher`
ist unter `PLANNING` für jede:n mit Konto lesbar (eine Dozentin muss sehen, wer für ein Modul
verantwortlich ist), also hätte ein Konto-Feld daran die Rollen aller hinter dieselbe Lesung
gehängt. Siehe [[scope-enforcement]].

## Fallstricke, die Zeit gekostet hätten

- **`model.Person` wird an drei Stellen gebaut**: `graph/people_mapping.go`,
  `graph/person.resolvers.go` (`me`) und `graph/session.resolvers.go` — die beiden letzten aus
  `principal.Actor`, der kein `Active` kennt. Ohne ausdrückliches `Active: true` antwortet
  `me { active }` für alle `false`, und **kein vorhandener Test merkt es**: das Struct-Literal
  kompiliert. Dafür gibt es jetzt `TestMeSaysWhetherTheAccountIsActive` über beide Türen.
- **`TEACHER_WITHOUT_MAIL` war schon vergeben** — als Wert der Enum `ZpaProjectionFinding`
  (Befund des Imports). Der Fehlercode heißt deshalb `TEACHER_HAS_NO_MAIL`; ein Token soll
  nicht zwei Dinge in derselben API bedeuten.
- **`Teacher.isUser` ignorierte `person.active`** und blieb für deaktivierte Konten `true`,
  obwohl das Feld „darf sich anmelden" verspricht. Als eigener `fix:` vorab behoben
  (`AND p.active` in der Join-Bedingung, nicht im `WHERE` — sonst fällt die Lehrperson aus der
  Liste statt das Konto aus der Verknüpfung).
- **Der Fixture-Satz hatte die gewöhnliche Zeile nicht.** 201 (verantwortlich, zugelassen) und
  202 (ohne Adresse) sind Sonderfälle; 254 von 257 echten sind „lehrt, hat eine Adresse,
  niemand hat sie zugelassen". Das ist jetzt 203, mit einer Adresse **außerhalb** der
  Persona-Besetzung — eine, die zufällig zu einer Persona passt, würde von jedem Test
  zugelassen, der diese Persona seedet, und der Normalfall wäre lautlos nicht mehr abgedeckt.

## Bekannter, nicht von hier stammender Befund

`TestTwoSyncsProduceOneWinner` und `TestTheLockIsReleasedForTheNextRun`
(`internal/store/zpa_test.go`) fallen gelegentlich um, wenn die Suite parallel läuft: der
ZPA-Sync-Lock ist ein `pg_advisory_lock` auf einer Konstanten und gilt **pro Datenbank**,
während `storetest` pro **Schema** isoliert. Zwei parallele Tests, die ihn nehmen, kollidieren
also echt. Reproduzierbar mit
`go test ./internal/store/ -run 'TestTwoSyncsProduceOneWinner|TestTheLockIsReleasedForTheNextRun' -count=10`.
Nicht durch diese Arbeit verursacht und hier bewusst nicht mitrepariert — der naheliegende
Weg wäre, den Schlüssel aus `current_schema()` abzuleiten, und das ändert ein tragendes
Produktionsmittel.
