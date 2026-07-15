#!/usr/bin/env bash
# Remove ONLY gantry-labeled Docker resources. Never touches unlabeled resources
# on this machine (SPEC.md §5, §19.3).
set -uo pipefail
cd "$(dirname "$0")/.."

L="dev.gantry.managed=true"

echo "==> compose down (infra)"
docker compose --env-file deploy/.env -f deploy/docker-compose.yml down -v --remove-orphans 2>/dev/null || true

echo "==> removing gantry-labeled containers"
docker ps -aq --filter "label=${L}" | xargs -r docker rm -f 2>/dev/null || true

echo "==> removing gantry-labeled images"
docker images -q --filter "label=${L}" | xargs -r docker rmi -f 2>/dev/null || true

echo "==> removing gantry-labeled volumes"
docker volume ls -q --filter "label=${L}" | xargs -r docker volume rm 2>/dev/null || true

echo "==> removing gantry-labeled networks"
docker network ls -q --filter "label=${L}" | xargs -r docker network rm 2>/dev/null || true

echo "done. (only dev.gantry.managed=true resources were touched)"
