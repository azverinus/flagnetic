#!/usr/bin/env bash
# Checks a pub package in <dir> as pub.dev will see it: `pub publish
# --dry-run` without a warning, and pana granting every point. A Flutter
# package is checked with the Flutter SDK on the PATH.
#
# release-please writes the version's CHANGELOG entry in the release pull
# request, and both checks want the version in CHANGELOG.md. Until that pull
# request, a stand-in entry is written in the checkout, so every pull request
# scores the package as it will be released; pass --keep-changelog to check
# the changelog as it is, as the publishing workflow does.
set -euo pipefail

keep=false
if [[ "${1:-}" == --keep-changelog ]]; then
  keep=true
  shift
fi
dir="${1:?usage: check-pub-package.sh [--keep-changelog] <package dir>}"
pana_version=0.23.19

cd "$dir"
version="$(sed -n 's/^version: *//p' pubspec.yaml)"
if [[ "$keep" == false ]] && ! grep -qF "$version" CHANGELOG.md 2>/dev/null; then
  {
    printf '# Changelog\n\n## %s\n\n* Not released yet.\n' "$version"
    if [[ -f CHANGELOG.md ]]; then sed '1{/^# Changelog$/d;}' CHANGELOG.md; fi
  } > CHANGELOG.md.new
  mv CHANGELOG.md.new CHANGELOG.md
fi

dart pub publish --dry-run

flutter_sdk=()
if grep -qE '^\s+sdk: flutter' pubspec.yaml; then
  flutter_sdk=(--flutter-sdk "$(dirname "$(dirname "$(command -v flutter)")")")
fi
dart pub global activate pana "$pana_version" > /dev/null
dart pub global run pana --no-warning --exit-code-threshold 0 ${flutter_sdk[@]+"${flutter_sdk[@]}"} .
