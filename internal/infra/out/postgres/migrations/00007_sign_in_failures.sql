-- +goose Up

-- Consecutive failed sign-ins per email, known to the instance or not, so
-- an unknown email locks like a known one. A failure is counted before the
-- password is verified and the row cleared when it was right.
CREATE TABLE sign_in_email_failures (
    -- The email trimmed and in lower case.
    email            text        PRIMARY KEY,
    failures         integer     NOT NULL CHECK (failures > 0),
    -- Null while the email has fewer failures than lock it.
    locked_until     timestamptz,
    last_failure_at  timestamptz NOT NULL
);

-- Failed attempts per source (an IPv4 address or an IPv6 /64) in its
-- current window of fifteen minutes, opened by its first failure.
CREATE TABLE sign_in_source_failures (
    source        cidr        PRIMARY KEY,
    window_start  timestamptz NOT NULL,
    failures      integer     NOT NULL CHECK (failures >= 0)
);
