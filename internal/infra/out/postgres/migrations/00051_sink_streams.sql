-- +goose Up

-- Sink streams: the delivery of one event kind of one sink. A stream reads the
-- raw events of the sink's project past its cursor, in batches, below the
-- snapshot horizon, which no transaction still open can commit behind, and moves the cursor only when the target
-- acknowledged the batch. A stream is created when its sink is first
-- enabled, or an event kind is added to it, and goes with the sink.
CREATE TABLE sink_streams (
    sink_id                  uuid        NOT NULL REFERENCES sinks (id) ON DELETE CASCADE,
    event_kind               text        NOT NULL CHECK (event_kind IN ('exposure', 'alias', 'evaluation_summary')),
    -- The position of the last row delivered or passed.
    cursor_xid               xid8        NOT NULL,
    cursor_seq               bigint      NOT NULL,
    -- The batch being delivered: its upper bound and the environments it is
    -- filtered to (empty for every one) are written before the first send, and
    -- every retry on any replica re-reads exactly that range, so a retry
    -- carries the same delivery id.
    inflight_xid             xid8,
    inflight_seq             bigint,
    inflight_environments    uuid[],
    -- Rejections of the batch in flight, counted so that a batch the target
    -- keeps refusing can be set aside.
    inflight_rejections      integer     NOT NULL DEFAULT 0 CHECK (inflight_rejections >= 0),
    -- The lease on the stream: which replica's attempt it is, until when. The
    -- outcome of an attempt is written only while the token matches.
    lease_token              text,
    lease_until              timestamptz,
    next_attempt_at          timestamptz NOT NULL,
    consecutive_failures     integer     NOT NULL DEFAULT 0 CHECK (consecutive_failures >= 0),
    -- Why the last call failed, by class, status and code, never the target's
    -- words; null once a batch is acknowledged.
    last_failure_class       text,
    last_failure_status      integer,
    last_failure_code        text,
    last_failure_at          timestamptz,
    last_delivered_at        timestamptz,
    delivered_total          bigint      NOT NULL DEFAULT 0 CHECK (delivered_total >= 0),
    consecutive_dead_letters integer     NOT NULL DEFAULT 0 CHECK (consecutive_dead_letters >= 0),
    -- A replay's position, applied before the stream's next batch, and the
    -- positions a bounded replay stops and resumes at.
    pending_cursor_xid       xid8,
    pending_cursor_seq       bigint,
    replay_stop_xid          xid8,
    replay_stop_seq          bigint,
    replay_resume_xid        xid8,
    replay_resume_seq        bigint,
    created_at               timestamptz NOT NULL,
    PRIMARY KEY (sink_id, event_kind),
    CHECK ((inflight_xid IS NULL) = (inflight_seq IS NULL)),
    CHECK ((inflight_xid IS NULL) = (inflight_environments IS NULL)),
    CHECK ((lease_token IS NULL) = (lease_until IS NULL)),
    CHECK ((pending_cursor_xid IS NULL) = (pending_cursor_seq IS NULL)),
    CHECK ((replay_stop_xid IS NULL) = (replay_stop_seq IS NULL)),
    CHECK ((replay_resume_xid IS NULL) = (replay_resume_seq IS NULL)),
    CHECK ((last_failure_class IS NULL) = (last_failure_at IS NULL))
);

-- What the worker claims: the streams due, oldest first.
CREATE INDEX sink_streams_due ON sink_streams (next_attempt_at);
