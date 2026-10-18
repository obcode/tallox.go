---
name: partner-reader
description: Rolle PARTNER_READER für FK10/MUC.DAI/MUC.HEALTH/DPH — gescopt auf Studiengänge, liest allein gehalten WENIGER als ein normales Konto, schreibt nichts
metadata:
  type: project
---

Migration 32 (`20261018140000_partner_reader.sql`), 2026-10-18. Oliver hat die Variante „Rolle mit
Studiengangs-Scope“ gewählt, nachdem klar war: **ein normales Konto liest Bedarf, Controlling und
veröffentlichte Zuteilung ohnehin vollständig, auch per Token.** Die Rolle erweitert also nichts,
sie **verengt**.

**Nur allein gehalten.** `policy.PartnerOnly(a)` = authentifiziert ∧ Rollen == {PARTNER_READER}.
Wer zusätzlich irgendeine Rolle hat, gehört zum Kollegium und sieht wie bisher alles (Vereinigung
der Rechte). Die Rollenansicht „Partnerfakultät“ zeigt damit genau die Partnersicht.

**Was verengt wird** (`policy.DemandReadScope`, leere Menge = nichts):
- `courseInstances` — `WHERE`-Klausel (`all_programmes`/`programme_ids`), `DemandFilter.Reach`;
  der Zero-Value liest nichts, interne Schreibpfade setzen ausdrücklich `All`.
- `courseInstance(id)` — Guard nach dem Lesen.
- `teachingLoad`, `studyYearLoad`, `programmeQuotas` — Zeilenfilter in Go (`withinReach`), ehrlich,
  weil jede Zeile *ein* Studiengang ist und nichts über die Grenze summiert wird.
- Zuteilungen: nur veröffentlichte der eigenen Studiengänge, vorher keine.
- Wünsche: **keine**, auch nicht veröffentlichte.
Offen bleiben Katalog, Semester, Fachgruppen-Struktur, Planungsmarken (Stammdaten, Prozessfakten).

**Was gesperrt wird**, mit eigenem Code `PARTNER_READS_ONLY` statt der sonst passenden, aber
falschen Meldung „Semester abgeschlossen“: Wunsch, Wunsch-SWS, eigene Kompetenz,
`setMySubjectGroups` (`policy.MayServeOneself`).

**Verwaltung:** `setPersonProgrammes(…, role: PARTNER_READER)` — derselbe Picker; ein Partner darf
auch einem nicht geplanten Studiengang folgen (die Leitung nicht). `Person.partnerProgrammes`
getrennt von `programmes`: der Loader `ProgrammeScopesFor` teilt jetzt nach Rolle. Der
Anmeldeweg (View `person_role_scope`) trug die Rolle schon immer mit.

Offen für die Partner selbst (Rückfrage 11/12): welche Personen, welche Studiengänge, und ob das
Controlling dazugehört. Verwandt: [[access-administration]] im geteilten Memory.
