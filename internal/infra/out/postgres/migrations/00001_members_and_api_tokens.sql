-- +goose Up

-- Members: the people who use the management API and the console. There is
-- no deletion; an offboarded member is deactivated and keeps their history.
CREATE TABLE members (
    id              uuid        PRIMARY KEY,
    email           text        NOT NULL,
    display_name    text        NOT NULL,
    status          text        NOT NULL CHECK (status IN ('invited', 'active', 'deactivated')),
    instance_admin  boolean     NOT NULL,
    -- The console language the member chose; null follows the browser.
    locale          text,
    -- An argon2id PHC string; null until the member sets a password.
    password_hash   text,
    created_at      timestamptz NOT NULL,
    updated_at      timestamptz NOT NULL
);

-- One member per address, whatever its case.
CREATE UNIQUE INDEX members_email_key ON members (lower(email));

-- API tokens: a personal access token acts for its member; a project token
-- holds one role in one project and acts for no member. Only the SHA-256 of
-- the value is stored.
CREATE TABLE api_tokens (
    id          uuid        PRIMARY KEY,
    kind        text        NOT NULL CHECK (kind IN ('personal', 'project')),
    member_id   uuid        REFERENCES members (id),
    project_id  uuid,
    role        text        CHECK (role IN ('owner', 'maintainer', 'developer', 'viewer')),
    name        text        NOT NULL,
    hint        text        NOT NULL,
    token_hash  bytea       NOT NULL UNIQUE CHECK (length(token_hash) = 32),
    scopes      text[]      NOT NULL,
    expires_at  timestamptz,
    revoked_at  timestamptz,
    created_at  timestamptz NOT NULL,
    CHECK (
        (kind = 'personal' AND member_id IS NOT NULL AND project_id IS NULL AND role IS NULL)
        OR (kind = 'project' AND member_id IS NULL AND project_id IS NOT NULL AND role IS NOT NULL)
    )
);

CREATE INDEX api_tokens_member ON api_tokens (member_id) WHERE member_id IS NOT NULL;
