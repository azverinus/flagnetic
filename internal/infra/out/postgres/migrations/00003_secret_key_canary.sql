-- +goose Up

-- The canary: one value sealed under the instance's secret key when the
-- instance first starts, opened on every start to prove the key is still
-- the one the instance's stored secrets are sealed under. One row at most.
CREATE TABLE secret_key_canary (
    id          boolean     PRIMARY KEY CHECK (id),
    sealed      text        NOT NULL,
    created_at  timestamptz NOT NULL
);
