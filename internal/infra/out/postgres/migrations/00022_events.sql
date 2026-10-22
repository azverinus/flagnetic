-- +goose Up

-- The raw events SDKs post, and the evaluations the server answers itself,
-- as received: what reports and sinks are computed from. Each raw table is
-- partitioned by the UTC day of received_at, so retention drops a whole day
-- at once instead of deleting rows; a worker creates each day's partitions
-- ahead of time. Every row carries the inserting transaction's ID and a
-- sequence number: readers walk the rows in commit order by (xid, seq), and
-- a row of a transaction still open holds them back until it commits. Raw
-- rows reference no other table: deleting a project or an environment
-- leaves them until retention drops their day, rather than deleting from
-- every partition at once.
CREATE SEQUENCE event_seq;

-- One row per flag, variation and reason of an evaluation summary. source
-- says who counted the evaluations: an SDK reporting its own (sdk), or the
-- server answering a remote evaluation (remote_evaluation), so that the
-- evaluations a client SDK also counts from the answers it received are
-- never added twice. event_id is the summary's, null for the server's own.
CREATE TABLE evaluation_summary_events (
    xid               xid8        NOT NULL DEFAULT pg_current_xact_id(),
    seq               bigint      NOT NULL DEFAULT nextval('event_seq'),
    source            text        NOT NULL CHECK (source IN ('sdk', 'remote_evaluation')),
    event_id          uuid,
    project_id        uuid        NOT NULL,
    environment_id    uuid        NOT NULL,
    sdk_key_id        uuid        NOT NULL,
    flag_key          text        NOT NULL,
    -- Null when no variation was served.
    variation_key     text,
    evaluation_reason text        NOT NULL,
    count             bigint      NOT NULL CHECK (count > 0),
    period_start      timestamptz NOT NULL,
    period_end        timestamptz NOT NULL,
    sdk_name          text        NOT NULL,
    sdk_version       text        NOT NULL,
    platform          text,
    app_version       text,
    ruleset_version   bigint      NOT NULL,
    received_at       timestamptz NOT NULL,
    CONSTRAINT evaluation_summary_events_event_id CHECK ((source = 'sdk') = (event_id IS NOT NULL))
) PARTITION BY RANGE (received_at);

CREATE INDEX evaluation_summary_events_cursor ON evaluation_summary_events (xid, seq);
CREATE INDEX evaluation_summary_events_project ON evaluation_summary_events (project_id, received_at);

-- An alias links the unit ID in use before sign-up to the one after.
CREATE TABLE alias_events (
    xid            xid8        NOT NULL DEFAULT pg_current_xact_id(),
    seq            bigint      NOT NULL DEFAULT nextval('event_seq'),
    event_id       uuid        NOT NULL,
    project_id     uuid        NOT NULL,
    environment_id uuid        NOT NULL,
    sdk_key_id     uuid        NOT NULL,
    from_unit_type text        NOT NULL,
    from_unit_id   text        NOT NULL,
    to_unit_type   text        NOT NULL,
    to_unit_id     text        NOT NULL,
    sdk_name       text        NOT NULL,
    sdk_version    text        NOT NULL,
    platform       text,
    occurred_at    timestamptz NOT NULL,
    received_at    timestamptz NOT NULL
) PARTITION BY RANGE (received_at);

CREATE INDEX alias_events_cursor ON alias_events (xid, seq);
CREATE INDEX alias_events_project ON alias_events (project_id, received_at);

-- The event IDs received, per project and kind, so a retried event is
-- stored once. Not partitioned: an event retried on a later day than it was
-- first received must still find its ID. Rows older than the retention
-- window are purged by received_at.
CREATE TABLE evaluation_summary_dedup (
    project_id  uuid        NOT NULL,
    event_id    uuid        NOT NULL,
    received_at timestamptz NOT NULL,
    PRIMARY KEY (project_id, event_id)
);

CREATE INDEX evaluation_summary_dedup_received ON evaluation_summary_dedup (received_at);

CREATE TABLE alias_dedup (
    project_id  uuid        NOT NULL,
    event_id    uuid        NOT NULL,
    received_at timestamptz NOT NULL,
    PRIMARY KEY (project_id, event_id)
);

CREATE INDEX alias_dedup_received ON alias_dedup (received_at);
