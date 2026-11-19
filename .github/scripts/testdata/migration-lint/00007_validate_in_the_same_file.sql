-- +goose Up
-- goose runs the file in one transaction, so the ACCESS EXCLUSIVE lock of ADD
-- CONSTRAINT is held through VALIDATE's full scan: the two belong in separate
-- files.
ALTER TABLE flags ADD CONSTRAINT flags_owner_set CHECK (owner IS NOT NULL) NOT VALID;
ALTER TABLE flags VALIDATE CONSTRAINT flags_owner_set;
