-- +goose Up

-- The exposures SDKs post, as received, like the other raw events: partitioned
-- by the UTC day of received_at, each row carrying the inserting
-- transaction's ID and a sequence number so readers walk them in commit
-- order. An exposure names an experiment that existed when it arrived, of any
-- status; the row keeps the experiment's ID and the keys the SDK sent, and
-- references no other table, so deleting an experiment, a flag or an
-- environment leaves the rows until retention drops their day.
CREATE TABLE exposure_events (
    xid               xid8        NOT NULL DEFAULT pg_current_xact_id(),
    seq               bigint      NOT NULL DEFAULT nextval('event_seq'),
    event_id          uuid        NOT NULL,
    project_id        uuid        NOT NULL,
    environment_id    uuid        NOT NULL,
    sdk_key_id        uuid        NOT NULL,
    experiment_id     uuid        NOT NULL,
    experiment_key    text        NOT NULL,
    flag_key          text        NOT NULL,
    variation_key     text        NOT NULL,
    unit_type         text        NOT NULL,
    unit_id           text        NOT NULL,
    evaluation_reason text        NOT NULL,
    -- Whether an override decided the variation: analysis leaves such
    -- exposures out.
    override          boolean     NOT NULL,
    ruleset_version   bigint      NOT NULL,
    sdk_name          text        NOT NULL,
    sdk_version       text        NOT NULL,
    platform          text,
    app_version       text,
    occurred_at       timestamptz NOT NULL,
    received_at       timestamptz NOT NULL
) PARTITION BY RANGE (received_at);

CREATE INDEX exposure_events_cursor ON exposure_events (xid, seq);
CREATE INDEX exposure_events_experiment ON exposure_events (experiment_id, received_at);
CREATE INDEX exposure_events_project ON exposure_events (project_id, received_at);

-- The IDs of exposures received, like the other dedup tables: not
-- partitioned, so an exposure retried on a later day finds its ID.
CREATE TABLE exposure_dedup (
    project_id  uuid        NOT NULL,
    event_id    uuid        NOT NULL,
    received_at timestamptz NOT NULL,
    PRIMARY KEY (project_id, event_id)
);

CREATE INDEX exposure_dedup_received ON exposure_dedup (received_at);

-- Exposures refused for an experiment that exists, per experiment,
-- environment and reason: what tells a team its SDKs send exposures the
-- experiment cannot take. An exposure naming no experiment adds no row, so
-- keys made up by anyone holding a client key cannot grow the table; the
-- counts go with the experiment.
CREATE TABLE experiment_rejection_counts (
    experiment_id    uuid        NOT NULL REFERENCES experiments (id) ON DELETE CASCADE,
    environment_id   uuid        NOT NULL REFERENCES environments (id) ON DELETE CASCADE,
    reason           text        NOT NULL CHECK (reason IN ('unit_type_mismatch', 'unknown_variation', 'event_invalid')),
    count            bigint      NOT NULL CHECK (count > 0),
    last_rejected_at timestamptz NOT NULL,
    PRIMARY KEY (experiment_id, environment_id, reason)
);

CREATE INDEX experiment_rejection_counts_environment ON experiment_rejection_counts (environment_id);
