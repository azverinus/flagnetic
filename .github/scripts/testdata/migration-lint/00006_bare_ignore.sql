-- +goose Up
-- squawk-ignore ban-drop-column
ALTER TABLE flags DROP COLUMN never_read;
