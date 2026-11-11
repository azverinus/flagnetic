-- +goose Up

-- Streams remember where they stopped as the transaction ID of a row they
-- read, and a transaction ID counts the transactions of one PostgreSQL
-- cluster. A logical dump or logical replication moves rows and cursors into
-- a cluster whose counter starts from the beginning, so every new row would
-- lie behind every cursor. This row ties the rows to the cluster that
-- numbered them: xid_offset is added to every transaction ID of the current
-- cluster, so that the rows it receives come after everything stored from
-- another one. The server sets the offset at startup when
-- system_identifier is not the cluster's own, and never lowers it.
CREATE TABLE event_cluster (
    singleton         boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    system_identifier bigint  NOT NULL,
    xid_offset        bigint  NOT NULL CHECK (xid_offset >= 0)
);

INSERT INTO event_cluster (system_identifier, xid_offset) SELECT system_identifier, 0 FROM pg_control_system();

-- +goose StatementBegin
-- The ID the current transaction gives a raw event: its own, moved by the
-- offset.
CREATE FUNCTION event_xid() RETURNS xid8 LANGUAGE sql STABLE AS $$
    SELECT (pg_current_xact_id()::text::bigint + xid_offset)::text::xid8 FROM event_cluster
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- The ID below which every transaction has ended, in the same numbering as
-- the raw events': a stream reads only rows under it.
CREATE FUNCTION event_horizon() RETURNS xid8 LANGUAGE sql STABLE AS $$
    SELECT (pg_snapshot_xmin(pg_current_snapshot())::text::bigint + xid_offset)::text::xid8 FROM event_cluster
$$;
-- +goose StatementEnd

-- Changing a default is a change of the catalog alone: no row is rewritten
-- and no index rebuilt, so each table is locked for an instant.
ALTER TABLE evaluation_summary_events ALTER COLUMN xid SET DEFAULT event_xid();
ALTER TABLE alias_events ALTER COLUMN xid SET DEFAULT event_xid();
ALTER TABLE exposure_events ALTER COLUMN xid SET DEFAULT event_xid();
