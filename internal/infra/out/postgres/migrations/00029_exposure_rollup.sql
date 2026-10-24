-- +goose Up

-- Where each rollup stream has read its raw event table to: the
-- inserting transaction's ID and the sequence number of the last row it
-- applied. A stream reads only rows of transactions below the snapshot
-- horizon, every one of which has ended, so no row can commit behind its
-- cursor; it moves the cursor in the transaction that applies the rows, so
-- a crash repeats nothing and skips nothing.
CREATE TABLE rollup_cursors (
    stream      text        PRIMARY KEY,
    xid         xid8        NOT NULL,
    seq         bigint      NOT NULL,
    advanced_at timestamptz NOT NULL
);

-- A unit's first exposure to an experiment in an environment: its
-- non-override exposure with the earliest occurred_at, ties broken by the
-- smaller event_id, replaced when an earlier one arrives late. seen holds,
-- per variation the unit was exposed to, how many exposures and the last of
-- them by occurrence, as {"<variation>": {"count": n, "last_at": t,
-- "last_id": id}}; conflicts and the conflicting variation are derived from
-- it and stored for reading. Kept until the experiment or the environment
-- goes.
CREATE TABLE first_exposures (
    environment_id      uuid        NOT NULL REFERENCES environments (id) ON DELETE CASCADE,
    experiment_id       uuid        NOT NULL REFERENCES experiments (id) ON DELETE CASCADE,
    unit_type           text        NOT NULL,
    unit_id             text        NOT NULL,
    event_id            uuid        NOT NULL,
    occurred_at         timestamptz NOT NULL,
    variation_key       text        NOT NULL,
    seen                jsonb       NOT NULL,
    -- The exposures of the unit naming another variation than variation_key.
    conflicts           bigint      NOT NULL CHECK (conflicts >= 0),
    -- The other variation the unit was last exposed to, by occurrence, and
    -- when; null without a conflict.
    conflicting_variation_key text,
    conflicting_at      timestamptz,
    PRIMARY KEY (environment_id, experiment_id, unit_type, unit_id),
    CHECK ((conflicts > 0) = (conflicting_at IS NOT NULL)),
    CHECK ((conflicting_at IS NULL) = (conflicting_variation_key IS NULL))
);

CREATE INDEX first_exposures_experiment ON first_exposures (experiment_id);
CREATE INDEX first_exposures_conflicts ON first_exposures (experiment_id, environment_id, conflicting_at DESC)
    WHERE conflicts > 0;

-- An experiment's exposures per variation and UTC day in an environment:
-- exposures and overridden exposures by the day they were received, first
-- exposures by the day the first exposure occurred, so a late earlier
-- exposure replacing a first exposure moves its count between days.
CREATE TABLE exposure_counts_daily (
    environment_id  uuid   NOT NULL REFERENCES environments (id) ON DELETE CASCADE,
    experiment_id   uuid   NOT NULL REFERENCES experiments (id) ON DELETE CASCADE,
    variation_key   text   NOT NULL,
    day             date   NOT NULL,
    exposures       bigint NOT NULL CHECK (exposures >= 0),
    overridden      bigint NOT NULL CHECK (overridden >= 0),
    first_exposures bigint NOT NULL CHECK (first_exposures >= 0),
    PRIMARY KEY (experiment_id, environment_id, day, variation_key)
);

CREATE INDEX exposure_counts_daily_environment ON exposure_counts_daily (environment_id);

-- The events a project received per UTC day and type, as the rollups read
-- them: what the event volume is read from. Kept after retention drops the
-- raw rows.
CREATE TABLE event_counts_daily (
    project_id uuid   NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    day        date   NOT NULL,
    event_type text   NOT NULL CHECK (event_type IN ('evaluation_summary', 'alias', 'exposure')),
    count      bigint NOT NULL CHECK (count >= 0),
    PRIMARY KEY (project_id, day, event_type)
);
