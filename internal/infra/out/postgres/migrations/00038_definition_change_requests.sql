-- +goose Up

-- What a change request to a definition, applied in every environment,
-- is judged against beyond its entity's version.
--
-- base_referrers are, for a segment's definition, the flags whose
-- configurations named the segment when the change request was based on
-- it: a write to one of their configurations moves what the definition was
-- reviewed with, even a write that stops naming the segment.
--
-- approval_covers are the environments protected when a definition was
-- approved: its application changes them although they are protected, and
-- an environment protected since supersedes it.
--
-- Both are identifiers alone: a flag or an environment deleted since only
-- leaves an identifier nothing matches.
ALTER TABLE change_requests
    ADD COLUMN base_referrers  uuid[],
    ADD COLUMN approval_covers uuid[],
    ADD CONSTRAINT change_requests_base_referrers CHECK (base_referrers IS NULL OR segment_id IS NOT NULL),
    ADD CONSTRAINT change_requests_approval_covers CHECK (
        approval_covers IS NULL OR (kind <> 'flag_configuration' AND decision = 'approved')
    );

-- Every write to a flag's configuration looks up the waiting change requests
-- to the project's segments' definitions, whose referrers it may be among.
CREATE INDEX change_requests_waiting_segment_definitions ON change_requests (project_id)
    WHERE segment_id IS NOT NULL AND state IN ('open', 'approved');
