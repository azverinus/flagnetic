-- +goose Up

-- Audit entries: who changed what, when, from which value to which. The
-- project has no foreign key, since entries outlive the project they record.
CREATE TABLE audit_entries (
    id               uuid        PRIMARY KEY,
    occurred_at      timestamptz NOT NULL,
    project_id       uuid,
    environment_id   uuid,
    actor_kind       text        NOT NULL CHECK (actor_kind IN ('member', 'token', 'cli', 'system')),
    actor_id         text        NOT NULL,
    actor_name       text        NOT NULL,
    actor_member_id  uuid,
    action           text        NOT NULL,
    entity_kind      text        NOT NULL,
    entity_id        text        NOT NULL,
    entity_key       text        NOT NULL,
    before           jsonb,
    after            jsonb,
    request_id       text
);
