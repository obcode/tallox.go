package domain

import "strings"

// PlainName is the spelling somebody is shown under: given name, surname, and no academic
// titles — "Vorname Nachname" rather than "Prof. Dr. Vorname Nachname".
//
// Two spellings of the same colleague reach this system. The examination office publishes a
// written-out name that carries whatever titles somebody holds, and a surname-first one that
// is "Nachname, Vorname" for all 257 and carries none. An account created by hand has only
// what the administration typed into it. Showing whichever happened to be nearest is how one
// screen ends up listing "Vorname Nachname" next to "Prof. Dr. Vorname Nachname" — the same
// faculty in two registers, with the reader left to wonder what the difference is meant to
// say. It says nothing: it is which table the row came from.
//
// So the surname-first spelling decides wherever there is one. It is the only field that is
// free of titles by construction, and turning it round costs one comma. Where there is none —
// an administrative account, anybody the examination office does not publish — the written-out
// name is passed through untouched: guessing which word of a name is a title is how somebody
// whose surname is Dr. loses it.
//
// Titles are dropped rather than added everywhere, which is a decision and not a simplifica-
// tion: this system is a planning tool among colleagues, and the one spelling it can produce
// for everybody is the one without them.
func PlainName(name, sortName string) string {
	family, given, found := strings.Cut(sortName, ",")
	family, given = strings.TrimSpace(family), strings.TrimSpace(given)
	if !found || family == "" || given == "" {
		return name
	}
	return given + " " + family
}
