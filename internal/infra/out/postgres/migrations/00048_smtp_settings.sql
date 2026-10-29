-- +goose Up

-- The SMTP server the instance mails through: one row once an instance admin
-- configures it, none before and again none after it is removed. The
-- password is sealed under the instance's secret key; a username and a
-- password are stored together or not at all.
CREATE TABLE smtp_settings (
    id              boolean     PRIMARY KEY DEFAULT true CHECK (id),
    host            text        NOT NULL CHECK (host <> ''),
    port            integer     NOT NULL CHECK (port BETWEEN 1 AND 65535),
    security        text        NOT NULL CHECK (security IN ('starttls', 'tls', 'none')),
    username        text        NOT NULL DEFAULT '',
    password_sealed text        NOT NULL DEFAULT '',
    sender          text        NOT NULL CHECK (sender <> ''),
    updated_at      timestamptz NOT NULL,
    CHECK ((username = '') = (password_sealed = ''))
);
