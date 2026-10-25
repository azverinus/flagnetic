-- +goose Up

-- Webhook subscriptions: a project's standing request to be sent chosen
-- events over signed HTTP, or the instance's, with no project, for the
-- audit entries that name none. A project's go with it. The secret signing
-- every call is sealed under the instance's secret key, so a database dump
-- holds no signing key; after a rotation the secret it replaced signs too
-- until it expires.
CREATE TABLE webhook_subscriptions (
    id                         uuid        PRIMARY KEY,
    project_id                 uuid        REFERENCES projects (id) ON DELETE CASCADE,
    name                       text        NOT NULL CHECK (name <> ''),
    url                        text        NOT NULL,
    events                     text[]      NOT NULL CHECK (cardinality(events) > 0),
    -- Environment keys, which never change; empty for every environment.
    environments               text[]      NOT NULL,
    enabled                    boolean     NOT NULL,
    disabled_reason            text        CHECK (disabled_reason IN ('persistent_failure')),
    secret_sealed              text        NOT NULL,
    previous_secret_sealed     text,
    previous_secret_expires_at timestamptz,
    -- The member or the project token that created the subscription, at
    -- most one; neither is ever deleted.
    created_by_member_id       uuid        REFERENCES members (id),
    created_by_token_id        uuid        REFERENCES api_tokens (id),
    last_success_at            timestamptz,
    consecutive_failures       integer     NOT NULL CHECK (consecutive_failures >= 0),
    enabled_at                 timestamptz NOT NULL,
    created_at                 timestamptz NOT NULL,
    updated_at                 timestamptz NOT NULL,
    version                    bigint      NOT NULL CHECK (version >= 1),
    -- A reason without the disabling would leave a notice nobody can clear.
    CHECK (disabled_reason IS NULL OR NOT enabled),
    CHECK ((previous_secret_sealed IS NULL) = (previous_secret_expires_at IS NULL)),
    CHECK (created_by_member_id IS NULL OR created_by_token_id IS NULL),
    -- An instance subscription takes every audit entry naming no project
    -- and nothing else.
    CHECK (project_id IS NOT NULL OR (events = '{audit.entry}' AND environments = '{}'))
);

-- A project's subscriptions oldest first, and the instance's.
CREATE INDEX webhook_subscriptions_project ON webhook_subscriptions (project_id, id);
CREATE INDEX webhook_subscriptions_instance ON webhook_subscriptions (id) WHERE project_id IS NULL;
CREATE INDEX webhook_subscriptions_created_by_member ON webhook_subscriptions (created_by_member_id)
    WHERE created_by_member_id IS NOT NULL;
CREATE INDEX webhook_subscriptions_created_by_token ON webhook_subscriptions (created_by_token_id)
    WHERE created_by_token_id IS NOT NULL;
