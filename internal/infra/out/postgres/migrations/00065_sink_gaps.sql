-- +goose Up

-- A sink's gaps: the rows retention dropped before one of its streams had
-- delivered them, recorded in the drop's own transaction like a rollup's.
-- sink_id names the sink, which a rollup's gap (null) has none; a sink's gaps
-- go with it. A member sees a sink's gaps and dismisses them once seen, which
-- keeps them listed and stops them counting; dismissed_at is the first time.
ALTER TABLE event_gaps
    ADD COLUMN sink_id      uuid        REFERENCES sinks (id) ON DELETE CASCADE,
    ADD COLUMN dismissed_at timestamptz,
    ADD CONSTRAINT event_gaps_dismissed_by_sink CHECK (dismissed_at IS NULL OR sink_id IS NOT NULL);

-- A sink's gaps oldest first, and those not yet dismissed.
CREATE INDEX event_gaps_sink ON event_gaps (sink_id, period_start, id) WHERE sink_id IS NOT NULL;
CREATE INDEX event_gaps_sink_undismissed ON event_gaps (sink_id) WHERE sink_id IS NOT NULL AND dismissed_at IS NULL;
