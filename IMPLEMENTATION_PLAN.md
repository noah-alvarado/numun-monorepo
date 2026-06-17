# NUMUN Implementation Plan

## Context

This plan turns the ~5,900-line design corpus in `/Users/nalvarado/numun-monorepo` into a milestone-ordered build sequence. Design is fixed; this is execution. Two products live in one monorepo: a CMS-edited Astro landing site at `numun.org`, and a SolidJS portal at `portal.numun.org` backed by a Go Lambdalith on AWS. The hard constraints driving sequencing are: **prod-only deployment** (so `make dev` is a foundational deliverable, not a convenience), **<$100/yr AWS spend**, **trunk-based deploys with no PR previews**, **single-table DDB design**, and a Cognito-backed auth system whose sessions live server-side in DDB.

The aim of the plan is to reach a deployable Lambdalith and a runnable local-prod mirror as fast as possible, then bring the system up feature-by-feature in dependency order, with each milestone yielding something demonstrable end-to-end. The plan flags ambiguities the design docs leave open and risks for first-time AWS-stack mechanics (Cognito-via-SES wiring, the Decap OAuth `postMessage` handshake, Connect-over-Lambda response shape, OAC, OIDC trust policies) so they don't ambush the build.

---

## Recommended starting milestone

**Start with M0 — Foundation.** The single biggest dependency in the whole plan is `make dev`. Until that runs, every later milestone is paying a slow inner-loop tax. M0 is also the safest place to discover and surface tool-version friction (Node 24.16.0, Go ≥1.22, SAM CLI, Buf, Docker) before they affect a feature deliverable.

---

## Milestones

### M0 — Foundation (no AWS yet)

Goal: `make dev` brings up the empty stack; CI runs lint/typecheck on hello-world code.

- Repo skeleton matching APPLICATION.md §1: `/site`, `/portal`, `/api`, `/cms`, `/content`, `/infra`, `/.github/workflows`, `/docs`, `/docs/runbooks`.
- Toolchain pins: `.nvmrc` (`24.16.0`), `pnpm-workspace.yaml`, `/api/go.mod` (Go ≥1.22), root `Makefile`, root `.editorconfig`, `.gitignore`.
- Buf workspace under `/api/proto/numun/v1/` with `buf.yaml`, `buf.gen.yaml`, and one stub `health.proto` to prove the Go + TS codegen pipeline. Generated code committed under `/api/internal/gen/` and `/portal/src/gen/`.
- `/api/cmd/api/main.go` hello-world Lambdalith using `net/http.ServeMux` + `aws-lambda-go-api-proxy/httpadapter` + `connect-go`. Single `HealthService.Check` RPC plus a plain `GET /v1/health` for non-Connect readers.
- `/portal/` Vite + SolidJS + Tailwind skeleton showing one page that calls the generated `HealthService` client.
- `/site/` Astro + Tailwind skeleton with one page; shared `tailwind.config.js` exported from a workspace package consumed by site + portal.
- `/cms/` Decap static bundle with placeholder `config.yml` (not yet wired to OAuth).
- `docker-compose.yml` with services: `dynamodb-local:8000`, `localstack:4566` (S3 + SES + SQS + SNS services enabled), `mailhog:1025/8025`, plus an init container that creates the `numun-prod` table and LocalStack buckets/queues.
- `Makefile` targets: `dev`, `dev-down`, `seed`, `reset`, `api-restart`, `proto`, `lint`, `typecheck`, `test`, `integration-tests`, `doctor` (verify tool versions).
- `.github/workflows/ci.yml` running lint + typecheck + unit tests for all three packages and `buf lint` + `buf breaking --against ".git#branch=main"`.
- **Stub runbooks** created as one-paragraph placeholders, filled out per-milestone: all nine listed in SECURITY.md §7.1, plus `cms-editor-onboarding.md`, `cms-oauth-app-setup.md`, `seed-users.md`.

**Verification:** `make doctor` passes; `make dev` brings up all services and `curl localhost:3000/v1/health` returns 200; `pnpm --filter portal dev` loads at localhost:5173 and shows the health response.

### M1 — Infrastructure baseline (first prod deploy)

Goal: A real `api.numun.org/v1/health` answers in production.

- SAM templates under `/infra/` for: the `numun-prod` DDB table (PK/SK + GSI1 + GSI2 per DATA_MODEL.md §4–§5, on-demand billing, PITR on, TTL attribute `expiresAt`), the six S3 buckets (`numun-org-site`, `-portal`, `-cms`, `-assets`, `-uploads`, `-artifacts`) with account-level Block Public Access + OAC-only CloudFront access, four CloudFront distributions (site, portal, cms, assets), a wildcard ACM cert in `us-east-1` (cross-region reference), Route 53 hosted zone records, API Gateway HTTP API with custom domain `api.numun.org`, the Lambdalith function, SSM parameter scaffolding under `/numun/prod/*`, SNS topic for alarms, CloudWatch billing alarm at $10/mo.
- Cognito user pool with: password policy (12-char min, lowercase + digits), custom attribute `custom:role`, MFA enabled at pool level (opt-in TOTP, no enforcement), email config initially using the Cognito default sender (Cognito→SES wiring is M2.5).
- `.github/workflows/api.yml`, `site.yml`, `portal.yml`, `cms.yml` using GitHub OIDC federation to per-workflow IAM roles per SECURITY.md §3.2.
- **Manual DNS step documented** in `/docs/runbooks/dns-cutover.md`: point GoDaddy nameservers at the four Route 53 NS records. Performed once; the runbook captures exact NS values + verification steps.
- **Manual SES domain identity step documented** in `/docs/runbooks/ses-domain-setup.md`: verify `mail.numun.org`, add DKIM CNAMEs + SPF + DMARC (`p=quarantine; pct=10`) to Route 53. Performed manually; SAM doesn't manage the DKIM-CNAME content (SES generates it). The three sender identities (`noreply@`, `announcements@`, `cognito@`) get verified here too.
- **Submit SES sandbox-exit request** at the end of M1 so it's approved by the time M2 needs it.

**Verification:** `https://api.numun.org/v1/health` returns 200 from a browser; CloudFront distributions resolve to "It works" placeholders; billing alarm subscribes a real email.

### M2 — Auth end-to-end

Goal: An advisor can self-register at `portal.numun.org/sign-up`, verify their email, sign in, and call an authenticated RPC.

- Proto-first: `auth.proto`, `users.proto`, `common.proto`, `errors.proto`. `AuthService.Exchange`, `AuthService.Logout`, `UserService.GetMe`, `UserService.UpdateUser`, `UserService.InviteStaff`.
- Domain types + DDB repositories for `User`, `Session`, `AuthAuditEvent` (DATA_MODEL.md §2.2, §2.15, §2.16; AUTH.md §13). Optimistic-lock pattern centralized in the repo layer.
- Auth middleware: cookie → Session lookup → access-token refresh against Cognito if expired → claims on `r.Context()`. CSRF double-submit. Role mirror reconcile per AUTH.md §4.2.
- Scope helpers stubbed in `/api/internal/auth/scope.go` (`mustHaveScopeOnDelegation`, `…OnDelegate`, etc.) — return `not_found` defaults. The lint rule that blocks handlers from touching repositories without a preceding `mustHaveScopeOn*` call ships as a `bash`-based grep gate in `.github/workflows/ci.yml` per SECURITY.md §2.1.
- Cognito post-confirmation Lambda (`/api/cmd/cognito-post-confirmation/main.go`) writes the `User` row + `signup_completed` audit event. Wired in SAM as a Cognito trigger.
- **`DEV_BYPASS_AUTH=true` middleware path**: when set, accepts `X-Dev-User-Id` and synthesizes a context as if the named seed user had a valid session. Refuses to operate unless an env var `DEV_MODE=true` is also set (defense-in-depth so a misconfigured prod can't accidentally bypass).
- Portal: sign-up, sign-up/verify, sign-in, sign-in/forgot, sign-in/reset, sign-in/new-password screens. `amazon-cognito-identity-js` for the direct Cognito calls; portal then `POST`s tokens to `/v1.AuthService/Exchange`.
- `make seed` populates DDB Local with three seed users (one of each role) per `/docs/seed-users.md`.
- **First-admin bootstrap runbook** (`/docs/runbooks/first-admin-bootstrap.md`) and the actual one-time bootstrap of the first staff-admin in prod Cognito via `aws cognito-idp admin-create-user`. Until this runs, the portal has no admin.

**M2.5 — Cognito-via-SES wiring (folded into M2's tail)**

The SES domain identity from M1 is verified by now and ideally sandbox-exited. Configure the Cognito user pool's email config: source = SES, ARN of the verified `cognito@mail.numun.org` identity, ReplyTo unset (per EMAIL.md). Without this, prod is capped at 50 Cognito emails/day. Doing it here, not in M9, because real signups depend on it.

**Verification:** End-to-end advisor self-sign-up works in prod; `UserService.GetMe` returns the caller's profile; logout clears cookies and invalidates the Session row.

### M2.7 — Staging environment + IaC parameterization

Goal: The current single-account "prod" deployment becomes a proper `test` staging environment, and the same SAM templates can stand up a future real-prod stack in a different AWS account by passing `Env=prod`.

- All five SAM templates (`infra/bootstrap`, `infra/base-data`, `infra/base-cdn`, `infra/api`, `infra/billing-alarm`) accept an `Env` parameter that threads through every named resource and cross-stack Export. `numun-prod-*` and `numun-org-*` literals are gone.
- Templates also accept `RootDomain` (default `numun.org`) and `EnvSubdomain` (empty for prod, `test` for staging) and compose the effective apex via a `HasSubdomain` Condition. Cookie domain, CORS origins, CloudFront aliases, API Gateway custom domain, and the www-redirect CloudFront function body all derive from the composed apex.
- `base-cdn` adds the five Route 53 alias records (apex, www, portal, cms, assets) that were deferred in M1, so each env owns its own DNS records once the hosted zone is authoritative.
- GitHub deploy workflows flip `environment:` from `nalvarado` to `test`. Stack/bucket names derive from `${{ vars.ENV_NAME }}`. The portal build picks up `VITE_API_BASE_URL`, `VITE_COGNITO_USER_POOL_ID`, and `VITE_COGNITO_CLIENT_ID` from env-scoped vars.
- App-code: `defaultTableName = "numun-prod"` is gone — `DDB_TABLE_NAME` is now required at boot. Local-dev parity (docker-compose, LocalStack init, sam-env-vars.json, `make seed`) renames to `numun-test-*` to match the staging shape.
- New runbook `/docs/runbooks/fresh-environment-deploy.md` captures the full teardown + rebuild procedure so a future real-prod buildout in a new AWS account is a single document away.
- New `/scripts/verify-deploy.sh` generalizes the M2 verification one-shot for any env/pool/admin combination.

**Verification:** A clean teardown of `numun-prod-*` followed by a rebuild as `numun-test-*` results in CI green on a no-op push, the bootstrapped admin in the new pool round-trips Exchange → GetMe → Logout via `scripts/verify-deploy.sh`, and the runbook is followable end-to-end without local-state cheats.

### M3 — Data layer + first entity flows

Goal: An advisor registers a delegation; a staff-admin approves it.

- Protos: `conferences.proto`, `delegations.proto`, `public.proto`.
- Repositories for `Conference`, `Delegation`, `DelegationAdvisor`, `StaffDelegationAssignment`, `StaffCommitteeAssignment` (the staff-link entities are added now so scope helpers can resolve real bindings).
- Handlers: `ConferenceService` (create/list/update — admin only), `DelegationService` (create/get/list/update/approve/reject/add-advisor/remove-advisor), `PublicService.GetActiveConference`.
- Scope helpers stubs from M2 made real: read `DelegationAdvisor`, `StaffDelegationAssignment`, `StaffCommitteeAssignment` links. `not_found` vs `permission_denied` per AUTH.md §7.3.
- `protovalidate` annotations on the protos for input validation; server emits `google.rpc.BadRequest`.
- Audit-log writers for `delegation_approved`, `delegation_rejected`, `scope_granted`, `scope_revoked`.
- (Idempotency-Key middleware deferred to M12 — see Hardening.)

**Verification:** Seed an advisor; create a delegation; admin approves; advisor `GetDelegation` returns approved. Repeat using a wrong scope: returns `not_found`.

### M4 — Portal shell + first authenticated view

Goal: A signed-in advisor sees "My Delegation" populated from real API calls.

- Solid Router topology: `/sign-in`, `/sign-up`, `/dashboard`, `/delegation`, `/admin/*`. App shell gated on `UserService.GetMe`; SameSite-Strict cookie navigation flash mitigated per AUTH.md §9.1.
- `createResource` wrappers per service. Connect interceptor that attaches `X-CSRF-Token` from the cookie and `Idempotency-Key` UUIDv7 on mutating calls.
- Forms via `modular-forms` + `Valibot`, mirroring `protovalidate` rules (manual sync for v1; codegen path is an open item).
- One real screen end-to-end: "My Delegation" (school, address, preferences, advisors list, status). Edits go through `DelegationService.UpdateDelegation` with `expected_version`.
- Admin: "Pending delegations" list using `GSI2` status filter.

**Verification:** Round-trip a delegation edit; observe optimistic-lock 409 by hand-editing through two browser tabs.

### M5 — Site shell + Decap CMS wired up

Goal: A non-developer can edit landing-page content at `cms.numun.org`, and changes deploy.

- Astro site with brand tokens in `tailwind.config.js` — **this is where the Northwestern brand decisions land** (open item below).
- Decap CMS `config.yml` collections per CMS_CONTENT_MODEL.md: `pages/`, `leadership/`, `news/`, `past-conferences/`, `awards-archive/`, `background-guides/`, `faq/`, `config/`, `uploads/`. `publish_mode: simple`.
- **CMS OAuth proxy on the Lambdalith**, mounted as plain HTTP routes on the same `ServeMux` *outside* the Connect router: `GET /cms-oauth/auth`, `GET /cms-oauth/callback`. HMAC-signed `state`, signed cookie, `postMessage` to `https://cms.numun.org`.
- Manual setup: register the GitHub OAuth App (callback `https://api.numun.org/cms-oauth/callback`, scope `repo`), store client id + secret as SSM `SecureString` parameters `/numun/prod/cms_oauth/client_id` and `/cms_oauth/client_secret`. Captured in `/docs/cms-oauth-app-setup.md` (M5 fills out the M0 stub).
- `.github/workflows/site.yml` runs on `/site/**` and `/content/**` pushes: fetch `PublicService.GetActiveConference`, write `/content/_generated/active-conference.json`, build Astro, `aws s3 sync` to `numun-org-site`, invalidate CloudFront. Branch protection on `main` carries an exception allowing direct commits to `/content/**` (Decap requirement; documented as accepted risk per SECURITY.md §6.4).
- Sanitize Markdown with `rehype-sanitize`; inline HTML stripped.
- `/docs/cms-editor-onboarding.md` fills out (the audience is non-technical NUMUN leadership).

**Verification:** Log into `cms.numun.org` as a non-developer GitHub user; edit a leadership entry; watch GitHub Actions deploy; see the change at `numun.org`.

### M6 — Bulk delegate import

Goal: An advisor uploads a CSV/XLSX or pastes a Google Sheets URL, previews the parsed rows, and commits.

- Protos: `delegates.proto` with `ListDelegates`, `GetDelegate`, `CreateDelegate`, `UpdateDelegate`, `DeleteDelegate`, `CheckIn`, `PreviewUpsertDelegatesBulk`, `UpsertDelegatesBulk`.
- Repositories for `Delegate`, `BulkImportPreview`, `BulkImportJob`.
- Parser layer in `/api/internal/parse/`: CSV (stdlib), XLSX (`xuri/excelize/v2` — pending final selection; see ambiguities), Google Sheets via the safe-HTTP-client wrapper.
- `/api/internal/httpclient/safe.go`: SSRF guardrails per SECURITY.md §2.7 (host allowlist `docs.google.com` + `*.googleusercontent.com`, private-IP block, 5s connect / 10s overall timeout, no cross-origin redirects). Lint-enforced against direct `net/http.Client` usage.
- S3 presign endpoints for the `bulk-delegates/<userId>/<uuid>.{csv|xlsx}` key shape; 10-min presign TTL; 5 MB max via Content-Length-Range; 30-day bucket lifecycle.
- Commit path: TransactWriteItems batched to 25 items, splitting across batches up to the 100-op limit; if exceeded, write a `BulkImportJob` and process sequentially. Soft-delete via `isDeleted=true` (Full-sync mode).
- XLSX macro rejection (`xl/vbaProject.bin` check).
- Audit events: `bulk_import_previewed`, `bulk_import_committed` per AUTH.md §11.
- Rate limit: 10 previews + 10 commits / hour / advisor.
- Portal: upload-or-paste → preview table → confirm → progress + result.

**Verification:** Upload a 200-row CSV; observe preview; commit; verify delegates listed. Run a 600-row import to exercise multi-batch path.

### M7 — Committees, positions, assignment algorithm

Goal: Admin defines committees/positions; algorithm produces a proposal; admin approves it.

- Protos: `committees.proto` (CommitteeService + PositionService), `assignments.proto` (AssignmentService + AssignmentRunService).
- Repositories for `Committee`, `Position`, `Assignment`, `AssignmentRun`. Position validator: `dualDelegation ⇒ maxDelegates == 2`.
- `/api/internal/domain/assignment/` implements the algorithm: Phase A (dual-delegation seeding), Phase B (greedy with deferred-delegate backtracking, 1000-attempt cap), Phase C (2-opt local search, 15s budget), Phase D (validate H1–H6, attach score + reason). Deterministic; canonical seed = `hash(conferenceId)`; re-shuffle uses `hash(conferenceId, runOrdinal++)`.
- Concurrency: conditional write on `AssignmentRun` with `status=running` enforces single-run-at-a-time; `GSI2` PK = `CONF#<id>#ASSIGNMENT_RUN_STATUS#<status>` lookup before start.
- Lambda config: API Gateway 29s ceiling; algorithm soft cap 20s; Lambda memory bumped to 2048 MB for this code path (split into a dedicated function if the cold start of bumping the whole Lambdalith proves wasteful — re-evaluate after first run).
- API surface per ASSIGNMENT_ALGORITHM.md §5: `Propose` (with `dry_run` and optional `seed`), `Approve`/`Unapprove`, `UpdateAssignment` (manual edit). `AssignmentRunService.GetCurrentRun` for in-flight visibility.
- Audit events: `assignment_approved`, `assignment_unapproved`, `assignment_manually_edited`.
- Portal: Committees admin (CRUD with positions nested), Assignment Studio (run proposal, view proposal, edit, approve in bulk).

**Verification:** Use the seed dataset; run `Propose dry_run=true`; inspect the result; run for real; approve all; observe assignments under `/delegation/.../assignments`.

### M8 — Payments ledger

Goal: Staff records payments; balances + paid-in-full reflect ledger.

- Proto: `payments.proto` (`PaymentService`).
- Repository for `PaymentRecord`. Append-only ledger; `Delegation.balanceDue` + `paidInFull` updated transactionally with the new ledger row (TransactWriteItems).
- Audit event: `payment_recorded`.
- Portal: payments tab on the delegation view; admin-only RecordPayment form.
- CSV export shim: `GET /v1/exports/payments.csv` per API.md §12.

**Verification:** Record a charge + two partial payments; balance + paid-in-full flip correctly.

### M9 — Email pipeline (worker + feedback + templates)

Goal: Triggered events produce delivered emails; bounces/complaints suppress future sends.

- `email-worker` Lambda (`/api/cmd/email-worker/`): SQS-triggered on `numun-prod-email-send`, 60s visibility, max-receive 3, DLQ `numun-prod-email-send-dlq`. Idempotency by `clientToken` lookup against `EmailEvent`. Renders templates and calls SES.
- `email-feedback` Lambda (`/api/cmd/email-feedback/`): SNS-triggered on `numun-prod-email-feedback`. Resolves user by recipient email; flips `User.emailStatus` for hard bounces and complaints; writes `EmailEvent` rows.
- Templates under `/api/templates/email/` with `_layout.html.tmpl` + `_layout.txt.tmpl` and per-kind files for: `delegation_approved`, `delegation_rejected`, `payment_recorded`, `bulk_import_committed`, `assignment_run_completed`, `scope_role_changed`, `new_registration_summary`, `announcement`. Each renders HTML + plaintext; `multipart/alternative` always.
- New-registration debounce: `NotificationDedupe` partition `NOTIFY_DEDUPE#new-registration#<conferenceId>` with 15-min TTL; conditional `PutItem`-first-wins enqueues a 900s-delayed SQS message.
- List-Unsubscribe header + handler honoring `User.announcementsOptIn`.
- Wire existing M3/M8 audit-event writers to enqueue SQS sends.
- Protos: `announcements.proto` (`Send`, `PreviewSendAudience`, list/get), `email_health.proto` (`ListSuppressed`, `Unsuppress`).
- Admin email-health screen in the portal.
- MailHog substitution for SES in `make dev`: app sends via the LocalStack SES endpoint; LocalStack forwards SMTP to MailHog for inspection.
- Begin DMARC progression schedule: 2–4 weeks at `pct=10`, then `pct=100`, then `p=reject; pct=100` (tracked in M12).

**Verification:** Approve a delegation; advisor receives a real email in prod; complaint webhook flips `emailStatus`; subsequent sends suppressed; admin unsuppresses; sends resume.

### M10 — Day-of features

Goal: Staff check in delegates; assignment approve/unapprove UX is solid for live use.

- `DelegateService.CheckIn` handler + audit; portal scan/search-and-check-in screen.
- Assignment approve/unapprove polish: bulk select, last-minute swap UX, edge cases (re-approve after manual edit).
- Stress-test the scope helpers with the staff-staffer (case c) committee path.

**Verification:** Live-run the seed conference: check in delegates, swap assignments, observe audit-log entries.

### M11 — Post-conference

Goal: Awards captured; CSV exports cover everything needed for post-conference paperwork.

- `AwardService` (CRUD).
- Audit events: `award_created`, `award_modified`, `award_deleted`.
- `ExportService` parallel HTTP routes for assignments, delegates, payments (BOM + CRLF, scope-filtered server-side).
- Public awards listing on the site (CMS-managed `awards-archive/` collection populated from this year's data).

**Verification:** Generate a per-conference awards page; download all three CSVs; spot-check column completeness.

### M12 — Hardening

Goal: All the SECURITY.md operational controls actually exist.

- Rate-limit middleware: per-IP 60/min, per-user 300/min, `PublicService` 60/min/IP, bulk-import limits enforced (some live earlier; this is the full audit pass).
- All nine runbooks under `/docs/runbooks/` filled out: `account-takeover.md`, `data-breach-suspected.md`, `site-defacement.md`, `email-reputation-collapse.md`, `ddos-or-api-abuse.md`, `sign-up-abuse.md`, `breakglass-access.md`, `mfa-enrollment.md`, `first-admin-bootstrap.md` (latter two already exist from M2; finalize content).
- Slog redaction wrapper + Sentry `BeforeSend` scrubber audited against the field allowlist.
- DMARC tightening completed (`p=reject; pct=100`).
- HSTS preload submission (after 30 days of stable deploy).
- SES sandbox-exit confirmed completed (should be done by now; verify).
- govulncheck + pnpm audit gating in CI.
- Backup verification: do a restore drill of DDB PITR into a side table.
- **Idempotency-Key middleware** (24h TTL) for mutating RPCs. Deferred from M3 — pick the depth (in-flight lock vs. completed-status replay vs. full byte-for-byte replay per API.md §8) and implement. Open item; see plan ambiguity #12.

**Verification:** Walk each runbook against a tabletop exercise; confirm alarms fire on injected failures.

### M13 — Post-M12 cleanup

Goal: Pay down the small set of follow-ups discovered during the M12 dep-audit pass. Nothing here blocks launch; each item is independently shippable.

- **`@numun/cms` build script.** `cms/package.json` references `node scripts/build.mjs` but the script does not exist on `main` (pre-existed M12; surfaced during the workspace-wide `pnpm run build`). Decap CMS is a static bundle — the build is just an `aws s3 sync` of `cms/index.html` + `cms/config.yml`. Either author `cms/scripts/build.mjs` to assemble the bundle, or replace the `build` script with a no-op + push the actual sync logic into `.github/workflows/cms.yml` (which is where the M5 design originally put it). Recommendation: the latter, since the CMS has no real build step.
- **Astro 7-beta → stable.** When `astro@7.0.0` ships, bump out of `7.0.0-beta.4` along with the four ecosystem packages (`@astrojs/mdx`, `sitemap`, `rss`, `check`).
- **Vite 9 readiness.** Astro internals use `resolve.alias.customResolver`, which Vite 8 deprecates and Vite 9 will remove. Astro's responsibility; track upstream.
- **Markdown processor.** Astro 7 deprecated the top-level `markdown.remarkPlugins` / `rehypePlugins` in favor of a fully-composed `markdown.processor` from `unified()`. Already migrated in M12 dep-audit; revisit if the surface grows beyond `rehype-sanitize`.
- **Sentry follow-ups.** Once the project's SENTRY_DSN secrets are populated per [docs/runbooks/sentry-setup.md](./docs/runbooks/sentry-setup.md), validate the BeforeSend redaction in production with the smoke-test from [docs/runbooks/operational-launch-checklist.md](./docs/runbooks/operational-launch-checklist.md) §5.
- **Optional: backwards-compat audit gate.** `pnpm audit --audit-level=high --prod` currently passes (1 low + 1 moderate remain). Re-evaluate periodically; the moderate is in a transitive of `@astrojs/check` and will clear when the ecosystem releases drop their beta-tag.

**Verification:** Workspace-wide `pnpm run build` succeeds across all five packages (currently fails on cms).

### M14 — Version-controlled environment config

Goal: Move per-environment configuration out of GitHub Environment variables and into the repo, with secrets centralized in SSM Parameter Store. Eliminates "config drift" failure modes like the M12 launch incident where `vars.AWS_REGION` was unset and four deploy workflows silently failed for two pushes before anyone noticed.

The four deploy workflows (`api.yml`, `portal.yml`, `site.yml`, `cms.yml`) stay separated — the deploy targets (Lambda+SAM vs. S3-sync) are different enough that one-per-stack is clearer than a matrix.

#### Storage shape

- **`infra/envs/<env>.yaml`** — one file per environment (`test`, `prod`), version-controlled. Carries the stable identifiers:
  ```yaml
  ENV_NAME: test
  ENV_SUBDOMAIN: test         # empty for prod
  ROOT_DOMAIN: numun.org
  APEX_DOMAIN: test.numun.org
  AWS_REGION: us-east-2
  AWS_ACCOUNT_ID: "034083889387"
  ALARM_EMAIL: noah@alvarado.dev
  SAM_ARTIFACTS_BUCKET: numun-test-sam-deploys-034083889387
  ```
  Each deploy workflow loads it via `yq` (one-liner; pre-installed on `ubuntu-latest`):
  ```bash
  set -a; eval "$(yq '. | to_entries | .[] | "export " + .key + "=" + (.value | @sh)' infra/envs/$ENV.yaml)"; set +a
  ```
- **`aws cloudformation describe-stacks`** for stack outputs (cert ARNs, hosted-zone ID, Cognito IDs, CloudFront distribution IDs, deploy-role ARNs). Read live at deploy time rather than mirrored into config. The stack is the source of truth; CFN drift is impossible by construction.
- **SSM SecureString `/numun/${env}/...`** for secrets. Already used by the application (cms_oauth, email/unsubscribe, github_app); extend to anything that needs to survive across local dev + CI + Lambda runtime.
- **GitHub Secrets**: emptied. Only retained for secrets where SSM can't reach (none today; possibly a future "break-glass" PAT for repo-write actions).

#### Scope of work

1. **Author `infra/envs/test.yaml` and `infra/envs/prod.yaml`** with the seven identifier fields above. Migrate values from current `gh variable list --env test`.
2. **Workflow loader.** Each of `api.yml`, `portal.yml`, `site.yml`, `cms.yml` gains a "load env" step early that reads the YAML and exports the keys as job env vars. Replaces every `${{ vars.X }}` reference.
3. **Stack-output lookup.** A small `scripts/load-stack-outputs.sh` (idempotent, parameterized on stack name) that runs `aws cloudformation describe-stacks --query Outputs` and exports each output as an env var. Called by each workflow after env YAML loads. Replaces `${{ vars.API_CERTIFICATE_ARN }}`, `vars.COGNITO_*`, `vars.PORTAL_DISTRIBUTION_ID`, all `vars.DEPLOY_ROLE_*_ARN`, etc.
4. **Sentry-DSN migration (re-does the M12 wiring along the new pattern).**
   - **API**: drop the `SentryDsn` SAM parameter and the `secrets.SENTRY_DSN` workflow input. Refactor `api/internal/observability/InitFromEnv` to fetch from SSM at cold-start (matches `cms.LoadConfigFromSSM` shape). Lambda IAM grant on `/numun/${env}/sentry/*` is already covered by the wildcard policy on `/numun/${env}/*`.
   - **Portal**: workflow gains an "ssm get-parameter" step before `pnpm build` that exports `VITE_SENTRY_DSN` from `/numun/${env}/sentry/dsn`. Portal-deploy IAM role gets `ssm:GetParameter` on `/numun/${env}/sentry/*` added to its policy in `infra/bootstrap/oidc-roles.yaml`.
5. **Decommission GitHub Environment variables.** Once workflows are green on the new path, `gh variable delete` each migrated entry. Delete the stale `nalvarado` environment (left over from pre-M2.7).
6. **Update runbooks**:
   - `fresh-environment-deploy.md`: replace the "set GitHub env variables" section with "author `infra/envs/<env>.yaml`" + "put secrets in SSM."
   - `sentry-setup.md`: change "create env-scoped GitHub secret" → "create `/numun/${env}/sentry/dsn` SSM SecureString."
   - `operational-launch-checklist.md`: §5 Sentry verification updated to match.

#### Constraints + risks

- **Account ID disclosure**: `AWS_ACCOUNT_ID` ends up in version control. Already disclosed in `infra/bootstrap/oidc-roles.yaml`; not a new exposure. If the repo ever goes public, this is the kind of value worth re-evaluating.
- **Migration is per-env**: do `test` first end-to-end, prove it works, then `prod`. Keep both code paths in the workflow during the transition (`if [ -f infra/envs/$ENV.yaml ]; then load_yaml; else use_gh_vars; fi`) so we can roll forward in steps. Remove the fallback once `prod` is migrated.
- **Local dev unchanged**: `make dev` continues to use `.env.local` (gitignored). The new YAMLs are for CI deploys; the local prod-mirror doesn't read them.
- **Cost**: SSM Parameter Store standard tier is free up to 10,000 params + free `GetParameter` calls below 40 TPS. Our footprint is tens of params and dozens of reads/month. Net cost: $0/mo.

#### Verification

- `gh variable list --env test` returns no migrated entries (only secrets remain, if any).
- `aws ssm get-parameter --name /numun/test/sentry/dsn --with-decryption` returns the configured DSN.
- A clean PR-and-deploy cycle from a freshly-cloned repo (no GitHub env vars touched) produces a working `test` deploy of all four stacks.
- Sentry events flow from `test` after the migration (smoke per `operational-launch-checklist.md` §5).

**Verification:** `scripts/verify-deploy.sh` (M2.7) round-trip succeeds against the migrated `test` env with zero GitHub-variable reads.

---

## Inter-milestone dependencies

```
M0 ── M1 ── M2 ── M2.7 ── M3 ── M4 ── M6 ── M7 ── M10 ── M11 ── M12
            │             │     │     │     │
            │             └──── M5    │     │
            │                   │     │     │
            │                   │     │     M8
            │                   │     │
            │                   └─── M9 (triggered by M3/M8/M7 events; templates can land later)
            │
            (M2 has internal M2.5 SES-Cognito wiring step that depends on M1's
             SES domain identity verification done as an out-of-SAM manual step.
             M2.7 reshapes the prod AWS account into a `test` staging environment
             and parameterizes the IaC so a future real-prod stack can come up
             in a new account.)
```

Critical chains:
- **M2 blocks M2.7, M3, M4** (auth needed for both).
- **M2.7 blocks M3** only in the sense that M3's new entities should land on the renamed table — it isn't a hard sequencing requirement on the code, but is on the runtime resource shape.
- **M3 blocks M4, M5, M6, M7, M8, M9** (entities + repos + scope helpers).
- **M5 needs the Lambdalith and PublicService deployed** (M3 slice), but is otherwise independent of M4 — can run in parallel.
- **M6 needs DelegateService (M3) + S3 uploads + safe-http-client.**
- **M7 needs committees + positions defined (M7 itself) and delegates populated (M6) to be runnable on real data, but the algorithm and protos can be built against synthetic data first.**
- **M9 needs M3 (User entity) and ideally M7/M8 audit-event writers in place to have real triggers, but the worker + feedback + template infrastructure can be built earlier.**
- **M1 SES domain identity** must be completed before M2's tail (M2.5 Cognito-SES wiring), or else prod is stuck on Cognito's 50/day default sender.

Items that look like dependencies but aren't:
- M5 (CMS) and M4 (portal) are independent — they share only the Lambdalith host.
- M8 (payments) does not block M7 (algorithm).

---

## Within-milestone sequencing notes

Where the order matters:

- **Protos before code, code before tests.** Each milestone that introduces new RPCs: edit `.proto`, run `make proto`, commit generated code, then write the handler + test against the generated server stub. This catches breaking-change errors at the lint gate.
- **Repos before handlers.** Repositories with optimistic-lock + soft-delete plumbing land first; handlers wire them. The lint rule that flags handlers reading repos without a preceding scope check requires the scope helpers to exist before any handler is merged.
- **Audit-event writers as part of the same change.** Don't defer audit. Every mutating handler ships with its audit-event writer.
- **Manual AWS console / DNS / OAuth-app steps are runbook-first.** Write the runbook, then execute it, then commit the runbook with the verified-correct values inlined.
- **Cognito user pool changes are write-once-careful.** Custom attributes and password policy on a user pool are not freely editable after users exist. Get them right in M1.
- **SAM template edits are deploy-and-verify.** A bad SAM template can leave a stack in `UPDATE_ROLLBACK_FAILED`. Prefer small SAM PRs and check the CloudFormation console after each deploy.

---

## Forthcoming files to author

Per the design corpus, these documents are referenced but not yet written. The plan creates each as a one-paragraph **stub in M0** and **fills out per-milestone** when the corresponding feature lands:

| File | Filled in milestone |
|---|---|
| `/docs/runbooks/account-takeover.md` | M12 (auth context exists from M2; full content M12) |
| `/docs/runbooks/data-breach-suspected.md` | M12 |
| `/docs/runbooks/site-defacement.md` | M12 (depends on M5) |
| `/docs/runbooks/email-reputation-collapse.md` | M9 |
| `/docs/runbooks/ddos-or-api-abuse.md` | M12 |
| `/docs/runbooks/sign-up-abuse.md` | M12 |
| `/docs/runbooks/breakglass-access.md` | M1 (created when the IAM user exists) |
| `/docs/runbooks/mfa-enrollment.md` | M2 (Cognito pool live) |
| `/docs/runbooks/first-admin-bootstrap.md` | M2 (executed once for real) |
| `/docs/runbooks/dns-cutover.md` | M1 |
| `/docs/runbooks/ses-domain-setup.md` | M1 |
| `/docs/cms-editor-onboarding.md` | M5 |
| `/docs/cms-oauth-app-setup.md` | M5 |
| `/docs/seed-users.md` | M2 (advisor seeded earlier as part of M0 dev-loop; full content M2) |
| Privacy notice (CMS content) | M11 or earlier (required before public launch) |

---

## Risky / first-time-tricky items

Each of these is a known landmine; build a tiny end-to-end proof for it before relying on it in a real milestone.

1. **Cognito-via-SES wiring (M2.5).** The Cognito email config's `SourceArn` must be the **exact** ARN of the verified SES identity, not the friendly name. Misconfig silently disables outbound auth mail. Validate by triggering a real signup verification email after wiring.
2. **Decap OAuth `postMessage` handshake (M5).** Decap (at `cms.numun.org`) waits for a `postMessage` of the literal string `authorization:github:success:{"token":"..."}` from the popup. Wrong origin, wrong format, or stringly-typed JSON inside breaks the dance silently. Test by opening the popup with browser devtools attached.
3. **Connect + Lambda response shape (M0/M1).** `aws-lambda-go-api-proxy/httpadapter` must correctly stream the Connect responses — Connect uses chunked Transfer-Encoding for streaming RPCs in some modes. v1 has no streaming RPCs, but verify a non-streaming Connect call works end-to-end through API Gateway HTTP API → Lambda → adapter at the start of M1.
4. **`docker-compose` parity with SAM Local (M0).** SAM Local launches handlers inside Docker; from inside those containers, `localhost:8000` is not the host's `dynamodb-local`. Use `host.docker.internal` or a Docker network alias. This trips first-time SAM users every time.
5. **CloudFront OAC vs. OAI (M1).** OAC is the current recommended pattern, but bucket policies + OAC permissions must be set together; an OAI lying around from copy-pasted templates causes 403s. Build the bucket + distribution + OAC in one SAM stack.
6. **GitHub OIDC trust policy (M1).** The IAM role's trust policy must constrain `token.actions.githubusercontent.com:sub` to the specific `repo:org/repo:ref:refs/heads/main` so a fork's workflow can't assume the role.
7. **ACM cross-region cert reference (M1).** SAM templates in `us-east-2` must reference the ACM cert ARN in `us-east-1` (CloudFront requirement) as a string parameter, not a `Ref`.
8. **SES sandbox-exit lag (M1→M2).** Approval can take a day or two and AWS sometimes asks follow-up questions. Submit at M1 to avoid blocking M2.
9. **DMARC tightening discipline (M1→M12).** Starting at `pct=10` is intentional; remember to actually step it up. Calendar reminders, not informal intent.
10. **DDB optimistic-lock 409 ergonomics (M3).** Returning `aborted` (HTTP 409) is correct, but the portal must distinguish version-conflict from CSRF-mismatch (both share 409 territory) and prompt re-fetch rather than retry blindly.
11. **`numun-org-uploads` presign + CORS (M6).** The bucket's CORS config must allow `PUT` from `https://portal.numun.org`. Easy to forget; test by hand.
12. **Algorithm 20s soft cap inside a 29s API Gateway ceiling (M7).** If the algorithm ever exceeds, the request returns a misleading API Gateway 504 with no diagnostic from Lambda. The 20s soft cap + Phase C 15s budget exist to guarantee headroom; respect them.
13. **Branch protection exception for `/content/**` (M5).** GitHub branch protection rules don't natively support path-based exceptions; the workaround is a "Restrict who can push to matching branches" allowlist with the Decap GitHub user explicitly listed, plus broad-PR-required for everything else enforced via CODEOWNERS / required reviews.
14. **Session refresh-token encryption at rest (M2).** DDB encrypts the table by default; the open item is whether to additionally application-layer-encrypt the token. M2 ships with DDB-default; SECURITY.md re-evaluates in M12.

---

## Ambiguities and decisions still needed

These are items the design docs leave open, marked TBD, or that downstream choices depend on. Each should be resolved before the milestone that needs it; calling them out here so they don't ambush an unsuspecting later milestone.

1. **Northwestern brand tokens (M5).** APPLICATION.md §2 commits to "extracting brand tokens into `tailwind.config.js`" but doesn't list the concrete values (exact purple hex(es), approved typeface families, type scale, spacing). Resolve before M5 ships the Astro shell. Source: https://www.northwestern.edu/brand/visual-identity/.
2. **`/docs/seed-users.md` exact roster (M0/M2).** DEVELOPERS.md references it but doesn't enumerate the users. Need three real seed users (advisor, staff-staffer with at least one delegation assignment and one committee assignment, staff-admin), their seed delegation, their pre-populated session cookie values for `DEV_BYPASS_AUTH=true`.
3. **`.env.local` schema (M0).** DEVELOPERS.md doesn't enumerate the local env vars. Minimum needed: `DEV_MODE`, `DEV_BYPASS_AUTH`, `AWS_REGION` (locally `us-east-2`), `AWS_ENDPOINT_URL_DYNAMODB`, `AWS_ENDPOINT_URL_S3`, `AWS_ENDPOINT_URL_SES`, `COGNITO_USER_POOL_ID` (unused locally), `SES_FROM_*` addresses, MailHog SMTP config. Author this with M0.
4. **`DEV_BYPASS_AUTH` middleware shape (M2).** The doc commits to the env var + `X-Dev-User-Id` header but doesn't specify whether scope helpers are bypassed too, whether CSRF is bypassed, or how the synthetic Session looks. Default proposal: scope helpers and audit logging still run; CSRF is skipped (no real cookie); a synthetic `Session` row is loaded for the named user. Confirm before M2.
5. **`ExportService` Connect RPC vs. only `GET /v1/exports/*.csv` (M8, M11).** API.md §16 explicitly flags this as undecided. Cheapest path: skip the RPC shim, keep only the HTTP routes. Decide before M8.
6. **XLSX library final choice (M6).** Design says `xuri/excelize/v2` is the candidate. Confirm + pin a specific version.
7. **`UpdatePayment` semantics (M8).** API.md lists it but DATA_MODEL.md treats `PaymentRecord` as append-only. The likely intent: edit only `notes` and `reference`; never `amount`, `kind`, `method`. Codify in the handler.
8. **First-admin bootstrap mechanic (M2).** SECURITY.md §7.1 lists the runbook but doesn't fix the procedure. Options: (a) `aws cognito-idp admin-create-user` via the break-glass IAM user; (b) a one-shot `bootstrap-admin` CLI tool in `/api/cmd/`. Recommend (a).
9. **`password_reset_completed` audit-event source (M2).** AUTH.md §13.2 leaves this open: Cognito doesn't emit a native trigger. Options: portal calls an `AuditService.RecordEvent` RPC after success, OR a Cognito post-authentication trigger detects the flow. Recommend the portal RPC (simpler).
10. **Application-layer encryption of Session refresh tokens (M2/M12).** SECURITY.md §10 leaves open. v1 lands without it; M12 confirms decision.
11. **License selection (any time before first external PR).** README.md flags this. Doesn't block any milestone.
12. **CSV-export idempotency-key TTL of 24h (M3).** API.md flags as a guess. Acceptable v1; tune later.
13. **Privacy notice content (before launch).** SECURITY.md §10. Required before opening signups beyond seed users.
14. **`security@numun.org` mailbox (any).** SECURITY.md §7.3 flags it. Operational, not a code decision.
15. **Branch-protection-vs-Decap reconciliation specifics (M5).** Path-based exception requires the workaround in §13 of risks above; confirm exact GitHub config before relying on it.
16. **Astro image optimization for CMS uploads (M11).** M5 ships plain `<img>` tags for CMS-hosted images. Upgrade path documented at https://docs.astro.build/en/guides/images/#using-images-from-a-cms-or-cdn: add `image.remotePatterns` (or `image.domains`) to `site/astro.config.mjs` and switch to `<Image>` for the perf win (WebP/AVIF, responsive `srcset`). Decide where binaries live for build-time access (symlink `site/public/uploads` → `../../content/uploads`, dedicated `aws s3 sync` step, or move to `assets.numun.org`). Revisit when galleries become content-heavy or Lighthouse flags it.

---

## Verification strategy

Per milestone, verification is the combination of:
1. **CI green** — lint, typecheck, unit tests, `buf lint`, `buf breaking`.
2. **Integration tests against LocalStack + DDB Local** — `make integration-tests`. Nice-to-have until M3, then mandatory for each repository.
3. **Hand-run of the milestone's primary user flow** through the portal in both `make dev` (local) and prod.
4. **Audit log spot-check** — every mutating action of the milestone produces the expected `AuthAuditEvent` row.
5. **Negative tests** — wrong scope returns `not_found`; missing CSRF returns `permission_denied`; expired Session returns `unauthenticated`. These three are the most-likely-broken paths; rerun them every milestone.

A milestone is **done** when steps 1–5 pass and any runbooks owned by the milestone are filled in (not stubs).
