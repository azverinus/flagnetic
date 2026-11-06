-- +goose Up

-- A salt is still used by one experiment only, but the check may wait for the
-- end of the unit of work: an import may hand the salt of a draft to another
-- experiment of the project, or swap two drafts' salts, and the experiment
-- giving a salt up is not always written before the one taking it. The checks
-- stay immediate unless a unit of work defers them.
DROP INDEX experiments_inclusion_salt;
DROP INDEX experiments_variation_salt;
ALTER TABLE experiments
    ADD CONSTRAINT experiments_inclusion_salt UNIQUE (project_id, inclusion_salt) DEFERRABLE INITIALLY IMMEDIATE,
    ADD CONSTRAINT experiments_variation_salt UNIQUE (project_id, variation_salt) DEFERRABLE INITIALLY IMMEDIATE;
