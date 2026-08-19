# Memory — tallox.go (Backend)

Wissensbasis des Backends. Übergreifendes und Betriebswissen liegt im privaten `tallox.dev`
(im Container unter `../tallox.dev/.claude/memory/`).

**Dieses Repo ist öffentlich.** Keine Hostnamen, keine Zugangsdaten, keine Namen von
Kolleg:innen — das gehört nach `tallox.dev`.

- [Layering-Regel](layering-rule.md) — warum nur `internal/store` an die Datenbank darf und wie das erzwungen wird
- [Identität und Autorisierung](identity-and-auth.md) — was der erste Fachschritt gebaut hat, welche Entscheidungen darin stecken, welche Fallstricke Zeit gekostet haben
- [Scope-Durchsetzung](scope-enforcement.md) — `@scope` per Operation, warum leer „unbeschränkt" heißt, und warum PUBLIC nicht wegnehmbar ist
- [ZPA-Import](zpa-import.md) — Cache, Sync, Cron und der Fehler, den erst der echte Lauf zeigte
- [Semester und Phase](semester-and-phase.md) — die erste Domänen-Tabelle: Identität, Nachbarschaftsregel, Compare-and-Set, wer schalten darf

<!-- Weitere Notizen entstehen mit dem Code. Konventionen für neue Einträge:
     eine Datei = ein Sachverhalt, Frontmatter mit name/description/metadata.type,
     Querverweise als [[slug]]. -->
