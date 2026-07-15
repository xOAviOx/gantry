#!/usr/bin/env bash
# `make dev`: bring up infra, migrate, then run controld + web as host processes.
# Ctrl-C tears everything down cleanly.
set -euo pipefail
cd "$(dirname "$0")/.."

COMPOSE=(docker compose --env-file deploy/.env -f deploy/docker-compose.yml)

set -a
. deploy/.env
set +a

echo "==> starting infra (postgres + caddy)"
"${COMPOSE[@]}" up -d

bash scripts/wait-db.sh

echo "==> applying migrations"
go run ./services/controld/cmd/controld migrate

if [ ! -d apps/web/node_modules ]; then
  echo "==> installing web deps (first run)"
  (cd apps/web && pnpm install)
fi

pids=()
cleanup() {
  echo
  echo "==> stopping host processes"
  for pid in "${pids[@]}"; do
    kill "$pid" 2>/dev/null || true
  done
}
trap cleanup EXIT INT TERM

echo "==> starting web (:3000)"
# Invoke Next directly (not `pnpm dev`) to bypass pnpm 11's pre-run dep check,
# which fails on sharp's un-approved native build script.
(cd apps/web && node node_modules/next/dist/bin/next dev) &
pids+=($!)

echo "==> starting controld (:8080)"
go run ./services/controld/cmd/controld &
pids+=($!)

echo "==> up. dashboard: http://paas.localhost   (Ctrl-C to stop)"
wait
