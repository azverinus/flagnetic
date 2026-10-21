-- +goose Up

-- The instance's auth settings: one row, created with the defaults of a
-- fresh instance and changed by an instance admin. Durations are in whole
-- seconds.
CREATE TABLE auth_settings (
    id                                boolean PRIMARY KEY DEFAULT true CHECK (id),
    session_idle_timeout_seconds      bigint  NOT NULL CHECK (session_idle_timeout_seconds > 0),
    session_absolute_timeout_seconds  bigint  NOT NULL CHECK (session_absolute_timeout_seconds > 0),
    token_max_lifetime_seconds        bigint  NOT NULL CHECK (token_max_lifetime_seconds > 0),
    token_expiry_required             boolean NOT NULL,
    password_min_length               integer NOT NULL CHECK (password_min_length >= 12)
);

-- A day idle, thirty days in all, tokens for a year at most and without a
-- required expiry, passwords of twelve characters at least.
INSERT INTO auth_settings (session_idle_timeout_seconds, session_absolute_timeout_seconds, token_max_lifetime_seconds,
                           token_expiry_required, password_min_length)
VALUES (86400, 2592000, 31536000, false, 12);
