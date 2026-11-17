# Flagnetic Helm chart

Installs Flagnetic on Kubernetes against a PostgreSQL you run. The chart installs no database, and it never generates the secret key: the server refuses to start under a key other than the one it first stored, and a key made when the chart renders would change under `helm template` and Argo CD.

## Install

Create a Secret with the database URL and the secret key (keep the key for the life of the instance):

```bash
kubectl create namespace flagnetic
kubectl create secret generic flagnetic -n flagnetic \
  --from-literal=DATABASE_URL='postgres://flagnetic:PASSWORD@db.example.com:5432/flagnetic?sslmode=require' \
  --from-literal=FLAGNETIC_SECRET_KEY="$(openssl rand -base64 32)"
```

Install the chart with it, naming the address the console is reached at:

```bash
helm install flagnetic oci://ghcr.io/azverinus/charts/flagnetic -n flagnetic \
  --set secrets.existingSecret=flagnetic \
  --set config.base_url=https://flags.example.com
```

Then create the first instance admin once, as the notes the install prints say:

```bash
kubectl exec -n flagnetic deploy/flagnetic -- flagnetic admin bootstrap --email you@example.com
```

`helm test flagnetic -n flagnetic` asks the Service for `/readyz`.

Without an existing Secret, set `secrets.databaseUrl` and `secrets.secretKey`: the chart renders its own Secret from them. With neither, rendering fails with a message.

## Migrations

By default a Job runs `flagnetic migrate` before every install and upgrade (a Helm hook), and the Deployment starts with `FLAGNETIC_AUTO_MIGRATE=false`. A failed migration stops the upgrade before any pod is replaced, and the Job's log says why. Argo CD runs Helm hooks as PreSync on every sync, which is harmless because `migrate` is idempotent and takes a lock. Set `migrations.mode` to `initContainer` or `auto` for a tool that does not run hooks.

## Uninstall

The chart's own Secret, when you use one, is a Helm hook, so `helm uninstall` leaves it behind with the database URL and the secret key, and `helm rollback` does not restore an older one. Delete it yourself when the instance is gone, or keep the secrets in an `existingSecret` you manage.

## Rolling

Pods are replaced one at a time behind a PodDisruptionBudget. A stopping pod keeps serving for `FLAGNETIC_SHUTDOWN_DRAIN` while `/readyz` answers 503, then finishes in-flight requests within `FLAGNETIC_SHUTDOWN_GRACE`; the 30 s grace period of the pod covers both.

## Supported versions

| | Supported |
|---|---|
| Kubernetes | the three newest minors: 1.35, 1.36 and 1.37 |
| Helm | 3.18 and newer, and 4 |
| PostgreSQL | 16 or newer |

The chart's `version` and `appVersion` are the server's: a chart installs the image of its own version unless `image.tag` says otherwise.

## Values

| Value | Default | Description |
|---|---|---|
| `nameOverride` | `""` | Replaces the chart name in resource names. |
| `fullnameOverride` | `""` | Replaces the whole resource name prefix. |
| `image.repository` | `ghcr.io/azverinus/flagnetic` | The server image. |
| `image.tag` | `""` | The image tag; the chart's `appVersion` when empty. |
| `image.pullPolicy` | `IfNotPresent` | The image pull policy. |
| `imagePullSecrets` | `[]` | Secrets to pull the image with. |
| `replicaCount` | `2` | Replicas of the server. Each holds its own connection pool and rate limits. |
| `secrets.existingSecret` | `""` | Name of a Secret holding `DATABASE_URL` and `FLAGNETIC_SECRET_KEY`. Recommended. Without it the chart renders its own Secret from the two values below, or refuses to render. |
| `secrets.databaseUrlKey` | `DATABASE_URL` | Key of the database URL in the Secret. |
| `secrets.secretKeyKey` | `FLAGNETIC_SECRET_KEY` | Key of the secret key in the Secret. |
| `secrets.databaseUrl` | `""` | The database URL for the chart's own Secret, which is used when `secrets.existingSecret` is empty. PostgreSQL 16 or newer. |
| `secrets.secretKey` | `""` | The secret key for the chart's own Secret. At least 32 bytes, kept for the life of the instance. The chart never generates one. |
| `config` | `{}` | Settings as `FLAGNETIC_*` variables: the key is the setting's file key in [the configuration reference](../../../docs/configuration.md), so `log_level` becomes `FLAGNETIC_LOG_LEVEL`. The listeners, `auto_migrate`, `database_url` and `secret_key` are the chart's own and are refused. |
| `extraEnv` | `[]` | Extra container environment variables, as in a container spec. |
| `extraEnvFrom` | `[]` | Extra `envFrom` sources, as in a container spec. |
| `migrations.mode` | `hook` | `hook` runs `flagnetic migrate` in a Job before every install and upgrade, so a failed migration stops the upgrade before a pod is replaced. `initContainer` migrates in every pod before it serves. `auto` lets the server migrate when it starts. Use `initContainer` or `auto` where hooks are not run, such as `helm template` output applied by hand. |
| `migrations.activeDeadlineSeconds` | `900` | Deadline of the migration Job. The server waits up to five minutes for the migration lock, so keep it above 330. |
| `migrations.resources` | `{}` | Resources of the migration container. |
| `serviceAccount.create` | `true` | Create a ServiceAccount. It never mounts a token. |
| `serviceAccount.name` | `""` | Name of the ServiceAccount; the release's full name when empty. |
| `serviceAccount.annotations` | `{}` | Annotations of the ServiceAccount. |
| `podAnnotations` | `{}` | Annotations of the pods. |
| `podLabels` | `{}` | Extra labels of the pods. |
| `terminationGracePeriodSeconds` | `30` | Time a pod has to stop. It covers the server's drain (5 s) and grace (15 s) with a margin; keep it at 25 or more. |
| `probes.startup.periodSeconds` | `2` | Period of the startup probe on `/readyz`. |
| `probes.startup.failureThreshold` | `60` | Failures the startup probe tolerates, which leaves two minutes for a cold start. |
| `probes.readiness.periodSeconds` | `5` | Period of the readiness probe on `/readyz`. |
| `probes.readiness.failureThreshold` | `3` | Failures before a pod leaves the Service. |
| `probes.liveness.periodSeconds` | `10` | Period of the liveness probe on `/healthz`. |
| `probes.liveness.failureThreshold` | `3` | Failures before a pod is restarted. |
| `resources.requests.cpu` | `100m` | CPU the server container requests. |
| `resources.requests.memory` | `256Mi` | Memory the server container requests. Password hashing takes 64 MiB per sign-in in flight. |
| `resources.limits.memory` | `512Mi` | Memory limit of the server container. |
| `service.type` | `ClusterIP` | Type of the `http` Service. |
| `service.port` | `8080` | Port of the `http` Service and of the server's listener. |
| `service.annotations` | `{}` | Annotations of the `http` Service. |
| `service.metrics.port` | `9090` | Port of the `metrics` Service and of the metrics listener, which stays on a ClusterIP Service. |
| `service.metrics.annotations` | `{}` | Annotations of the `metrics` Service. |
| `podDisruptionBudget.enabled` | `true` | Render a PodDisruptionBudget. |
| `podDisruptionBudget.maxUnavailable` | `1` | Pods that may be unavailable during a voluntary disruption. |
| `topologySpreadConstraints.enabled` | `true` | Spread replicas over zones and hosts. The constraints are soft: a cluster with one zone or one node still schedules every replica. |
| `topologySpreadConstraints.maxSkew` | `1` | Largest difference in replicas between two zones or two hosts. |
| `nodeSelector` | `{}` | Node selector of the pods. |
| `tolerations` | `[]` | Tolerations of the pods. |
| `affinity` | `{}` | Affinity of the pods. |
| `test.image.repository` | `busybox` | Image of the `helm test` Pod. |
| `test.image.tag` | `1.37.0` | Tag of the `helm test` image. |
| `test.resources` | `{}` | Resources of the `helm test` Pod. |
