-- +goose Up

-- The order in which the entries of a project were committed. An entry's
-- identifier is minted from the clock of the replica that writes it, so the
-- order of identifiers is not the order of commits: a change committed later
-- through a replica whose clock is behind has the smaller identifier. Every
-- write of what a project's document holds (its flags, segments, environments,
-- unit types, attributes and experiments) holds the project's row until it
-- commits, so the number those writes draw from this sequence while the row is
-- held grows in commit order within the project. It is what the state tag of an
-- export and of a dry run reads. Entries written before this column existed
-- have none.
CREATE SEQUENCE audit_entry_seq;

ALTER TABLE audit_entries ADD COLUMN seq bigint;

-- A default, not a value the insert names, so that a replica of the previous
-- minor, whose insert does not know the column, numbers its entries too:
-- otherwise a change written through it during a rolling upgrade would not
-- move the tag. Set apart from ADD COLUMN, a volatile default fills no
-- existing row and rewrites nothing.
ALTER TABLE audit_entries ALTER COLUMN seq SET DEFAULT nextval('audit_entry_seq');

ALTER SEQUENCE audit_entry_seq OWNED BY audit_entries.seq;
