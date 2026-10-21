-- +goose Up

-- The member whose write made a configuration what it is; null when a
-- project token wrote it. Members are never deleted, only deactivated, so it
-- always resolves to someone to ask.
ALTER TABLE flag_configurations ADD COLUMN updated_by uuid REFERENCES members (id);
