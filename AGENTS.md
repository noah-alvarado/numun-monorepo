# AGENTS.md

This file provides guidance to AI agents when working with code in this repository.

## Repo shape

Monorepo holding four deployable surfaces plus shared infra and content:

- `/site` — Astro + Tailwind static site (`numun.org`)
- `/portal` — SolidJS + Vite SPA (`portal.numun.org`); calls the API via generated Connect clients
- `/api` — Go Lambdalith (`api.numun.org`); one binary, one Lambda, multiple `cmd/` entrypoints (`api`, `cognito-post-confirmation`, `seed`, and forthcoming `email-worker`, `email-feedback`)
- `/cms` — Decap CMS static bundle (`cms.numun.org`) committing into `/content`
- `/content` — CMS-managed Markdown / JSON / images consumed by `/site`
- `/infra` — five SAM template groups: `bootstrap` (OIDC roles), `base-data` (DDB + Cognito), `base-cdn` (S3 + CloudFront + DNS), `api` (Lambda + API GW), `billing-alarm`
- `/docs` — design docs (`PROJECT`, `INFRASTRUCTURE`, `APPLICATION`, `DATA_MODEL`, `API`, `AUTH`, `SECURITY`) plus `runbooks/` and `subsystems/`

`DEVELOPERS.md`, `IMPLEMENTATION_PLAN.md`, and the design docs under `/docs` are the source of truth for architecture decisions. Consult them before inventing patterns; they were written before code existed and define the target shape.

## Common commands

All routine work goes through the root `Makefile`. From the repo root:

| Task                                                        | Command                                             |
| ----------------------------------------------------------- | --------------------------------------------------- |
| Bring up local prod-mirror (DDB Local, LocalStack, MailHog) | `make dev`                                          |
| Tear it down                                                | `make dev-down`                                     |
| Run SAM Local API GW + Go Lambdalith on `:3000`             | `make dev-api`                                      |
| Run Vite (portal) on `:5173`                                | `make dev-portal`                                   |
| Run Astro (site) on `:4321`                                 | `make dev-site`                                     |
| Restart SAM after editing `/api` (no auto-reload)           | `make api-restart`                                  |
| Seed DDB Local                                              | `make seed`                                         |
| Reset DDB Local + LocalStack volumes                        | `make reset`                                        |
| Codegen Go + TS from protos                                 | `make proto`                                        |
| Lint everything                                             | `make lint` (or `lint-go`, `lint-js`, `lint-proto`) |
| Typecheck everything                                        | `make typecheck`                                    |
| Unit tests across workspaces                                | `make test`                                         |
| Integration tests (requires `make dev`)                     | `make integration-tests`                            |
| Validate all SAM templates                                  | `make sam-validate`                                 |
| Verify tool versions                                        | `make doctor`                                       |

Targeted equivalents:

- Single Go test: `cd api && go test ./internal/handlers -run TestName`
- Single portal test: `pnpm --filter portal test -- -t "test name"`
- API only: `cd api && go build ./...` / `go test ./...`
- One workspace lint/typecheck: `pnpm --filter portal run lint` / `pnpm --filter site run typecheck`
- Post-deploy smoke: `scripts/verify-deploy.sh` (parameterized per env)

Node is pinned to **24.16.0** (`.nvmrc`); Go to the version in `api/go.mod`. Use `pnpm` (via corepack), not npm/yarn.

## Architecture you can't see by reading one file

**Lambdalith + Connect/Protobuf.** The portal does not talk to a REST API. `/api/proto/numun/v1/*.proto` is the contract; `buf generate` emits Go server stubs into `/api/internal/gen/` and TS clients into `/portal/src/gen/`. Both generated trees are committed. When changing the contract: edit `.proto` → `buf lint` → `buf breaking --against ".git#branch=main"` → `buf generate` → commit generated files alongside the proto change. Only additive changes within `numun.v1`; breaking changes require a new package version.

**Single-table DynamoDB.** One table (`numun-${env}`) with PK/SK + GSI1 + GSI2. Entity definitions and access patterns live in `docs/DATA_MODEL.md`; repositories under `/api/internal/store/` are the only layer that touches DDB. Optimistic locking is centralized there. `DDB_TABLE_NAME` is required at boot (no default).

**Auth = Cognito + server-side sessions.** Cognito issues JWTs; the portal does the Cognito calls directly (custom screens, not Hosted UI), then POSTs tokens to `AuthService.Exchange`, which writes an HttpOnly cookie scoped to `.numun.org` and stores a `Session` row in DDB. Middleware turns cookie → Session → refreshed claims on `r.Context()`. Role is a `custom:role` Cognito attribute mirrored into the `User` row.

**Scope enforcement is a lint gate, not a convention.** Every mutating handler must call a `mustHaveScopeOn*` helper (`/api/internal/auth/scope.go`) before touching repositories. A CI grep gate (`scripts/check-scope-helpers.sh`) blocks merges that skip it. See `docs/SECURITY.md` §2.1. The helpers return `not_found` (not `permission_denied`) for missing scope — preserve that distinction; `docs/AUTH.md` §7.3 explains why.

**Local dev does not stand up Cognito.** Set `DEV_BYPASS_AUTH=true` + `DEV_MODE=true` in the SAM env (`scripts/sam-env-vars.json`) and pass `X-Dev-User-Id: <seed-user>`. Refuses to operate unless both env vars are set — defense in depth so prod can't accidentally bypass.

**Multi-env via parameterized SAM.** Templates accept `Env`, `RootDomain`, `EnvSubdomain` and compose the effective apex via a `HasSubdomain` condition. Every named resource, Export, cookie domain, CORS origin, CloudFront alias, and API GW domain derives from these. Current deployed env is `test` at `test.numun.org`; the same templates stand up `prod` in a different AWS account. Do not hardcode `numun-prod-*` or `numun.org` — thread `Env` and the composed apex through instead. See `docs/runbooks/fresh-environment-deploy.md`.

**Trunk-based, prod-only deploys.** Every merge to `main` deploys via path-filtered workflows (`api.yml`, `site.yml`, `portal.yml`, `cms.yml`). No PR previews, no staging gate. Ship behind flags or in small PRs. `ci.yml` blocks merge on lint/typecheck/tests/`buf lint`/`buf breaking`. `/content/**` direct-to-main is allowed (Decap requires it); code paths are not.

**Cost ceiling is real.** Target under $100/yr AWS spend; billing alarm at $10/mo. Prefer serverless/on-demand; avoid always-on resources. If a design choice would push past the ceiling, flag it.

## Conventions worth knowing

- **Generated files are committed.** `/api/internal/gen/` and `/portal/src/gen/` ship in the repo (often hidden in editor file trees). CI fails if codegen drifted from protos.
- **Brand tokens** (Northwestern purple, typefaces) live in a shared Tailwind config consumed by both `/site` and `/portal`. No hardcoded brand colors in components.
- **No external UI component library** in the portal — components are built on Tailwind primitives. Forms use `modular-forms` + `Valibot`.
- **Idempotency-Key middleware** (24h TTL) on mutating RPCs. Audit-log writes happen in the handler, not the store layer.
- **Email templates** live under `/api/templates/email/<kind>.{html,txt}.tmpl` with a render test asserting no `<no value>` markers per `docs/subsystems/EMAIL.md`.
- **Squash-merge** is the default; PR title becomes the commit subject (imperative, ≤70 chars).
