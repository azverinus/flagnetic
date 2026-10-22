-- +goose Up

-- The SDKs that use each environment: one row per key, SDK name and
-- version, platform and app version, fed by every kind of SDK request.
-- Absent parts are stored as '' so that they take part in the key. A row is
-- written at most once a minute and deleted once its SDK has not been seen
-- for the retention.
CREATE TABLE sdk_inventory (
    environment_id       uuid        NOT NULL REFERENCES environments (id) ON DELETE CASCADE,
    sdk_key_id           uuid        NOT NULL REFERENCES sdk_keys (id) ON DELETE CASCADE,
    sdk_name             text        NOT NULL,
    sdk_version          text        NOT NULL,
    platform             text        NOT NULL,
    app_version          text        NOT NULL,
    first_seen_at        timestamptz NOT NULL,
    last_seen_at         timestamptz NOT NULL,
    -- Null until the SDK fetched or was sent a ruleset.
    last_ruleset_version bigint,
    PRIMARY KEY (environment_id, sdk_key_id, sdk_name, sdk_version, platform, app_version)
);

CREATE INDEX sdk_inventory_seen ON sdk_inventory (environment_id, last_seen_at DESC);
CREATE INDEX sdk_inventory_last_seen ON sdk_inventory (last_seen_at);
CREATE INDEX sdk_inventory_key ON sdk_inventory (sdk_key_id);
