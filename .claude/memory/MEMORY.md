# Memory — tallox.go (Backend)

Wissensbasis des Backends. Übergreifendes und Betriebswissen liegt im privaten `tallox.dev`
(im Container unter `../tallox.dev/.claude/memory/`).

**Dieses Repo ist öffentlich.** Keine Hostnamen, keine Zugangsdaten, keine Namen von
Kolleg:innen — das gehört nach `tallox.dev`.

- [Layering-Regel](layering-rule.md) — warum nur `internal/store` an die Datenbank darf und wie das erzwungen wird
- [Identität und Autorisierung](identity-and-auth.md) — was der erste Fachschritt gebaut hat, welche Entscheidungen darin stecken, welche Fallstricke Zeit gekostet haben
- [Scope-Durchsetzung](scope-enforcement.md) — `@scope` per Operation, warum leer „unbeschränkt" heißt, und warum PUBLIC nicht wegnehmbar ist
- [ZPA-Import](zpa-import.md) — Cache, Sync, Cron und der Fehler, den erst der echte Lauf zeigte
- [Integrationstests in der CI](integration-tests-in-ci.md) — zwei Test-Binaries, zwei Container, ein Ryuk: woran man den Ärger erkennt
- [Welche Studiengänge geplant werden](programme-planning-status.md) — warum das ZPA es nicht sagt (gemessen), und was aus der Spalte folgt
- [Lokale Module und FWP-Platzhalter](local-modules.md) — warum eine `module`-Zeile statt einer eigenen Tabelle, und warum drei FWPs drei Züge sind
- [Zulassung von Lehrenden](admitting-a-teacher.md) — wie aus einer ZPA-Lehrperson ein Konto wird, und die drei Entscheidungen darin
- [Zugriffslog](access-log.md) — was es festhält, was es bewusst nicht kann, und warum die Reihenfolge der Middlewares tragend ist
- [Fachgruppen](subject-groups.md) — semesterunabhängig, die Leitung als Scope-Tabelle, und was Umhängen rückwirkend ändert
- [Semester und Phase](semester-and-phase.md) — die erste Domänen-Tabelle: Identität, Nachbarschaftsregel, Compare-and-Set, wer schalten darf, und das Planungssemester

<!-- Weitere Notizen entstehen mit dem Code. Konventionen für neue Einträge:
     eine Datei = ein Sachverhalt, Frontmatter mit name/description/metadata.type,
     Querverweise als [[slug]]. -->
