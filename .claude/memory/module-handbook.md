---
name: module-handbook
description: Link aufs öffentliche Modulhandbuch — aus zpa.baseurl abgeleitet, nur für aktive ZPA-Module, warum kein eigener Konfigurationsschlüssel
metadata:
  type: project
---

`Module.handbookUrl`, 2026-10-17, aus dem Collab-Wunsch „ein Link auf das Modul im
Modulhandbuch wäre schön, momentan sieht man nur den Modulverantwortlichen".

- **Form:** `<zpa.baseurl>/pub/#m=<zpaId>`. Die Kennung im Fragment ist dieselbe `module_id`, die
  der Import als `zpa_module_ref` speichert; am 2026-10-17 gegen den öffentlichen Katalog
  geprüft (400 von 402 gemeinsamen Modulen mit gleichen SWS/ECTS/Lehrform, die zwei Abweichungen
  sind Änderungen seit dem Spike).
- **Kein eigener Schlüssel.** Handbuch und REST-Schnittstelle sind eine Installation; ein zweiter
  Schlüssel wäre eine zweite Stelle, die beim Umzug vergessen wird — und wegen `UnmarshalExact`
  ein vorwärts-only-Schlüssel mehr. Die Adresse bleibt aus dem öffentlichen Repo heraus wie die
  der Schnittstelle. Ohne ZPA-Konfiguration (DevContainer, CI) ist das Feld `null`.
- **Nur aktive, importierte Module.** Der öffentliche Katalog listet nur aktive Module (403 von
  506). Ein Link auf ein inaktives öffnet dort ein leeres Panel mit dem Titel „Modul" — das liest
  sich als kaputter Link. Lokale Module ([[local-modules]]) hat das ZPA nicht.
- Drei aktive Module fehlen trotzdem im Katalog (Stand 2026-10-17); hingenommen, nicht abgefangen —
  Tallox fragt den öffentlichen Katalog nicht ab.

Gebaut in `internal/domain/modulehandbook.go`, getragen von `CatalogueService`. Verwandt:
[[zpa-import]].
