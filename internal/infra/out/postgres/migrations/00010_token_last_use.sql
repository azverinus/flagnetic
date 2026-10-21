-- +goose Up

-- When an API token was last presented and from which address, written at
-- most once a minute; null until it is first used.
ALTER TABLE api_tokens
    ADD COLUMN last_used_at timestamptz,
    ADD COLUMN last_used_ip inet;
