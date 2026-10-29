-- +goose Up

-- The sources each member signed in from, and when they last did: a source
-- known to its member is not refused by their email's lock. Kept apart from
-- the sessions, which are purged once revoked or ended, so revoking every
-- session does not forget where the member signs in from.
CREATE TABLE sign_in_known_sources (
    member_id     uuid        NOT NULL REFERENCES members (id) ON DELETE CASCADE,
    -- An IPv4 address or an IPv6 /64, as failures are counted.
    source        cidr        NOT NULL,
    signed_in_at  timestamptz NOT NULL,
    PRIMARY KEY (member_id, source)
);

CREATE INDEX sign_in_known_sources_signed_in_at ON sign_in_known_sources (signed_in_at);

-- Consecutive failed attempts on an email from one source known to its
-- member, counted and locked apart from the email's own, which every other
-- source shares. A failure is counted before the password is verified and
-- the row cleared when it was right.
CREATE TABLE sign_in_known_source_failures (
    -- The email trimmed and in lower case.
    email            text        NOT NULL,
    source           cidr        NOT NULL,
    failures         integer     NOT NULL CHECK (failures > 0),
    -- Null while the source has fewer failures than lock it.
    locked_until     timestamptz,
    last_failure_at  timestamptz NOT NULL,
    PRIMARY KEY (email, source)
);
