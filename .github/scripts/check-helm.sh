#!/usr/bin/env bash
# Checks the Helm chart without a cluster: helm lint, the helm-unittest suites,
# and promtool on the alerts, and kubeconform on what the chart renders for every values file in
# deploy/helm/flagnetic/ci and render, against each supported Kubernetes minor,
# and the Gateway API and Prometheus Operator custom resources against the
# schemas of the Datree CRDs catalog. CI runs it
# with Helm 3 and Helm 4; `task pr:check` runs it with the Helm on the PATH.
#
#   check-helm.sh
#
# HELM, KUBECONFORM and KUBERNETES_VERSIONS override the tools and the minors.
# Needs helm, the helm-unittest plugin and kubeconform; promtool too, which is
# optional outside CI (CI sets CI and fails without it).
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"
chart=deploy/helm/flagnetic
helm="${HELM:-helm}"
kubeconform="${KUBECONFORM:-kubeconform}"
promtool="${PROMTOOL:-promtool}"
# The three newest Kubernetes minors, as in Chart.yaml and the README.
versions="${KUBERNETES_VERSIONS:-1.35.0,1.36.0,1.37.0}"

echo "== $("$helm" version --short)"

# ci/ holds what `ct install` installs; render/ holds values that need CRDs a
# kind cluster lacks, so they are rendered and checked but never installed.
values_files=("$chart"/ci/*-values.yaml "$chart"/render/*-values.yaml)
# The catalog is pinned to a commit so a change there cannot change the result.
crds='https://raw.githubusercontent.com/datreeio/CRDs-catalog/784ec895547a36e548e6810b41f27baade3a0890/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json'

for values in "${values_files[@]}"; do
  echo "== helm lint $values"
  "$helm" lint --strict "$chart" -f "$values"
done

echo "== helm unittest"
"$helm" unittest "$chart"

for values in "${values_files[@]}"; do
  echo "== kubeconform $values ($versions)"
  # A missing schema is an error.
  for v in $(echo "$versions" | tr ',' ' '); do
    "$helm" template flags "$chart" -f "$values" --kube-version "$v" |
      "$kubeconform" -strict -kubernetes-version "$v" -schema-location default -schema-location "$crds" -
  done
done
# The alerts must be rules Prometheus accepts, expressions and templates
# included. The rendered resource's spec is a rule file's body.
if command -v "$promtool" >/dev/null 2>&1; then
  echo "== promtool check rules"
  rules=$(mktemp)
  trap 'rm -f "$rules"' EXIT
  "$helm" template flags "$chart" -f "$chart/ci/default-values.yaml" --set prometheusRule.enabled=true \
    --show-only templates/prometheusrule.yaml | sed -n '/^spec:/,$p' | sed '1d; s/^  //' >"$rules"
  "$promtool" check rules "$rules"
elif [ -n "${CI:-}" ]; then
  echo "promtool is needed to check the alerts" >&2
  exit 1
else
  echo "== promtool is not installed: the alerts are not checked"
fi
echo "ok: the chart passes lint, its unit tests, promtool and kubeconform"
