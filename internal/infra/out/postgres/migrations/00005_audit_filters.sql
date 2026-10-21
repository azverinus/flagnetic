-- +goose Up

-- The audit log is read newest first, one page at a time, by project or for
-- the whole instance, and filtered. The filters that narrow it most (project,
-- member, action, entity, environment) are each answered from an index
-- ordered like the log, so such a page costs the same however long the log
-- grows; the others (actor kind, entity kind, period) only narrow further
-- what one of these or the log's own order yields.
CREATE INDEX audit_entries_project_id ON audit_entries (project_id, id DESC);
CREATE INDEX audit_entries_actor_member_id ON audit_entries (actor_member_id, id DESC);
CREATE INDEX audit_entries_action ON audit_entries (action, id DESC);
CREATE INDEX audit_entries_entity ON audit_entries (entity_id, entity_kind, id DESC);
CREATE INDEX audit_entries_environment_id ON audit_entries (environment_id, id DESC);

-- Free-text search finds a substring of the action, the entity's key or the
-- actor's name through trigrams, which a leading wildcard cannot use a
-- B-tree for.
--
-- The extension may live in a schema outside the search path (another
-- schema of the same database created it first), so its operator class is
-- named through the schema it is in.
-- +goose StatementBegin
DO $$
DECLARE
    trgm_schema text;
BEGIN
    CREATE EXTENSION IF NOT EXISTS pg_trgm;
    SELECT n.nspname INTO trgm_schema
    FROM pg_extension e JOIN pg_namespace n ON n.oid = e.extnamespace
    WHERE e.extname = 'pg_trgm';
    EXECUTE format(
        'CREATE INDEX audit_entries_search ON audit_entries '
        'USING gin ((entity_key || E''\n'' || actor_name || E''\n'' || action) %I.gin_trgm_ops)',
        trgm_schema);
END
$$;
-- +goose StatementEnd
