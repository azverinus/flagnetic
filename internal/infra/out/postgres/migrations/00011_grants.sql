-- +goose Up

-- Grants: one role held by one member in one project. A member may hold
-- several in a project, from several sources, and acts with their union;
-- the same role from the same source is held once.
CREATE TABLE grants (
    id          uuid        PRIMARY KEY,
    project_id  uuid        NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    member_id   uuid        NOT NULL REFERENCES members (id),
    role        text        NOT NULL CHECK (role IN ('owner', 'maintainer', 'developer', 'viewer')),
    source      text        NOT NULL CHECK (source IN ('manual')),
    -- The member who made the grant; null when none did.
    created_by  uuid        REFERENCES members (id),
    created_at  timestamptz NOT NULL,
    UNIQUE (project_id, member_id, role, source)
);

-- The projects a member holds grants in, for their project list and count.
CREATE INDEX grants_member_idx ON grants (member_id, project_id);
