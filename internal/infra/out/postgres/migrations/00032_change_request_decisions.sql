-- +goose Up

-- The decision taken on a change request and its application. A change
-- request is approved or rejected at most once, by a member, and applied at
-- most once, by a member, a project token, or the system at its scheduled
-- time. What was decided stays after the change request moves on, so a
-- change request cancelled or superseded once approved still says who
-- approved it.
ALTER TABLE change_requests
    ADD COLUMN decision          text        CHECK (decision IN ('approved', 'rejected')),
    ADD COLUMN decided_by        uuid        REFERENCES members (id),
    ADD COLUMN decided_at        timestamptz,
    ADD COLUMN decision_comment  text,
    ADD COLUMN applied_at        timestamptz,
    ADD COLUMN applied_by_kind   text        CHECK (applied_by_kind IN ('member', 'token', 'system')),
    ADD COLUMN applied_by        uuid        REFERENCES members (id),
    ADD COLUMN applied_by_token  uuid        REFERENCES api_tokens (id),
    ADD CONSTRAINT change_requests_decision CHECK (
        (decision IS NULL) = (decided_by IS NULL)
        AND (decision IS NULL) = (decided_at IS NULL)
        AND (decision IS NULL) = (decision_comment IS NULL)
    ),
    ADD CONSTRAINT change_requests_application CHECK (
        (applied_at IS NULL) = (applied_by_kind IS NULL)
        AND (applied_by IS NOT NULL) = (applied_by_kind IS NOT DISTINCT FROM 'member')
        AND (applied_by_token IS NOT NULL) = (applied_by_kind IS NOT DISTINCT FROM 'token')
        AND (applied_at IS NULL OR decision = 'approved')
    );

-- The entries an application of a change request writes carry its
-- identifier, so an auditor traces a change to its approval. The audit log
-- outlives change requests, so it is no foreign key.
ALTER TABLE audit_entries ADD COLUMN change_request_id uuid;

CREATE INDEX audit_entries_change_request ON audit_entries (change_request_id, id DESC) WHERE change_request_id IS NOT NULL;

