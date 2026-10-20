-- +goose Up
-- A comment naming now() is not a read of the clock.
/* Nor is a block comment naming
   current_timestamp. */
CREATE TABLE stamped (
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    seen_at timestamptz DEFAULT clock_timestamp ()
);
SELECT localtimestamp, transaction_timestamp(), statement_timestamp(), current_date, current_time, localtime;
