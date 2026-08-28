-- Migration 21: one register of names, for accounts made before there was one.
--
-- Two spellings of the same colleague reach this system. The examination office publishes a
-- written-out name that carries whatever titles somebody holds, and a surname-first one —
-- "Nachname, Vorname" for all 257 — that carries none. Reading whichever was nearest is how one
-- screen came to list "Vorname Nachname" beside "Prof. Dr. Vorname Nachname": the same faculty
-- in two registers, saying nothing except which table the row came from.
--
-- Everything that shows a name now turns the surname-first spelling round instead
-- (domain.PlainName), so the reading side is settled without touching a row. This migration is
-- for the one place that has no surname-first spelling beside it to derive from: person.name,
-- which `me` answers with and which admission copied from the written-out name.
--
-- ONLY WHERE NOBODY HAS TYPED SOMETHING ELSE
--
-- The condition is that person.name is still character for character what admission copied out
-- of teacher.full_name. That is the signature of a row this system wrote and nobody has since
-- corrected; an account named by hand — the deanery, an administrator, a colleague spelled the
-- way they asked to be — does not match it and is left exactly as it is. Renaming those would
-- be this migration deciding something it was not asked to decide.
--
-- Teachers with no surname-first spelling are skipped for the same reason PlainName skips them:
-- guessing which word of a name is a title is how somebody whose surname is Dr. loses it.

-- +goose Up

UPDATE person p
SET name       = trim(substr(t.short_name, strpos(t.short_name, ',') + 1)) || ' ' ||
                 trim(left(t.short_name, strpos(t.short_name, ',') - 1)),
    updated_at = now()
FROM teacher t
WHERE t.mail = p.mail
  AND p.name = t.full_name
  AND strpos(t.short_name, ',') > 1
  AND trim(substr(t.short_name, strpos(t.short_name, ',') + 1)) <> '';

-- +goose Down

-- The written-out spelling, back where this migration replaced its own output and nowhere else.
-- A row somebody has renamed since does not match and stays renamed.
UPDATE person p
SET name       = t.full_name,
    updated_at = now()
FROM teacher t
WHERE t.mail = p.mail
  AND p.name = trim(substr(t.short_name, strpos(t.short_name, ',') + 1)) || ' ' ||
               trim(left(t.short_name, strpos(t.short_name, ',') - 1))
  AND strpos(t.short_name, ',') > 1
  AND trim(substr(t.short_name, strpos(t.short_name, ',') + 1)) <> '';
