# scrumboard-api
#
# `make dev` should be all it takes to start working.

SHELL := /bin/bash
.DEFAULT_GOAL := help

# --- deployment target ------------------------------------------------------
# VM_HOST is never committed: this repository is public, and the VM's address
# is not something to publish (00-scope.md §7). Put it in .env.local, which
# .gitignore already covers:
#
#     echo 'VM_HOST=203.0.113.10' >> .env.local
#
-include .env.local
export

VM_USER ?= ubuntu
VM_KEY  := $(HOME)/.ssh/oracle_chavarria_vm
VM_DIR  := ~/scrumboard-api

IMAGE := ghcr.io/alejandrogmota/scrumboard-api

# --- development ------------------------------------------------------------

DEV_DB    ?= /tmp/scrumboard-dev.db
DEV_TOKEN ?= local-dev-token-not-a-secret

.PHONY: dev
dev: ## Run the API locally, with SQLite in /tmp
	SCRUMBOARD_TOKEN=$(DEV_TOKEN) \
	DB_PATH=$(DEV_DB) \
	PORT=8020 \
	CORS_ORIGIN=http://localhost:5173 \
	go run ./cmd/api

.PHONY: build
build: ## Build the binary into ./bin
	go build -o bin/api ./cmd/api

.PHONY: clean
clean: ## Remove build output and the development database
	rm -rf bin cover.out cover.html
	rm -f $(DEV_DB) $(DEV_DB)-wal $(DEV_DB)-shm

# --- tests ------------------------------------------------------------------

.PHONY: test
test: ## Run every test — the everyday one, target under 10 seconds
	go test ./...

.PHONY: test-race
test-race: ## Run every test with the race detector — before every PR
	go test -race ./...

.PHONY: test-endpoints
test-endpoints: ## Run only the endpoint suite, verbosely
	go test ./internal/adapter/in/http/... -v

.PHONY: test-one
test-one: ## Run one test: make test-one t=TestCardMove
	go test ./... -run $(t) -v

.PHONY: cover
cover: ## Coverage profile and HTML report — to find what was forgotten, not to hit a number
	go test -coverprofile=cover.out ./...
	go tool cover -html=cover.out -o cover.html
	@echo "open cover.html"

.PHONY: golden-update
golden-update: ## Regenerate the golden files. REVIEW THE DIFF BY HAND.
	go test ./... -update
	@echo
	@echo "Golden files regenerated. A golden file updated without looking at"
	@echo "the diff is a deleted test — check them before committing."

.PHONY: lint
lint: ## go vet, plus staticcheck when it is installed
	go vet ./...
	@command -v staticcheck >/dev/null 2>&1 && staticcheck ./... || \
		echo "staticcheck not installed; skipping (go install honnef.co/go/tools/cmd/staticcheck@latest)"

.PHONY: fmt
fmt: ## gofmt the tree
	gofmt -l -w .

# --- migrations (Atlas) -----------------------------------------------------
#
# Atlas is an authoring tool and lives in development and CI only. The
# production image never sees it: migrations/*.sql is embedded with go:embed
# and applied by the binary itself at startup (01-structure.md §1.8).

.PHONY: migrate-new
migrate-new: ## Generate a migration from schema.sql: make migrate-new name=add_x
	@test -n "$(name)" || { echo "usage: make migrate-new name=add_something"; exit 1; }
	atlas migrate diff $(name) --env local

.PHONY: migrate-lint
migrate-lint: ## Warn before a destructive migration reaches main
	atlas migrate lint --env local --latest 1

.PHONY: migrate-hash
migrate-hash: ## Recompute atlas.sum. Never edit that file by hand.
	atlas migrate hash --env local

.PHONY: migrate-apply
migrate-apply: ## Apply pending migrations to the development database
	atlas migrate apply --env local --url "sqlite://$(DEV_DB)"

.PHONY: schema-check
schema-check: ## Fail if schema.sql and migrations/ have drifted apart
	@out=$$(atlas schema diff --from file://migrations --to file://schema.sql \
		--dev-url "sqlite://dev?mode=memory&_fk=1" 2>&1); \
	if [ "$$out" != "Schemas are synced, no changes to be made." ]; then \
		echo "schema.sql and migrations/ have drifted. Run: make migrate-new name=..."; \
		echo "$$out"; exit 1; \
	fi
	@echo "schema.sql and migrations/ agree"

# --- docker -----------------------------------------------------------------

.PHONY: docker-build
docker-build: ## Build the arm64 image the VM runs
	docker buildx build --platform linux/arm64 -t $(IMAGE):latest .

# --- the VM -----------------------------------------------------------------

.PHONY: require-host
require-host:
	@test -n "$(VM_HOST)" || { \
		echo "VM_HOST is not set. Put it in .env.local (never in git — this repo is public):"; \
		echo "    echo 'VM_HOST=203.0.113.10' >> .env.local"; exit 1; }

.PHONY: ssh
ssh: require-host ## Open a shell on the VM
	ssh -i $(VM_KEY) $(VM_USER)@$(VM_HOST)

.PHONY: logs
logs: require-host ## Follow the container logs over SSH
	ssh -i $(VM_KEY) $(VM_USER)@$(VM_HOST) "cd $(VM_DIR) && docker compose logs -f"

.PHONY: backup
backup: require-host ## Run the nightly backup now
	ssh -i $(VM_KEY) $(VM_USER)@$(VM_HOST) "$(VM_DIR)/backup.sh"

.PHONY: restore-check
restore-check: require-host ## Pull the newest backup and run PRAGMA integrity_check on it
	@echo "A backup nobody has restored is a hypothesis, not a backup."
	mkdir -p tmp
	@latest=$$(ssh -i $(VM_KEY) $(VM_USER)@$(VM_HOST) \
		"ls -t /var/backups/scrumboard/scrumboard-*.db 2>/dev/null | head -1"); \
	test -n "$$latest" || { echo "no backups on the VM yet"; exit 1; }; \
	echo "newest backup: $$latest"; \
	scp -i $(VM_KEY) $(VM_USER)@$(VM_HOST):$$latest tmp/restore-check.db
	go run ./cmd/api -verify tmp/restore-check.db

.PHONY: deploy
deploy: require-host ## Emergency manual deploy. Normally CI does this.
	scp -i $(VM_KEY) deploy/docker-compose.yml $(VM_USER)@$(VM_HOST):$(VM_DIR)/docker-compose.yml
	ssh -i $(VM_KEY) $(VM_USER)@$(VM_HOST) "cd $(VM_DIR) && \
		docker compose pull && \
		docker compose --env-file .env up -d --remove-orphans && \
		docker image prune -f"
	ssh -i $(VM_KEY) $(VM_USER)@$(VM_HOST) "curl -sf http://localhost:8020/health"

.PHONY: help
help: ## List the targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'
