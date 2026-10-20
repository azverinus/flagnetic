# Security policy

## Reporting a vulnerability

Please do not open a public issue. Report it privately through GitHub: **Security → Report a vulnerability** on this repository ([private vulnerability reporting](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing-information-about-vulnerabilities/privately-reporting-a-security-vulnerability)).

Include what you found, how to reproduce it, the version or commit, and the impact you see. We acknowledge reports within 5 working days and keep you informed until a fix is released. We credit reporters in the advisory unless you prefer otherwise.

## Supported versions

Flagnetic has not had a release yet. Until 1.0, fixes go to `main` and the latest release only.

## Scope

In scope: the server, the console, the SDKs in this repository, and their release artifacts. Particularly interesting: anything a client-side SDK key (embedded in public apps) can learn or do beyond reading served flag values, authentication and authorization bypasses, server-side request forgery through integrations, and denial of service from unauthenticated requests.
