# INFRASTRUCTURE.md

This document defines the cloud infrastructure for the NUMUN landing page and conference portal described in [PROJECT.md](./PROJECT.md). It covers **what services we use, where they live, and how content is served**. It is intentionally infrastructure-only — application/code architecture, schema design, and IaC tooling are separate concerns.

## Guiding constraints

- **Total monthly spend target: under $10/mo (~$100/yr).** Every choice below is justified against this ceiling.
- **AWS-native** wherever practical (single bill, single IAM model).
- **Prod only** — no staging environment. Developers run a local prod mirror.
- **No payment processing** (handled off-platform — see PROJECT.md).

---

## 1. Domain & DNS

| Item | Choice | Notes |
|---|---|---|
| Registrar | **GoDaddy** (unchanged) | No transfer needed. |
| DNS zone | **Amazon Route 53** | ~$0.50/mo per hosted zone. |
| Migration step | Point GoDaddy nameservers at the four Route 53 NS records for the `numun.org` hosted zone. | One-time; zero downtime if existing records are mirrored first. |
| TLS certificates | **AWS Certificate Manager (ACM)** in `us-east-1` | Free. DNS validation via Route 53. Wildcard cert `*.numun.org` + apex `numun.org`. |

### Subdomain layout

| Hostname | Purpose | Origin |
|---|---|---|
| `numun.org`, `www.numun.org` | Public landing page | S3 + CloudFront (static) |
| `portal.numun.org` | Portal web app (advisors, staff) | S3 + CloudFront (static SPA) |
| `api.numun.org` | Portal backend API | API Gateway (HTTP API) → Lambda |
| `cms.numun.org` | Decap CMS admin UI | S3 + CloudFront (static) |
| `assets.numun.org` | Public-readable file assets (background guides, CMS images) | S3 + CloudFront |

`www.numun.org` redirects to `numun.org` via a CloudFront function. The legacy `numun.org/portal` path issues a 301 redirect to `portal.numun.org`.

---

## 2. Architecture overview

```
┌─────────────────────────────── Public internet ───────────────────────────────┐
│                                                                               │
│   numun.org / www      portal.numun.org      api.numun.org      cms.numun.org │
│        │                     │                    │                  │        │
│        ▼                     ▼                    ▼                  ▼        │
│   ┌─────────┐           ┌─────────┐         ┌──────────────┐    ┌─────────┐   │
│   │CloudFrnt│           │CloudFrnt│         │  API Gateway │    │CloudFrnt│   │
│   └────┬────┘           └────┬────┘         │   (HTTP API) │    └────┬────┘   │
│        │                     │              └──────┬───────┘         │        │
│        ▼                     ▼                     ▼                 ▼        │
│   ┌─────────┐           ┌─────────┐         ┌──────────────┐    ┌─────────┐   │
│   │S3: site │           │S3: app  │         │   Lambda     │    │S3: cms  │   │
│   └─────────┘           └─────────┘         │  functions   │    └─────────┘   │
│                                             └──────┬───────┘                  │
│                                                    │                          │
│                                       ┌────────────┼──────────────┐           │
│                                       ▼            ▼              ▼           │
│                                  ┌─────────┐ ┌─────────┐    ┌──────────┐      │
│                                  │DynamoDB │ │S3 priv. │    │ SES      │      │
│                                  └─────────┘ │uploads  │    │(email)   │      │
│                                              └─────────┘    └──────────┘      │
└───────────────────────────────────────────────────────────────────────────────┘

   GitHub (content repo) ──► GitHub Actions ──► aws s3 sync ──► S3: site bucket
                                                                       │
                              (triggered by Decap CMS commits)         │
                                                                       ▼
                                                              CloudFront invalidation
```

---

## 3. Service-by-service

### 3.1 Landing page — S3 + CloudFront

- **Bucket:** `numun-org-site` (private, accessed only via CloudFront OAC).
- **Distribution:** one CloudFront distribution covering `numun.org` and `www.numun.org`.
- **Cache policy:** `Managed-CachingOptimized`. HTML cached short (5 min); static assets cached long with content-hash filenames.
- **Build artifacts:** **Astro** static-site output (APPLICATION.md §2).

### 3.2 Portal frontend — S3 + CloudFront

- **Bucket:** `numun-org-portal`.
- **Distribution:** separate CloudFront distribution for `portal.numun.org`.
- **App style:** single-page application. CloudFront error responses for 403/404 rewrite to `/index.html` so client-side routing works.
- **Auth tokens** are stored in cookies scoped to `.numun.org` so the SPA at `portal.numun.org` can call `api.numun.org` without cross-site cookie headaches.

### 3.3 CMS — Decap CMS, git-backed

- **Editor UI:** Decap CMS as a static bundle served from `numun-org-cms` bucket behind CloudFront at `cms.numun.org`.
- **Auth:** GitHub OAuth. Editors authenticate as their own GitHub user; their permissions are gated by the GitHub repo's collaborator list.
- **Content storage:** Markdown / JSON / image files under `/content/` in the monorepo (APPLICATION.md §1; CMS_CONTENT_MODEL.md §2).
- **Publish pipeline:** A commit to the content repo (made by Decap on behalf of the editor) triggers a **GitHub Actions** workflow that:
  1. Runs the static site generator.
  2. `aws s3 sync` to `numun-org-site`.
  3. Creates a CloudFront invalidation for changed paths.
- **Cost:** $0 ongoing — uses the same S3+CloudFront the landing page already runs on.
- **Documentation requirement:** A non-technical runbook (in this repo) must describe how a new content editor is onboarded: adding their GitHub user as a repo collaborator, logging into `cms.numun.org`, editing, and seeing changes deploy. This is critical for annual leadership turnover.

### 3.4 Portal backend — API Gateway + Lambda

- **API Gateway:** **HTTP API** (not REST API). $1.00 per million requests vs. $3.50 for REST. NUMUN won't approach 1M requests/month.
- **Compute:** AWS Lambda. Free tier: 1M requests + 400,000 GB-seconds per month, **always free** (not 12-month trial).
- **Runtime:** **Go** on `provided.al2023` (APPLICATION.md §4).
- **Custom domain:** `api.numun.org` mapped via API Gateway custom domain + ACM cert.
- **CORS:** allow `https://portal.numun.org` only.
- **Auth:** JWT issued by the backend, validated on each request. SSO is a future upgrade (PROJECT.md).

### 3.5 Database — DynamoDB

- **Why DynamoDB:** The $100/yr ceiling rules out RDS (~$144/yr for db.t4g.micro) and Aurora Serverless v2 (~$516/yr floor). DynamoDB's always-free tier (25 GB storage, 25 RCU + 25 WCU) covers NUMUN's scale (66 delegations, ~850 delegates/year) with room to spare.
- **Tradeoff:** the data is relational-shaped (delegations → delegates → assignments). Modeling this in a key-value store requires deliberate single-table or multi-table design. Acceptable given the cost constraint and the small entity counts.
- **Billing mode:** **On-demand** for v1 (simplest, no capacity planning, free-tier compatible). Switch to provisioned only if costs warrant it.
- **Backups:** Enable **Point-In-Time Recovery (PITR)**. Cost: ~$0.20/GB/month for backup storage; at NUMUN scale, well under $1/mo.

### 3.6 File storage — S3

Three buckets, separated by access pattern:

| Bucket | Access | Contents | Lifecycle |
|---|---|---|---|
| `numun-org-assets` | Public (via CloudFront at `assets.numun.org` or via the site distribution) | Background guide PDFs, CMS-managed images | Retain |
| `numun-org-uploads` | Private; presigned-URL access only | Advisor-uploaded CSV/XLSX during bulk delegate add | **30-day expiration** lifecycle rule |
| `numun-org-artifacts` | Private; presigned-URL access | Generated certificates, exports, future post-conference artifacts | Retain |

Background guides are publicly downloadable per PROJECT.md — no per-delegation access control.

### 3.7 Email — Amazon SES

- **Region:** same as the rest of the stack (`us-east-2`; see §5).
- **Setup:** verify the dedicated **`mail.numun.org`** subdomain; add DKIM CNAMEs to the Route 53 zone; configure SPF + DMARC TXT records (EMAIL.md §2.1).
- **Sandbox exit:** new SES accounts start in sandbox (can only email verified addresses). Submit a production-access request before launch.
- **Cost:** 62,000 emails/month free when sent from Lambda. Plenty of headroom for ~96 advisors.
- **Sending domain:** dedicated **`mail.numun.org`** subdomain with three purpose-scoped no-reply addresses (`noreply@`, `announcements@`, `cognito@`). See EMAIL.md §2.

### 3.8 Secrets — SSM Parameter Store

- All secrets (third-party API keys, JWT signing key, SES config, Sentry DSN) stored as **SecureString** parameters under `/numun/prod/...`.
- Lambda functions read via IAM-scoped paths at cold-start.
- **Cost:** $0 (standard parameters are free).
- Upgrade path: AWS Secrets Manager only if a future need for automatic rotation appears.

### 3.9 Observability

- **CloudWatch Logs:** all Lambda + API Gateway logs. Retention set to **30 days** (default is "never expire" which silently grows costs).
- **CloudWatch Metrics:** built-in Lambda / API Gateway / DynamoDB metrics. Free.
- **Alarms:** at minimum, billing alarm at $10/mo; Lambda error-rate alarm; DynamoDB throttle alarm. Alarms publish to an SNS topic that emails NUMUN staff.
- **Sentry (free tier):** error tracking for both the portal frontend and the Lambda backend. Free tier covers 5k errors/month — plenty.

### 3.10 CI/CD — GitHub + GitHub Actions

- **Source host:** GitHub. Free for public repos; private repos get 2,000 Actions minutes/month free.
- **Pipelines:**
  - **Landing site:** content repo push → build static site → `aws s3 sync` → CloudFront invalidate.
  - **Portal frontend:** app repo push → build SPA → `aws s3 sync` → CloudFront invalidate.
  - **Portal backend:** app repo push → package Lambda → deploy via IaC.
- **AWS access from Actions:** use **OIDC federation** (no long-lived AWS keys stored in GitHub secrets).

---

## 4. Local "prod mirror" for development

Per PROJECT.md, there is no staging environment, so developers must be able to stand up a prod-shaped environment locally.

- **DynamoDB:** `amazon/dynamodb-local` Docker image.
- **S3:** `localstack/localstack` (free community edition covers S3).
- **Lambda + API Gateway:** **AWS SAM CLI** (`sam local start-api`) — runs handlers in a local Lambda-like container.
- **SES:** stubbed; outbound emails written to a local maildir or a service like MailHog.
- **Glue:** a `docker-compose.yml` + a `Makefile` target (e.g., `make dev`) brings up the full stack in one command. **This is a hard requirement, not optional**, because there is no shared staging environment.

---

## 5. Region

- **Primary region:** `us-east-2` (Ohio) — typically cheapest US region, good latency for Northwestern / Midwest users.
- **Exception:** ACM certs for CloudFront **must** live in `us-east-1` (N. Virginia). All other resources stay in the primary region.
- Single-region only. No multi-region failover (out of budget, out of scope).

---

## 6. Cost estimate

Assumes typical year: 1 registration rush, ~850 delegates, modest traffic.

| Service | Estimated monthly | Notes |
|---|---|---|
| Route 53 hosted zone | $0.50 | Per-zone fee. |
| S3 (storage + requests, all buckets combined) | ~$0.20 | Tens of GB at most. |
| CloudFront | ~$0.00–1.00 | Free tier covers 1 TB egress for first 12 months; after that ~$0.085/GB. |
| API Gateway (HTTP API) | ~$0.00 | Free tier 1M req/mo for first 12 months; afterward $1/M. |
| Lambda | $0.00 | Always-free tier covers expected volume. |
| DynamoDB (on-demand + PITR) | ~$0.00–1.00 | Always-free tier covers throughput; PITR pennies. |
| SES | $0.00 | Free tier covers volume. |
| ACM | $0.00 | Free. |
| SSM Parameter Store | $0.00 | Standard params free. |
| CloudWatch | $0.00–0.50 | Free tier 5 GB ingestion + 5 GB storage. |
| Sentry | $0.00 | Free tier. |
| **Total** | **~$1–3/mo (~$12–36/yr)** | Comfortably under the $100/yr ceiling. |

A **billing alarm at $10/mo** will fire if anything drifts.

---

## 7. Decisions log

| Decision | Choice | Reason |
|---|---|---|
| DNS provider | Route 53 | Tight integration with ACM, CloudFront aliases; $6/yr. |
| Registrar | Keep at GoDaddy | No transfer cost or downtime. |
| Portal location | `portal.numun.org` (subdomain) | Independent deploys, clean cookie scoping, separate CDN configs. |
| Architecture | Serverless (S3+CF / API GW + Lambda / DynamoDB) | True $0 idle cost, scales for registration rush, fits $100/yr ceiling. |
| Database | DynamoDB | Only AWS-native DB option under $100/yr. |
| CMS | Decap CMS (git-backed) at `cms.numun.org` | Zero ongoing infra cost; reuses S3+CF; GitHub OAuth handles editor access. |
| Repo + CI | GitHub + GitHub Actions | Free for public repos / generous free tier; first-class Decap support. |
| Email | Amazon SES | Cheapest option; works directly from Lambda. |
| Secrets | SSM Parameter Store | Free (vs. $0.40/secret/mo for Secrets Manager). |
| Observability | CloudWatch + Sentry free tier | Sufficient for v1. |
| Environments | Prod only + mandatory local prod mirror | Doubles cost to add staging; not justified. |
| Region | `us-east-2` (CloudFront certs in `us-east-1`) | Cheapest US region. |

---

## 8. Open items / explicitly deferred

- **IaC tool** (Terraform / AWS CDK / AWS SAM) — chosen in the app design doc.
- **Static-site generator** for the landing page — chosen in the app design doc.
- **Portal frontend framework** and **Lambda runtime** — app design doc.
- **DynamoDB data model** (single-table vs. multi-table) — app design doc.
- **GitHub repo structure** (monorepo vs. separate content/app/infra repos) — app design doc.
- **Sender address(es) for SES** — final naming decision.
- **Content-editor onboarding runbook** — to be authored before first non-developer editor is onboarded.
