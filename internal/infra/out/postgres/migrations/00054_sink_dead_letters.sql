-- +goose Up

-- Sink dead letters: a batch the target rejected over and over, set aside so
-- that its stream could go on. It keeps the range of rows and the
-- environments the batch was filtered to, so that the rows still stored can
-- be sent again, and how the batch ended; it goes with its sink.
CREATE TABLE sink_dead_letters (
    id                   uuid        PRIMARY KEY,
    sink_id              uuid        NOT NULL REFERENCES sinks (id) ON DELETE CASCADE,
    event_kind           text        NOT NULL CHECK (event_kind IN ('exposure', 'alias', 'evaluation_summary')),
    -- The rows of the batch: past the first position, up to and including the
    -- second.
    from_xid             xid8        NOT NULL,
    from_seq             bigint      NOT NULL,
    to_xid               xid8        NOT NULL,
    to_seq               bigint      NOT NULL,
    environments         uuid[]      NOT NULL,
    -- What the batch held when it was set aside, told to a member in place of
    -- the positions, which are never exposed.
    rows                 integer     NOT NULL CHECK (rows >= 0),
    first_received_at    timestamptz NOT NULL,
    last_received_at     timestamptz NOT NULL,
    -- How many times the target rejected the batch, a failed redelivery
    -- included, and why the last time, by class, status and code, never the
    -- target's words.
    attempts             integer     NOT NULL CHECK (attempts >= 1),
    last_failure_class   text        NOT NULL,
    last_failure_status  integer,
    last_failure_code    text,
    last_failure_at      timestamptz NOT NULL,
    status               text        NOT NULL CHECK (status IN ('pending', 'redelivered', 'expired')),
    created_at           timestamptz NOT NULL,
    redelivered_at       timestamptz,
    CHECK ((status = 'redelivered') = (redelivered_at IS NOT NULL))
);

-- A sink's dead letters oldest first, and those still waiting.
CREATE INDEX sink_dead_letters_sink ON sink_dead_letters (sink_id, created_at, id);
CREATE INDEX sink_dead_letters_pending ON sink_dead_letters (sink_id) WHERE status = 'pending';
