-- +goose Up

-- A wish that lapsed because its cohort was withdrawn by somebody who may write the programme's
-- demand (planDemand with dropWishes). Until now only its owner ever removed a wish; this is the
-- one path on which somebody else does, so it leaves a record.
--
-- Confidential like the wish it was: nothing reads it except the mail fan-out, and that only for
-- the subscriber's own rows (person_id = subscriber). No query counts it.
--
-- course_instance_id is not a foreign key: the instance is gone by the time the row matters, and
-- the id is what the MODULE_WITHDRAWN event carries.
--
-- Additive: the previous image never reads the table.
CREATE TABLE lapsed_wish (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    course_instance_id uuid NOT NULL,
    person_id uuid NOT NULL REFERENCES person (id) ON DELETE CASCADE,
    semester_id uuid NOT NULL REFERENCES semester (id) ON DELETE CASCADE,
    module_id uuid NOT NULL REFERENCES module (id) ON DELETE CASCADE,
    programme_id uuid NOT NULL REFERENCES programme (id) ON DELETE CASCADE,
    track text NOT NULL,
    lapsed_at timestamptz NOT NULL DEFAULT now(),
    lapsed_by uuid REFERENCES person (id) ON DELETE SET NULL
);

CREATE INDEX lapsed_wish_instance_person ON lapsed_wish (course_instance_id, person_id);

-- +goose Down

DROP TABLE lapsed_wish;
