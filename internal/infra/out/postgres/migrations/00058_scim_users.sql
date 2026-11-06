-- +goose Up

-- SCIM tokens: the credentials an identity provider presents to /scim/v2.
-- Each belongs for life to one auth provider. Only the SHA-256 of the value is
-- kept, sealed under the instance's secret key: the few tokens an instance
-- holds are opened and compared one by one, so a database dump is no offline
-- guessing target and no index on the hash exists.
CREATE TABLE scim_tokens (
    id                uuid        PRIMARY KEY,
    -- A provider with an unrevoked token is not deleted; the tokens it leaves
    -- behind, all revoked, go with it.
    provider_id       uuid        NOT NULL REFERENCES auth_providers (id) ON DELETE CASCADE,
    hint              text        NOT NULL,
    token_hash_sealed text        NOT NULL,
    expires_at        timestamptz,
    revoked_at        timestamptz,
    created_at        timestamptz NOT NULL,
    -- When the token was last presented, written at most once a minute.
    last_used_at      timestamptz
);

CREATE INDEX scim_tokens_provider ON scim_tokens (provider_id);

-- Provisioned members: a member an identity provider created or adopted
-- through SCIM belongs to that provider, named by its slug as grants name
-- theirs. The external ID is the directory's own identifier of the person,
-- unique among the provider's members; a sign-in is linked by it.
ALTER TABLE members ADD COLUMN provider text REFERENCES auth_providers (slug);
ALTER TABLE members ADD COLUMN external_id text;
ALTER TABLE members ADD CONSTRAINT members_external_id_check CHECK (
    external_id IS NULL OR (provider IS NOT NULL AND external_id <> '')
);
CREATE UNIQUE INDEX members_provider_external_id_key ON members (provider, external_id) WHERE external_id IS NOT NULL;
CREATE INDEX members_provider ON members (provider) WHERE provider IS NOT NULL;

-- Changes SCIM makes are audited under the token that made them.
ALTER TABLE audit_entries DROP CONSTRAINT audit_entries_actor_kind_check;
ALTER TABLE audit_entries ADD CONSTRAINT audit_entries_actor_kind_check CHECK (
    actor_kind IN ('member', 'token', 'cli', 'system', 'sso', 'scim')
);
