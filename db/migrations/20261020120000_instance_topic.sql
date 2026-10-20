-- Migration: the topic of a cohort — what this one is about, where the catalogue names only the
-- umbrella.
--
-- The examination office holds some modules once that the faculty runs many times a semester,
-- each with its own title and its own lecturer: a seminar module whose offerings this term are
-- Design Thinking, AI Fundamentals and Green Software Engineering. Those are planned as one
-- cohort per seminar of the one catalogue module (the same form as three cohorts of an elective
-- placeholder), so the link to the examination office and every figure counted over modules
-- stays intact. What the cohort letter cannot say is which seminar it is. That is this column.
--
-- NOT PART OF THE IDENTITY
--
-- The unique key stays (semester, module, programme, cohort). A topic changes while the cohort
-- stays the same — it is often only known once the cohort is staffed — and an identity that
-- changes with an edit is not one.
--
-- NULL, NOT EMPTY
--
-- Nearly every cohort has no topic and needs none: the module's name says what it is. NULL is
-- that, and the CHECK makes the empty or untrimmed string unrepresentable, so "no topic" has one
-- spelling. The bound is a heading's length, not a paragraph's — the note is for the paragraph.
--
-- Additive only. The previous image inserts cohorts without naming the column and gets NULL.

-- +goose Up

ALTER TABLE course_instance
    ADD COLUMN topic text,
    ADD CONSTRAINT course_instance_topic_is_a_heading
        CHECK (topic IS NULL OR (topic = btrim(topic) AND char_length(topic) BETWEEN 1 AND 120));

-- +goose Down

ALTER TABLE course_instance
    DROP CONSTRAINT course_instance_topic_is_a_heading,
    DROP COLUMN topic;
