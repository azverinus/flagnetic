-- +goose Up

-- Password resets: the one-time links that let an active member set a new
-- password. Only the SHA-256 of the secret is stored. A row is kept once it
-- can no longer be completed, so a used link answers exactly like an unknown
-- one, until the retention worker deletes it.
CREATE TABLE password_resets (
    id           uuid        PRIMARY KEY,
    member_id    uuid        NOT NULL REFERENCES members (id),
    secret_hash  bytea       NOT NULL UNIQUE CHECK (length(secret_hash) = 32),
    -- The instance admin who issued it; null for one the member asked for
    -- themselves.
    issued_by    uuid        REFERENCES members (id),
    created_at   timestamptz NOT NULL,
    expires_at   timestamptz NOT NULL,
    -- When it was completed, or ended by the completion of another reset, a
    -- password change or its member's deactivation; null while it is open.
    consumed_at  timestamptz
);

CREATE INDEX password_resets_member ON password_resets (member_id, created_at);

-- The retention worker finds resets long past use by the time they ended.
CREATE INDEX password_resets_ended ON password_resets (LEAST(consumed_at, expires_at));
