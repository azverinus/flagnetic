-- +goose Up
-- The shape the rule asks for: only additions, which the previous minor ignores.
CREATE TABLE flag_owners (
    flag_id text NOT NULL,
    owner   text NOT NULL,
    PRIMARY KEY (flag_id)
);
ALTER TABLE flags ADD COLUMN owner_note text;
