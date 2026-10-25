-- +goose Up

-- The latest sample-ratio check of an experiment in an environment holding
-- its rule. A stopped experiment keeps the one its stop took, since nothing
-- checks it again. chi_square and p_value are null when the check was
-- skipped, and skipped_reason says why.
CREATE TABLE experiment_sample_ratios (
    experiment_id  uuid        NOT NULL REFERENCES experiments (id) ON DELETE CASCADE,
    environment_id uuid        NOT NULL REFERENCES environments (id) ON DELETE CASCADE,
    n              bigint      NOT NULL CHECK (n >= 0),
    chi_square     double precision,
    p_value        double precision,
    skipped_reason text        CHECK (skipped_reason IN ('below_minimum_sample', 'expected_below_5')),
    checked_at     timestamptz NOT NULL,
    PRIMARY KEY (experiment_id, environment_id),
    CHECK ((skipped_reason IS NULL) = (p_value IS NOT NULL)),
    CHECK ((chi_square IS NULL) = (p_value IS NULL))
);

CREATE INDEX experiment_sample_ratios_environment ON experiment_sample_ratios (environment_id);

-- Every condition a check found on an experiment, of every kind, in one
-- table: one shape gives every kind the same raise, clear, acknowledgement
-- and re-raise. details holds one block per kind, the alert's own set. An
-- alert about the experiment as a whole has no environment, so at most one
-- alert of a kind is active per experiment and environment, a missing
-- environment counting as one.
CREATE TABLE experiment_alerts (
    id              uuid        PRIMARY KEY,
    experiment_id   uuid        NOT NULL REFERENCES experiments (id) ON DELETE CASCADE,
    environment_id  uuid        REFERENCES environments (id) ON DELETE CASCADE,
    kind            text        NOT NULL CHECK (kind IN ('srm', 'no_exposures', 'exposure_conflicts', 'past_expected_end')),
    raised_at       timestamptz NOT NULL,
    cleared_at      timestamptz CHECK (cleared_at >= raised_at),
    acknowledged_at timestamptz CHECK (acknowledged_at >= raised_at),
    details         jsonb       NOT NULL
);

CREATE UNIQUE INDEX experiment_alerts_active ON experiment_alerts (experiment_id, environment_id, kind)
    NULLS NOT DISTINCT WHERE cleared_at IS NULL;
CREATE INDEX experiment_alerts_experiment ON experiment_alerts (experiment_id, raised_at DESC);
CREATE INDEX experiment_alerts_environment ON experiment_alerts (environment_id);
