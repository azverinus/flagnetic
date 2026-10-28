-- +goose Up

-- The weekly stale-flag digest slots each project has handled, so a slot is
-- sent once however many replicas tick it. A row is written in the
-- transaction that queues the digest's event, and also for a project with
-- nothing stale, so its slot is not reconsidered at every tick. A slot's
-- start is its name; rows older than five weeks are pruned.
CREATE TABLE stale_flag_digests (
    project_id uuid        NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    slot       timestamptz NOT NULL,
    PRIMARY KEY (project_id, slot)
);

-- Pruning finds the old slots without reading the table.
CREATE INDEX stale_flag_digests_slot ON stale_flag_digests (slot);
