-- +goose Up

-- One link per environment and pair of unit IDs, written as aliases are
-- received: what lets an analyst join an anonymous ID to the ID known after
-- sign-up once the raw alias events are dropped. A pair sent again (a user
-- signing in again on the same device) adds no row; the row keeps the alias
-- that occurred earliest. Kept for as long as the environment exists.
CREATE TABLE alias_links (
    environment_id uuid        NOT NULL REFERENCES environments (id) ON DELETE CASCADE,
    from_unit_type text        NOT NULL,
    from_unit_id   text        NOT NULL,
    to_unit_type   text        NOT NULL,
    to_unit_id     text        NOT NULL,
    event_id       uuid        NOT NULL,
    occurred_at    timestamptz NOT NULL,
    received_at    timestamptz NOT NULL,
    PRIMARY KEY (environment_id, from_unit_type, from_unit_id, to_unit_type, to_unit_id)
);
