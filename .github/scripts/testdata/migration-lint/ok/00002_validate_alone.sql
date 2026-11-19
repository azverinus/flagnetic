-- +goose Up
-- The second half of an expand step: validating a constraint added NOT VALID
-- by an earlier migration takes only a SHARE UPDATE EXCLUSIVE lock.
ALTER TABLE flags VALIDATE CONSTRAINT flags_owner_set;
