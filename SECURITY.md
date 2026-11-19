# Security policy

## Reporting a vulnerability

Please do not open a public issue. Report it privately through GitHub: **Security → Report a vulnerability** on this repository ([private vulnerability reporting](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing-information-about-vulnerabilities/privately-reporting-a-security-vulnerability)).

Include what you found, how to reproduce it, the version or commit, and the impact you see. We acknowledge reports within 5 working days and keep you informed until a fix is released. We credit reporters in the advisory unless you prefer otherwise.

## Supported versions

Until 1.0, fixes go to `main` and the latest release only, currently 0.5. From 1.0, the latest minor release is supported: fixes, security fixes included, ship in the next patch or minor release from `main`, with no release branches. See [Compatibility and support](docs/compatibility.md#support).

## Scope

In scope: the server, the console, the SDKs in this repository, and their release artifacts. Particularly interesting: anything a client-side SDK key (embedded in public apps) can learn or do beyond reading served flag values, authentication and authorization bypasses, server-side request forgery through integrations, and denial of service from unauthenticated requests.

### What a database writer can do

Someone who can write to the PostgreSQL database is outside the threat model: the database is the instance's source of truth, and a writer can set `instance_admin`, grants, an auth provider's trust anchors (issuer, certificates, domains) and password hashes directly, whatever the server checks on the way in. Protect the database with the access you would give the instance's administrators.

What the secret key protects is the confidentiality of secrets at rest: auth provider client secrets, SMTP passwords, the hashes of SCIM tokens and the like are sealed under it, so a database dump, a backup or a read-only replica is no source of them. Sessions are signed with it, so a writer cannot mint one. Each sealed value is also bound to its row, and a SCIM token's hash to its provider and expiry, so a value copied between rows, or a token pointed at another provider, does not open; that is defense in depth against mistakes and leaked values, not a promise that a writer cannot change what the instance does.
