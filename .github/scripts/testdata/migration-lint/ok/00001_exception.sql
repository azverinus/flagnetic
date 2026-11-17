-- +goose Up
-- The column was never read by any release, so dropping it breaks no replica.
-- squawk-ignore ban-drop-column
ALTER TABLE flags DROP COLUMN never_read;
