-- +goose Up

-- The time a stream started from a time reads from: no row received before
-- it is sent. A stream starting from a time starts no later than the snapshot
-- horizon, so that a transaction still open then is read once it commits; any
-- transaction open anywhere in the database holds that horizon back, so the
-- stream reads rows received before its start too, and its cursor alone
-- cannot leave them out. Null for a stream that sends every row past its
-- cursor, as every existing one does.
ALTER TABLE sink_streams ADD COLUMN received_floor timestamptz;

-- The floor the set-aside batch was read with, which a redelivery reads its
-- rows with again, so that it sends no row the batch left out. Null when the
-- batch was read with none.
ALTER TABLE sink_dead_letters ADD COLUMN received_floor timestamptz;
