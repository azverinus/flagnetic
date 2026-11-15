-- +goose Up

-- When the current streak of failed attempts began: the time of its first
-- failure, null while the last attempt succeeded. A subscription is disabled
-- for failing a day, counted from here and not from its last success, so a
-- subscription that had no event for a day is not disabled by the first
-- outage of its receiver.
ALTER TABLE webhook_subscriptions ADD COLUMN failing_since timestamptz;

-- A streak already running began no earlier than its last success or enabling,
-- the bound the subscription was judged by until now.
UPDATE webhook_subscriptions
SET failing_since = greatest(enabled_at, coalesce(last_success_at, enabled_at))
WHERE consecutive_failures > 0;

-- No constraint ties failing_since to consecutive_failures: the version
-- running while this one rolls out counts failures and enables subscriptions
-- without writing failing_since, and must not be refused for it.
