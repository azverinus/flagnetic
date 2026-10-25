-- +goose Up

-- The rows a stream lost because retention dropped their partition while its
-- cursor still pointed before them, recorded in the drop's own transaction.
-- stream names the reader: a rollup stream, so the loss is explicit rather
-- than silent and the cursor moves to just before the first row still
-- stored. event_type is the raw table the rows were in; from and to are the
-- cursor range lost, period_start and period_end when the first and the last
-- row lost were received. Kept forever, like the aggregates the rows would
-- have fed.
CREATE TABLE event_gaps (
    id           uuid        PRIMARY KEY,
    stream       text        NOT NULL,
    event_type   text        NOT NULL CHECK (event_type IN ('evaluation_summary', 'alias', 'exposure')),
    from_xid     xid8        NOT NULL,
    from_seq     bigint      NOT NULL,
    to_xid       xid8        NOT NULL,
    to_seq       bigint      NOT NULL,
    period_start timestamptz NOT NULL,
    period_end   timestamptz NOT NULL,
    rows         bigint      NOT NULL CHECK (rows > 0),
    dropped_at   timestamptz NOT NULL,
    CHECK (period_start <= period_end)
);

CREATE INDEX event_gaps_period ON event_gaps (period_start, period_end);
