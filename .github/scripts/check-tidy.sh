#!/usr/bin/env bash
# Fails when the go.mod or go.sum of a module is not tidy.
#
# go mod tidy ignores go.work, so a module requiring another module of this
# repository at v0.0.0 (one not released yet, resolved only through the
# workspace) cannot be tidied as it stands: tidy would look the version up
# and fail. For such a module the check runs on a copy of it, where every
# such requirement is replaced by its directory in the workspace, and
# compares the tidied go.mod, the replacements dropped again, and go.sum
# with the originals. Any other module is checked in place.
set -euo pipefail

dir="${1:?usage: check-tidy.sh <module directory>}"
module="$(cd "$dir" && pwd)"
# The workspace in effect for the module: the committed go.work, or the
# one CI builds of the published modules alone.
work="$(cd "$module" && go env GOWORK)"
if [[ -z "$work" || "$work" == "off" ]]; then
  echo "$dir is not in a workspace" >&2
  exit 1
fi
base="$(dirname "$work")"

scratch="$(mktemp -d)"
trap 'rm -rf "$scratch"' EXIT

# "<module path> <directory>" for every module in the workspace.
go work edit -json "$work" | jq -r '.Use[].DiskPath' | while read -r use; do
  [[ "$use" == /* ]] || use="$base/$use"
  echo "$(awk '$1 == "module" { print $2; exit }' "$use/go.mod") $(cd "$use" && pwd)"
done > "$scratch/workspace"

# "<module path> <directory>" for every requirement at v0.0.0 that the
# workspace holds and the module does not already replace.
go mod edit -json "$module/go.mod" |
  jq -r '([.Replace[]?.Old.Path]) as $replaced | .Require[]? | select(.Version == "v0.0.0") | select(.Path as $p | $replaced | index($p) | not) | .Path' |
  while read -r path; do
    if ! awk -v p="$path" '$1 == p { found = 1; print } END { exit !found }' "$scratch/workspace"; then
      echo "$dir requires $path at v0.0.0, which the workspace does not hold" >&2
      exit 1
    fi
  done > "$scratch/unreleased"

if [[ ! -s "$scratch/unreleased" ]]; then
  (cd "$module" && GOWORK=off go mod tidy -diff)
  exit
fi

copy="$scratch/module"
mkdir "$copy"
cp -R "$module/." "$copy/"
while read -r path target; do
  (cd "$copy" && go mod edit -replace="$path=$target")
done < "$scratch/unreleased"
(cd "$copy" && GOWORK=off GOFLAGS=-mod=mod go mod tidy)
while read -r path _; do
  (cd "$copy" && go mod edit -dropreplace="$path")
done < "$scratch/unreleased"

status=0
diff -u "$module/go.mod" "$copy/go.mod" || status=1
if [[ -f "$module/go.sum" || -f "$copy/go.sum" ]]; then
  diff -u "$module/go.sum" "$copy/go.sum" || status=1
fi
if [[ $status -ne 0 ]]; then
  echo "$dir is not tidy: run go mod tidy there with a replace of each v0.0.0 requirement" >&2
fi
exit $status
