-- +goose Up

-- Invitations: the one-time links that let an invited member set their
-- first password. Only the SHA-256 of the secret is stored. A row is kept
-- once it can no longer be accepted, so a used link answers exactly like an
-- unknown one, until the retention worker deletes it.
CREATE TABLE invitations (
    id           uuid        PRIMARY KEY,
    member_id    uuid        NOT NULL REFERENCES members (id),
    secret_hash  bytea       NOT NULL UNIQUE CHECK (length(secret_hash) = 32),
    created_at   timestamptz NOT NULL,
    expires_at   timestamptz NOT NULL,
    -- When it was accepted, replaced, or consumed by its member's
    -- deactivation or password change; null while it is open.
    consumed_at  timestamptz
);

CREATE INDEX invitations_member_idx ON invitations (member_id, created_at);
