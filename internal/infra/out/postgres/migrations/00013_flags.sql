-- +goose Up

-- The version of an environment's ruleset: every write that changes what an
-- SDK of the environment receives moves it on in its own transaction, so a
-- replica or an SDK holding an older version knows to fetch again. It starts
-- at 1 and only grows: the application sets it to the greater of the next
-- number and the time of the write in milliseconds, so a database restored
-- from an older backup still serves versions above any an SDK already holds.
ALTER TABLE environments ADD COLUMN ruleset_version bigint NOT NULL DEFAULT 1 CHECK (ruleset_version >= 1);

-- Flags: a flag's definition, the same in every environment of its project.
-- The key names the flag in code, so it allows capitals and dots and is
-- unique case sensitively; neither it nor the type ever changes.
CREATE TABLE flags (
    id                   uuid        PRIMARY KEY,
    project_id           uuid        NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    key                  text        NOT NULL CHECK (key ~ '^[a-zA-Z][a-zA-Z0-9_.-]{0,127}$'),
    type                 text        NOT NULL CHECK (type IN ('boolean', 'string', 'number', 'json')),
    name                 text        NOT NULL,
    description          text        NOT NULL,
    -- Members are never deleted, only deactivated, so an owner always
    -- resolves to someone to ask.
    owner_id             uuid        NOT NULL REFERENCES members (id),
    tags                 text[]      NOT NULL,
    lifetime             text        NOT NULL CHECK (lifetime IN ('permanent', 'temporary')),
    expected_removal_at  timestamptz CHECK ((lifetime = 'temporary') = (expected_removal_at IS NOT NULL)),
    automatic_exposure   boolean     NOT NULL,
    -- The ordered variations: [{key, value, name, description}].
    variations           jsonb       NOT NULL,
    archived_at          timestamptz,
    -- The member who created the flag; null when a project token did.
    created_by           uuid        REFERENCES members (id),
    created_at           timestamptz NOT NULL,
    updated_at           timestamptz NOT NULL,
    version              bigint      NOT NULL CHECK (version >= 1),
    UNIQUE (project_id, key)
);

-- Flag configurations: what one flag serves in one environment, one row for
-- every flag in every environment of its project, dropped with either. The
-- document is written and read whole; the GIN index answers which
-- configurations name a variation, a segment, a flag or a declaration
-- without reading them all.
CREATE TABLE flag_configurations (
    flag_id         uuid        NOT NULL REFERENCES flags (id) ON DELETE CASCADE,
    environment_id  uuid        NOT NULL REFERENCES environments (id) ON DELETE CASCADE,
    document        jsonb       NOT NULL,
    updated_at      timestamptz NOT NULL,
    version         bigint      NOT NULL CHECK (version >= 1),
    PRIMARY KEY (flag_id, environment_id)
);

CREATE INDEX flag_configurations_environment ON flag_configurations (environment_id);
CREATE INDEX flag_configurations_document ON flag_configurations USING gin (document jsonb_path_ops);
