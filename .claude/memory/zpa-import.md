---
name: zpa-import
description: Der Modulimport aus dem ZPA — warum der Cache untypisiert ist, warum der Auslöser im Cron liegt, und was beim ersten echten Lauf schiefging
metadata:
  type: project
---

Gebaut am 2026-08-19 auf `feat/zpa-config`, `feat/zpa-client`, `feat/zpa-cache` und
`feat/zpa-api`. Die erste ausgehende HTTP-Verbindung dieses Systems und das erste Stück
geplanter Arbeit — bewusst in vier Schritten, weil zwei Premieren in einem Branch die Art
sind, wie ein Deploy dienstags um zwei schiefgeht.

Das Datenmodell der Gegenseite steht in `tallox.dev/docs/zpa-datenmodell.md`, die Payloads als
Fixtures in `tallox.dev/zpa/fixtures/`. Hier steht nur, was im Code entschieden wurde.

## Zugang

**`Authorization: Token <wert>`, nicht `Bearer`** — Django REST Framework. Kostet eine halbe
Stunde, wenn man es nicht weiß, weil falsches Schema und gar kein Schema dieselbe Absage
erzeugen. Deshalb steht es im Doc-Kommentar von `ZPAConfig.Token` **und** in einem Test.

Erreichbar nur im eduVPN: von außen blockt Apache `/rest/*` **vor** der Anwendung.
**HTML-403 heißt falsches Netz, JSON-401 heißt falsches Credential** — der Client behält den
Content-Type in der Fehlermeldung, weil das der halbe Diagnoseweg ist.

## Der Cache ist nicht die Fachlichkeit

`zpa_object`, `zpa_sync_run`, `zpa_sync_run_kind`, `zpa_change`. **Nichts zeigt hinein**, und
`TestNoDomainTableReferencesTheZpaCache` liest `pg_constraint` und wird rot, sobald ein FK von
außerhalb des `zpa_`-Präfixes darauf zeigt — derselbe Zug wie `internal/arch` für pgx.

Eine ZPA-ID ist ein **Import-Schlüssel**, keine Identität. Die Quelle liefert das Argument
selbst: `module_code` wäre der naheliegende natürliche Schlüssel und ist keiner — er hängt am
Tripel `(module, spo, basket)`, 264 von 487 Modulen tragen mehr als einen, eines acht, und 21 %
der Zeilen tragen keinen. Genau die `ancode`-Falle aus plexams.

**Kehrseite und Feature:** weil nichts hineinzeigt, darf der Cache jederzeit geleert und neu
aufgebaut werden, ohne eine Planungszeile anzufassen.

## Warum das Payload untypisiert bleibt

Eine typisierte Spiegeltabelle je Endpunkt macht aus jeder Formänderung drüben eine Migration
hier — mit Drei-Release-Tanz, für den Cache einer fremden Datenbank. Schlimmer: sie **verwirft
still**, was sie nicht modelliert, und die erste Frage nach einem nicht modellierten Feld endet
mit „wissen wir nicht und haben es nie gewusst".

Der Spike liefert den Beleg: eine letzte Woche geschriebene Spiegeltabelle hätte eine
`name`-Spalte am Modul gehabt (die Modulobjekte haben gar keinen Namen — der steht nur
verschachtelt in einer `msba`-Zeile) und `credits` als Zahl (alles kommt als String, die
Booleans als „True"). Eine Projektion **aus** der Landetabelle ist dagegen jederzeit
deterministisch neu aufbaubar.

`content_hash` ist `GENERATED ALWAYS AS ... STORED` über `payload::text` — kann nicht driften
und kein Go-Code kann ihn vergessen. jsonb statt text, damit „Hash geändert" Inhalt heißt und
nicht Schlüsselreihenfolge. Braucht `pgcrypto`, mit demselben Idempotenz-Tanz wie `citext`.

## Der teuerste Fehler, gefunden durch Ausführen

`ChangedKeys` verglich die Werte **byteweise**. Ein geändertes Feld eines Moduls meldete acht.

Das ZPA escapet Nicht-ASCII (`nach Ankündigung`), jsonb speichert das dekodierte Zeichen —
eine Seite ist normalisiert, die andere nicht. Jedes Feld mit Umlaut gilt damit als verändert,
und **jede deutsche Modulbeschreibung hat Umlaute**. Der Bericht hätte praktisch immer alle
Felder genannt, also genau so viel Information wie der Hash allein.

Der Vergleich des *ganzen* Objekts war schon richtig (`sameJSON`); nur der je Schlüssel nicht.
Lehre: das hätte kein Lesen gefunden, nur der Lauf gegen die echten Daten.

## Nie beim Login, sondern Host-Cron

Fünf Gründe gegen Login-Sync, jeder allein ausreichend: ein ausgehender Aufruf auf dem einen
Pfad, der nie brechen darf (`msba_info` allein braucht 8,4 s); vierzig Anmeldungen am
Nachmittag, an dem die Wunschphase aufgeht; `started_by` benennt dann, wer morgens zuerst da
war; „wann wurde zuletzt synchronisiert" wird unbeantwortbar; und der Login läuft über **beide**
Türen, ein PAT-Skript stieße also ohne Menschen davor das Prüfungsamt an.

Stattdessen `-zpa-sync` aus der Crontab, versetzt gegen das Backup. Es gab genau ein Vorbild
für Geplantes in dieser Anlage, und das ist eine Zeile daneben. Der Not-Aus hat die richtige
Form: Zeile auskommentieren, und im Prozess existiert kein Timer, der sich irren könnte.

`-zpa-sync` **migriert nicht** — das Schema gehört dem servenden Container.

Der Advisory-Lock (`pg_try_advisory_lock`, nicht die blockierende Form) sitzt **im Service**,
damit Cron und Knopf nicht unterschiedliches Nebenläufigkeitsverhalten bekommen.

**Fallstrick:** ein Advisory-Lock ist datenbankweit, `storetest` gibt aber je Test nur ein
Schema in einer geteilten Datenbank. Die beiden Lock-Tests sind deshalb **nicht** `t.Parallel()`
— sonst scheitert der Verlierer aus einem Grund, der nichts mit seiner Zusicherung zu tun hat.
`migrate.go` beschreibt dieselbe Falle von der anderen Seite.

## Was den Import wirklich kaputt macht

Nicht ein falscher Diff, sondern ein Job, der leise aufgehört hat: eine Crontab, die auf einer
neuen VM nie angelegt wurde, eine Freischaltung ohne den Host, ein abgelaufener Token. In allen
drei Fällen sieht alles gesund aus und geplant wird mit alten Daten.

Dagegen drei Dinge: die GUI zeigt „letzter erfolgreicher Lauf" ganz oben als Satz,
`deploy/smoketest/4-zpa` prüft, dass er jünger als 48 h ist, und die Cron-Mail geht **nur**,
wenn es etwas zu berichten gibt. Der letzte Punkt macht den zweiten nötig: eine Mail, die nicht
kommt, weil nichts passiert ist, sieht aus wie eine, die nicht kommt, weil nichts läuft.

Die `RUNNING`-Zeile wird **vor** dem ersten Abruf geschrieben. Ein abgestürzter Lauf ist damit
sichtbar statt unsichtbar; ein Startschritt neben `reconcileProtectedAdmins` schließt alte.

## Was nicht nach außen geht

Keine rohen Payloads in der API. Ein Feld hier ist ein veröffentlichter Kontrakt, es bräuchte
einen JSON-Skalar, und es gibt noch keinen Tallox-`Module`-Typ — ZPA-IDs zuerst zu
veröffentlichen machte sie zu dem, worauf fremde Skripte keyen, also der `ancode`-Fehler eine
Ebene höher. Dazu trägt `module_info.responsible` Mailadressen von Kolleg:innen.

Fläche **ADMIN**, nicht eine neue Fläche `IMPORT`: jedes Feld dort wäre für ein Token ohnehin
unerreichbar, also ein Versprechen im Enum. `policy.MayReadZPAImport` ist die **Vereinigung von
ADMIN und DEANS_OFFICE** — die erste Regel hier, die diese beiden verbindet. Die Handlung ist
betrieblich, der Bedarf entsteht in der Planung, und das Dekanat für einen Aktualisieren-Knopf
zu ADMIN zu zwingen provoziert genau, was der Zugangsentwurf vermeiden will. Verengen ist
später nicht breaking.

## Kleinkram, der Zeit kostet

- **`log.Fatal` überspringt `defer`.** `gocritic` fängt es (`exitAfterDefer`). Der ZPA-Client
  wird deshalb **vor** `store.Open` gebaut, und `-zpa-sync` gibt einen Exit-Code zurück, statt
  ihn selbst zu setzen.
- **`RETURNING` kann nicht joinen.** `FinishRun` liest deshalb über die Join-Query nach —
  gefunden von dem Test, der die Zuschreibung prüft, nicht beim Lesen.
- **sqlc typisiert `gone_at IS NOT NULL` als `interface{}`**, bis man `::boolean` castet.
- **pre-commit stasht unstaged Änderungen, aber keine untracked Dateien.** Ein Commit, der eine
  neue Datei ohne ihren Aufrufer staged, scheitert deshalb an „unused" — in
  Abhängigkeitsreihenfolge committen.
- **Ein Konfigurationsschlüssel ist vorwärtsgerichtet wie eine Migration** (`UnmarshalExact`).
  Steht als Regel in `CLAUDE.md`; `-check-config` macht sie prüfbar, ohne Datenbank.

Siehe [[semester-and-phase]] und [[scope-enforcement]].
