# Flagnetic

Self-hosted, open-source feature flags and experiments. One Go binary with an embedded web console; PostgreSQL is the only thing it needs.

> **Status: developer preview.** Version 0.1 is the first release: try it, build against its Go SDK, and tell us what breaks, but do not run it where an outage would hurt. The SDK API and the ruleset format only grow from 0.1 on, and upgrades only migrate forward, without data loss; the management API and the console may still change in 0.x, each change marked in the release notes. See [what 0.1 promises](docs/sdks/compatibility.md#what-01-promises).

## Why

The open-source flag products keep a paid tier, and the features a real team needs sit behind it: roles, SSO, audit log, approvals, environments, streaming, experiment analysis. Flagnetic ships all of it under Apache-2.0, with no cloud edition, no enterprise license and no feature gates. Ever.

## What 0.1 contains

- **Flags** of four types (boolean, string, number, JSON) in every environment, with targeting rules, segments (private ones included), percentage rollouts, prerequisites, overrides and a kill switch; revert, copy and compare between environments, history, and a tester that shows what a context is served, and why.
- **Access**: roles and permissions per project, personal access tokens and project tokens with scopes, an audit log of every change, local sign-in with lockout and throttling, members and invitations.
- **Delivery to SDKs**: server and client SDK keys per environment, with rotation and an overlap, revocation and allowed origins; the ruleset with conditional requests; the ruleset stream, which brings a change, a kill switch included, to a server SDK within 2 seconds; remote evaluation for client keys, which learn served values only; the events SDKs post; the attributes SDKs send that the project has not declared; and the SDKs seen using each environment.
- **The Go SDK** and its OpenFeature provider: evaluation in the SDK, so a flag check costs no network call and the SDK keeps serving its last good ruleset if the server goes away.
- **SDKs for JavaScript, Dart and Flutter, Ruby and Python**, listed below.
- **Operations**: any number of identical replicas on one PostgreSQL, with no sticky sessions; Prometheus metrics; a console in English and Russian; a Docker image for `linux/amd64` and `linux/arm64` with provenance and an SBOM.

## Not there yet

Each arrives in a later release:

- **Experiments**: they can be created and read, but a rule cannot serve one yet, they cannot be started, exposures are not stored and there is no analysis.
- **Reports** on the evaluation summaries SDKs send, such as flags no SDK evaluates any more.
- **Governance**: protected environments with change requests, scheduled changes, webhooks.
- **Identity**: OIDC, SAML and SCIM.
- **Export** of exposures and evaluation summaries to ClickHouse, object storage or a webhook.
- A Helm chart.

## SDKs

Each SDK is published under the name `flagnetic` on its own registry, and the OpenFeature providers beside it. All speak the same protocol and work with every server from 0.1.0 on; see [SDKs](docs/sdks/README.md) for modes, runtimes and how soon a change reaches each.

| Language | Install |
|---|---|
| [Go](sdks/go/README.md) | `go get github.com/azverinus/flagnetic/sdks/go`, and `go get github.com/azverinus/flagnetic/sdks/go-openfeature` for OpenFeature |
| [JavaScript and TypeScript](sdks/js/README.md): Node, browsers, React | `npm install flagnetic` |
| [Dart](sdks/dart/README.md) | `dart pub add flagnetic` |
| [Flutter](sdks/flutter/README.md) | `flutter pub add flagnetic_flutter` |
| [Ruby](sdks/ruby/README.md) | `bundle add flagnetic`, and `bundle add flagnetic-openfeature` for OpenFeature |
| [Python](sdks/python/README.md) | `pip install flagnetic`, and `pip install 'flagnetic[openfeature]'` for OpenFeature |

## Quick start

With Docker:

```bash
git clone https://github.com/azverinus/flagnetic.git && cd flagnetic/deploy
echo "FLAGNETIC_SECRET_KEY=$(openssl rand -base64 32)" > .env   # once; keep this file
docker compose up -d
```

This runs PostgreSQL, applies the migrations and starts the server on http://localhost:8080. Create the first instance admin, then sign in with the email and the password it prints:

```bash
docker compose exec flagnetic flagnetic admin bootstrap --email you@example.com
```

Then create a project, add an SDK key to one of its environments, and follow the [SDK](#sdks) for your language.

## Documentation

- [Installing Flagnetic](docs/install.md): Docker Compose or the binary, TLS and a reverse proxy, PostgreSQL, upgrades.
- [Operations](docs/operations.md) and the [configuration reference](docs/configuration.md).
- [Flags](docs/flags.md), [segments](docs/segments.md), [targeting](docs/targeting.md) and [access](docs/access.md).
- [SDKs](docs/sdks/README.md), [SDK keys](docs/sdks/keys.md), the [ruleset stream](docs/sdks/stream.md) and [compatibility](docs/sdks/compatibility.md); [everything else](docs/README.md).

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
