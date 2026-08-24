---
name: local-modules
description: Lehrveranstaltungen, die die Fakultät selbst einträgt — warum eine module-Zeile, was der CHECK garantiert, und warum drei FWPs drei Züge sind
metadata:
  type: project
---

Migration 11 (`db/migrations/20260824140000_local_modules.sql`), 2026-08-24. Zwei Fälle, eine
Form: eine `module`-Zeile mit `source = 'LOCAL'`.

- eine Lehrveranstaltung, die (noch) nicht im ZPA steht,
- ein **FWP-Platzhalter** — „wir brauchen drei Wahlpflichtfächer, gern technisch".

## Warum eine Zeile und keine eigene Tabelle

Weil alles Nachgelagerte schon weiß, was ein Modul ist: Zerlegung, Instanz, Teile,
SWS-Arithmetik, später Wünsche und Zuteilung laufen unverändert darauf. Die im August erwogene
Alternative — `demand_wildcard` neben `course_instance` — kostet auf **jeder** dieser Ebenen
einen zweiten Zweig, dauerhaft, für Zeilen, die sich exakt wie Module verhalten. Das war die
Entscheidung; die alte Vertagung in [[open-questions]] ist damit beantwortet.

**Ein Platzhalter ist deshalb kein neues Konzept.** „Drei davon" sind **drei Züge eines
Platzhalters** — und das ist keine Notlösung, sondern das, was die Instanz-Identität ohnehin
sagt: drei Angebote eines Moduls in einem Studiengang und Semester *müssen* sich im `track`
unterscheiden ([[instance-identity]]). Die Bedarfstabelle kann das mit dem Zug-Zähler, den sie
hat. Der Constraint aus der Anforderung („gern technisch") ist der **Name** des Platzhalters —
keine Constraint-Spalte, keine Prädikatsprache.

## Was den Import draußen hält

`module_local_has_no_zpa_ref`. Mechanisch könnte `ProjectModules` eine lokale Zeile ohnehin
nicht treffen (`ON CONFLICT (zpa_module_ref)`, und die ist NULL) — aber das ist eine Eigenschaft
eines *Statements*, und Statements werden umgeschrieben. Der CHECK ist die Eigenschaft der
Tabelle, und darauf zeigen die Tests.

Zwei Constraints folgen daraus:

- **`module_local_is_never_retired`** — `retired_at` heißt „ein erfolgreicher Import erwähnt sie
  nicht mehr". Über eine lokale Zeile hat der Import keine Meinung, also ist `active = false`
  der Weg, eine nicht mehr gebrauchte stillzulegen. Gelöscht wird nichts: Instanzen und später
  Wünsche zeigen darauf.
- **`module_local_name_idx`** — partiell, auf `(home_programme_id, lower(name))`. Eine lokale
  Zeile hat keine andere Identität als den Namen, also sind zwei Klicks auf „anlegen" eine
  Zeile. Partiell, weil zwei importierte Module denselben Namen tragen dürfen und einige es tun.

**Die stillste Stelle des ganzen Vorhabens:** `CountModulesWithoutName` und
`CountInactiveModules` in `db/queries/catalogue.sql` liefen ungefiltert über `module`. Ohne
`AND zpa_module_ref IS NOT NULL` stünde jeder stillgelegte Platzhalter ab der nächsten Nacht als
Befund eines Imports im Bericht, der ihn nie gesehen hat. Dafür gibt es
`TestLocalModulesDoNotAppearInTheProjectionReport`.

## Policy: nichts Neues

`policy.MayPlanProgramme(actor, homeProgrammeID)` — dieselbe Regel, die `setModuleComponents`
schon benutzt, mit derselben Begründung: ein Modul wird geplant, wo es zu Hause ist.

**Keine Phasen-Bedingung.** Ein Modul hängt an keinem Semester, also gibt es keine Phase, die es
schließen könnte; das Phasentor greift an der Instanz, wo es hingehört. Folge: **weder
`planning_matrix.golden` noch `write_matrix.golden` ändern sich.** Wenn ein
`-update-golden`-Lauf doch etwas ändert, ist das ein Signal, dass die Annahme falsch war — den
Diff lesen, nicht übernehmen.

`changeLocalModule` nimmt den Studiengang **nicht** entgegen: er ist das, wogegen die
Berechtigung geprüft wird, und ihn verschiebbar zu machen hieße, eine Zeile im selben Request
aus der eigenen Reichweite zu schieben. Eine importierte Zeile antwortet dort `MODULE_NOT_FOUND`
wie eine nicht existierende — sie ist nicht Sache dieser Mutation, und zu sagen, welcher der
beiden Fälle vorliegt, lüde nur zum zweiten Versuch ein.

## `adoptModule` bewusst später

Wenn ein lokal angelegtes Fach später doch im ZPA auftaucht, ist das Verheiraten ein `UPDATE`
(`source` auf `'ZPA'`, Referenz eintragen) — der CHECK erlaubt es. Gebaut ist nichts davon: zwei
fachliche Fragen sind offen, nämlich welche ZPA-Zeile die richtige ist (das kann nur ein Mensch
sagen) und was mit den Instanzen und später Wünschen passiert, die schon an der lokalen Zeile
hängen. Koexistenz ist keine Korruption, sondern zwei Katalogzeilen, von denen eine aufhört,
angehakt zu werden.

## Fallstrick beim Generieren

`model.Module` trug ein handgeschriebenes Feld `Source domain.Module` (der Domänenwert für die
Felder mit Argument). Das neue Schema-Feld `source` kollidierte damit, und gqlgen machte
stillschweigend zwei Resolver-Methoden daraus. Jetzt heißt das interne Feld `Domain`, und
`ModuleSource`/`ModuleKind` sind in `gqlgen.yml` an die Domänentypen gebunden — wie `Frequency`
und `CourseType`, aus demselben Grund.

Siehe [[instance-identity]], [[catalogue-projection]], [[programme-planning-status]].
