-- +goose Up

-- The version of a provider's group mappings, moved on by every replacement
-- that changes the list. It is apart from the provider's own version: an edit
-- of the provider must not refuse a replacement of its mappings, and the
-- reverse. A write of the list names the version it was based on, so an admin
-- who read the list before another changed it cannot put back what was
-- removed, or revoke what was just granted.
ALTER TABLE auth_providers ADD COLUMN group_mappings_version bigint NOT NULL DEFAULT 1 CHECK (group_mappings_version >= 1);
