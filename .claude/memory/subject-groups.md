---
name: subject-groups
description: Fachgruppen — semesterunabhängig, warum die Leitung eine Scope-Tabelle ist, und die rückwirkende Folge des Umhängens
metadata:
  type: project
---

Migration 14 (`db/migrations/20260825100000_subject_groups.sql`), gebaut am 2026-08-25 als
Voraussetzung der Wunschphase. Bis dahin war `SUBJECT_GROUP_LEAD` eine Rolle ohne Gegenstand, und
`role.go` sagte ausdrücklich, dass alles, was davon abhängt, auf die gescopte Form warten muss,
statt sie mit der ungescopten anzunähern. Die Wunschregel war, was gewartet hat.

## Vier Tabellen, weil es vier Sachverhalte sind

    subject_group               die Gruppe
    module_subject_group        genau eine Gruppe je Modul (module_id ist der PK)
    person_subject_group        Mitgliedschaft, n:m
    person_subject_group_scope  die Leitung

**Mitgliedschaft und Leitung sind nicht ein Flag an einer Tabelle.** Sie beantworten
verschiedene Fragen und werden durch verschiedene Akte vergeben: Mitgliedschaft sagt, in welchen
Fächern jemand arbeitet, und berechtigt zu nichts; Leitung ist eine Rollenzuordnung und muss mit
der Rolle entziehbar sein.

## Warum die Leitung keine `lead_person_id`-Spalte ist

Der zusammengesetzte Fremdschlüssel auf `person_role`. Ohne ihn überlebt ein entzogenes
`SUBJECT_GROUP_LEAD` seine Fachgruppen und bringt sie beim erneuten Vergeben still zurück — eine
Verbreiterung, die niemand vorgenommen hat. Eine Spalte kann das nicht ausdrücken.

„Keine Fachgruppe ohne Person, die sich ihrer annimmt" ist deshalb eine **Arbeitsliste**
(`subjectGroupsWithoutLead`) und kein `NOT NULL`: die Gruppe muss anlegbar sein, bevor die
Leitung feststeht, und die Leitung entziehbar, ohne die Gruppe zu zerstören.

## Semesterunabhängig — und die rückwirkende Folge

Eine Fachgruppe hat kein Semester. Die Fachgruppe von etwas Geplantem wird deshalb **abgeleitet**:

    wish/assignment → instance_part → course_instance → module → module_subject_group

Daraus folgt: **ein Modul umzuhängen ändert rückwirkend, wer dessen unveröffentlichte Wünsche
lesen darf.** Das ist die gewollte Wirkung — zuständig ist, wer jetzt zuständig ist — und genau
der Grund, warum nichts denormalisiert wird: eine Kopie auf der Wunschzeile würde die
Zuständigkeit im Moment der Eintragung einfrieren. `internal/store` hat dafür einen Test, damit
es festgehalten und nicht eines Tages entdeckt wird.

Teilen einer Gruppe (Mathe → klassisch / ML) ist damit: neue anlegen, Zeilen umhängen, alte
stilllegen. Ein `UPDATE`, kein Migrationspfad — was open-questions.md als Betriebsanforderung
nennt.

## Ein ungescopter Lead darf nichts, nicht alles

Dieselbe Lesart wie bei `person_programme_scope` und dieselbe Begründung: ein Scope ist keine
Verengung des Grants, sondern sein Gegenstand. Hier wäre die andere Richtung schlimmer gewesen —
sie hätte den Deploy dieser Migration zu dem Moment gemacht, in dem jede Fachgruppenleitung still
fakultätsweiten Zugriff auf fremde unveröffentlichte Wünsche bekommt.

**Mitgliedschaft berechtigt zu nichts.** Der Kickoff-Satz „jeder in einer Fachgruppe müsste alles
lesen können" gilt für Planungsdaten, nicht für unveröffentlichte Wünsche: das Windhundverfahren
spielt sich *innerhalb* der Fachgruppe ab.

## Die View `person_role_scope`

Die Vereinigung beider Scope-Tabellen plus die Ablaufregel, an **einer** Stelle. Vorher stand die
Regel „ein abgelaufener Grant trägt keine Scopes" viermal im Repo (dreimal in `person.sql`, einmal
in `token.sql`); eine zweite gescopte Rolle hätte daraus acht Kopien gemacht. Die
Fremdschlüssel decken den *entzogenen* Grant ab, die View den *abgelaufenen*.

`principal.RoleScope` trägt seither zwei benannte Ziele statt eines anonymen. Genau eines ist
gesetzt, und der Rollenstring sagt schon welches — zwei Felder deshalb, weil eine Zeile mit
falschem Rollenstring sonst nicht mehr als fehlgeformt erkennbar wäre, sondern als Scope der
jeweils anderen Art lesbar. `policy.TestTheTwoScopesDoNotReadEachOther` und
`store.TestRoleScopesNameExactlyOneThing` nageln es fest.

## Gerendert wird es in `assignment_matrix.golden`

Vor der Zuteilungsphase, aus demselben Grund, aus dem die Wunschmatrix in Woche 1 entstand: der
Scope entscheidet schon etwas Echtes, und ein Scope ohne gerenderte Regel ist einer, den niemand
geprüft hat.

Siehe auch [[identity-and-auth]], [[scope-enforcement]], [[programme-planning-status]].
