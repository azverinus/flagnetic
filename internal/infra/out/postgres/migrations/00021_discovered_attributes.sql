-- +goose Up

-- The attributes evaluated contexts carried that a project does not
-- declare: the key and the JSON types seen, never a value. At most 500 per
-- project, dismissed ones included, which the application keeps.
CREATE TABLE discovered_attributes (
    project_id     uuid        NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    key            text        NOT NULL CHECK (key ~ '^[a-z][a-z0-9_-]{0,63}$'),
    -- In the order first seen.
    observed_types text[]      NOT NULL,
    first_seen     timestamptz NOT NULL,
    last_seen      timestamptz NOT NULL,
    count          bigint      NOT NULL CHECK (count > 0),
    dismissed      boolean     NOT NULL,
    PRIMARY KEY (project_id, key)
);
