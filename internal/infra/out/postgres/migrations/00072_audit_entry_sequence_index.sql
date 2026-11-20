-- +goose NO TRANSACTION
-- +goose Up

-- The newest commit of a project, which the state tag reads. Built
-- concurrently, outside a transaction: every write appends an audit entry, so
-- a plain build would stop all writes for as long as it scans the table.
CREATE INDEX CONCURRENTLY audit_entries_project_seq ON audit_entries (project_id, seq DESC NULLS LAST);
