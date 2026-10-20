#!/usr/bin/env bash
# Checks that every non-merge commit in base..head carries a Developer
# Certificate of Origin sign-off matching its author. Commits by GitHub bots
# (Dependabot, release-please) are exempt: no person certifies them.
set -euo pipefail

base="${1:?usage: check-dco.sh <base> <head>}"
head="${2:?usage: check-dco.sh <base> <head>}"
failed=0

for sha in $(git rev-list --no-merges "$base..$head"); do
  author="$(git log -1 --format='%an <%ae>' "$sha")"
  if [[ "$author" == *"[bot]@users.noreply.github.com>" ]]; then
    continue
  fi
  if git log -1 --format='%B' "$sha" | grep -qiF "Signed-off-by: $author"; then
    continue
  fi
  echo "::error::$(git log -1 --format='%h %s' "$sha"): missing 'Signed-off-by: $author'" >&2
  failed=1
done

if [[ $failed -ne 0 ]]; then
  echo "Sign off with 'git commit -s' (fix existing commits: git rebase --signoff $base)." >&2
  exit 1
fi
echo "every commit is signed off"
