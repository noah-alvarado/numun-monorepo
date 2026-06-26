# APPLICATION.md

This document defines the **application architecture** — repo layout, frameworks, libraries, build/deploy pipelines, and auth — for the NUMUN landing page and conference portal. Infrastructure decisions live in [INFRASTRUCTURE.md](./INFRASTRUCTURE.md); product requirements live in [PROJECT.md](./PROJECT.md). **Data model design** (DynamoDB schemas, access patterns) is deferred to `DATA_MODEL.md`.

---

## 1. Repository

- **Layout:** **Monorepo**, public, hosted on GitHub.
- **Structure (top-level directories at repo root):**

```
/.github/     — GitHub Actions workflows
/api/         — [Go](https://go.dev/) backend (served at api.numun.org)
/cms/         — [Decap CMS](https://decapcms.org/) static admin bundle (served at cms.numun.org)
/content/     — Markdown / JSON / image content edited via Decap CMS
/docs/        — runbooks (content-editor onboarding, local dev, deploy)
/infra/       — AWS SAM templates (CloudFormation)
/portal/      — [SolidJS](https://www.solidjs.com/) SPA (served at portal.numun.org)
/site/        — [Astro](https://astro.build/) static site (landing page, served at numun.org)
PROJECT.md
INFRASTRUCTURE.md
APPLICATION.md
DATA_MODEL.md 
README.md
```

`site`, `portal`, and `api` live at the repo root

- **Package manager:** **pnpm** for everything JS/TS (site, portal, cms).
- **Node version:** pinned to **24.16.0** via `.nvmrc`.
- **Go version:** pinned via `go.mod`.

---

## 2. Landing page — `/site`

| Item | Choice |
|---|---|
| Static site generator | **Astro** |
| Styling | **Tailwind CSS** |
| Content source | Markdown / JSON files under `/content`, edited via Decap CMS |
| Output | Fully static HTML/CSS/JS, deployed to S3 bucket `numun-org-site` |

### Brand & visual identity

The landing page must follow **Northwestern University's visual identity guidelines** (https://www.northwestern.edu/brand/visual-identity/). Practically, this means extracting brand tokens (the official Northwestern purple, approved typefaces, spacing, etc.) into `tailwind.config.js` as the source of truth. All components reference those tokens; no hardcoded brand colors or fonts in JSX/HTML.

### Astro config notes

- Build output: `static` (no SSR).
- Image optimization: Astro's built-in `<Image>` for CMS-provided imagery.
- MDX enabled (Decap CMS edits Markdown; MDX allows embedded components on richer pages if needed).

---

## 3. Portal frontend — `/portal`

| Item | Choice | Notes |
|---|---|---|
| Framework | **SolidJS** | Fine-grained reactivity, small bundles. |
| Build tool | **Vite** | First-class Solid template. |
| Language | **TypeScript** | strict mode on. |
| Router | **Solid Router** (`@solidjs/router`) | |
| Server-state | **Solid `createResource`** (built-in) | Add `@tanstack/solid-query` later only if needs grow beyond what `createResource` handles cleanly. |
| Forms | **modular-forms** + **Valibot** | Solid-native, type-safe, tiny validator. |
| Styling | **Tailwind CSS** | Same brand tokens as the landing page (shared config exported from `/site`). |
| UI components | **Built from scratch** on Tailwind primitives | No external component library. |
| Output | Static SPA, deployed to S3 bucket `numun-org-portal` |

### SPA wiring

- Single `index.html` entry. CloudFront error responses (403/404) rewrite to `/index.html` so client-side routing works (see INFRASTRUCTURE.md §3.2).
- API base URL: `https://api.numun.org` — configured via Vite env vars at build time.
- Auth tokens: HttpOnly + Secure cookies scoped to `.numun.org`, set by the backend (see §5).

---

## 4. Portal backend — `/api`

| Item | Choice | Notes |
|---|---|---|
| Language | **Go** | |
| Style | **Lambdalith** (single Lambda, internal router) | |
| Router | **`net/http.ServeMux`** (Go 1.22+ pattern routing) | Idiomatic, zero deps. |
| Lambda adapter | **`aws-lambda-go-api-proxy/httpadapter`** | Bridges API Gateway HTTP API events ↔ `http.Handler`. |
| AWS SDK | **AWS SDK for Go v2** (`aws-sdk-go-v2`) | Direct, no wrapper. |
| DynamoDB | **`service/dynamodb`** + **`feature/dynamodb/attributevalue`** for struct marshaling | Wrapper libraries (e.g., `guregu/dynamo`) deferred — revisit if verbosity becomes a pain. |
| Auth | **Cognito JWT validation middleware** | |
| Email | **AWS SDK v2 `service/sesv2`** | |
| Logging | `log/slog` with JSON handler → CloudWatch Logs | |
| Errors | Sentry Go SDK (`getsentry/sentry-go`) | Free tier. |

### Handler shape

```go
// cmd/api/main.go
func main() {
    mux := http.NewServeMux()
    mux.Handle("POST /v1/delegations", authMiddleware(createDelegation))
    mux.Handle("GET  /v1/delegations/{id}", authMiddleware(getDelegation))
    // ... etc
    lambda.Start(httpadapter.NewV2(mux).ProxyWithContext)
}
```

- One Lambda, one binary, one deployment artifact.
- Cold start optimization: Go binary < 20 MB; init phase loads Cognito JWKS once and reuses.
- If a route grows heavyweight split it into its own Lambda.

### Project layout under `/api`

```
/api
  cmd/
    api/main.go            — Lambda entrypoint (mux wiring)
  internal/
    handlers/              — HTTP handlers per resource
    middleware/            — auth, logging, request ID
    store/                 — DynamoDB access (one file per access pattern group)
    domain/                — domain types & business logic (algorithm lives here later)
    email/                 — SES helpers
  go.mod
  go.sum
```

---

## 5. Auth — Amazon Cognito

| Item | Choice |
|---|---|
| Identity provider | **Amazon Cognito User Pool** |
| Flow | Hosted UI **not** used; custom screens in the SolidJS portal call Cognito directly via `amazon-cognito-identity-js` (or the newer JS SDK) for sign-up / sign-in / password reset. |
| Tokens | Cognito-issued **JWT ID + Access tokens**. |
| Storage in browser | **HttpOnly + Secure cookies** scoped to `.numun.org`, set by a thin `/auth/exchange` endpoint on the backend that takes Cognito tokens and writes the cookie. The raw Cognito tokens never live in JS-readable storage. |
| Validation | Go middleware on each request: fetch Cognito JWKS once at cold-start, verify signature + `iss` + `aud` + `exp`, attach claims to `r.Context()`. Libraries: `github.com/golang-jwt/jwt/v5` + `github.com/lestrrat-go/jwx/v2/jwk`. |
| Roles | A custom Cognito attribute `custom:role` with values `advisor` or `staff`. Validated server-side on every request; client never trusted. |

### v1 flows (built on top of Cognito's APIs)
- Advisor self-sign-up (email + password) → Cognito email verification → first login → create `Delegation` record on first action.
- Advisor password reset (Cognito built-in).
- Staff accounts created by an existing staff member via an admin route (no self-sign-up for staff).

### Deferred (PROJECT.md "planned future upgrade")
- SSO via Google / Microsoft / Apple — Cognito identity providers added later; no app changes beyond config.

---

## 6. CMS — `/cms`

- Decap CMS static admin bundle (`index.html` + `config.yml`) deployed to S3 bucket `numun-org-cms`.
- `config.yml` points to the GitHub content repo and `/content` directory.
- Editors authenticate via **GitHub OAuth** (Decap's built-in flow). NUMUN runs a tiny serverless OAuth proxy if needed — covered in INFRASTRUCTURE.md.
- All content edits are commits on the `main` branch by the editor's GitHub account; merge protection rules deferred (single-branch workflow for v1).

---

## 7. Infrastructure-as-Code — AWS SAM

- **All AWS resources** (S3, CloudFront, API Gateway, Lambda, DynamoDB, Cognito, SES config, ACM cert references, Route 53 records, IAM roles, SSM params) defined in **AWS SAM** templates under `/infra`.
- **One stack per environment** — only `prod` exists today. The same template is the basis for the local prod mirror.
- **Local prod mirror:** `sam local start-api` runs the Go Lambdalith locally; `dynamodb-local` + `localstack` (S3, SES stub) provide the AWS dependencies. A `Makefile` target (`make dev`) brings the whole thing up via `docker-compose`. See INFRASTRUCTURE.md §4.

---

## 8. CI/CD — GitHub Actions

- **Branching:** trunk-based on `main`. No long-lived branches, no PR previews.
- **Triggers:** every merge to `main` deploys to prod and tags a version. Manual rollback = redeploy the previous tag.
- **AWS auth:** **OIDC federation** between GitHub Actions and AWS — no static AWS keys stored in GitHub.

### Pipelines

| Workflow file | Trigger | Steps |
|---|---|---|
| `.github/workflows/site.yml` | Push to `main` touching `/site/**` or `/content/**` | pnpm install → astro build → `aws s3 sync` to `numun-org-site` → CloudFront invalidate |
| `.github/workflows/portal.yml` | Push to `main` touching `/portal/**` | pnpm install → vite build → `aws s3 sync` to `numun-org-portal` → CloudFront invalidate |
| `.github/workflows/api.yml` | Push to `main` touching `/api/**` or `/infra/**` | `go build` (linux/arm64) → `sam deploy` |
| `.github/workflows/cms.yml` | Push to `main` touching `/cms/**` | Build → `aws s3 sync` to `numun-org-cms` → invalidate |
| `.github/workflows/ci.yml` | Pull request | Lint, typecheck, unit tests across all packages. Blocks merge on failure. |

### Quality gates blocking merge
- **Lint** — ESLint defaults (no custom rules) for JS/TS, `gofmt -l` + `go vet` for Go.
- **Format** — Prettier defaults for JS/TS, `gofmt` for Go.
- **Typecheck** — `tsc --noEmit` for site and portal; `go build` for api.
- **Unit tests** — `vitest` (site, portal), `go test ./...` (api).
- **Integration tests** against LocalStack are **nice-to-have**, not blocking.

---

## 9. Tooling defaults (deliberately minimal)

| Tool | Config policy |
|---|---|
| ESLint | Defaults only. No custom rule set. |
| Prettier | Defaults only. No `.prettierrc` overrides unless we hit a real problem. |
| TypeScript | `strict: true`; otherwise tsconfig defaults. |
| Tailwind | One shared `tailwind.config.js` exporting Northwestern brand tokens; consumed by both `/site` and `/portal`. |
| Go | `gofmt` + `go vet`; no `golangci-lint` config in v1 unless needed. |
| Husky / pre-commit hooks | **None.** CI is the single source of truth for quality gates. |

---

## 10. Decisions log

| Decision | Choice | Reason |
|---|---|---|
| Repo layout | Public monorepo, `/site` `/portal` `/api` at root | Small team; coordinated changes; one CI |
| Static site | Astro + Tailwind | Best content-first SSG; CMS-friendly |
| Portal SPA | SolidJS + Vite + TypeScript | Tiny bundles, fine-grained reactivity |
| Portal data fetching | Solid `createResource` (built-in) | Avoid extra deps; revisit TanStack Solid Query if pain emerges |
| Portal forms | modular-forms + Valibot | Solid-native, tiny validator |
| UI components | Built from scratch on Tailwind | Full control over Northwestern brand styling |
| Backend language | Go | Fast cold starts, small binaries |
| Backend style | Lambdalith with `net/http.ServeMux` | Simpler ops, idiomatic Go |
| AWS SDK | aws-sdk-go-v2 directly | No wrapper deps |
| Auth | Amazon Cognito + custom UI in SolidJS | Free at scale; future SSO via identity providers |
| IaC | AWS SAM | Best fit for serverless; matches local dev story |
| CI/CD | GitHub Actions, OIDC, trunk-based, no PR previews | Free, simple |
| Package manager | pnpm | Fast, monorepo-friendly |
| Node | 24.16.0 (`.nvmrc`) | Pinned |

---

## 11. Open items / deferred

- **Assignment algorithm design** — separate doc after data model is settled.
- **Decap CMS OAuth proxy** — exact mechanism (Lambda-based vs. third-party) deferred to a CMS setup runbook.
- **Email templates** — content and tooling for SES-sent announcements.
- **Northwestern brand token extraction** — concrete `tailwind.config.js` values pulled from the brand guide.
- **GitHub repository name(s) and OAuth app registrations** — operational setup, not a design choice.
