#!/usr/bin/env bash
# The rolling-upgrade test. In a kind cluster that publishes NODE_PORT on the
# host and holds the head image, it installs the head chart with the last
# release's image, seeds the instance through the management API, and runs
# flagnetic-upgrade, which keeps light SDK load and a streaming Go SDK on the
# instance while `helm upgrade` moves it to the head image. The chart's
# migration hook, its disruption budget and the server's drain are all in the
# path, which is the point of doing it in a cluster.
#
#   UPGRADE_FROM    image tag of the last server release, such as 0.5.0 (required)
#   HEAD_IMAGE      repository:tag of the head image, loaded into the cluster
#                   (default flagnetic:upgrade-head)
#   NODE_PORT       the port kind publishes on the host (default 30080)
#
# The pods the chart starts need no registry for the head image: it runs with
# pullPolicy Never.
set -euo pipefail

: "${UPGRADE_FROM:?UPGRADE_FROM must name the image tag of the last release, such as 0.5.0}"
head_image="${HEAD_IMAGE:-flagnetic:upgrade-head}"
node_port="${NODE_PORT:-30080}"
release=flagnetic
root="$(git rev-parse --show-toplevel)"
work="$(mktemp -d)"

diagnose() {
  echo "::group::the cluster at the failure"
  kubectl get pods,jobs,events -o wide || true
  kubectl logs -l "app.kubernetes.io/instance=$release" --all-containers --tail=60 --prefix || true
  helm history "$release" || true
  echo "::endgroup::"
}
cleanup() {
  local status=$?
  [ "$status" -eq 0 ] || diagnose
  rm -rf "$work"
  exit "$status"
}
trap cleanup EXIT

(cd "$root/e2e" && go build -o "$work/flagnetic-upgrade" ./cmd/flagnetic-upgrade)

echo "upgrade: PostgreSQL"
kubectl apply -f "$root/deploy/helm/test/postgres.yaml"
kubectl rollout status deployment/postgres --timeout=120s

chart="$root/deploy/helm/flagnetic"
values="$chart/ci/default-values.yaml"

echo "upgrade: installing the head chart with the image of $UPGRADE_FROM"
helm install "$release" "$chart" -f "$values" \
  --set "image.tag=$UPGRADE_FROM" --wait --wait-for-jobs --timeout 5m

# A Service of the test, not of the chart: it publishes the pods on the node
# port, and the proxy of the node sends a request only to a ready pod, as a
# load balancer in front of the chart would.
kubectl apply -f - <<YAML
apiVersion: v1
kind: Service
metadata:
  name: $release-node
spec:
  type: NodePort
  selector:
    app.kubernetes.io/name: flagnetic
    app.kubernetes.io/instance: $release
  ports:
    - port: 8080
      targetPort: http
      nodePort: $node_port
YAML

admin="$("$work/flagnetic-upgrade" admin)"
kubectl exec "deploy/$release" -- env "FLAGNETIC_ADMIN_PASSWORD=$(jq -r .password <<<"$admin")" \
  flagnetic admin bootstrap --email "$(jq -r .email <<<"$admin")" --name "$(jq -r .name <<<"$admin")" \
  --if-absent --token-file /tmp/bootstrap-token

url="http://127.0.0.1:$node_port"
for _ in $(seq 1 60); do
  curl -fsS "$url/readyz" >/dev/null 2>&1 && break
  sleep 1
done
curl -fsS "$url/readyz" >/dev/null

head_repo="${head_image%:*}"
head_tag="${head_image##*:}"
cat >"$work/helm-upgrade.sh" <<SCRIPT
set -euo pipefail
helm upgrade "$release" "$chart" -f "$values" \\
  --set "image.repository=$head_repo" --set "image.tag=$head_tag" --set image.pullPolicy=Never \\
  --wait --wait-for-jobs --timeout 5m
# A hook Job that failed fails the upgrade above; a release whose last
# revision is not deployed is the other way an upgrade can go wrong.
test "\$(helm status "$release" -o json | jq -r .info.status)" = deployed
SCRIPT
"$work/flagnetic-upgrade" run --url "$url" -- bash "$work/helm-upgrade.sh"
