-- +goose Up

-- Projects, environments, unit types and attributes count their accepted
-- changes, as flags, configurations and segments do, so every write of them
-- names the version it was based on and two people editing the same one
-- never silently overwrite each other. Existing rows start at 1; the
-- default is dropped afterwards so every insert names its version, as the
-- application decides it.
ALTER TABLE projects ADD COLUMN version bigint NOT NULL DEFAULT 1 CHECK (version >= 1);
ALTER TABLE projects ALTER COLUMN version DROP DEFAULT;
ALTER TABLE environments ADD COLUMN version bigint NOT NULL DEFAULT 1 CHECK (version >= 1);
ALTER TABLE environments ALTER COLUMN version DROP DEFAULT;
ALTER TABLE unit_types ADD COLUMN version bigint NOT NULL DEFAULT 1 CHECK (version >= 1);
ALTER TABLE unit_types ALTER COLUMN version DROP DEFAULT;
ALTER TABLE attributes ADD COLUMN version bigint NOT NULL DEFAULT 1 CHECK (version >= 1);
ALTER TABLE attributes ALTER COLUMN version DROP DEFAULT;
