---
name: programme-leads
description: Programme.leads — wer einen Studiengang leitet, ist seit 2026-10-20 für jedes Konto und über beide Türen lesbar; warum, und was nicht als Leitung zählt
metadata:
  type: project
---

Seit 2026-10-20 (`feat/programme-leads`) hat `Programme` das Feld `leads: [Person!]!`. Anlass
war die Übersicht „Wer leitet was“ auf der Startseite.

**Bewusst geöffnet.** Vorher stand die Zuordnung nur in `people` (ADMIN ∧ interaktiv). Jetzt
gilt dieselbe Linie wie für `SubjectGroup.leads`: Wer einen Studiengang leitet, ist in der
Fakultät nicht vertraulich, und das Kollegium muss wissen, wen es zum Bedarf fragt. Ausgeliefert
werden eine Rollenzuordnung, ein Name und eine Mailadresse, keine Personaldaten im Sinne von
Regel 2. Kein `@interactiveOnly`.

**Nicht als Leitung zählen:**

- ein abgelaufener Grant. Es greift derselbe Ablauffilter wie in `SubjectGroupLeadsFor`.
- `PARTNER_READER`-Zeilen in `person_programme_scope`. Dieselbe Tabelle trägt seit Migration
  `20261018140000` auch Partnerkonten. Ohne den Filter `s.role = 'PROGRAMME_LEAD'` stünde ein
  Partnerkonto als Studiengangsleitung auf der Startseite.

Eine Abfrage pro Studiengang (Feld-Resolver), wie bei `SubjectGroup.modules`. Bei zehn
Studiengängen ist ein Dataloader nicht nötig.

Tests: `internal/store/programme_leads_test.go`,
`bootstrap/catalogue_test.go:TestProgrammeLeadsAreReadableByAnybodyWithAnAccount` (`EachDoor`).

Siehe [[subject-groups]], [[partner-reader]].
