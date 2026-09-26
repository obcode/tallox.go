---
name: competences
description: Die Kompetenzzone — am Modul ohne Semester, vertraulich ohne Veröffentlichung, Mitgliedschaft als Schreibregel, Lehrende ohne Konto durch die Fachgruppenleitung
metadata:
  type: project
---

Migration 23 (`db/migrations/20260926100000_competences.sql`), gebaut am 2026-09-26 auf
`feat/competence`. Die drei Entscheidungen dahinter hat der Nutzer am selben Tag getroffen.

## Was es ist

„Wer kann was halten, wenn es brennt" (`CAN_TEACH`) und „wer würde mal gerne" (`WOULD_LIKE`) —
**am Modul, ohne Semester**. Ein Pool je Modul, der jedes Semester überdauert, wie eine Fachgruppe.

## Lesen: die Wunschregel ohne Veröffentlichung

`WOULD_LIKE` ist ein Wunsch ohne Semester. Also dieselbe Regel — eigene, oder zuständig (Leitung
des **Heimat**studiengangs des Moduls, Leitung seiner Fachgruppe, Dekanat) und dann nur interaktiv —
und weil es kein Semester gibt, gibt es **kein Datum, an dem sie fällt**. Durch ein Token nur die
eigenen, auch fürs Dekanat. `competence_visibility_matrix.golden`.

Der Studiengang ist der Heimatstudiengang, weil eine Kompetenz keine Instanz und damit keinen
nachfragenden Studiengang hat. Modul 101 im Fixture zählt auch in PB — PB liest trotzdem nichts.

## Schreiben: drei Regeln

1. **Eigene nur für Module der eigenen Fachgruppen.** Anforderung „nur fachgruppenspezifische
   Fächer, keine Rosinen". Mitgliedschaft ist Selbstbedienung (`setMySubjectGroups`), also keine
   Schranke, sondern der ausdrückliche Akt, der die Pflichtfächer der Gruppe vor einen legt. Die
   Prüfung braucht die DB (`CompetenceModuleContext`), deshalb steht sie in `internal/domain` und die
   reine Hälfte (`MayStateOwnCompetence`) in `internal/policy`.
2. **Lehrende ohne Konto trägt die Fachgruppenleitung ein** (oder das Dekanat), nur interaktiv.
   Deshalb gibt es hier `entered_by` — anders als beim Wunsch. `CHECK (person_id IS NULL OR
   entered_by = person_id)` hält fest, dass eine Personenzeile immer die eigene Aussage ist.
3. **Hat der `teacher` ein Konto, wird verweigert** (`COMPETENCE_TEACHER_HAS_ACCOUNT`), statt wie bei
   der Zuteilung auf das Konto zu kanonisieren: kanonisieren hieße hier, im Namen einer Person
   etwas zu behaupten, das sie selbst sagen kann.

Wer eine Fachgruppe verlässt, **behält** seine Einträge; sie tragen `outsideSubjectGroups` und
bleiben löschbar. Stilles Löschen verliert, was jemand getippt hat.

## Mindestzahl: Warnung, keine Validierung

`policy.MinCompulsoryCompetences = 3` („mindestens 3 oder 4"). Gezählt wird `CAN_TEACH` auf
**aktiven Pflichtmodulen** der Gruppe; Pflicht = `module_offering.is_duty` in mindestens einer
SPO-Fassung. `myCompetenceStatus` für die eigene Zahl, `competenceMemberStatus` und
`modulesWithoutCompetence` als Arbeitslisten der Leitung.

**Aggregate über eine Gruppe nur für die Gruppenachse** (`MayReadSubjectGroupCompetences`): eine
Studiengangsleitung liest die Module ihres Studiengangs, die quer durch Gruppen liegen — sie darf
einzelne Zeilen einer Gruppe lesen, aber nicht über alle zählen. Die drei Aggregat-Queries tragen
den Zeilenfilter nicht; `TestEveryCompetenceQueryIsFiltered` kennt sie namentlich.

## Scope-Bereich

`WISHES`, nicht `PROFILE` und kein neuer Bereich: „würde gern" ist vertraulich aus dem Grund, aus dem
ein Wunsch es ist, und ein auf `PLANNING` beschränktes Auswerteskript soll es nicht erreichen.

## Namensfalle

Im `graph.Resolver` heißt der Service `Expertise`, weil `Competences` die generierte
Query-Methode ist — dieselbe Kollision, die `Staffing` vermeidet.

Siehe auch [[wishes]], [[subject-groups]], [[assignments]].
