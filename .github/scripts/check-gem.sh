#!/usr/bin/env bash
# Builds a gem as it is published and fails when the files it packs are not
# the files the repository tracks for it: everything under lib/, the README
# and the LICENSE. The gemspec lists its files with a glob, so a file the
# glob misses (a new kind of file under lib/) or one the repository does not
# track (left over in a checkout) is found on the pull request rather than
# in the published gem.
#
#   check-gem.sh <gem directory> <gemspec> [output directory]
#
# The gem is written to the output directory (a temporary one by default),
# from which the release job publishes it.
set -euo pipefail

dir="${1:?usage: check-gem.sh <gem directory> <gemspec> [output directory]}"
gemspec="${2:?usage: check-gem.sh <gem directory> <gemspec> [output directory]}"
out="${3:-}"
scratch="$(mktemp -d)"
trap 'rm -rf "$scratch"' EXIT
mkdir -p "${out:=$scratch/gem}"
out="$(cd "$out" && pwd)"
cd "$dir"

name="$(ruby -e 'print Gem::Specification.load(ARGV[0]).file_name' "$gemspec")"
# --strict turns every warning about the gemspec (an open-ended requirement,
# missing metadata) into a failure.
gem build --strict "$gemspec" --output "$out/$name"

ruby -rrubygems/package -e 'puts Gem::Package.new(ARGV[0]).contents.sort' "$out/$name" > "$scratch/packed"
git ls-files -- lib README.md LICENSE | LC_ALL=C sort > "$scratch/tracked"
if ! diff -u --label tracked --label packed "$scratch/tracked" "$scratch/packed"; then
  echo "$name does not pack the files the repository tracks in $dir: '-' only tracked, '+' only packed" >&2
  exit 1
fi
echo "$name packs the $(wc -l < "$scratch/packed" | tr -d ' ') files the repository tracks"
