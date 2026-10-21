-- +goose Up

-- Projects: the isolation boundary for flags, experiments, segments,
-- environments and grants. The key names the project in URLs and SDK calls
-- and never changes.
CREATE TABLE projects (
    id           uuid        PRIMARY KEY,
    key          text        NOT NULL UNIQUE CHECK (key ~ '^[a-z][a-z0-9_-]{0,63}$'),
    name         text        NOT NULL,
    description  text        NOT NULL,
    created_at   timestamptz NOT NULL,
    updated_at   timestamptz NOT NULL
);

-- Environments: the deployment targets of a project, listed by sort order.
CREATE TABLE environments (
    id          uuid        PRIMARY KEY,
    project_id  uuid        NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    key         text        NOT NULL CHECK (key ~ '^[a-z][a-z0-9_-]{0,63}$'),
    name        text        NOT NULL,
    sort_order  integer     NOT NULL,
    created_at  timestamptz NOT NULL,
    updated_at  timestamptz NOT NULL,
    UNIQUE (project_id, key)
);

-- Unit types and attributes: what a project's targeting refers to. A key is
-- unique across both within a project, so a rule names one thing by it; the
-- handlers keep that under the project's row lock, since no constraint
-- spans two tables.
CREATE TABLE unit_types (
    id           uuid        PRIMARY KEY,
    project_id   uuid        NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    key          text        NOT NULL CHECK (key ~ '^[a-z][a-z0-9_-]{0,63}$'),
    name         text        NOT NULL,
    description  text        NOT NULL,
    created_at   timestamptz NOT NULL,
    updated_at   timestamptz NOT NULL,
    UNIQUE (project_id, key)
);

CREATE TABLE attributes (
    id           uuid        PRIMARY KEY,
    project_id   uuid        NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    key          text        NOT NULL CHECK (key ~ '^[a-z][a-z0-9_-]{0,63}$'),
    type         text        NOT NULL CHECK (type IN ('string', 'number', 'boolean', 'semver', 'date', 'list')),
    name         text        NOT NULL,
    description  text        NOT NULL,
    -- Standard attributes are predeclared in every project and cannot be
    -- deleted, so SDKs can rely on them.
    standard     boolean     NOT NULL,
    created_at   timestamptz NOT NULL,
    updated_at   timestamptz NOT NULL,
    UNIQUE (project_id, key)
);

-- A project token belongs to one project and dies with it.
ALTER TABLE api_tokens
    ADD CONSTRAINT api_tokens_project_fkey FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE;
