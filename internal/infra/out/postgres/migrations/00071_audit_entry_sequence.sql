-- +goose Up

-- The order in which the entries of a project were committed. An entry's
-- identifier is minted from the clock of the replica that writes it, so the
-- order of identifiers is not the order of commits: a change committed later
-- through a replica whose clock is behind has the smaller identifier. Every
-- write of a project holds the project's row until it commits, so a number
-- drawn from this sequence while the row is held grows in commit order within
-- the project. It is what the state tag of an export and of a dry run reads.
-- Entries written before this column existed have none.
CREATE SEQUENCE audit_entry_seq;

ALTER TABLE audit_entries ADD COLUMN seq bigint;

CREATE INDEX audit_entries_project_seq ON audit_entries (project_id, seq DESC NULLS LAST);
