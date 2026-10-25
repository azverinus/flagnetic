-- +goose Up

-- What a stop decided: the outcome, the winning variation of a winner and a
-- note saying why ('' for none). Null until the experiment stops; an
-- experiment archived from its draft never has one.
ALTER TABLE experiments
    ADD COLUMN decision_outcome text CHECK (decision_outcome IN ('winner', 'removed')),
    ADD COLUMN decision_winner  text,
    ADD COLUMN decision_note    text,
    ADD CONSTRAINT experiments_decision CHECK (
        (decision_outcome IS NULL AND decision_winner IS NULL AND decision_note IS NULL)
        OR (decision_outcome = 'winner' AND decision_winner IS NOT NULL AND decision_note IS NOT NULL)
        OR (decision_outcome = 'removed' AND decision_winner IS NULL AND decision_note IS NOT NULL));
