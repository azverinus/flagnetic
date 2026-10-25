-- +goose Up

-- The audit entry of the write that superseded a change request by moving
-- what it was based on. A change request found moved only when it was
-- approved or applied names none, since no write it could name moved it.
-- It is the entry's identifier alone, as an entry keeps the change request
-- it applied by identifier alone.
ALTER TABLE change_requests
    ADD COLUMN superseded_by uuid,
    ADD CONSTRAINT change_requests_superseded_by CHECK (superseded_by IS NULL OR state = 'superseded');
