-- +goose Up

-- The outbox: catalogue events written in the transaction of the change
-- that caused them, so an event exists exactly when its change committed.
-- The fan-out is their one reader: it turns each into a delivery per
-- subscription wanting it and deletes it. A project's go with it.
CREATE TABLE webhook_outbox (
    id              uuid        PRIMARY KEY,
    type            text        NOT NULL,
    -- Null for an event of the instance.
    project_id      uuid        REFERENCES projects (id) ON DELETE CASCADE,
    -- The key of the environment the event happened in, null for none;
    -- subscriptions filter by key, and keys never change.
    environment     text,
    envelope        jsonb       NOT NULL,
    -- When the change happened, which a subscription created later never
    -- receives.
    created_at      timestamptz NOT NULL
);

-- One event sent, or to be sent, to one subscription, with every attempt.
-- The body is the envelope as sent, so a redelivery sends the same event. A
-- lease names the replica attempting the delivery by its token and runs
-- until lease_until, after which another replica may attempt it.
CREATE TABLE webhook_deliveries (
    id              uuid        PRIMARY KEY,
    subscription_id uuid        NOT NULL REFERENCES webhook_subscriptions (id) ON DELETE CASCADE,
    event_id        uuid        NOT NULL,
    event_type      text        NOT NULL,
    body            jsonb       NOT NULL,
    status          text        NOT NULL CHECK (status IN ('pending', 'succeeded', 'failed', 'exhausted', 'dropped')),
    attempts        jsonb       NOT NULL,
    next_attempt_at timestamptz,
    lease_until     timestamptz,
    lease_token     uuid,
    created_at      timestamptz NOT NULL,
    updated_at      timestamptz NOT NULL,
    -- Only a pending delivery is due, and only a pending one is leased.
    CHECK ((status = 'pending') = (next_attempt_at IS NOT NULL)),
    CHECK (status = 'pending' OR (lease_until IS NULL AND lease_token IS NULL)),
    CHECK ((lease_until IS NULL) = (lease_token IS NULL))
);

-- The pending deliveries by when they are due, for the delivery worker.
CREATE INDEX webhook_deliveries_due ON webhook_deliveries (next_attempt_at) WHERE status = 'pending';
-- A subscription's delivery log, newest first.
CREATE INDEX webhook_deliveries_log ON webhook_deliveries (subscription_id, id DESC);
