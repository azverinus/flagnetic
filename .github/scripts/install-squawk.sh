#!/usr/bin/env bash
# Installs the pinned squawk (the PostgreSQL migration linter) into a directory
# and prints nothing but errors. The download is checked against the SHA-256
# below before it is made executable, so a changed release asset cannot run.
#
#   install-squawk.sh DIR      puts DIR/squawk in place
#
# To move to a new release: change VERSION, then replace the sums with those of
# the assets, `gh release view vX.Y.Z -R sbdchd/squawk --json assets`.
set -euo pipefail

VERSION=2.73.0
SUM_LINUX_X64=9ae3747ca5eafa48b4d5c535fc64b61e0735a763164193644aa6436cc97c4274
SUM_LINUX_ARM64=84c925d19fba29c845b58cd12e1003cbfe9c70dc014bbf7174a84e1b433518d5
SUM_DARWIN_X64=e3c99b11a221d714241f3a9edfc844743ab2e098d833e2145b20d06d083c9954
SUM_DARWIN_ARM64=1806b8d16204606c35e6496b3727f76cf13a4d0665439ea5df1dcca579954551

dir="${1:?usage: install-squawk.sh DIR}"

case "$(uname -s)-$(uname -m)" in
  Linux-x86_64) asset=squawk-linux-x64; want="$SUM_LINUX_X64" ;;
  Linux-aarch64|Linux-arm64) asset=squawk-linux-arm64; want="$SUM_LINUX_ARM64" ;;
  Darwin-x86_64) asset=squawk-darwin-x64; want="$SUM_DARWIN_X64" ;;
  Darwin-arm64) asset=squawk-darwin-arm64; want="$SUM_DARWIN_ARM64" ;;
  *) echo "install-squawk: no squawk build for $(uname -s) $(uname -m)" >&2; exit 1 ;;
esac

sha256() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi; }

mkdir -p "$dir"
tmp="$(mktemp "$dir/squawk.XXXXXX")"
trap 'rm -f "$tmp"' EXIT
curl --fail --silent --show-error --location --retry 3 \
  --output "$tmp" "https://github.com/sbdchd/squawk/releases/download/v$VERSION/$asset"
got="$(sha256 "$tmp")"
if [ "$got" != "$want" ]; then
  echo "install-squawk: $asset has SHA-256 $got, expected $want; not installing it" >&2
  exit 1
fi
chmod +x "$tmp"
mv "$tmp" "$dir/squawk"
