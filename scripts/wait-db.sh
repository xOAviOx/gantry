#!/usr/bin/env bash
# Block until the gantry-postgres container answers pg_isready.
set -uo pipefail
cd "$(dirname "$0")/.."

set -a
[ -f deploy/.env ] && . deploy/.env
set +a

echo "waiting for postgres..."
for _ in $(seq 1 60); do
  if docker exec gantry-postgres pg_isready -U "${POSTGRES_USER:-gantry}" -d "${POSTGRES_DB:-gantry}" >/dev/null 2>&1; then
    echo "postgres ready"
    exit 0
  fi
  sleep 1
done

echo "postgres did not become ready in time" >&2
exit 1
