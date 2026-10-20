# Migrations

The schema of the server, applied in order by `flagnetic migrate`.

- SQL files only, named `NNNNN_what_it_does.sql` with goose's `-- +goose Up`
  annotation. They are embedded in the binary.
- Use only what PostgreSQL 16 offers: it is the oldest supported version.
- Never read the database clock (`now()`, `current_timestamp` and their
  relatives), not even in a column default: every time is written by the
  application from its own clock, so that a test moving the clock moves all
  time at once.
- From the first public release on, a released migration is never edited:
  databases in the field have already applied it. A change is a new migration.
