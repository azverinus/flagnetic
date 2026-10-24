-- +goose Up

-- A protected environment changes its ruleset only through an approved
-- change request. Environments that exist when this is applied stay
-- unprotected, so scripts writing them directly keep working until an owner
-- decides otherwise; projects created from now on create their production
-- environment protected, which the application decides.
ALTER TABLE environments ADD COLUMN protected boolean NOT NULL DEFAULT false;
