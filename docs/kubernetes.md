---
title: "Running Flagnetic on Kubernetes"
description: "The Helm chart, PostgreSQL, the Secret, migration modes, routes, metrics, scaling, network policy and upgrades."
---

# Running Flagnetic on Kubernetes

The Helm chart in [`deploy/helm/flagnetic`](../deploy/helm/flagnetic/README.md) runs Flagnetic on Kubernetes: two replicas behind a Service, a PodDisruptionBudget, a Job that migrates the database before every install and upgrade, and, when you turn them on, an Ingress or Gateway API route, Prometheus rules, an autoscaler and a network policy. This guide takes you from an empty cluster to a running instance and through its upgrades. The chart's README lists every value; [Installing Flagnetic](install.md) covers Docker Compose and the binary, and [Operations](operations.md) covers running the server once it is up.

## Prerequisites

- **Kubernetes**: the three newest minor versions (1.35, 1.36 and 1.37 as this is written). Newer ones are not refused.
- **Helm**: 3.18 or newer, or 4.
- **PostgreSQL 16 or newer** that the cluster can reach, with the `pg_trgm` extension available (see [What it needs](operations.md#what-it-needs)). The chart installs no database; a managed service is the simplest choice, and the next section shows one you run in the cluster.
- **A secret key**, generated once and kept for the life of the instance: `openssl rand -base64 32`. The chart never generates one, because a key made when the chart renders would change under `helm template` and Argo CD, and the server refuses to start under a key other than the one it first stored. See [The secret key](operations.md#the-secret-key).
- **TLS in front**, for production: the server speaks plain HTTP, and the console's session cookie is `Secure` anywhere but `localhost`. See [Routes](#routes).

## PostgreSQL

Use your cloud's managed PostgreSQL if you have one, and skip to [The Secret](#the-secret). To run PostgreSQL in the cluster, [CloudNativePG](https://cloudnative-pg.io) is one way: it manages the instances, failover and backups. With its operator installed, this `Cluster` creates a database `flagnetic` owned by a role of the same name, and the extension Flagnetic needs:

```yaml
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: flagnetic-db
  namespace: flagnetic
spec:
  instances: 3
  storage:
    size: 20Gi
  bootstrap:
    initdb:
      database: flagnetic
      owner: flagnetic
      # The owner may not create an extension, so the superuser does it once.
      postInitApplicationSQL:
        - CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public
```

The operator writes the owner's credentials to the Secret `flagnetic-db-app`, whose `uri` key is a connection string for the read-write Service `flagnetic-db-rw`. Backups, a PostgreSQL version and the rest are the operator's to configure; see its documentation. Every replica of Flagnetic holds its own connection pool, so keep the replicas' total within the database's `max_connections` (see [PostgreSQL connections](install.md#postgresql-connections)).

## The Secret

Keep the database URL and the secret key in a Secret of your own and name it with `secrets.existingSecret`. With CloudNativePG's `uri`:

```bash
kubectl create namespace flagnetic
kubectl create secret generic flagnetic -n flagnetic \
  --from-literal=DATABASE_URL="$(kubectl get secret flagnetic-db-app -n flagnetic -o jsonpath='{.data.uri}' | base64 -d)" \
  --from-literal=FLAGNETIC_SECRET_KEY="$(openssl rand -base64 32)"
```

With another database, give `DATABASE_URL` its connection string, for example `postgres://flagnetic:PASSWORD@db.example.com:5432/flagnetic?sslmode=require`. **Back the secret key up outside the cluster**: with a lost key the stored secrets (SSO client secrets, sink and SMTP credentials) cannot be read again.

The Secret may have other key names; set `secrets.databaseUrlKey` and `secrets.secretKeyKey`. For a tool that keeps secrets in git, an external secrets operator or a sealed secret produces the same Secret. Without an `existingSecret`, `secrets.databaseUrl` and `secrets.secretKey` make the chart render a Secret of its own from your values: convenient for a trial, but the values then sit in your release's configuration. With neither, rendering fails with a message.

## Install

```bash
helm install flagnetic oci://ghcr.io/azverinus/charts/flagnetic -n flagnetic \
  --version <version> \
  --set secrets.existingSecret=flagnetic \
  --set config.base_url=https://flags.example.com
```

`config.base_url` is the address the console is reached at; set it to the public URL even before the route exists. Pass `--version` to pin the chart: its version is the server's, and it installs the image of its own version.

Every other setting is a value under `config`, named by its file key in the [configuration reference](configuration.md): `config.log_level: debug` becomes `FLAGNETIC_LOG_LEVEL`. Keep your values in a file and pass it with `-f values.yaml`:

```yaml
replicaCount: 2
secrets:
  existingSecret: flagnetic
config:
  base_url: https://flags.example.com
```

The release waits for the migration Job, then rolls out the pods. Check it, then create the first instance admin once, which prints a password and an API token:

```bash
helm test flagnetic -n flagnetic
kubectl exec -n flagnetic deploy/flagnetic -- flagnetic admin bootstrap --email you@example.com
```

`helm test` asks the Service for `/readyz`. Until a route is set up, `kubectl port-forward -n flagnetic svc/flagnetic 8080:8080` reaches the console at http://localhost:8080.

## Migrations

`migrations.mode` decides who migrates the database:

| Mode | What happens | Pick it when |
|---|---|---|
| `hook` (default) | A Job runs `flagnetic migrate` before every install and upgrade, and the pods start with auto-migration off. A failed migration stops the upgrade before any pod is replaced, and the Job's log says why. | Helm runs the release: `helm install` and `helm upgrade`. |
| `initContainer` | Every pod runs `flagnetic migrate` before its server starts. Replicas take turns through a PostgreSQL lock, and each migration is applied once. | The tool applying the chart does not run Helm hooks: `helm template` output applied with `kubectl`, or a renderer that drops hooks. |
| `auto` | The server migrates when it starts. | A single replica where you accept that a long migration delays startup; the startup probe allows two minutes. |

**Argo CD** renders the chart with `helm template` and runs Helm hooks as `PreSync` hooks on every sync, so the default works: the Job runs before each sync, which is harmless because `migrate` is idempotent and takes a lock. If you would rather not run a Job on every sync, use `initContainer`. The chart's own Secret, when you let it render one, is a hook too, so under Argo CD keep the secrets in an `existingSecret`.

**`helm template`** output applied with `kubectl` creates the hook Job as an ordinary resource, at the same time as the pods rather than before them, so render with `--set migrations.mode=initContainer` (or run `flagnetic migrate` yourself first and use `auto`).

A migration the new version brings only moves the schema forward, in a way the previous minor still runs against, which is what makes the rolling upgrade below safe.

## Routes

Neither route is on by default; turn on the one your cluster uses. An SDK holds a ruleset stream open for up to 65 minutes, and the ruleset stream and the console's change stream send a heartbeat every 15 seconds. The proxy in front must therefore not buffer those streams, must time out an idle connection above 15 seconds, and must not end a request before the stream does; an hour is a safe idle timeout.

**Ingress**, here for ingress-nginx, with the timeouts and TLS:

```yaml
ingress:
  enabled: true
  className: nginx
  hosts: [flags.example.com]
  annotations:
    nginx.ingress.kubernetes.io/proxy-read-timeout: "3600"
    nginx.ingress.kubernetes.io/proxy-send-timeout: "3600"
    nginx.ingress.kubernetes.io/proxy-buffering: "off"
  tls:
    - secretName: flagnetic-tls
      hosts: [flags.example.com]
```

**Gateway API**: attach an `HTTPRoute` to your Gateway, and raise the Gateway's idle timeout above 15 seconds and its request timeout above 65 minutes (or turn the request timeout off) in your implementation's own way, since the route cannot:

```yaml
httpRoute:
  enabled: true
  parentRefs:
    - name: public
      namespace: gateways
  hostnames: [flags.example.com]
```

The metrics are on a Service of their own that neither route reaches. Behind a proxy, also read [Behind a reverse proxy](operations.md#behind-a-reverse-proxy).

## Metrics

The `metrics` Service exposes the server's Prometheus metrics on port 9090 (see [Metrics](operations.md#metrics)). With the Prometheus Operator's CRDs installed, the chart renders the scrape configuration and four alerts:

```yaml
serviceMonitor:
  enabled: true
  labels:
    release: prometheus   # whatever your Prometheus selects ServiceMonitors by
prometheusRule:
  enabled: true
  labels:
    release: prometheus
```

The alerts are `FlagneticListenerDown`, `FlagneticRulesetPropagationSlow` (the 99th percentile above `prometheusRule.propagationSeconds`, 2 s by default), `FlagneticSinkStopped` and `FlagneticWebhookDeliveriesFailing`. They select the series of this release's metrics Service, so they rely on the ServiceMonitor or on a scrape job of the same name. Without the Operator, point your own scrape job at the `metrics` Service.

## Scaling

The chart runs two replicas, which every API and background worker is identical on, with no sticky sessions: any number of replicas works against one database. Spread over zones and hosts is on by default as a soft constraint, so a cluster with one node still schedules them. A PodDisruptionBudget lets one pod be unavailable during a voluntary disruption such as a node drain.

To scale by load, replace the fixed count with a HorizontalPodAutoscaler on CPU:

```yaml
autoscaling:
  enabled: true
  minReplicas: 2
  maxReplicas: 6
  targetCPUUtilizationPercentage: 70
```

The target is a share of `resources.requests.cpu` (100m by default), so size the request to the pod's real use. Each replica holds its own PostgreSQL pool, so keep `maxReplicas` times the pool size within `max_connections`. Turning the autoscaler on in an upgrade removes the Deployment's replica count, which Kubernetes may reset to one until the autoscaler scales back up. The memory request and limit (256 Mi and 512 Mi) are sized for password hashing, which takes 64 MiB per sign-in in flight.

## Network policy

With a network plugin that enforces them, `networkPolicy.enabled` restricts the server's pods: ingress to the `http` port from the sources you list (every source when none), to the `metrics` port only from the sources you list, and egress to DNS, PostgreSQL and the rules you add. List everything the instance reaches in `networkPolicy.egress.rules`: webhook receivers, sinks, SMTP servers and identity providers (OIDC and SAML).

```yaml
networkPolicy:
  enabled: true
  ingress:
    http:
      from:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: ingress-nginx
    metrics:
      from:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: monitoring
  egress:
    postgresql:
      to:
        - podSelector:
            matchLabels:
              cnpg.io/cluster: flagnetic-db
    rules:
      - to:
          - ipBlock:
              cidr: 0.0.0.0/0
              except: [10.0.0.0/8]
        ports:
          - port: 443
```

The example admits the ingress controller to the console and the monitoring namespace to the metrics, restricts PostgreSQL to the CloudNativePG pods, and lets the server call HTTPS endpoints outside the cluster's private range: the shape webhook receivers, sinks and identity providers usually have. Narrow the last rule to the hosts you use. The migration Job and the `helm test` Pod are not selected by the policy.

## Upgrading

Read the release notes of every version between yours and the new one; a change that needs you to act is marked there. Back up the database first. Then upgrade the release:

```bash
helm upgrade flagnetic oci://ghcr.io/azverinus/charts/flagnetic -n flagnetic \
  --version <new version> -f values.yaml
```

Pass your values file again rather than `--reuse-values`, which would keep the old release's defaults for values the new chart adds. The hook Job migrates the database, and only when it succeeds does Helm roll the Deployment, replacing the pods a few at a time while the others keep serving. A stopping pod answers `/readyz` with 503 for the drain, then finishes its requests, so the Service stops routing to it before it goes and no request is refused; the 30-second `terminationGracePeriodSeconds` covers both (see [Stopping](operations.md#stopping)). A failed migration fails the upgrade with every pod still on the old version; read the Job's log with `kubectl logs -n flagnetic job/flagnetic-migrate`, fix the cause and upgrade again.

`helm rollback` returns the chart to a previous revision but not the database. Migrations only move forward and there are no down migrations, so to go back after a migration, restore the backup taken before the upgrade. The rest of the policy, and the note for each release, is in [Upgrading Flagnetic](upgrades.md) and [Compatibility](compatibility.md).

## Uninstalling

```bash
helm uninstall flagnetic -n flagnetic
```

This removes the workloads and Services but not your data: it is in PostgreSQL, which the chart does not own. The chart's own Secret, when you let it render one, is a Helm hook and so is left behind with the database URL and the secret key; delete it yourself when the instance is gone. An `existingSecret`, the CloudNativePG `Cluster` and its volumes are yours and stay until you delete them.

## Verifying the chart

Each release publishes the chart to GHCR with a build attestation made by the release workflow. Check it before installing, with the GitHub CLI:

```bash
helm pull oci://ghcr.io/azverinus/charts/flagnetic --version <version>
gh attestation verify oci://ghcr.io/azverinus/charts/flagnetic:<version> --repo azverinus/flagnetic
```

[Verifying a release](verify.md) covers `cosign` too, and the image and the SDK packages.

The chart is also listed on Artifact Hub.
