---
name: access-log
description: Das Zugriffslog — was es festhält, was es bewusst nicht kann, die Reihenfolge der Middlewares und warum ADMIN hier nicht das Dekanat einschließt
metadata:
  type: project
---

Gebaut am 2026-08-24 auf `feat/access-log`, in drei Schritten: Tabelle und Store, dann Schema,
Policy und die beiden Schreibpfade, dann der nächtliche Bericht.

Das Audit-Log war seit Migration 2 an einem Dutzend Stellen **versprochen** —
`person_role.granted_by` zeigt darauf, `@interactiveOnly` nennt es unter dem, was ein Token nie
erreicht, `policy.Narrow` begründet `NarrowedFrom` teilweise damit — und existierte nicht. Der
Auslöser war die Türkonstruktion selbst: über `sso.hm.edu` klopft die ganze Hochschule an,
hereingelassen wird nur, wer eine `person`-Zeile hat, und **jede Abweisung war eine Info-Zeile
im Container-Log**, die niemand liest.

Entschieden wurde: alle Operationen inklusive Lesezugriffe, plus abgewiesene Anmeldungen ·
90 Tage Aufbewahrung · Mail aus dem Host-Cron über msmtp · Mailinhalt sind Kennzahlen und
Ereignisse, nicht die Rohliste.

## Die eine Regel, an der alles hängt

**ADMIN steht bewusst nicht auf der Ausnahmeliste der Wunsch-Sichtbarkeit** (siehe
[[visibility-policy]] im geteilten Memory). Diese Entscheidung überlebt nur, weil das Log
**Operationsnamen und Wurzelfelder speichert und nie Argumente, Variablen oder Antworten**.
„Prof. Eins hat `myWishes` aufgerufen" ist kein Wunsch; `wish(id: …)` mit Argument wäre eine
Kopie der vertraulichen Daten ohne die Policy daneben.

Dieselbe Allow-List-Logik wie `internal/obs/scrub.go`, aus demselben Grund: was niemand
vorgesehen hat, fehlt, statt ungeprüft dabei zu sein. **Eine Spalte nachzurüsten, die das
ändert, ist keine kleine Änderung** — sie braucht dieses Argument beantwortet, nicht nur eine
Migration.

## Die Reihenfolge der Middlewares ist tragend

`graph.RecordAccess` wird in `bootstrap.graphqlHandler` **vor** `graph.EnforceScopes`
registriert und liegt damit außen. Nur so landet eine Scope-Absage überhaupt im Log:
`EnforceScopes` antwortet mit `graphql.OneShot`, statt durchzurufen, und das sieht nur ein
Middleware, das darum herumliegt. Andersherum enthielte das Log jede erlaubte Operation und
keine einzige abgewiesene — genau falsch herum für ein Audit-Log, und **kein anderer Test
würde rot**. Deshalb `TestARefusedOperationIsLoggedNotSwallowed`.

## Zwei Schreibpfade, ein Log

1. **Operationen** — `graph.RecordAccess`, ein `AroundOperations`. Übersprungen wird eine
   Operation, deren Wurzelfelder **alle** „still" sind (`isQuietField`): Introspection und
   `buildInfo`. Beide werden von etwas gepollt, das kein Mensch ist — ein Editor beim Tippen,
   die Überwachung im Sekundentakt —, und beide sagen über niemanden etwas. Der Preis wäre
   nicht Plattenplatz, sondern Aufmerksamkeit: tausende nichtssagende Zeilen begraben die
   Handvoll, die etwas sagt. **Die Ausnahme entscheidet nur, OB eine Zeile geschrieben wird,
   nie was darin steht** — `{ buildInfo, me }` ist eine Zeile, und sie nennt beide Felder. Und
   sie greift nur auf dem Operationspfad: eine abgewiesene Anmeldung wird protokolliert, egal
   wonach sie gefragt hat.
2. **Abgewiesene Anmeldungen** — im `err != nil`-Zweig von `auth.Middleware`. `internal/auth`
   darf nicht wissen, was ein Log-Eintrag ist, bekommt also eine Ein-Methoden-Naht
   (`auth.AccessRecorder`) wie `UserLookup` und `TokenLookup`; `bootstrap` adaptiert sie.

Beide sind **best effort**: ein fehlgeschlagener Insert ist eine Log-Zeile auf Error-Level und
nie ein Fehler für die aufrufende Seite. Eine volle Platte darf keine Betriebsstörung der
ganzen Installation werden.

**Die Identität kommt aus dem Request, nicht aus der Fehlermeldung.** Die Adresse steht heute
in `fmt.Errorf("%w: %s", ErrUnknownUser, mail)` — ein Audit-Log, das Sätze parst, wird leer,
sobald jemand einen Satz umformuliert. Dafür hat `Authenticator` jetzt `Asserted(r)` und
`Kind()`.

Der Wurzelfeld-Walk ist **derselbe** wie beim Scope-Check (`graph.forEachRootField`). Das
Schwesterprojekt folgt in seinem Walk keinen Fragmenten; ein zweiter Walk hier wäre derselbe
Fehler mit einem Audit-Log daran.

## Warum hier nicht die Vereinigung mit dem Dekanat steht

`policy.MayReadZPAImport` ist ADMIN ∪ DEANS_OFFICE, mit dem Argument, dass der *Bedarf*
innerhalb der Planung entsteht. Davon überträgt sich hier nichts: das Log sagt, wann
Kolleg:innen gearbeitet haben und von wo. `policy.MayReadAccessLog` ist deshalb ADMIN **und**
interaktiv — dieselbe Form wie `MayAdministerPeople`. Verengen ist später nicht breaking,
Erweitern unter Druck schon.

## Kleinkram, der Zeit gekostet hat

- **`token_id` ist kein Fremdschlüssel.** Die Spalte hält, was *präsentiert* wurde, und das
  Interessanteste, was jemand präsentiert, ist eine Token-ID, die es nicht gibt. Ein FK würde
  genau diese Zeilen ablehnen — in einem Insert, der Fehler schluckt.
- **Der Eintrag trägt `personId` und `personName`, keinen `Person`.** Ein `Person` trägt
  `roles` und `active` von *heute*; neben einem Protokoll von damals ist das genau die
  Verwechslung, gegen die die Tabelle gebaut ist.
- **`oc.OperationName` ist nicht der Name im Dokument.** gqlgen füllt es aus dem
  `operationName`-Feld des JSON-Requests, das ein Client nur bei mehreren Operationen schickt —
  ausgerechnet die sauber benannten Queries wären namenlos protokolliert worden.
- **Ein abgewiesenes Token hat keine Mailadresse.** Die erste Fassung der Zusammenfassung
  verlangte eine und ließ damit jede Token-Abweisung still unter den Tisch fallen. Gefunden
  hat das erst der Lauf gegen echte Daten, nicht ein Test.
- **`errcheck` schlägt bei `fmt.Fprintf` zu**, anders als bei `fmt.Printf`. Der Bericht
  schreibt deshalb über einen kleinen `reportWriter`, der den ersten Fehler behält.

## Was noch offen ist

**Mitbestimmung.** Ein 90 Tage aufbewahrtes, nächtlich versandtes Protokoll über die
Arbeitszeiten von Beschäftigten ist mitbestimmungsrelevant (Art. 75 BayPVG). Deshalb ist die
Aufbewahrungsdauer eine Konstante mit Begründung in `internal/domain` und **kein
Konfigurationsschlüssel**: was die Installation aufbewahrt, muss in einem Satz sagbar sein.
Vor dem Produktivgang aktiv klären — siehe `open-questions` im geteilten Memory.
