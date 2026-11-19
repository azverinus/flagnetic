#!/usr/bin/env bash
# Fails when a migration that has shipped is not pinned in released.sum, the
# list of SHA-256 sums that TestReleasedMigrationsAreUnchanged holds the
# released files to. A migration missing there is not guarded: someone can edit
# it, every check stays green, and instances that applied the released file end
# up with a different schema from a fresh install.
#
#   check-released-migrations.sh [--tag TAG]  every migration of TAG (default:
#                                             the latest server tag, vX.Y.Z) is
#                                             listed, with the sum it had there
#   check-released-migrations.sh --release    every migration of the working
#                                             tree is listed: the server's
#                                             release pull request ships them
#   check-released-migrations.sh --self-test  the check against a throwaway
#                                             repository with a fixture tag
#
# Exit 0: every migration is pinned (or there is no tag yet), 1: findings,
# 2: bad usage.
set -uo pipefail

MIGRATIONS=internal/infra/out/postgres/migrations

# check <repository> <tag|""> <tree|tag>: lists the migrations of the tag (or of
# the working tree) that the repository's released.sum does not pin.
check() {
  local repo="$1" tag="$2" source="$3" file want got status=0 names
  local sum="$repo/$MIGRATIONS/released.sum"
  [ -f "$sum" ] || { echo "check-released-migrations: $MIGRATIONS/released.sum is missing" >&2; return 1; }
  if [ "$source" = tag ]; then
    names="$(git -C "$repo" ls-tree --name-only "$tag" -- "$MIGRATIONS/" | grep '\.sql$' | xargs -n1 basename 2>/dev/null)"
  else
    names="$(cd "$repo/$MIGRATIONS" && find . -maxdepth 1 -name '*.sql' | sed 's|^\./||' | sort)"
  fi
  for file in $names; do
    want="$(awk -v f="$file" '$2 == f { print $1 }' "$sum")"
    if [ -z "$want" ]; then
      echo "check-released-migrations: $file ships ${tag:+in $tag}${tag:-in this release} but is not in released.sum: append \`sha256sum $file >> released.sum\` (from $MIGRATIONS)" >&2
      status=1
      continue
    fi
    if [ "$source" = tag ]; then
      got="$(git -C "$repo" show "$tag:$MIGRATIONS/$file" | shasum -a 256 | cut -d' ' -f1)"
      if [ "$got" != "$want" ]; then
        echo "check-released-migrations: $file is pinned with a sum that is not the one it had in $tag" >&2
        status=1
      fi
    fi
  done
  return $status
}

latest_tag() { # <repository>
  git -C "$1" tag -l 'v[0-9]*.[0-9]*.[0-9]*' | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | sort -V | tail -1
}

self_test() {
  local dir rc=0 git=(git -c user.name=t -c user.email=t@example.com -c commit.gpgsign=false -c tag.gpgsign=false)
  dir="$(mktemp -d)" || return 2
  trap 'rm -rf "$dir"' RETURN
  mkdir -p "$dir/$MIGRATIONS"
  git -C "$dir" init -q
  echo "-- +goose Up" >"$dir/$MIGRATIONS/00001_a.sql"
  echo "-- +goose Up" >"$dir/$MIGRATIONS/00002_b.sql"
  : >"$dir/$MIGRATIONS/released.sum"
  git -C "$dir" add -A && "${git[@]}" -C "$dir" commit -q -m a && "${git[@]}" -C "$dir" tag v0.1.0
  # A later tag of another module must not be taken for the server's.
  "${git[@]}" -C "$dir" tag eval/v9.9.9

  pin() { (cd "$dir/$MIGRATIONS" && shasum -a 256 "$@" | sed 's/ \*/  /') >>"$dir/$MIGRATIONS/released.sum"; }
  expect() { # <pass|fail> <description> <args to check>
    local want="$1" what="$2"; shift 2
    if check "$@" >/dev/null 2>&1; then got=pass; else got=fail; fi
    if [ "$got" != "$want" ]; then
      echo "check-released-migrations: self-test: $what: want $want, got $got" >&2
      rc=1
    fi
  }

  [ "$(latest_tag "$dir")" = v0.1.0 ] || { echo "check-released-migrations: self-test: latest tag is $(latest_tag "$dir"), want v0.1.0" >&2; rc=1; }
  expect fail "a tagged file absent from the sum" "$dir" v0.1.0 tag
  pin 00001_a.sql
  expect fail "one of two tagged files absent from the sum" "$dir" v0.1.0 tag
  pin 00002_b.sql
  expect pass "every tagged file pinned" "$dir" v0.1.0 tag
  cp "$dir/$MIGRATIONS/released.sum" "$dir/good.sum"
  sed -i.bak 's/^[0-9a-f]\{64\}  00002_b.sql/'"$(printf '0%.0s' {1..64})"'  00002_b.sql/' "$dir/$MIGRATIONS/released.sum"
  expect fail "a pinned file whose sum is not the tag's" "$dir" v0.1.0 tag
  cp "$dir/good.sum" "$dir/$MIGRATIONS/released.sum"
  echo "-- +goose Up" >"$dir/$MIGRATIONS/00003_c.sql"
  expect pass "an unreleased file is free before the release" "$dir" v0.1.0 tag
  expect fail "a release shipping an unpinned file" "$dir" "" tree
  pin 00003_c.sql
  expect pass "a release whose files are all pinned" "$dir" "" tree
  return $rc
}

mode=tag
tag=""
while [ $# -gt 0 ]; do
  case "$1" in
    --tag) shift; tag="${1:?--tag needs a tag}" ;;
    --release) mode=tree ;;
    --self-test) mode=self-test ;;
    *) echo "check-released-migrations: unknown argument $1" >&2; exit 2 ;;
  esac
  shift
done

root="$(git rev-parse --show-toplevel)" || exit 2
case "$mode" in
  self-test) self_test; exit $? ;;
  tree) check "$root" "" tree; exit $? ;;
esac
[ -n "$tag" ] || tag="$(latest_tag "$root")"
if [ -z "$tag" ]; then
  echo "check-released-migrations: no server tag (vX.Y.Z) in this checkout; nothing released to pin (CI fetches the tags)"
  exit 0
fi
echo "check-released-migrations: the migrations of $tag"
check "$root" "$tag" tag
