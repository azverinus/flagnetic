-- +goose Up
-- Every statement below breaks a replica of the previous minor or blocks it:
-- the lint must fail on this file. It lives outside the migrations directory,
-- so it is never applied.
ALTER TABLE flags RENAME COLUMN name TO title;
ALTER TABLE flags DROP COLUMN description;
ALTER TABLE flags ADD COLUMN owner text NOT NULL;
CREATE INDEX flags_owner ON flags (owner);
