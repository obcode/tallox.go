---
name: observer-role
description: Rolle OBSERVER („Einsicht“) — fakultätsweit lesen ohne Vertrauliches, nichts schreiben, nur befristet (≤ 180 Tage); Abgrenzung zur abgelehnten DEVELOPER-Rolle
metadata:
  type: project
---

Migration `20261103100000_observer_role.sql`, 2026-11-03. Anlass: Eine Kollegin entwickelt ein
Campus-Management-Tool und braucht umfassenden Lesezugriff. Oliver hat **„nur Unvertrauliches“**
gewählt, gegen „wie das Dekanat, nur ohne Schreiben“. Damit steht die Rolle auf **keiner**
Ausnahmeliste der Vertraulichkeitsregel, und die Absage an eine DEVELOPER-Rolle bleibt gültig.
Die Golden-Matrizen haben nur neue `OBSERVER`-Zeilen bekommen, keine bestehende hat sich geändert.

**Die Inventur hat gezeigt:** Fast alles Lesbare ist für *jedes* Konto offen (Bedarf, Katalog,
Controlling, Fachgruppen, veröffentlichte Zuteilungen). Die Rolle ist deshalb vor allem
**Verengung + Schreibsperre + Befristung**, nicht Erweiterung. Einzige Erweiterung: der
ZPA-Importstatus (`MayReadZPAImport`). `MayTriggerZPASync` war bis dahin „dieselbe Menge wie
Lesen“ und wurde ausdrücklich getrennt, sonst hätte die Einsicht einen Import auslösen können.

**Allein gehalten** (`policy.ObserverOnly`), wie bei der Partnerfakultät:
- `policy.Onlooker(a)` = `PartnerOnly ∨ ObserverOnly`. Diese Abzweigung steht in Wunsch-,
  Wunsch-SWS- und Eingeplante-SWS-Sicht und in `MayServeOneself`/`MayStateDesiredHours`. **Wer eine
  neue Leseregel über Aussagen anderer schreibt, fragt `Onlooker`, nicht `PartnerOnly`**, sonst
  öffnet sie veröffentlichte Wünsche für die Einsicht, ohne dass es jemand entschieden hat.
- Wünsche, Vormerkungen, Wunsch-SWS, eingeplante SWS anderer: **keine, auch keine
  veröffentlichten**. `desiredHoursOverview` liefert eine leere Liste.
- Zuteilungen: wie jedes Konto, also veröffentlichte alle, vorher nur eigene (dort *keine* eigene
  Abzweigung, `PartnerOnly` bleibt in `assignment.go`).
- **Anders als die Partnerfakultät:** nicht auf Studiengänge verengt, und sie sieht die
  Personenlisten (Lehrende, Leitungen, Mitglieder). Die veröffentlichten Zuteilungen nennen
  dieselben Namen ohnehin.
- Schreiben: `OBSERVER_READS_ONLY` (eigener Fehler `ErrObserverWritesNothing`, Helfer
  `onlookerRefusal` in `domain/desiredhours.go` für alle Selbstbedienungs-Schreibpfade).

**Befristung:** `OBSERVER_GRANT_MUST_EXPIRE` im Domain-Code *und* als CHECK
`person_role_observer_expires` (Spiegelbild der ADMIN-Regel). Die Obergrenze beträgt 180 Tage
(`MaxObserverGrantDays`), aber nur, wenn `OBSERVER` die einzige hinzukommende Rolle des Aufrufs
ist, sonst gelten die üblichen 30. Verlängern heißt: Rolle entziehen und neu vergeben (`SetRoles`
vergibt bereits gehaltene Rollen nicht neu). `storetest.SeedPerson` gibt `OBSERVER` automatisch
30 Tage.

**Nachtbericht:** Abschnitt „ROLLE EINSICHT“ mit Ablauf und vergebender Person. Er macht den
Bericht **allein nicht berichtenswert**: Bei 180 Tagen wäre das jede Nacht eine Mail. Die Nacht
der Vergabe ist ohnehin berichtenswert (Mutation).

**API:** Bedarf & Co. liest auch ein normales Token. Für den ZPA-Status braucht es den Browser
oder ein Planungs-Token (`@fullReach`); das ist unbedenklich, weil die Rolle selbst nichts
Vertrauliches freigibt.

Verwandt: [[partner-reader]], [[full-reach-tokens]], [[access-log]], im geteilten Memory
[[access-administration]].
