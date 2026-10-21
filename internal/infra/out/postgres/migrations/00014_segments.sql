-- +goose Up

-- Segments: reusable, named sets of attribute conditions a project's rules
-- target. The key names the segment in rules and never changes. The
-- conditions are written and read whole; the GIN index answers which
-- segments name an attribute or unit type without reading them all.
CREATE TABLE segments (
    id           uuid        PRIMARY KEY,
    project_id   uuid        NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    key          text        NOT NULL CHECK (key ~ '^[a-z][a-z0-9_-]{0,63}$'),
    name         text        NOT NULL,
    description  text        NOT NULL,
    private      boolean     NOT NULL,
    -- The ordered conditions: [{kind, attribute, op, value}].
    conditions   jsonb       NOT NULL,
    created_at   timestamptz NOT NULL,
    updated_at   timestamptz NOT NULL,
    version      bigint      NOT NULL CHECK (version >= 1),
    UNIQUE (project_id, key)
);

CREATE INDEX segments_conditions ON segments USING gin (conditions jsonb_path_ops);
