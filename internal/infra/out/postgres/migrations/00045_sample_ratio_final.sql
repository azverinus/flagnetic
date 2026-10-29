-- +goose Up

-- Whether a sample-ratio result is the last one its experiment keeps. Every
-- result a running experiment stores is final, since nothing waits there. The
-- stop stores its own as not final: the exposures accepted before it may not
-- have been rolled up yet, and the checks complete it once they have. The
-- rows that exist before the column are final: they were taken by the
-- checks that existed then, and nothing is known to be pending behind them.
ALTER TABLE experiment_sample_ratios ADD COLUMN final boolean NOT NULL DEFAULT true;

-- The results still waiting for their final check, which the checks list.
CREATE INDEX experiment_sample_ratios_pending ON experiment_sample_ratios (experiment_id) WHERE NOT final;
