-- +goose Up

-- Experiments: a time-boxed comparison of variations of one flag on a
-- declared unit type, with the two salts that make its assignment
-- independent of every other experiment's and rollout's. The key names the
-- experiment in rules and in SDK exposures and never changes. A flag's
-- experiments are dropped with the flag, since an experiment means nothing
-- without the variations it compares.
CREATE TABLE experiments (
    id                  uuid        PRIMARY KEY,
    project_id          uuid        NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    key                 text        NOT NULL CHECK (key ~ '^[a-zA-Z][a-zA-Z0-9_.-]{0,127}$'),
    name                text        NOT NULL,
    hypothesis          text        NOT NULL,
    -- Members are never deleted, only deactivated, so an owner always
    -- resolves to someone to ask.
    owner_id            uuid        NOT NULL REFERENCES members (id),
    tags                text[]      NOT NULL,
    flag_id             uuid        NOT NULL REFERENCES flags (id) ON DELETE CASCADE,
    unit_type           text        NOT NULL,
    -- The ordered weights, in basis points: [{variation, weight}]. Their
    -- order fixes the cumulative bucket ranges.
    variations          jsonb       NOT NULL,
    traffic_allocation  integer     NOT NULL CHECK (traffic_allocation BETWEEN 0 AND 10000),
    inclusion_salt      text        NOT NULL CHECK (inclusion_salt ~ '^[0-9a-f]{16}$'),
    variation_salt      text        NOT NULL CHECK (variation_salt ~ '^[0-9a-f]{16}$' AND variation_salt <> inclusion_salt),
    status              text        NOT NULL CHECK (status IN ('draft', 'running', 'stopped', 'archived')),
    expected_end_at     date,
    started_at          timestamptz,
    stopped_at          timestamptz,
    archived_at         timestamptz,
    -- The member who created the experiment; null when a project token did.
    created_by          uuid        REFERENCES members (id),
    created_at          timestamptz NOT NULL,
    updated_at          timestamptz NOT NULL,
    version             bigint      NOT NULL CHECK (version >= 1),
    UNIQUE (project_id, key)
);

-- A salt is used by one experiment only; the application also keeps it
-- apart from every rollout's, which this index cannot see.
CREATE UNIQUE INDEX experiments_inclusion_salt ON experiments (project_id, inclusion_salt);
CREATE UNIQUE INDEX experiments_variation_salt ON experiments (project_id, variation_salt);
CREATE INDEX experiments_flag ON experiments (flag_id);
