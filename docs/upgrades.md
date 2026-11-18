---
title: "Upgrading Flagnetic"
description: "From 0.x to 1.0, rolling upgrades between 1.x releases with the chart, Compose or systemd, what adjacent minors means, and going back."
---

# Upgrading Flagnetic

Read the release notes of every version between yours and the new one first: a change that needs you to act is marked there. Then follow the part of this page for the version you run:

- [From 0.x to 1.0](#from-0x-to-10): stop every replica, back up, migrate, start.
- [Between 1.x releases](#between-1x-releases): rolling, with no downtime, with the chart, Docker Compose or systemd.
- [Going back](#going-back): restore the backup.

The SDK API (`/sdk/v1`) and the ruleset format only grow, so SDKs keep working across server upgrades without being upgraded themselves.

## From 0.x to 1.0

0.x does not promise that two versions serve side by side, so every replica stops for the upgrade and you accept a brief downtime.

1. **Stop every replica.** On Kubernetes, `kubectl scale deployment/flagnetic --replicas=0` before the upgrade: the chart's migration hook runs while the old pods are still up. With Compose, `docker compose stop flagnetic`; under systemd, `systemctl stop flagnetic` on every host.
2. **Back up the database.** Taken with nothing writing, it holds every change up to the stop, and it is the way back if anything goes wrong; see [Going back](#going-back).
3. **Migrate.** Run `flagnetic migrate` with the new version, as a job. The chart does it in its hook Job and the Compose file in its `migrate` service. Or leave `FLAGNETIC_AUTO_MIGRATE` on: the first new replica migrates, and replicas starting together take turns.
4. **Start the new replicas.** `helm upgrade` with the new chart (it sets the replica count back), `docker compose up -d` after setting the new `FLAGNETIC_VERSION` in `.env`, or `systemctl start flagnetic` with the new binary in place.

A replica never runs against a schema older than its own: with auto-migration off it refuses to start until `flagnetic migrate` has run (see [Migrations](operations.md#migrations)).

The management API (`/api/v1`) and the console may still change incompatibly between 0.x releases, each such change marked as breaking in the release notes; check scripts that use the management API against them.

## Between 1.x releases

From 1.0.0, replicas of one minor version run correctly on the schema of the next minor, and any two patches of one minor run together. An upgrade therefore needs no downtime: migrate first, while the current replicas keep serving, then replace the replicas one at a time.

### What "adjacent minors" means

Minor versions are adjacent when they follow one another: 1.4 and 1.5, but not 1.4 and 1.6. For every migration that ships in 1.(x+1):

- It only adds: it never renames or drops a table, a column or a constraint that 1.x reads or writes. What 1.(x+1) stops using is dropped one minor later, in 1.(x+2), when no replica of 1.x can still be running.
- 1.x and 1.(x+1) can therefore serve the same database at the same moment, which is what happens during the rollout: old replicas on the migrated schema, then a mix, then new replicas only.
- Patches of one minor carry no migration that breaks an older patch.

To go further than one minor, upgrade through each one in turn (1.4, 1.5, then 1.6), finishing a rollout before starting the next. To go straight from 1.4 to 1.6, treat it as the stop-the-world upgrade of [the 0.x steps](#from-0x-to-10).

### Kubernetes with the chart

```bash
helm upgrade flagnetic oci://ghcr.io/azverinus/charts/flagnetic -n flagnetic \
  --version <new version> -f values.yaml
```

Pass your values file again rather than `--reuse-values`, which keeps the old release's defaults for values the new chart adds. [Running on Kubernetes](kubernetes.md#upgrading) has the rest.

The chart's `pre-upgrade` hook Job runs `flagnetic migrate` with the new image before any pod is replaced, while the old pods serve. A failed migration stops the upgrade and leaves the old pods running. The Deployment's rolling update then replaces the pods, and the PodDisruptionBudget keeps at most one of them away from voluntary disruptions: a new pod receives requests when `/readyz` answers, an old one leaves the Service when it starts its [drain](operations.md#stopping) and finishes its in-flight requests within the termination grace period. Keep at least two replicas for this to be free of downtime, which is the chart's default. Settings that choose another migration mode (`migrations.mode`) are in the [chart's README](../deploy/helm/flagnetic/README.md).

### Docker Compose

The Compose file in `deploy/` runs one server replica, so an upgrade restarts it for a few seconds. Set the new `FLAGNETIC_VERSION` in `.env` and run `docker compose pull && docker compose up -d`: the `migrate` job runs with the new image first, then the server is recreated. If you run several replicas of the server behind a proxy, migrate first (`docker compose run --rm migrate`), then recreate the replicas one at a time and wait until each is healthy before the next.

### systemd

With the replicas on several hosts behind a load balancer:

1. On any host, run the new binary's `flagnetic migrate` with the configuration of the instance. The running replicas keep serving.
2. On each host in turn: put the new binary in place, `systemctl restart flagnetic`, and wait until `flagnetic healthcheck` exits 0 before the next host. The load balancer must send requests only to ready replicas (`/readyz`), and the unit's `TimeoutStopSec` must cover the drain, as in the [example unit](install.md#run-it).

With one host, the restart is a few seconds of downtime.

### How it is tested

Every pull request that adds a migration or changes the chart, every release pull request, and a nightly run install the chart in a Kubernetes cluster with the image of the last release, seed the instance through the management API, and upgrade it to the image of the change while SDK requests and a streaming SDK keep running. The run fails when the migration hook fails, when any request fails or answers `5xx` during the rollout, when the SDK stops evaluating, or when it does not receive a change made after the upgrade. Until v1.0.0 is released the result is only reported.

## Going back

Migrations only move forward, without data loss: a released migration is never edited, and there are no down migrations. To go back, restore the backup taken before the upgrade and deploy the previous version. Everything written since the backup is lost, so decide quickly and prefer a point-in-time restore where your PostgreSQL offers one.

A backup is a `pg_dump` or a physical copy; [Installing Flagnetic](install.md#with-docker-compose) shows both. A logical dump restored into a new cluster needs one more step for the rollups and sinks, described in [After restoring a logical dump](operations.md#after-restoring-a-logical-dump).

An older replica that meets a newer schema (with auto-migration off, as under the chart) starts and logs a warning naming both versions; that is what a rollout does for a few minutes between adjacent minors, and it is not a way to run old code for long. Restoring the backup is the way back.

## Notes by version

### Upgrading to 0.5

Two changes need a look before the upgrade:

- **The metrics listener answers on this host only.** `FLAGNETIC_METRICS_LISTEN_ADDR` now defaults to `127.0.0.1:9090`. The container image and the Compose file are unchanged in effect (the image sets `:9090`, and Compose publishes it on the host's loopback). A binary install that a Prometheus on another host scrapes sets the address of an interface it can reach, for example `10.0.0.5:9090`. See [Metrics](operations.md#metrics).
- **A script's API token can no longer create or widen access.** Granting a role, inviting a member to the instance or a project, reactivating or editing a member, and issuing a password reset need a signed-in session, as creating a token already did; a token is refused `403 session_required`. See [Only a session creates a credential](access.md#only-a-session-creates-a-credential).

Secrets sealed before 0.5 keep opening and are bound to their row the next time it is stored ([Operations](operations.md#the-secret-key)).
