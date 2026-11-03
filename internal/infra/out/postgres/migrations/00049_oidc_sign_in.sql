-- +goose Up

-- Sign-in attempts: a browser's trip through an identity provider, from the
-- start to the completion. The provider's answer is verified and its claims
-- are stored here; nothing about any member changes until the browser that
-- started the attempt completes it. Every secret the attempt checks is kept
-- only as its SHA-256, except the nonce and the code verifier, which the
-- callback must present to the provider and which are worthless once the
-- attempt is gone.
CREATE TABLE sso_attempts (
    id              uuid        PRIMARY KEY,
    provider_id     uuid        NOT NULL REFERENCES auth_providers (id) ON DELETE CASCADE,
    intent          text        NOT NULL CHECK (intent IN ('login')),
    -- The OIDC authorization code flow's state, nonce and PKCE verifier.
    state_hash      bytea       NOT NULL CONSTRAINT sso_attempts_state_hash_key UNIQUE CHECK (length(state_hash) = 32),
    nonce           text        NOT NULL,
    code_verifier   text        NOT NULL,
    -- Binds the attempt to the browser that started it.
    cookie_hash     bytea       NOT NULL CHECK (length(cookie_hash) = 32),
    return_to       text        NOT NULL,
    created_at      timestamptz NOT NULL,
    expires_at      timestamptz NOT NULL,
    -- When the provider's answer was taken; an answer replayed finds it set.
    callback_at     timestamptz,
    -- Set with the claims by a callback that verified: the token the browser
    -- completes with.
    completion_hash bytea       CONSTRAINT sso_attempts_completion_hash_key UNIQUE CHECK (length(completion_hash) = 32),
    subject         text,
    email           text,
    email_verified  boolean,
    display_name    text,
    CHECK ((completion_hash IS NULL) = (subject IS NULL)),
    CHECK ((completion_hash IS NULL) = (email IS NULL)),
    CHECK ((completion_hash IS NULL) = (email_verified IS NULL)),
    CHECK ((completion_hash IS NULL) = (display_name IS NULL))
);

CREATE INDEX sso_attempts_provider ON sso_attempts (provider_id);
CREATE INDEX sso_attempts_expiry ON sso_attempts (expires_at);

-- Identities: the subject an identity provider knows a member by. A subject
-- belongs to one member; a member may hold several, at one provider or at
-- different ones.
CREATE TABLE identities (
    id            uuid        PRIMARY KEY,
    provider_id   uuid        NOT NULL REFERENCES auth_providers (id),
    subject       text        NOT NULL CHECK (subject <> ''),
    member_id     uuid        NOT NULL REFERENCES members (id),
    linked_at     timestamptz NOT NULL,
    last_sign_in_at timestamptz,
    CONSTRAINT identities_provider_subject_key UNIQUE (provider_id, subject)
);

CREATE INDEX identities_member ON identities (member_id);

-- Sessions record the provider a single sign-on session opened through.
ALTER TABLE sessions ADD COLUMN provider text;
ALTER TABLE sessions DROP CONSTRAINT sessions_method_check;
ALTER TABLE sessions ADD CONSTRAINT sessions_method_check CHECK (
    (method = 'local' AND provider IS NULL) OR (method = 'oidc' AND provider IS NOT NULL)
);
