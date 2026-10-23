-- +goose Up

-- When the member last signed in; null until they do.
ALTER TABLE members ADD COLUMN last_sign_in_at timestamptz;

-- Sessions: a member's sign-in to the console, referred to by a cookie whose
-- random part is stored only as its SHA-256.
CREATE TABLE sessions (
    id            uuid        PRIMARY KEY,
    secret_hash   bytea       NOT NULL UNIQUE CHECK (length(secret_hash) = 32),
    member_id     uuid        NOT NULL REFERENCES members (id),
    method        text        NOT NULL CHECK (method IN ('local')),
    client_ip     inet,
    -- At most 256 bytes of the User-Agent header the session opened with.
    user_agent    text        NOT NULL,
    created_at    timestamptz NOT NULL,
    last_seen_at  timestamptz NOT NULL,
    -- The end the absolute timeout in force at sign-in gives the session.
    expires_at    timestamptz NOT NULL,
    revoked_at    timestamptz
);

CREATE INDEX sessions_member ON sessions (member_id);
