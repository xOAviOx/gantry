SHELL := /bin/bash
COMPOSE := docker compose --env-file deploy/.env -f deploy/docker-compose.yml

# NOTE: On this host, `make` is a Cygwin-flavored build whose default shell
# resolves Windows executables via /cygdrive paths and cannot exec docker/go/node
# (spaces in path + .exe). So every recipe that runs a tool is dispatched through
# `bash` (Git Bash / MSYS via PATH), where those binaries resolve correctly.
# See SPEC.md D12.

.PHONY: dev up down env migrate test it lint fmt typecheck web controld nuke help

help:
	@echo "Gantry targets:"
	@echo "  make dev        infra + migrate + controld + web (Ctrl-C to stop)"
	@echo "  make up         start postgres + caddy (detached)"
	@echo "  make down       stop postgres + caddy"
	@echo "  make migrate    apply DB migrations (idempotent)"
	@echo "  make controld   run controld only (:8080)"
	@echo "  make web        run Next.js dashboard only (:3000)"
	@echo "  make test       go unit tests"
	@echo "  make it         go integration tests (-tags=integration)"
	@echo "  make lint       go vet + golangci-lint + web typecheck"
	@echo "  make fmt        gofmt"
	@echo "  make nuke       remove ONLY gantry-labeled docker resources"

env deploy/.env:
	@bash scripts/ensure-env.sh

up: env
	@bash -c '$(COMPOSE) up -d'

down:
	@bash -c '$(COMPOSE) down'

dev: env
	@bash scripts/dev.sh

migrate: env
	@bash scripts/migrate.sh

controld: env
	@bash -c 'go run ./services/controld/cmd/controld'

web:
	@bash -c 'cd apps/web && node node_modules/next/dist/bin/next dev'

test:
	@bash -c 'go test ./...'

it:
	@bash -c 'export DATABASE_URL=$$(grep -E "^DATABASE_URL=" deploy/.env | cut -d= -f2-); go test -tags=integration ./...'

lint:
	@bash -c 'go vet ./... && { command -v golangci-lint >/dev/null 2>&1 && golangci-lint run ./... || echo "golangci-lint not installed; ran go vet only"; } && cd apps/web && node node_modules/typescript/bin/tsc --noEmit'

fmt:
	@bash -c 'gofmt -w ./services ./migrations'

typecheck:
	@bash -c 'cd apps/web && node node_modules/typescript/bin/tsc --noEmit'

nuke:
	@bash scripts/nuke.sh
