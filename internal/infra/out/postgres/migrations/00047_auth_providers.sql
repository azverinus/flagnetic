-- +goose Up

-- Auth providers: the identity providers an instance admin lets members sign
-- in through. A provider vouches only for the email domains it names. Fields
-- of one kind are null for every other, and a kind's fields are all present.
-- The client secret is sealed under the instance's secret key, so a database
-- dump holds no credential of an identity provider.
CREATE TABLE auth_providers (
    id                         uuid        PRIMARY KEY,
    -- Names the provider in sign-in buttons, URLs and its redirect URI; never
    -- changes.
    slug                       text        NOT NULL CONSTRAINT auth_providers_slug_key UNIQUE
                                           CHECK (slug ~ '^[a-z][a-z0-9_-]{0,63}$'),
    kind                       text        NOT NULL CHECK (kind IN ('oidc')),
    display_name               text        NOT NULL CHECK (display_name <> ''),
    enabled                    boolean     NOT NULL,
    -- Lowercase email domains the provider vouches for.
    domains                    text[]      NOT NULL CHECK (cardinality(domains) > 0),
    link_by_email              boolean     NOT NULL,
    auto_provision             boolean     NOT NULL,
    oidc_issuer                text,
    oidc_client_id             text,
    oidc_client_secret_sealed  text,
    oidc_scopes                text[],
    oidc_email_claim           text,
    oidc_name_claim            text,
    oidc_groups_claim          text,
    oidc_require_verified_email boolean,
    created_at                 timestamptz NOT NULL,
    updated_at                 timestamptz NOT NULL,
    version                    bigint      NOT NULL CHECK (version >= 1),
    CHECK ((kind = 'oidc') = (
        oidc_issuer IS NOT NULL AND oidc_client_id IS NOT NULL AND oidc_client_secret_sealed IS NOT NULL
        AND oidc_scopes IS NOT NULL AND oidc_email_claim IS NOT NULL AND oidc_name_claim IS NOT NULL
        AND oidc_groups_claim IS NOT NULL AND oidc_require_verified_email IS NOT NULL
    ))
);

-- The sign-in page lists the enabled providers.
CREATE INDEX auth_providers_enabled ON auth_providers (display_name, id) WHERE enabled;
