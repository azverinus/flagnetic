-- +goose Up

-- The deliveries the retention round may prune, oldest first: those no
-- longer pending, by when they were made. A pending delivery is kept
-- whatever its age, so it is left out of the index.
CREATE INDEX webhook_deliveries_retention ON webhook_deliveries (created_at) WHERE status <> 'pending';
