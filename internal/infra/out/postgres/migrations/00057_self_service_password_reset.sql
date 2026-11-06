-- +goose Up

-- Whether a member can ask for a password reset by mail. On for every
-- instance, existing ones included: a reset by mail needs an SMTP server, so
-- it changes nothing until an instance admin configures one.
ALTER TABLE auth_settings ADD COLUMN self_service_password_reset boolean NOT NULL DEFAULT true;
