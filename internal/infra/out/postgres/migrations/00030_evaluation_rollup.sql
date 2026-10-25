-- +goose Up

-- A flag's evaluations per UTC day of receipt in an environment, by the
-- variation served, the reason and who counted them: an SDK reporting its
-- own (sdk) or the server answering remote evaluations (remote_evaluation),
-- kept apart so that what a client SDK reads is never added to what it was
-- sent. variation_key is '' when no variation was served, so it can be part
-- of the key. A count stops at the bigint maximum rather than fail. Kept
-- until the flag or the environment goes.
CREATE TABLE evaluation_counts_daily (
    environment_id    uuid   NOT NULL REFERENCES environments (id) ON DELETE CASCADE,
    flag_id           uuid   NOT NULL REFERENCES flags (id) ON DELETE CASCADE,
    day               date   NOT NULL,
    source            text   NOT NULL CHECK (source IN ('sdk', 'remote_evaluation')),
    variation_key     text   NOT NULL,
    evaluation_reason text   NOT NULL,
    evaluations       bigint NOT NULL CHECK (evaluations >= 0),
    PRIMARY KEY (flag_id, environment_id, day, source, variation_key, evaluation_reason)
);

CREATE INDEX evaluation_counts_daily_environment ON evaluation_counts_daily (environment_id, day);

-- When an SDK last evaluated a flag in an environment: the latest interval
-- end of its summaries and the latest exposure it recorded, never later than
-- the server received them. Remote evaluations the server answered never
-- move it.
CREATE TABLE flag_last_evaluated (
    flag_id           uuid        NOT NULL REFERENCES flags (id) ON DELETE CASCADE,
    environment_id    uuid        NOT NULL REFERENCES environments (id) ON DELETE CASCADE,
    last_evaluated_at timestamptz NOT NULL,
    PRIMARY KEY (flag_id, environment_id)
);

CREATE INDEX flag_last_evaluated_environment ON flag_last_evaluated (environment_id);
