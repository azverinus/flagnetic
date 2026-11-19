-- +goose Up

-- The IDs of exposures refused for an experiment that exists, so a refusal is
-- counted once however often the exposure is sent: an app sending a batch
-- again after a lost answer, or two copies in one batch. Apart from
-- exposure_dedup, which holds only the exposures accepted: a refused event is
-- not received, and its sender may correct and send it again. Not
-- partitioned, like the other dedup tables.
CREATE TABLE exposure_refusal_dedup (
    project_id  uuid        NOT NULL,
    event_id    uuid        NOT NULL,
    received_at timestamptz NOT NULL,
    PRIMARY KEY (project_id, event_id)
);

CREATE INDEX exposure_refusal_dedup_received ON exposure_refusal_dedup (received_at);
