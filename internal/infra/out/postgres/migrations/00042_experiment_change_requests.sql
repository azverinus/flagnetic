-- +goose Up

-- A change request proposing an experiment's transition names the
-- experiment and its flag, so a write to the flag's configuration finds it
-- without reading experiments, and no environment: it is applied in every
-- environment holding the experiment's rule.
--
-- base_holders are those environments when the change request was based on
-- the experiment: a write to the flag's configuration in one of them moves
-- what the transition was reviewed as changing. They are identifiers alone:
-- an environment deleted since only leaves an identifier nothing matches.
ALTER TABLE change_requests
    ADD COLUMN base_holders uuid[],
    ADD CONSTRAINT change_requests_base_holders CHECK (base_holders IS NULL OR experiment_id IS NOT NULL),
    ADD CONSTRAINT change_requests_experiment_target CHECK (
        kind <> 'experiment' OR (flag_id IS NOT NULL AND environment_id IS NULL AND base_holders IS NOT NULL)
    );
