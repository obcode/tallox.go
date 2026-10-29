# Memory — tallox.go (Backend)

Wissensbasis des Backends. Übergreifendes und Betriebswissen liegt im privaten `tallox.dev`
(im Container unter `../tallox.dev/.claude/memory/`).

**Dieses Repo ist öffentlich.** Keine Hostnamen, keine Zugangsdaten, keine Namen aus dem
Kollegium — das gehört nach `tallox.dev`.

- [Security-Audit 2026-10](security-audit-2026-10.md) — was gefunden und gebaut wurde (Presenter, Grenzen, ADMIN unbefristet, Partner ohne Roster), und welche Hinweise bewusst offen bleiben
- [Layering-Regel](layering-rule.md) — warum nur `internal/store` an die Datenbank darf und wie das erzwungen wird
- [Identität und Autorisierung](identity-and-auth.md) — was der erste Fachschritt gebaut hat, welche Entscheidungen darin stecken, welche Fallstricke Zeit gekostet haben
- [API-Vertrag `schema.graphql`](api-contract.md) — seit 2026-10-29: gerendert aus dem ausgeführten Schema, mit Direktiven; Bruch-Prüfung mit `API-Break:`-Trailer; Release-Anhang; GUI-Umstellung offen
- [Planungs-Tokens mit voller Reichweite](full-reach-tokens.md) — seit 2026-10-28: Lesen und Schreiben nur zusammen, was browser-only bleibt (Tokens, Personen/Rollen, Log), 90 Tage, Nachtbericht
- [Scope-Durchsetzung](scope-enforcement.md) — `@scope` per Operation, warum leer „unbeschränkt" heißt, und warum PUBLIC nicht wegnehmbar ist
- [ZPA-Import](zpa-import.md) — Cache, Sync, Cron und der Fehler, den erst der echte Lauf zeigte
- [Integrationstests in der CI](integration-tests-in-ci.md) — zwei Test-Binaries, zwei Container, ein Ryuk: woran man den Ärger erkennt
- [Welche Studiengänge geplant werden](programme-planning-status.md) — warum das ZPA es nicht sagt (gemessen), und was aus der Spalte folgt
- [Abschluss am Studiengang](programme-degree.md) — Bachelor/Master von Hand gepflegt, NULL statt geraten, Feld-Resolver gegen das stille null
- [Studiengangsleitungen lesbar](programme-leads.md) — `Programme.leads` für alle Konten, ohne Partnerkonten und abgelaufene Grants
- [Lokale Module und FWP-Platzhalter](local-modules.md) — warum eine `module`-Zeile statt einer eigenen Tabelle, und warum drei FWPs drei Züge sind
- [Zulassung von Lehrenden](admitting-a-teacher.md) — wie aus einer ZPA-Lehrperson ein Konto wird, und die drei Entscheidungen darin
- [Zugriffslog](access-log.md) — was es festhält, was es bewusst nicht kann, und warum die Reihenfolge der Middlewares tragend ist
- [Wünsche](wishes.md) — nur selbst, kein COUNT, die zwei Geltungsbereiche, und drei Funde beim Bauen
- [Fachgruppen](subject-groups.md) — semesterunabhängig, die Leitung als Scope-Tabelle, und was Umhängen rückwirkend ändert
- [Zuteilung](assignments.md) — person oder teacher, zwei Achsen zum Schreiben, Compare-and-Set, und drei Funde beim Bauen
- [Teile aufteilen, andere Fakultät](divided-parts.md) — Stück statt Anteil (Migration 29), `divided_from_id` wegen der Gruppenzahl, `taught_by` nicht besetzbar/nicht in der Last, Zuteilungsregel
- [Aufteilung übertragen](split-application.md) — geänderte SWS-Aufteilung auf bestehende Instanzen, mit Vorschau, alle Studiengänge; blind wird nichts entfernt, damit die Antwort keine Zuteilung verrät
- [Module über zwei Semester](two-semester-modules.md) — `semester_span` als Teiler, Aufteilung und Instanz je Semester, kein Fachsemester-Automatismus
- [Deckung über Studiengänge](instance-coverage.md) — ein Bedarf, von einem anderen Studiengang gehalten: der Fremdschlüssel mit vier Invarianten, warum das Koppeln beim Planen einseitig sein darf, und die Beförderung beim Rückzug
- [SWS-Controlling](sws-controlling.md) — `teachingLoad`: Lehr-SWS je Heimatstudiengang wie das alte Excel-Sheet, Kontingent, Saldo (WiSe: Hälfte, SoSe: Rest nach dem WiSe), `studyYearLoad`, Filter `foreignHome`; Körbe offen
- [Planungsmarken](planning-marks.md) — zwei Marken statt einer Phase, und warum fehlende Zeile hier offen heißt
- [Semester und Phase](semester-and-phase.md) — die erste Domänen-Tabelle: Identität, Nachbarschaftsregel, Compare-and-Set, wer schalten darf, und das Planungssemester
- [Kompetenzzone](competences.md) — am Modul ohne Semester, vertraulich ohne Veröffentlichung, Mitgliedschaft als Schreibregel, Lehrende ohne Konto durch die Fachgruppenleitung
- [Studiengangs-Review](programme-review.md) — Phase REVIEW vor FINAL, alles offen bis Semesterende, Marke „Review fertig"
- [Block und Englisch je Zug](instance-format.md) — Override gegen den ZPA-Default, Schreibregel der Zuteilung, Mitnahmepfade
- [Thema je Zug (Sammelmodule)](instance-topic.md) — WI-Seminare als Züge eines ZPA-Moduls, bis 26 Züge, Schreibregel Bedarf ∪ Zuteilung, Mitnahme nur bei der Semesterkopie
- [Modulhandbuch-Link](module-handbook.md) — aus zpa.baseurl abgeleitet, nur aktive ZPA-Module, kein eigener Schlüssel
- [Wunsch-SWS](desired-hours.md) — Min/Max je Person und Semester; Dekanat friert ein, korrigiert, veröffentlicht; Notiz nie öffentlich; Übersicht des Kollegiums
- [Eingeplante SWS](assigned-load.md) — die einzige erlaubte Summe über Zuteilungen: Leitung sieht die Last ihrer Mitglieder je Fachgruppe, auch fremde
- [Partnerfakultät](partner-reader.md) — PARTNER_READER verengt allein gehalten auf die eigenen Studiengänge, schreibt nichts, liest keine Wünsche
- [Bestätigung der Zuteilung](assignment-confirmation.md) — Prüftermin je Semester, „passt so“ je Person, Zustand aus Fingerabdruck abgeleitet

<!-- Weitere Notizen entstehen mit dem Code. Konventionen für neue Einträge:
     eine Datei = ein Sachverhalt, Frontmatter mit name/description/metadata.type,
     Querverweise als [[slug]]. -->
