# Migrations

The schema of the server, applied in order by `flagnetic migrate`.

- SQL files only, named `NNNNN_what_it_does.sql` with goose's `-- +goose Up`
  annotation. They are embedded in the binary.
- Use only what PostgreSQL 16 offers: it is the oldest supported version.
- Never read the database clock (`now()`, `current_timestamp` and their
  relatives), not even in a column default: every time is written by the
  application from its own clock, so that a test moving the clock moves all
  time at once.
- Migrations are append-only from v0.1.0 on: a released migration is never
  edited, renamed or removed, since databases in the field have already
  applied it and would never see the change. A change is a new migration.
- A migration runs forward only and loses no data. A change that would break
  the running version (renaming or dropping a column or table, tightening a
  constraint on existing rows) goes expand/contract: one release adds the new
  shape and writes both, a later one removes the old shape once nothing reads
  it.
- Name an index `<table>_<what it serves>`, without a type suffix; the schema
  tests check it.
