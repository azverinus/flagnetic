# Flagnetic

Self-hosted, open-source feature flags and experiments. One Go binary with an embedded web console; PostgreSQL is the only thing it needs.

> **Status: early development.** The codebase is being assembled in this repository piece by piece, starting with the platform and the evaluation core. Nothing is released yet and APIs may change until 1.0. Watch the repository or read `CHANGELOG.md` to follow along.

## Why

The open-source flag products keep a paid tier, and the features a real team needs sit behind it: roles, SSO, audit log, approvals, environments, streaming, experiment analysis. Flagnetic ships all of it under Apache-2.0, with no cloud edition, no enterprise license and no feature gates. Ever.

## What it does

- **Flags** of four types (boolean, string, number, JSON) in every environment, with targeting rules, segments, percentage rollouts, prerequisites, overrides and a kill switch.
- **Evaluation in the SDK.** SDKs hold the ruleset in memory: a flag check costs no network call, and the SDK keeps serving the last good configuration if the server goes away.
- **Experiments** with deterministic, reproducible bucketing (a published formula, no secret salt), explicit exposure events, deduplication and a sample-ratio check.
- **Governance**: roles and permissions, protected environments with change requests and scheduled changes, an audit log, webhooks.
- **Identity**: local sign-in, OIDC, SAML and SCIM.
- **Export**: exposures and evaluation summaries to ClickHouse, object storage or a webhook.
- **SDKs** for Go, JavaScript (server, browser, React), Dart and Flutter, Ruby, and Python, each with an OpenFeature provider.

Items arrive as the corresponding parts of the codebase land.

## Principles

- PostgreSQL is the only required dependency; everything else is an optional integration.
- Flag checks are free; the platform going down is not an incident.
- Assignment is deterministic; exposure is an explicit call, separate from evaluation.
- Contracts are versioned from the first release, because a mobile app in a store is served for years.
- Everything in the console is available through the API.

## Contributing

Read [CONTRIBUTING.md](CONTRIBUTING.md) and [ARCHITECTURE.md](ARCHITECTURE.md). Security issues: see [SECURITY.md](SECURITY.md).

## License

[Apache-2.0](LICENSE).
