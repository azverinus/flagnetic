-- +goose Up

-- Sinks: a project's standing request to copy its raw events to a
-- ClickHouse database, an S3-compatible bucket or a signed webhook. A
-- project's go with it. The secret of the sink's kind (the ClickHouse
-- password, the object storage secret access key, the secret signing a
-- webhook sink's calls) is sealed under the instance's secret key, so a
-- database dump holds none of them; after a rotation the webhook secret it
-- replaced signs too until it expires.
CREATE TABLE sinks (
    id                         uuid        PRIMARY KEY,
    project_id                 uuid        NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    name                       text        NOT NULL CHECK (name <> ''),
    kind                       text        NOT NULL CHECK (kind IN ('clickhouse', 'object_storage', 'webhook')),
    -- The configuration of the sink's kind and no other, without its secret.
    config                     jsonb       NOT NULL CHECK (jsonb_typeof(config) = 'object'),
    secret_sealed              text        NOT NULL CHECK (secret_sealed <> ''),
    previous_secret_sealed     text,
    previous_secret_expires_at timestamptz,
    -- The kinds of event exported; empty for all of them.
    event_kinds                text[]      NOT NULL CHECK (event_kinds <@ ARRAY['exposure', 'alias', 'evaluation_summary']),
    -- Environment keys, which never change; empty for every environment.
    environments               text[]      NOT NULL,
    batch_size                 integer     NOT NULL CHECK (batch_size >= 1),
    flush_interval_seconds     integer     NOT NULL CHECK (flush_interval_seconds >= 1),
    enabled                    boolean     NOT NULL,
    stopped_at                 timestamptz,
    at_risk_since              timestamptz,
    warnings                   text[]      NOT NULL,
    -- The member or the project token that created the sink, at most one;
    -- neither is ever deleted.
    created_by_member_id       uuid        REFERENCES members (id),
    created_by_token_id        uuid        REFERENCES api_tokens (id),
    created_at                 timestamptz NOT NULL,
    updated_at                 timestamptz NOT NULL,
    version                    bigint      NOT NULL CHECK (version >= 1),
    CHECK ((previous_secret_sealed IS NULL) = (previous_secret_expires_at IS NULL)),
    CHECK (previous_secret_sealed IS NULL OR kind = 'webhook'),
    CHECK (created_by_member_id IS NULL OR created_by_token_id IS NULL)
);

-- A project's sinks oldest first.
CREATE INDEX sinks_project ON sinks (project_id, id);
CREATE INDEX sinks_created_by_member ON sinks (created_by_member_id) WHERE created_by_member_id IS NOT NULL;
CREATE INDEX sinks_created_by_token ON sinks (created_by_token_id) WHERE created_by_token_id IS NOT NULL;
