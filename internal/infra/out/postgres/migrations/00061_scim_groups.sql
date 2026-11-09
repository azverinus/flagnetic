-- +goose Up

-- SCIM groups: the groups an identity provider pushes through /scim/v2. Each
-- belongs to the auth provider of the token that created it, and its name
-- and external ID are unique among that provider's groups only, so two
-- directories may each hold a group of the same name. A provider's group
-- mappings match the name, as they match a group a sign-in claims.
CREATE TABLE scim_groups (
    id           uuid        PRIMARY KEY,
    provider_id  uuid        NOT NULL REFERENCES auth_providers (id) ON DELETE CASCADE,
    display_name text        NOT NULL CHECK (btrim(display_name) <> ''),
    external_id  text        CHECK (external_id <> ''),
    created_at   timestamptz NOT NULL,
    updated_at   timestamptz NOT NULL
);

CREATE UNIQUE INDEX scim_groups_provider_display_name_key ON scim_groups (provider_id, display_name);
CREATE UNIQUE INDEX scim_groups_provider_external_id_key ON scim_groups (provider_id, external_id) WHERE external_id IS NOT NULL;

-- The members of a SCIM group: only members its provider provisions join.
CREATE TABLE scim_group_members (
    group_id  uuid NOT NULL REFERENCES scim_groups (id) ON DELETE CASCADE,
    member_id uuid NOT NULL REFERENCES members (id) ON DELETE CASCADE,
    PRIMARY KEY (group_id, member_id)
);

CREATE INDEX scim_group_members_member ON scim_group_members (member_id);
