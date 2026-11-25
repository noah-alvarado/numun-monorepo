.DEFAULT_GOAL := help
SHELL := /bin/bash

DEV_CONTAINER_NAMES := numun-ddb-local numun-ddb-init numun-localstack numun-mailpit

# ── Help ──────────────────────────────────────────────────────────────────────

help: ## Show this help.
	@awk 'BEGIN {FS = ":.*##"; printf "Available targets:\n"} /^[a-zA-Z0-9_.-]+:.*##/ { printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

# ── Local dev stack ───────────────────────────────────────────────────────────

dev-preflight: ## Remove stale local dev containers that can cause name conflicts.
	@for name in $(DEV_CONTAINER_NAMES); do \
	  docker rm -f $$name >/dev/null 2>&1 || true; \
	done

dev: ## Bring up the local prod-mirror containers (DDB Local, LocalStack, Mailpit) + init.
	@$(MAKE) reset
	@echo ""
	@echo "Local infra is up. In separate terminals run:"
	@echo "    make dev-api      # SAM Local API Gateway + Go Lambdalith at :3000"
	@echo "    make dev-portal   # Vite dev server at :5173"
	@echo "    make dev-site     # Astro dev server at :4321"
	@echo ""
	@echo "Mailpit web UI:        http://localhost:8025"
	@echo "DynamoDB Local:        http://localhost:8000"
	@echo "LocalStack:            http://localhost:4566"

dev-down: ## Tear down the local containers.
	docker compose down

dev-api: ## Start SAM Local API Gateway against the Go Lambdalith.
	cd api && sam local start-api --template ../infra/template.yaml --port 3000 --env-vars ../scripts/sam-env-vars.json

dev-worker: ## Run the email worker against LocalStack SQS, delivering to Mailpit (:8025).
	cd api && DEV_MODE=true AWS_REGION=us-east-2 AWS_ACCESS_KEY_ID=local AWS_SECRET_ACCESS_KEY=local \
		AWS_ENDPOINT_URL=http://localhost:4566 AWS_ENDPOINT_URL_DYNAMODB=http://localhost:8000 \
		DDB_TABLE_NAME=numun-test DEV_SMTP_ADDR=localhost:1025 \
		EMAIL_QUEUE_URL=http://localhost:4566/000000000000/numun-test-email-send \
		EMAIL_SENDER_TRANSACTIONAL=noreply@mail.test.numun.org EMAIL_SENDER_ANNOUNCEMENTS=announcements@mail.test.numun.org \
		EMAIL_UNSUBSCRIBE_BASE_URL=http://localhost:3000/v1/email/unsubscribe EMAIL_UNSUBSCRIBE_SECRET=dev-unsubscribe-secret-not-for-prod \
		PORTAL_BASE_URL=http://localhost:5173 ASSETS_BASE_URL=http://localhost:4566/numun-test-assets \
		go run ./cmd/email-worker -local

dev-portal: ## Start the portal Vite dev server.
	cd portal && pnpm dev

dev-site: ## Start the Astro dev server.
	cd site && pnpm dev

api-restart: ## Restart SAM Local (use after editing /api code).
	@pkill -f "sam local start-api" || true
	$(MAKE) dev-api

reset: ## Drop + recreate the DynamoDB Local table and LocalStack resources.
	docker compose down -v
	@$(MAKE) dev-preflight
	docker compose up -d
	@docker wait numun-ddb-init >/dev/null
	@$(MAKE) seed

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
	cd api/proto && buf generate --include-imports

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
	pnpm run test

integration-tests: ## Run Go integration tests against LocalStack + DDB Local (requires `make dev`).
	cd api && AWS_ENDPOINT_URL_DYNAMODB=http://localhost:8000 AWS_ENDPOINT_URL_S3=http://localhost:4566 \
		DDB_TABLE_NAME=numun-test go test -count=1 ./...

e2e-stack: ## Start DDB Local + LocalStack (keeps existing data) and seed — prerequisite for `make e2e`.
	docker compose up -d dynamodb-local ddb-init localstack
	@docker wait numun-ddb-init >/dev/null
	@until curl -fsS http://localhost:4566/_localstack/health >/dev/null 2>&1; do sleep 1; done
	@# Re-apply the (idempotent) init so an already-running LocalStack picks up new resources.
	@docker exec numun-localstack /etc/localstack/init/ready.d/init.sh >/dev/null
	@$(MAKE) seed

e2e: e2e-stack ## Run Playwright e2e tests; starts the Go API (:3000) + portal preview (:4173) itself.
	pnpm --filter @numun/e2e run e2e $(ARGS)

# ── Doctor ────────────────────────────────────────────────────────────────────

doctor: ## Verify required tools are installed at acceptable versions.
	@scripts/doctor.sh
