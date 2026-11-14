-- +goose Up

-- The time a bounded replay of a stream reads its window from: no row received
-- before it is sent. The replay starts no later than the snapshot horizon, so
-- that a transaction still open is read once it commits, and its rows between
-- the horizon and the first one received in the window are read too; the
-- cursor alone cannot leave them out, since the open transaction can have a
-- lower ID than rows received before the window. Null when no bounded replay
-- runs. The stream's own floor stays for the live stream the replay returns to.
ALTER TABLE sink_streams ADD COLUMN replay_floor timestamptz;
