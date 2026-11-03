-- +goose Up

-- SAML auth providers. The columns of one kind are null for every other and a
-- kind's are all present; the identity provider's metadata comes from a URL or
-- is pasted, never both. The instance's private key for a provider is sealed
-- under the instance's secret key, so a database dump holds no key an
-- identity provider trusts.
ALTER TABLE auth_providers DROP CONSTRAINT auth_providers_kind_check;
ALTER TABLE auth_providers ADD CONSTRAINT auth_providers_kind_check CHECK (kind IN ('oidc', 'saml'));

ALTER TABLE auth_providers
    ADD COLUMN saml_metadata_url          text,
    ADD COLUMN saml_metadata_xml          text,
    ADD COLUMN saml_idp_entity_id         text,
    ADD COLUMN saml_idp_sso_url           text,
    -- PEM certificates the identity provider signs with.
    ADD COLUMN saml_idp_certificates      text[],
    ADD COLUMN saml_metadata_refreshed_at timestamptz,
    ADD COLUMN saml_sign_requests         boolean,
    ADD COLUMN saml_name_id_format        text,
    ADD COLUMN saml_email_attribute       text,
    ADD COLUMN saml_name_attribute        text,
    ADD COLUMN saml_groups_attribute      text,
    ADD COLUMN saml_sp_certificate        text,
    ADD COLUMN saml_sp_key_sealed         text;

ALTER TABLE auth_providers ADD CONSTRAINT auth_providers_saml_fields CHECK ((kind = 'saml') = (
    (saml_metadata_url IS NULL) <> (saml_metadata_xml IS NULL)
    AND saml_idp_entity_id IS NOT NULL AND saml_idp_sso_url IS NOT NULL AND saml_idp_certificates IS NOT NULL
    AND cardinality(saml_idp_certificates) > 0 AND saml_metadata_refreshed_at IS NOT NULL
    AND saml_sign_requests IS NOT NULL AND saml_name_id_format IS NOT NULL AND saml_email_attribute IS NOT NULL
    AND saml_name_attribute IS NOT NULL AND saml_groups_attribute IS NOT NULL AND saml_sp_certificate IS NOT NULL
    AND saml_sp_key_sealed IS NOT NULL
));

-- A SAML sign-in remembers the ID of its AuthnRequest, which the response
-- must answer; its state is the request's RelayState.
ALTER TABLE sso_attempts ADD COLUMN saml_request_id text;

-- Assertions already accepted, kept until they could no longer be accepted, so
-- a response replayed within its validity is refused.
CREATE TABLE saml_replay (
    provider_id  uuid        NOT NULL REFERENCES auth_providers (id) ON DELETE CASCADE,
    assertion_id text        NOT NULL,
    expires_at   timestamptz NOT NULL,
    PRIMARY KEY (provider_id, assertion_id)
);

CREATE INDEX saml_replay_expiry ON saml_replay (expires_at);

ALTER TABLE sessions DROP CONSTRAINT sessions_method_check;
ALTER TABLE sessions ADD CONSTRAINT sessions_method_check CHECK (
    (method = 'local' AND provider IS NULL) OR (method IN ('oidc', 'saml') AND provider IS NOT NULL)
);
