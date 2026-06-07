.DEFAULT_GOAL := help
SHELL := /bin/bash

# ── Help ──────────────────────────────────────────────────────────────────────

help: ## Show this help.
	@awk 'BEGIN {FS = ":.*##"; printf "Available targets:\n"} /^[a-zA-Z0-9_.-]+:.*##/ { printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

# ── Local dev stack ───────────────────────────────────────────────────────────

dev: ## Bring up the local prod-mirror containers (DDB Local, LocalStack, MailHog) + init.
	docker compose up -d
	@echo ""
	@echo "Local infra is up. In separate terminals run:"
	@echo "    make dev-api      # SAM Local API Gateway + Go Lambdalith at :3000"
	@echo "    make dev-portal   # Vite dev server at :5173"
	@echo "    make dev-site     # Astro dev server at :4321"
	@echo ""
	@echo "MailHog web UI:        http://localhost:8025"
	@echo "DynamoDB Local:        http://localhost:8000"
	@echo "LocalStack:            http://localhost:4566"

dev-down: ## Tear down the local containers.
	docker compose down

dev-api: ## Start SAM Local API Gateway against the Go Lambdalith.
	cd api && sam local start-api --template ../infra/template.yaml --port 3000 --env-vars ../scripts/sam-env-vars.json

dev-portal: ## Start the portal Vite dev server.
	cd portal && pnpm dev

dev-site: ## Start the Astro dev server.
	cd site && pnpm dev

api-restart: ## Restart SAM Local (use after editing /api code).
	@pkill -f "sam local start-api" || true
	$(MAKE) dev-api

reset: ## Drop + recreate the DynamoDB Local table and LocalStack resources.
	docker compose down -v
	docker compose up -d

seed: ## Populate DynamoDB Local with the seed dataset (see /docs/seed-users.md).
	cd api && \
	  AWS_REGION=us-east-2 \
	  AWS_ACCESS_KEY_ID=local \
	  AWS_SECRET_ACCESS_KEY=local \
	  AWS_ENDPOINT_URL_DYNAMODB=http://localhost:8000 \
	  DDB_TABLE_NAME=numun-test \
	  go run ./cmd/seed

# ── Build / codegen ───────────────────────────────────────────────────────────

proto: ## Run Buf code-gen for Go + TS (writes into /api/internal/gen and /portal/src/gen).
	cd api/proto && buf lint
	cd api/proto && buf generate

# ── IaC validation ────────────────────────────────────────────────────────────

sam-validate: ## Validate all SAM/CFN templates under /infra (lint only; no deploy).
	@echo "=== bootstrap/oidc-roles.yaml ==="
	sam validate --lint --region us-east-2 --template infra/bootstrap/oidc-roles.yaml
	@echo "=== base-data/template.yaml ==="
	sam validate --lint --region us-east-2 --template infra/base-data/template.yaml
	@echo "=== base-cdn/template.yaml ==="
	sam validate --lint --region us-east-2 --template infra/base-cdn/template.yaml
	@echo "=== api/template.yaml ==="
	sam validate --lint --region us-east-2 --template infra/api/template.yaml
	@echo "=== billing-alarm/us-east-1.yaml ==="
	sam validate --lint --region us-east-1 --template infra/billing-alarm/us-east-1.yaml

sam-build-api: ## Build the api Lambda locally (verifies the SAM Makefile hook).
	cd infra/api && sam build

# ── Quality gates ─────────────────────────────────────────────────────────────

lint: lint-go lint-js lint-proto ## Run all linters.

lint-go:
	cd api && gofmt -l . | (! grep .) || (echo "gofmt issues above" && exit 1)
	cd api && go vet ./...

lint-js:
	pnpm -r --if-present run lint

lint-proto:
	cd api/proto && buf lint
	cd api/proto && buf breaking --against ".git#branch=main,subdir=api/proto" || true

typecheck: ## Typecheck TS packages and `go build` the API.
	pnpm -r --if-present run typecheck
	cd api && go build ./...

test: ## Run unit tests across all packages.
	cd api && go test ./...
	pnpm -r --if-present run test

integration-tests: ## Run Go integration tests against LocalStack + DDB Local (requires `make dev`).
	cd api && go test -tags=integration ./...

# ── Doctor ────────────────────────────────────────────────────────────────────

doctor: ## Verify required tools are installed at acceptable versions.
	@scripts/doctor.sh
