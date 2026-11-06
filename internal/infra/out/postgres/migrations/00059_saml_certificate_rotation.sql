-- +goose Up

-- A SAML provider's next key pair, generated when a certificate rotation
-- starts and made current when it completes. The private key is sealed under
-- the instance's secret key like the current one; the two columns are both set
-- or both null, and only a SAML provider holds them.
ALTER TABLE auth_providers
    ADD COLUMN saml_sp_next_certificate text,
    ADD COLUMN saml_sp_next_key_sealed  text;

ALTER TABLE auth_providers ADD CONSTRAINT auth_providers_saml_rotation CHECK (
    (saml_sp_next_certificate IS NULL) = (saml_sp_next_key_sealed IS NULL)
    AND (saml_sp_next_certificate IS NULL OR kind = 'saml')
);
