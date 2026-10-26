-- +goose Up

-- The time the system applies a change request at once it is approved, and
-- why its last scheduled application failed: when, the code and message of
-- the refusal, how many attempts in a row failed, and whether the system
-- stopped trying. The failure is the scheduler's record, never part of a
-- change request's version.
ALTER TABLE change_requests
    ADD COLUMN apply_at                 timestamptz,
    ADD COLUMN schedule_failed_at       timestamptz,
    ADD COLUMN schedule_failure_code    text,
    ADD COLUMN schedule_failure_message text,
    ADD COLUMN schedule_failures        integer     NOT NULL DEFAULT 0 CHECK (schedule_failures >= 0),
    ADD COLUMN schedule_stopped         boolean     NOT NULL DEFAULT false,
    ADD CONSTRAINT change_requests_schedule_failure CHECK (
        (schedule_failed_at IS NULL) = (schedule_failure_code IS NULL)
        AND (schedule_failed_at IS NULL) = (schedule_failure_message IS NULL)
        AND (schedule_failed_at IS NULL) = (schedule_failures = 0)
        AND (NOT schedule_stopped OR schedule_failed_at IS NOT NULL)
        AND (schedule_failed_at IS NULL OR apply_at IS NOT NULL)
    );

-- The approved change requests the scheduler applies, by their time.
CREATE INDEX change_requests_due ON change_requests (apply_at)
    WHERE state = 'approved' AND apply_at IS NOT NULL AND NOT schedule_stopped;
