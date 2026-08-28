-- Migration 20: a covered cohort is a programme's whole participation in that module.
--
-- Migration 19 built coverage as the exception with a ceremony: the guest's lead asks, the host's
-- lead agrees, both by hand. The faculty has since said it is the rule. A cohort declared beside
-- another programme's declaration of the same module is now held with it from the moment it is
-- declared, and planning separately is the thing somebody chooses.
--
-- That is a change in internal/store, not in this schema — coupling writes the same columns it
-- always did. What the schema owes the new rule is one index, and the index is here because it
-- carries two different weights at once.
--
-- WHAT IT SAYS
--
-- A covered cohort holds nothing and attends what is held elsewhere. Two of them side by side in
-- one programme would mean two cohorts sitting in the same event — and a programme that runs two
-- cohorts runs them because one does not fit. So the second cohort of a module holds its own
-- teaching, and internal/store falls back to that rather than meeting this index as a raw unique
-- violation out of a save.
--
-- WHAT IT MAKES SAFE
--
-- Withdrawing a holder now promotes a guest instead of being refused: the longest-standing guest
-- inherits the parts, and the remaining guests are re-pointed at it. That re-pointing has to pass
-- course_instance_coverage_crosses_programmes, and it can only ever do so because of this index —
-- with at most one covered cohort per programme, every guest of one host is in a different
-- programme, so none of them can end up pointing at the successor's own programme.
--
-- Without the index that case is reachable and the promotion needs a second mechanism for it:
-- release the successor's same-programme co-guests, rebuild their parts, report it. Measured
-- against one CREATE INDEX, that is machinery for a state this simply does not allow to exist.
--
-- WHY ON DELETE RESTRICT STAYS
--
-- The promotion does not remove the guard that refuses to delete a host somebody's demand hangs
-- off; it satisfies it, by re-pointing the guests first. The constraint keeps its meaning — a
-- guest is never stranded — and TestAHostCannotBecomeAGuestWhileItCoversSomebody keeps asserting
-- it at the level it lives on. A test that went red here would mean somebody took the guard away
-- instead of serving it.

-- +goose Up

CREATE UNIQUE INDEX course_instance_one_covered_cohort_per_programme
    ON course_instance (semester_id, module_id, programme_id)
    WHERE covered_by_instance_id IS NOT NULL;

-- +goose Down

DROP INDEX IF EXISTS course_instance_one_covered_cohort_per_programme;
