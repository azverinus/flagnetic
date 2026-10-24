-- +goose Up

-- Change requests: a proposed change to one entity, reviewed before it is
-- made. One table holds every kind, its entity named by the columns the
-- kind uses and no other: a flag's configuration names a flag and an
-- environment; a definition a flag or a segment; an experiment's transition
-- the experiment. A change request goes with its project and with the
-- entity it names, since it proposes nothing once that is gone.
CREATE TABLE change_requests (
    id               uuid        PRIMARY KEY,
    project_id       uuid        NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    kind             text        NOT NULL CHECK (kind IN ('flag_configuration', 'definition', 'experiment')),
    flag_id          uuid        REFERENCES flags (id) ON DELETE CASCADE,
    environment_id   uuid        REFERENCES environments (id) ON DELETE CASCADE,
    segment_id       uuid        REFERENCES segments (id) ON DELETE CASCADE,
    experiment_id    uuid        REFERENCES experiments (id) ON DELETE CASCADE,
    state            text        NOT NULL CHECK (state IN ('draft', 'open', 'approved', 'rejected', 'applied', 'cancelled',
                                                           'superseded')),
    title            text        NOT NULL CHECK (title <> ''),
    description      text        NOT NULL,
    -- The document of the entity the change request was based on, never
    -- answered, and its version; the document proposed; and the RFC 6902
    -- patch from the one to the other, kept as it was last computed.
    base             jsonb       NOT NULL,
    base_version     bigint      NOT NULL CHECK (base_version >= 1),
    proposed         jsonb       NOT NULL,
    diff             jsonb       NOT NULL,
    -- The author is a member or a project token, which acts for no member.
    -- Neither is ever deleted, so an author always resolves.
    author_id        uuid        REFERENCES members (id),
    author_token_id  uuid        REFERENCES api_tokens (id),
    created_at       timestamptz NOT NULL,
    updated_at       timestamptz NOT NULL,
    version          bigint      NOT NULL CHECK (version >= 1),
    CHECK ((author_id IS NULL) <> (author_token_id IS NULL)),
    CHECK (
        (kind = 'flag_configuration' AND flag_id IS NOT NULL AND environment_id IS NOT NULL
            AND segment_id IS NULL AND experiment_id IS NULL)
        OR (kind = 'definition' AND (flag_id IS NULL) <> (segment_id IS NULL)
            AND environment_id IS NULL AND experiment_id IS NULL)
        OR (kind = 'experiment' AND experiment_id IS NOT NULL AND segment_id IS NULL)
    )
);

-- A project's change requests newest first, and those naming an entity,
-- which the list filters by and every write to the entity will look up.
CREATE INDEX change_requests_project ON change_requests (project_id, id DESC);
CREATE INDEX change_requests_flag ON change_requests (flag_id, environment_id) WHERE flag_id IS NOT NULL;
CREATE INDEX change_requests_environment ON change_requests (environment_id) WHERE environment_id IS NOT NULL;
CREATE INDEX change_requests_segment ON change_requests (segment_id) WHERE segment_id IS NOT NULL;
CREATE INDEX change_requests_experiment ON change_requests (experiment_id) WHERE experiment_id IS NOT NULL;
CREATE INDEX change_requests_author ON change_requests (author_id) WHERE author_id IS NOT NULL;
CREATE INDEX change_requests_author_token ON change_requests (author_token_id) WHERE author_token_id IS NOT NULL;
