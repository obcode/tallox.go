-- Migration 25: the programme review, between the assignment and the close.
--
-- Asked for by the faculty (2026-10-16). The programme leads answer to the dean's office for what
-- their programme spends on teaching, and that can only be judged once the instances are filled:
-- three external lecturers found means the full demand, none found and the lead takes three
-- cohorts themselves means merged lectures and fewer groups. So after the assignment the leads go
-- over the whole once more, and everything stays writable while they do — until the semester has
-- run. FINAL now means the semester is over, not that it is planned.
--
-- TWO THINGS
--
-- A phase, REVIEW, so the semester page can say where the process stands. And an announcement per
-- programme, review_completion, the twin of demand_completion: "IF has reviewed SS29". It blocks
-- nothing, for the reason the demand announcement blocks nothing — a review that turns up
-- something later is the ordinary case, and withdrawing the announcement is what that looks like.
--
-- A table of its own rather than a kind column on demand_completion: the two are said at different
-- moments by possibly different people, and a reader asking "which programmes are done" should not
-- have to know that one table answers two questions.

-- +goose Up

ALTER TABLE semester DROP CONSTRAINT semester_phase_is_known;
ALTER TABLE semester ADD CONSTRAINT semester_phase_is_known CHECK (
    phase IN ('DEMAND_PLANNING', 'WISHES', 'ASSIGNMENT', 'REVIEW', 'FINAL')
);

CREATE TABLE review_completion (
    semester_id uuid NOT NULL REFERENCES semester (id) ON DELETE RESTRICT,
    programme_id uuid NOT NULL REFERENCES programme (id) ON DELETE RESTRICT,
    -- When it was last said; moves on re-announcing, because a reader wants to know how fresh it is.
    completed_at timestamptz NOT NULL DEFAULT now(),
    -- Who said so. NULL once that person's row is gone; the statement stays.
    completed_by uuid REFERENCES person (id) ON DELETE SET NULL,
    PRIMARY KEY (semester_id, programme_id)
);

-- +goose Down

DROP TABLE review_completion;

-- A semester in review goes back to the assignment, which is the phase that comes closest: the
-- old schema has nowhere else to put it, and everything REVIEW permits, ASSIGNMENT permits too.
UPDATE semester SET phase = 'ASSIGNMENT' WHERE phase = 'REVIEW';
ALTER TABLE semester DROP CONSTRAINT semester_phase_is_known;
ALTER TABLE semester ADD CONSTRAINT semester_phase_is_known CHECK (
    phase IN ('DEMAND_PLANNING', 'WISHES', 'ASSIGNMENT', 'FINAL')
);
