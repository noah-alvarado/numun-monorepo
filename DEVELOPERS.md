# DEVELOPERS.md

Welcome. This document orients a new contributor to the NUMUN repo and provides runbooks for development, testing, deployment, version control, and editor setup.

**Status note:** at the time of writing, the design phase is complete and code does not yet exist. Commands and procedures below describe how the codebase will be used once it lands; some steps reference paths that don't exist yet. Where that's the case, the design doc that defines the target is linked.

For higher-level context, read [README.md](./README.md) and then [PROJECT.md](./docs/PROJECT.md) before diving in here.

---

## 1. Codebase orientation

The repo is a single monorepo at `/Users/<you>/numun-monorepo` (or wherever you cloned it). Top-level directories:

```
/.github/      — CI workflows
/api/          — Go backend (api.numun.org); Connect/Protobuf service
/cms/          — Decap CMS static bundle (cms.numun.org)
/content/      — CMS-managed Markdown / images / PDFs
/docs/         — runbooks, onboarding guides
/infra/        — AWS SAM templates + parameter files
/portal/       — SolidJS SPA (portal.numun.org)
/site/         — Astro static site (numun.org)
```

Read [APPLICATION.md](./docs/APPLICATION.md) for the rationale.

Within `/api`:

```
/api
  /cmd/
    api/main.go                — Lambdalith entrypoint (API Gateway HTTP API → Connect → handlers)
    cognito-post-confirmation/ — Cognito trigger Lambda
    email-feedback/main.go     — SNS-subscribed Lambda for SES bounce/complaint events
    email-worker/main.go       — SQS-consumer Lambda for announcements + debounced summaries
  /internal/
    domain/                    — entity types, business logic, the assignment algorithm
    email/                     — SES send helpers, template rendering
    gen/                       — generated code from protos (committed to repo)
    handlers/                  — one package per service
    httpclient/                — hardened HTTP client wrapper (SSRF defense)
    log/                       — slog wrapper with redaction
    middleware/                — auth, CSRF, request-id, rate-limit, logging
    store/                     — DynamoDB access patterns
  /proto/numun/v1/             — Protobuf service & message definitions (source of truth)
  /templates/email/            — HTML + plaintext email templates
  buf.yaml / buf.gen.yaml      — Buf config
  go.mod / go.sum
```

Within `/portal`:

```
/portal
  /src/
    api/                       — generated Connect clients (auto-imported from /api/proto)
    components/                — UI primitives (no external library)
    forms/                     — modular-forms compositions
    gen/                       — generated TS types (Buf output)
    main.tsx
    routes/                    — Solid Router route components
  /public/
  package.json
  vite.config.ts
  tailwind.config.js           — shares brand tokens with /site
```

Within `/site`:

```
/site
  /src/
    components/                — Astro and Solid islands
    config/                    — shared Tailwind tokens
    layouts/                   — Astro layouts (includes the brand layout)
    pages/                     — file-system routing
  astro.config.mjs
  package.json
```

---

## 2. Required tools

Install once:

| Tool | Version | How |
|---|---|---|
| Node.js | **24.16.0** (pinned in `.nvmrc`) | `fnm install` from repo root |
| pnpm | as pinned in `package.json` `packageManager` | `corepack enable` (auto-activates the pinned version) |
| Go | as pinned in `/api/go.mod` | https://go.dev/dl/ |
| AWS CLI v2 | latest | https://aws.amazon.com/cli/ |
| AWS SAM CLI | latest | `brew install aws-sam-cli` (macOS) |
| Docker Desktop | latest | https://www.docker.com/products/docker-desktop/ |
| Buf | latest | `brew install bufbuild/buf/buf` |
| `golangci-lint` | latest | `brew install golangci-lint` |
| `govulncheck` | latest | `go install golang.org/x/vuln/cmd/govulncheck@latest` |

Verify:

```bash
node --version    # v24.16.0
pnpm --version
go version
sam --version
docker --version
buf --version
golangci-lint --version
```

---

## 3. First-time setup

```bash
# Clone and enter the repo
git clone <repo-url> numun-monorepo
cd numun-monorepo

# Pin Node version
fnm use   # reads .nvmrc → 24.16.0

# Install JS/TS dependencies across the workspace
pnpm install

# Initialize Go modules + verify
cd api && go mod download && go build ./... && cd -

# Verify Buf can compile the protos
buf build api/proto

# Generate code from protos (commit results)
buf generate api/proto

# Bring up the local prod mirror
make dev
```

After `make dev`, the following are running locally (see [INFRASTRUCTURE.md §4](./docs/INFRASTRUCTURE.md)):

- `localhost:8000` — DynamoDB Local
- `localhost:4566` — LocalStack (S3, SES stub)
- `localhost:3000` — SAM local API Gateway → Go Lambdalith
- `localhost:5173` — Vite dev server (portal)
- `localhost:4321` — Astro dev server (site)
- `localhost:1025` — MailHog SMTP (captured emails for inspection)
- `localhost:8025` — MailHog web UI

A first-time `make dev` may take several minutes (Docker pulls). Subsequent runs are fast.

---

## 4. Local development

### 4.1 The local prod mirror

[INFRASTRUCTURE.md §4](./docs/INFRASTRUCTURE.md) defines the local mirror. It runs the *same Go binary* in the same shape as production (Lambda inside a Docker container, behind a local API Gateway), so behavior parity is high.

| Command | What it does |
|---|---|
| `make dev` | Brings up Docker Compose + SAM Local + Vite + Astro |
| `make dev-down` | Tears everything down |
| `make seed` | Seeds DynamoDB Local with a representative test dataset |
| `make reset` | Drops + re-creates the DynamoDB Local table |

### 4.2 Editing flow

- **Portal**: edit `/portal/src/...` → Vite hot-reloads in your browser.
- **Site**: edit `/site/src/...` or `/content/...` → Astro hot-reloads.
- **API**: edit `/api/...` → re-run `sam local start-api` (or `make api-restart`); SAM does not auto-reload Go binaries.
- **Protos**: edit `/api/proto/...` → `buf generate api/proto` → commit generated files → restart the API.

### 4.3 Testing as different users

The seed dataset includes representative users (one advisor, one staff-staffer, one staff-admin). Their pre-populated cookies are in `/docs/seed-users.md`. In Vite's dev server, set the cookie manually via the browser's devtools.

For Cognito flows locally, **we do not stand up a local Cognito.** Instead, the API middleware has a `DEV_BYPASS_AUTH=true` environment variable that accepts a `X-Dev-User-Id` header and synthesizes a Cognito-shaped session for that user. Enabled only when `make dev` sets the env; never in production.

---

## 5. Testing

### 5.1 Unit tests

```bash
# Go
cd api && go test ./...

# Portal
pnpm --filter portal test

# Site
pnpm --filter site test
```

All three run in CI on every PR (`.github/workflows/ci.yml`).

### 5.2 Integration tests

Integration tests in `/api/internal/.../*_integration_test.go` use the running LocalStack + DynamoDB Local from `make dev`. They are **nice-to-have, not blocking** in CI per [APPLICATION.md §8](./docs/APPLICATION.md). Run locally with:

```bash
make integration-tests
```

### 5.3 Linting & typecheck

```bash
# Go
golangci-lint run ./api/...
govulncheck ./api/...

# TS/JS (portal + site)
pnpm run lint
pnpm run typecheck

# Protos
buf lint api/proto
buf breaking api/proto --against ".git#branch=main"
```

All of the above run in CI and **block merge** on failure.

### 5.4 Email template tests

Each template under `/api/templates/email/` has a test that renders it against a representative `Vars` map and asserts the output contains expected strings + no `<no value>` markers. See [EMAIL.md §3.4](./docs/subsystems/EMAIL.md).

```bash
cd api && go test ./internal/email/...
```

---

## 6. Working with Protobuf and Connect

[API.md](./docs/API.md) defines the contract. The workflow for changing it:

1. **Edit a `.proto` file** under `/api/proto/numun/v1/`.
2. **Run `buf lint api/proto`** — fixes obvious issues.
3. **Run `buf breaking api/proto --against ".git#branch=main"`** — surfaces any breaking change.
4. If you intend a breaking change, you need a new package version (`numun.v2`) — see [API.md §13](./docs/API.md). Within `v1`, only additive changes (new fields, new RPCs).
5. **Run `buf generate api/proto`** — regenerates Go server stubs under `/api/internal/gen/` and TS client code under `/portal/src/gen/`.
6. **Commit the generated files** alongside your `.proto` change.
7. **Implement the handler** in `/api/internal/handlers/<service>.go`.
8. **Add tests** covering happy path + at least one auth-failure + one validation-failure case.
9. **Update the role matrix** in [API.md §9.2](./docs/API.md) if the new RPC changes who can do what.

---

## 7. Version control

### 7.1 Branch model

Trunk-based on `main`. Per [APPLICATION.md §8](./docs/APPLICATION.md), every merge to `main` deploys to prod.

- **Feature branches:** named `<your-handle>/<short-description>`, e.g., `nick/add-payment-export`.
- **No long-lived branches.** PRs should be small and merge within a day or two of opening. If a change is too big, ship it behind a feature flag, or break it into smaller PRs.
- **No force-push to `main`.** Force-push on your own feature branches is allowed before review begins; ask for re-review after force-pushing once review has started.

### 7.2 Pull requests

- Open against `main`.
- Title format: imperative, ≤ 70 chars (`Add payment-export CSV column`, not `Added` or `Adding`).
- Description: what changed, why, and the test plan you ran locally.
- Pass all required status checks before requesting review.
- One approving review required.
- Squash-merge is the default; commit message follows the PR title.

### 7.3 Commit conventions

On your branch, commit freely — squash-merge collapses everything into a single commit at merge time. Aim for:

- Imperative-mood subject line, ≤ 70 chars.
- Optional body explaining the *why*, not the *what*.
- One logical change per commit when possible.

### 7.4 Branch protection rules

Configured on `main`:

- Required: 1 approving review, all required status checks pass.
- Force-push disabled.
- Direct-to-main commits **disabled** for code paths.
- Direct-to-main commits **allowed** for `/content/**` (Decap CMS requires this — see [CMS_CONTENT_MODEL.md §3](./docs/subsystems/CMS_CONTENT_MODEL.md)).

---

## 8. Deployment

See [INFRASTRUCTURE.md](./docs/INFRASTRUCTURE.md) for what each artifact deploys to.

### 8.1 Automated deploys

Each merge to `main` triggers a path-filtered workflow:

| Touched paths | Workflow | Target |
|---|---|---|
| `/site/**`, `/content/**` | `.github/workflows/site.yml` | S3 `numun-org-site` + CloudFront invalidation |
| `/portal/**` | `.github/workflows/portal.yml` | S3 `numun-org-portal` + CloudFront invalidation |
| `/api/**`, `/infra/**` | `.github/workflows/api.yml` | SAM deploy to the API stack |
| `/cms/**` | `.github/workflows/cms.yml` | S3 `numun-org-cms` + invalidation |

No PR previews, no staging environment — see [APPLICATION.md §8](./docs/APPLICATION.md).

### 8.2 Manual deploys

For emergencies or to deploy from a non-`main` commit, you can:

1. **Re-run the previous successful workflow** from GitHub Actions UI (rolls forward).
2. **Trigger `workflow_dispatch`** on a workflow (some support manual runs — e.g., the site workflow accepts a `force_rebuild` input).
3. **Locally as a last resort** (only break-glass admins should do this):
   ```bash
   aws sso login --profile numun-break-glass
   cd infra && sam deploy --profile numun-break-glass
   ```
   This requires the break-glass IAM user (see [SECURITY.md §3.2](./docs/SECURITY.md)).

### 8.3 Rollback

For the static surfaces (site, portal, cms): re-deploy the previous tagged commit. The site + cms buckets are versioned (S3 versioning, 90-day retention per [SECURITY.md §3.1](./docs/SECURITY.md)), so emergency rollback to a prior version is possible via the AWS CLI:

```bash
aws s3api list-object-versions --bucket numun-org-site --prefix index.html
aws s3api copy-object \
  --bucket numun-org-site \
  --copy-source 'numun-org-site/index.html?versionId=<id>' \
  --key index.html
aws cloudfront create-invalidation --distribution-id <id> --paths '/*'
```

For the API: re-deploy a prior `main` commit via `workflow_dispatch`. SAM creates a new CloudFormation deployment that replaces the live Lambda version. Brief (~30s) blip during transition is acceptable.

### 8.4 Confirming a deploy

After deployment, verify:

- The site loads and shows current content: https://numun.org
- The portal loads and the sign-in screen renders: https://portal.numun.org
- The API health endpoint responds: `curl https://api.numun.org/v1/health` → 200
- The Sentry dashboard shows no new error spikes.
- The CloudWatch billing alarm hasn't fired.

---

## 9. VS Code setup

### 9.1 Required extensions

The repo includes a `.vscode/extensions.json` recommending:

- **Go** (`golang.go`)
- **Astro** (`astro-build.astro-vscode`)
- **Tailwind CSS IntelliSense** (`bradlc.vscode-tailwindcss`)
- **Solid JS Snippets** (`abdulkadir-bayrak.solidjs-snippets`)
- **Buf** (`bufbuild.vscode-buf`)
- **ESLint** (`dbaeumer.vscode-eslint`)
- **Prettier** (`esbenp.prettier-vscode`)
- **Even Better TOML** (`tamasfe.even-better-toml`)
- **AWS Toolkit** (`amazonwebservices.aws-toolkit-vscode`) — useful for SAM debugging
- **DynamoDB Workbench** (optional, for browsing DynamoDB Local)
- **Sentry** (`sentry.sentry-vscode`) — optional, links errors to source

When you open the repo, VS Code prompts to install recommended extensions; accept.

### 9.2 Workspace settings

`.vscode/settings.json` sets:

- `editor.formatOnSave: true`
- `editor.defaultFormatter: esbenp.prettier-vscode` for TS/JS/JSON
- Go formatter: `gofmt` on save, `goimports` for import organization
- ESLint auto-fix on save
- Hide generated directories (`/api/internal/gen/`, `/portal/src/gen/`) from the file tree
- Tailwind class detection in Astro, TS, TSX files

### 9.3 Debugger configurations

`.vscode/launch.json` includes:

- **Debug API (Go, attach to SAM Local)** — attaches to the SAM Local Lambda process for breakpoint debugging.
- **Debug portal tests (Vitest)** — runs the current test file in inspect mode.
- **Debug Go tests** — debugs a single Go test under cursor.

### 9.4 Multi-root workspace

The repo includes a `numun.code-workspace` file that opens the monorepo with three roots (`/site`, `/portal`, `/api`) so VS Code's language servers operate per-root. Open via `File > Open Workspace from File > numun.code-workspace`.

---

## 10. Common-task recipes

### 10.1 Add a new RPC

1. Edit the appropriate `.proto` file under `/api/proto/numun/v1/`.
2. Add `protovalidate` annotations on every input field that has rules.
3. `buf generate api/proto` and commit the regenerated files.
4. Implement the handler under `/api/internal/handlers/`.
5. Wire the handler into the Connect router in `/api/cmd/api/main.go`.
6. Add the RPC to the role matrix in [API.md §9.2](./docs/API.md).
7. Write tests: happy path, auth failure, validation failure.
8. If the RPC mutates state, ensure your handler enforces `mustHaveScopeOn*` per [SECURITY.md §2.1](./docs/SECURITY.md).

### 10.2 Add a new DDB entity

1. Update [DATA_MODEL.md](./docs/DATA_MODEL.md) §2 with the entity definition, §4 with the PK/SK pattern, §8 decisions log with the rationale.
2. Add a Go struct in `/api/internal/domain/` reflecting the entity.
3. Add a repository file in `/api/internal/store/<entity>.go` with the access methods you need.
4. Add unit tests for the repository against DynamoDB Local.

### 10.3 Add a new email template

1. Create `/api/templates/email/<kind>.html.tmpl` and `<kind>.txt.tmpl`.
2. Add the kind enum to [EMAIL.md §3.4](./docs/subsystems/EMAIL.md) with the required `Vars` keys.
3. Add a test in `/api/internal/email/templates_test.go` that renders the template against a sample `Vars` and asserts the output.
4. Wire a call to `email.Send(ctx, req)` from the appropriate handler.

### 10.4 Add a new CMS collection

1. Update [CMS_CONTENT_MODEL.md §4](./docs/subsystems/CMS_CONTENT_MODEL.md) with the new collection's field schema.
2. Update `/cms/config.yml` (the Decap config) to add the collection.
3. Create a corresponding Astro renderer under `/site/src/pages/<collection>/...`.
4. Add a sample entry under `/content/<collection>/` (committed to repo) so the site has at least one rendered item.

### 10.5 Add a new GitHub Actions workflow step

1. Edit the workflow YAML under `.github/workflows/`.
2. If the step needs AWS access, add the necessary action to the OIDC role policy under `/infra/`.
3. Push and observe in the Actions tab.

### 10.6 Bump a dependency

- **Go:** `cd api && go get -u <pkg> && go mod tidy && go test ./...`
- **JS/TS:** `pnpm update <pkg> --filter <workspace>` then run tests
- Always review `pnpm audit` and `govulncheck` output before opening the PR.

---

## 11. Troubleshooting

### `make dev` fails to start

- Docker not running → start Docker Desktop.
- Port already in use → check `lsof -i :<port>`, kill the offender.
- LocalStack image pull failed → `docker pull localstack/localstack` manually.

### `buf generate` fails

- Confirm `buf --version` is recent.
- Check `buf.gen.yaml` plugin versions; pinned plugin images live in the Buf Schema Registry.

### Tests pass locally, fail in CI

- Check that you committed regenerated `*_pb.go` and `*connect.go` files.
- Check the Node version in CI matches `.nvmrc` (24.16.0).
- LocalStack version drift can cause integration-test differences; pin via Docker Compose.

### A deploy to prod broke something

1. Check Sentry for the new error.
2. Check CloudWatch logs for the affected Lambda.
3. If the issue is contained: roll forward with a fix-PR.
4. If the issue is broad: roll back via §8.3.

### A Cognito flow doesn't work locally

- The local mirror **does not** stand up Cognito. Use `DEV_BYPASS_AUTH=true` + `X-Dev-User-Id` header for local testing.
- Test Cognito-mediated flows by deploying to prod (no staging) and using your own admin account.

### Sentry shows an unfamiliar error

- Cross-reference with the recent deploy hash.
- If the error references a refresh token or session, **do not** include the raw value in any ticket — it's redacted from Sentry by design ([SECURITY.md §5.4](./docs/SECURITY.md)).

---

## 12. Getting help

- **Architectural questions:** start with the relevant design doc, then ask in the team channel.
- **Cost or AWS-specific questions:** [INFRASTRUCTURE.md](./docs/INFRASTRUCTURE.md).
- **Security concerns or suspected incidents:** [SECURITY.md §7](./docs/SECURITY.md), runbooks under `/docs/runbooks/`.
- **Anything user-facing:** [PROCEDURES.md](./PROCEDURES.md) and [PROCEDURES_ADMIN.md](./PROCEDURES_ADMIN.md).

When in doubt, ask. Better than building something the design didn't intend.
