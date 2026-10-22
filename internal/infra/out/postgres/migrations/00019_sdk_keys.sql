-- +goose Up

-- The SDK keys of each environment. Only the SHA-256 of a key's value is
-- stored, and key_id, the first 8 characters after its prefix, so a member
-- can tell which key a service holds. A key ends with its environment.
CREATE TABLE sdk_keys (
    id              uuid        PRIMARY KEY,
    environment_id  uuid        NOT NULL REFERENCES environments (id) ON DELETE CASCADE,
    kind            text        NOT NULL CHECK (kind IN ('server', 'client')),
    name            text        NOT NULL,
    key_id          text        NOT NULL,
    key_hash        bytea       NOT NULL UNIQUE,
    -- Normalized origins a client key is accepted from; empty allows any.
    allowed_origins text[]      NOT NULL,
    created_at      timestamptz NOT NULL,
    -- Written at most once a minute; null until the key is first used.
    last_used_at    timestamptz,
    -- The end of the rotation overlap of a rotated key.
    expires_at      timestamptz,
    revoked_at      timestamptz,
    CONSTRAINT sdk_keys_server_without_origins CHECK (kind = 'client' OR cardinality(allowed_origins) = 0)
);

CREATE INDEX sdk_keys_environment ON sdk_keys (environment_id, created_at, id);

-- Every replica keeps the keys it has authenticated, by hash. A change to
-- what decides whether a key is accepted (its expiry, revocation or allowed
-- origins), or the key's deletion, a cascade from its environment or
-- project included, notifies the key's identifier on sdk_keys once the
-- transaction commits, and every replica drops that key. A new key needs no
-- notification: an unknown key is never kept.
-- +goose StatementBegin
CREATE FUNCTION sdk_keys_notify() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_notify('sdk_keys', OLD.id::text);
    RETURN NULL;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER sdk_keys_changed
    AFTER UPDATE ON sdk_keys
    FOR EACH ROW
    WHEN (OLD.expires_at IS DISTINCT FROM NEW.expires_at
          OR OLD.revoked_at IS DISTINCT FROM NEW.revoked_at
          OR OLD.allowed_origins IS DISTINCT FROM NEW.allowed_origins)
    EXECUTE FUNCTION sdk_keys_notify();

CREATE TRIGGER sdk_keys_deleted
    AFTER DELETE ON sdk_keys
    FOR EACH ROW
    EXECUTE FUNCTION sdk_keys_notify();
