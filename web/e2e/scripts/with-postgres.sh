#!/usr/bin/env bash
# Runs its arguments with DATABASE_URL pointing at a throwaway PostgreSQL in
# Docker, so every run starts from an empty database. CI sets DATABASE_URL
# itself and never reaches this script.
set -euo pipefail

if [ -n "${DATABASE_URL:-}" ]; then
  exec "$@"
fi

name="flagnetic-e2e-$$"
port="$(node -e 'const s=require("net").createServer();s.listen(0,"127.0.0.1",()=>{console.log(s.address().port);s.close()})')"

cleanup() {
  docker rm -f "$name" >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker run -d --rm --name "$name" \
  -e POSTGRES_DB=flagnetic \
  -e POSTGRES_USER=flagnetic \
  -e POSTGRES_PASSWORD=flagnetic \
  -p "127.0.0.1:${port}:5432" \
  postgres:17-alpine >/dev/null

echo "waiting for PostgreSQL on 127.0.0.1:${port}"
for _ in $(seq 1 60); do
  # Over TCP: the server initdb starts first listens on the socket alone.
  if docker exec "$name" pg_isready -h 127.0.0.1 -U flagnetic -d flagnetic >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

export DATABASE_URL="postgres://flagnetic:flagnetic@127.0.0.1:${port}/flagnetic?sslmode=disable"
"$@"
