#!/usr/bin/env bash
# Builds and tests a published module outside the workspace, against the
# versions of this repository's modules its go.mod requires, which is what a
# program running go get receives.
#
# Before a module's dependency is first released the module requires it at
# v0.0.0, which only the workspace resolves, so the check is skipped then.
# It fails instead when that skip would hide a broken release: when the
# dependency has been tagged (its requirement is due for the bump), or when
# the module itself is being released, or was, while it still requires an
# unreleased version.
set -euo pipefail

dir="${1:?usage: check-released-requirements.sh <module directory>}"
root="$(cd "$(dirname "$0")/../.." && pwd)"
module="$(cd "$dir" && pwd)"
rel="${module#"$root"}"
rel="${rel#/}"
prefix="github.com/azverinus/flagnetic/"

# Direct requirements only: an indirect one at v0.0.0 is raised by the
# released version a direct requirement brings, and go mod tidy rewrites it
# with the bump that brings it.
unreleased="$(go mod edit -json "$module/go.mod" |
  jq -r --arg p "$prefix" '.Require[]? | select(.Indirect | not) | select(.Version == "v0.0.0") | select(.Path | startswith($p)) | .Path')"

if [[ -z "$unreleased" ]]; then
  echo "$rel requires only released versions; building and testing it outside the workspace"
  (cd "$module" && GOWORK=off go build ./... && GOWORK=off go test ./...)
  exit
fi

status=0
version="$(jq -r --arg d "$rel" '.[$d] // "0.0.0"' "$root/.release-please-manifest.json")"
if [[ "$version" != "0.0.0" ]]; then
  echo "$rel is released as $version but requires an unreleased module at v0.0.0:" >&2
  echo "$unreleased" | sed 's/^/  /' >&2
  status=1
fi
for path in $unreleased; do
  dep="${path#"$prefix"}"
  if [[ -n "$(git -C "$root" tag -l "$dep/v*")" ]]; then
    echo "$rel requires $path at v0.0.0, but $dep is released: bump the requirement" >&2
    status=1
  fi
done
if [[ $status -eq 0 ]]; then
  echo "$rel requires unreleased modules at v0.0.0, so only the workspace builds it; skipped:"
  echo "$unreleased" | sed 's/^/  /'
fi
exit $status
