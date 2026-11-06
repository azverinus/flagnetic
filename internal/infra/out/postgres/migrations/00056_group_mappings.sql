-- +goose Up

-- Group mappings: what an auth provider's directory groups grant. A mapping
-- names one group and either a role in a project or instance admin, never
-- both; a provider's mappings are ordered, and replaced as a whole.
CREATE TABLE group_mappings (
    provider_id    uuid    NOT NULL REFERENCES auth_providers (id) ON DELETE CASCADE,
    position       integer NOT NULL CHECK (position >= 0),
    "group"        text    NOT NULL CHECK (btrim("group") <> ''),
    project_id     uuid    REFERENCES projects (id) ON DELETE CASCADE,
    role           text    CHECK (role IN ('owner', 'maintainer', 'developer', 'viewer')),
    instance_admin boolean NOT NULL,
    PRIMARY KEY (provider_id, position),
    CHECK (instance_admin = (project_id IS NULL AND role IS NULL)),
    CHECK ((project_id IS NULL) = (role IS NULL))
);

-- The groups a member's last sign-in through a provider claimed, kept so that
-- replacing the provider's mappings can run them again for everyone the
-- provider knows without waiting for their next sign-in.
CREATE TABLE group_claims (
    member_id   uuid        NOT NULL REFERENCES members (id) ON DELETE CASCADE,
    provider_id uuid        NOT NULL REFERENCES auth_providers (id) ON DELETE CASCADE,
    groups      text[]      NOT NULL,
    at          timestamptz NOT NULL,
    PRIMARY KEY (member_id, provider_id)
);

CREATE INDEX group_claims_provider ON group_claims (provider_id);

-- A sign-in attempt carries the groups its provider claimed for the person,
-- null when the claim was absent or not a list of strings.
ALTER TABLE sso_attempts ADD COLUMN groups text[];

-- Grants made by a provider's group mappings sit beside the manual ones. The
-- provider is named by its slug, which never changes. Each source owns its
-- own grants, so one provider and another may both grant a member the same
-- role in a project, and so may a hand.
ALTER TABLE grants ADD COLUMN provider text REFERENCES auth_providers (slug);
ALTER TABLE grants DROP CONSTRAINT grants_source_check;
ALTER TABLE grants ADD CONSTRAINT grants_source_check CHECK (
    (source = 'manual' AND provider IS NULL) OR (source = 'sso' AND provider IS NOT NULL)
);
ALTER TABLE grants DROP CONSTRAINT grants_project_id_member_id_role_source_key;
CREATE UNIQUE INDEX grants_manual_key ON grants (project_id, member_id, role) WHERE source = 'manual';
CREATE UNIQUE INDEX grants_sso_key ON grants (project_id, member_id, role, provider) WHERE source = 'sso';
CREATE INDEX grants_provider ON grants (provider) WHERE provider IS NOT NULL;

-- The provider whose group mapping made a member an instance admin; only that
-- provider takes it away again. Null for an instance admin made by hand.
ALTER TABLE members ADD COLUMN instance_admin_provider text REFERENCES auth_providers (slug);
ALTER TABLE members ADD CONSTRAINT members_instance_admin_provider_check CHECK (
    instance_admin_provider IS NULL OR instance_admin
);
CREATE INDEX members_instance_admin_provider ON members (instance_admin_provider) WHERE instance_admin_provider IS NOT NULL;

-- Changes a provider's mappings make are audited under the provider.
ALTER TABLE audit_entries DROP CONSTRAINT audit_entries_actor_kind_check;
ALTER TABLE audit_entries ADD CONSTRAINT audit_entries_actor_kind_check CHECK (
    actor_kind IN ('member', 'token', 'cli', 'system', 'sso')
);
