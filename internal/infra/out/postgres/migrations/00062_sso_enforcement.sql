-- +goose Up

-- Whether members may sign in with a password at all. On for every
-- instance, existing ones included.
ALTER TABLE auth_settings ADD COLUMN local_login_enabled boolean NOT NULL DEFAULT true;

-- The email domains whose people sign in only through one auth provider.
-- A domain is kept lowercase, so the key is unique whatever its case. The
-- provider is named by its slug, which never changes, as grants and members
-- name it; the reference keeps a provider an enforcement names from being
-- deleted, which the API refuses earlier with provider_in_use.
CREATE TABLE sso_enforced_domains (
    domain    text NOT NULL PRIMARY KEY CHECK (domain <> '' AND domain = lower(domain)),
    provider  text NOT NULL REFERENCES auth_providers (slug),
    -- The order the instance admin listed the domains in.
    position  integer NOT NULL CHECK (position >= 0)
);

CREATE INDEX sso_enforced_domains_provider ON sso_enforced_domains (provider);
