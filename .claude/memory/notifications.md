---
name: notifications
description: Mail-Benachrichtigungen und Rundmails (2026-11-10) — Abgleich statt Hooks, Opt-in, eigene Zeilen, nur person.mail, SMTP in Go mit Outbox, Rundmails nur im Browser
metadata:
  type: project
---

Gebaut am 2026-11-10 auf `feat/notifications`, nach einem Plan, den Oliver freigegeben hat
(Plan und Ankündigungstext liegen im privaten `tallox.dev/docs/`).

## Die Entscheidungen von Oliver

- **SMTP-Client in Go mit Outbox** statt Cron/msmtp. Rundmails sollen sofort rausgehen, und
  Abo-Mails werden pro Person gerendert. Der Cron-Weg bleibt für die Betriebsberichte.
- **Nur Offizielles**: ein Modul gilt als angeboten, wenn es im *freigegebenen* Bedarf steht.
- **Immer Opt-in**, auch für Persönliches. Es gibt keine Default-Abos, nur „Empfohlene Abos“ per
  Klick (`subscribeRecommendedNotifications`).
- **Sammel-Ziele** (`MY_…`), aber nur über eigene Zeilen.
- **Nur an `person.mail`** aktiver Konten, also die Adresse aus dem Shibboleth-Login.
- **Rundmails**: FGL → Fachgruppe, SGL → Studiengang, Dekanat → Kollegium, Leitungen → Leitungen,
  ADMIN → alle Konten. Sie gehen **nur im Browser** (`@interactiveOnly`, auch die Vorschau).

## Abgleich statt Hooks — die Abweichung vom Plan

Im Plan stand „Event in derselben Transaktion wie der offizielle Akt“. Beim Bauen wurde daraus ein
**Abgleich im Dispatcher**: `db/queries/notifications.sql` vergleicht den Zustand (Freigabe, Tür,
Veröffentlichungs-Zeitstempel, Phase, Instanz im freigegebenen Bedarf, Vormerkung) mit dem bereits
Gemeldeten (`notification_announced`, `announced_instance`). Gemeldet wird erst, wenn der Zustand
`NotificationSettle` (10 min) steht. Drei Gründe:

1. Instanzen entstehen auf vielen Wegen (`declareCourseInstance`, `duplicate…`, `planDemand`,
   `copyDemandFromSemester`). Ein Hook pro Weg ist einer, den der nächste Weg vergisst.
2. Entprellen und Zurücknehmen ergeben sich von selbst: eine Freigabe, die vor Ablauf der Frist
   zurückgenommen wird, ist beim Abgleich einfach nicht mehr da; eine Tür, die zu- und wieder
   aufgemacht wird, vergleicht gleich. Ein Hook hätte ein eigenes Storno-Protokoll gebraucht.
3. Der Zustand steht ohnehin in der Datenbank — „abgeleitet, nie gespeichert“ wie bei der
   Bestätigung.

**Baseline**: die Migration trägt alles, was am Deploy-Tag steht, als gemeldet ein. Sonst würde
der erste Tick drei Jahre Planung in Ereignisse verwandeln. Eine neue Erkennungsquery braucht
also immer auch eine Baseline-Zeile in einer Migration.

Eine zurückgenommene und erneut erteilte Freigabe meldet **nicht** nochmal: die Zeile in
`notification_announced` bleibt. Zurücknehmen heißt „nochmal prüfen“, nicht „abgesagt“.

## Stolperstellen beim Bauen

- **Advisory Lock pro Schema.** `pg_try_advisory_xact_lock` gilt für die ganze Datenbank; mit
  festem Schlüssel haben sich die parallelen Store-Tests (ein Schema je Test) gegenseitig die
  Ticks übersprungen — drei Tests rot, ohne Fehlermeldung. Schlüssel jetzt
  `hashtext('tallox-notifications:' || current_schema())`.
- **`TestNoOtherQueryFileReadsWishes`** schlug an, als die Rundmail-Empfänger in
  `announcements.sql` die Tabelle `wish` lasen. Richtig so: die Empfängerliste ist eine Liste von
  Wünschenden, und ihr Count wird dem Absender gezeigt. Jetzt `WishersOfProgramme` in `wish.sql`,
  mit dem Wunschfilter des Absenders. Das Fan-out (`:execrows`) liest `wish` für `MY_WISHES` nur
  mit `w.person_id = s.person_id` — eigene Zeilen per Konstruktion.
- **Rückzug für eigene Wünsche/Zuteilungen gibt es nicht**: `wish.course_instance_id` ist
  `ON DELETE RESTRICT`, eine Instanz mit Wunsch oder Zuteilung lässt sich nicht zurückziehen.
  Deshalb fehlt `MODULE_WITHDRAWN` bei `MY_WISHES`/`MY_ASSIGNMENTS`.
- `go get go-mail` hat `golang.org/x/{sys,text,tools}` mit angehoben.
- Playwright für Screenshots im DevContainer: `require('/tallox/tallox.gui/node_modules/@playwright/test')`.

## Was die Mail sagt

Texte stehen deutsch in `internal/mail` (Ausnahme von der Englisch-Regel, in CLAUDE.md
nachgetragen). `-mail-preview <dir>` rendert jede Mailart aus erfundenen Daten, und
`internal/mail/testdata/*.golden` hält den Wortlaut als Diff fest. Rundmail-Text ist ein kleines
Markdown ohne HTML-Durchreichen (`MarkdownHTML`, Test gegen Script/Img/javascript:).

Der Nachtbericht (`-access-report`) hat einen Block MAIL mit Kennzahlen, über der Zeile
`Berichtenswert:`, die die letzte bleiben muss (das Cron-Skript liest sie dort).

## Offen

- Smoke-Test „wann ging die letzte Mail raus“ in `tallox.dev`.
- Mitbestimmungsfrage (Art. 75 BayPVG) um Rundmail-Archiv ergänzen — [[open-questions]].
