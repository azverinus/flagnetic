-- +goose Up

-- The version a revert returns its flag's configuration to; null for any
-- other change request. The server fills what a revert proposes from it,
-- and it never changes once the change request is created.
ALTER TABLE change_requests
    ADD COLUMN revert_to bigint,
    ADD CONSTRAINT change_requests_revert_to CHECK (
        revert_to IS NULL OR (revert_to >= 1 AND kind = 'flag_configuration')
    );
