#!/usr/bin/env bash
# Ensure postgres is up, then apply migrations (idempotent).
set -euo pipefail
cd "$(dirname "$0")/.."

docker compose --env-file deploy/.env -f deploy/docker-compose.yml up -d postgres
bash scripts/wait-db.sh
go run ./services/controld/cmd/controld migrate
