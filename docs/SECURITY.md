# SECURITY.md

This document is the **integrative security posture** for the NUMUN portal and landing page. It draws together the controls already specified in the per-feature docs, names the threats we're explicitly defending against (and the ones we aren't), and adds the bucket-policy / IAM / secrets / abuse / incident-response details that don't fit cleanly elsewhere.

It builds on every prior document. Where a control is fully specified elsewhere, this doc summarizes and links; where it's not, this doc is the source of truth.

References: [PROJECT.md](./PROJECT.md), [INFRASTRUCTURE.md](./INFRASTRUCTURE.md), [APPLICATION.md](./APPLICATION.md), [API.md](./API.md), [AUTH.md](./AUTH.md), [DATA_MODEL.md](./DATA_MODEL.md), [BULK_IMPORT.md](./subsystems/BULK_IMPORT.md), [EMAIL.md](./subsystems/EMAIL.md), [CMS_CONTENT_MODEL.md](./subsystems/CMS_CONTENT_MODEL.md), [ASSIGNMENT_ALGORITHM.md](./subsystems/ASSIGNMENT_ALGORITHM.md).

---

## 1. Scope & threat model

### 1.1 Threat actors we design against

| Actor | In scope? | Proportional mitigation |
|---|---|---|
| External anonymous attackers (scanners, opportunistic abuse) | yes | Infrastructure hardening, rate limits, no anonymous read paths |
| Hostile authenticated advisors (IDOR, exfiltration, disruption) | yes | Per-handler scope helpers (AUTH.md §7), default `not_found` on scope failure |
| Hostile `staff-staffer` exceeding scope (cases a / c) | yes | Same scope helpers; staffer scope is a *positive* allowlist via link entities |
| Compromised CMS editor accounts | yes | GitHub OAuth + audit via `git log`; revert is one command |
| Compromised `staff-admin` accounts | yes | MFA opt-in (§5.3); session-revocation surface (future); `AuthAuditEvent` enables forensic timelines |
| Compromised developer accounts | yes | Branch protection on `main` for code paths; required PR review; OIDC-federated CI |
| Insider abuse (current authorized user) | partial | Audit logs, soft-deletes, least-privilege IAM; no infrastructure prevents a high-privilege insider from misusing their access |
| Targeted nation-state attackers | **no** | Out of scope for a university club portal |

### 1.2 Asset sensitivity ranking

| Tier | Assets |
|---|---|
| **Highest** | Cognito credentials, session refresh tokens, GitHub repo write access, AWS account credentials |
| **High** | Delegate PII (names, emails), advisor PII (name, email, phone), payment ledger entries, audit logs |
| **Medium** | Delegation lists, committee/position assignments, delegation school/address, announcement bodies |
| **Low** | Landing-page CMS content, background-guide PDFs (intentionally public per PROJECT.md) |

Mitigations scale with tier. The highest tier is never logged, never shipped to the browser, and never crosses an unauthenticated boundary.

### 1.3 Out-of-scope guarantees

- **Provable confidentiality at rest beyond AWS defaults.** DDB and S3 are encrypted with AWS-managed KMS keys. No application-layer envelope encryption of refresh tokens in v1 — flagged as open item.
- **Provable compliance certification** (FERPA / GDPR / COPPA / SOC 2). Best-effort practices in §7.5 instead.
- **Full backup / disaster recovery for human-composed content beyond what's in Git or DDB PITR.**
- **Resistance against targeted persistent attackers** who specifically target NUMUN.

---

## 2. Application-layer threats (OWASP-flavored)

### 2.1 Broken access control / IDOR

Covered authoritatively in AUTH.md §7. Every handler that takes an entity-id parameter calls a mandatory `mustHaveScopeOn*` helper before touching the entity. Default failure mode is `not_found` (anti-enumeration); `permission_denied` only when the caller can prove they know the entity exists.

**Enforcement beyond review:** a CI check (grep-based in v1; promotable to a `golangci-lint` custom analyzer later) flags any handler in `/api/internal/handlers/` that:

1. Accepts a request struct containing a field ending in `Id`, **and**
2. Reads/writes the corresponding repository, **but**
3. Has no preceding call to a `mustHaveScopeOn*` helper.

Violations block CI. Tests cover both positive cases (correct scope → success) and negative cases (wrong scope → `not_found`).

### 2.2 XSS

- **Portal:** SolidJS renders user-supplied strings as text by default. No `innerHTML` on any user-controlled value. ESLint rule prohibits `innerHTML` assignment in `/portal/src/`.
- **Announcements:** the announcement composer accepts **plaintext only** in v1 (no HTML widget, no Markdown rendering). The renderer escapes the body and applies CSS-level whitespace handling (`white-space: pre-wrap`).
- **CMS Markdown:** the landing page renders Markdown via Astro's default Remark/Rehype pipeline with **`rehype-sanitize`** enabled. Inline raw HTML in Markdown is stripped. Editors who need richer layout work with structured fields (CMS_CONTENT_MODEL.md §4), not raw HTML.
- **Email templates:** Go's `html/template` escapes by default in HTML contexts. Plaintext templates render via `text/template`. Variables that contain user-supplied data (e.g., `delegationName`) pass through both packages' contextual escaping.

### 2.3 SQL / NoSQL injection

- DynamoDB is an API-driven store. There is no query string built from user input; all `FilterExpression` and `KeyConditionExpression` values use **parameterized placeholders** (`:val0`, `:val1`).
- Any future search feature must build filter values via allow-listed enums or fully-quoted/escaped identifiers — never string-interpolated user input.

### 2.4 CSRF

Covered in AUTH.md §9 (SameSite=Strict + double-submit token). Exemptions:

- `HealthService.Check` — read-only, no auth, no state.
- `AuthService.Exchange` — *is* the session establishment; CSRF concept doesn't apply yet.
- `PublicService.*` — read-only, no auth, CORS-open.

Every other RPC requires both the session cookie **and** the matching `X-CSRF-Token` header for mutating verbs.

### 2.5 Mass-assignment

Protobuf messages can carry fields the caller shouldn't be able to set. **Handlers explicitly enumerate allowed-update fields per role.** Pattern:

```go
// pseudocode
func (h *DelegationHandler) Update(ctx, req) (*UpdateDelegationResponse, error) {
    if err := mustHaveScopeOnDelegation(ctx, req.DelegationId); err != nil { return nil, err }

    allowed := map[string]bool{}
    switch role {
    case "advisor":       allowed = advisorEditableFields
    case "staff-admin":   allowed = staffAdminEditableFields
    case "staff-staffer": allowed = nil  // staffers cannot Update via this RPC
    }
    patch := pickAllowed(req.Patch, allowed)
    ...
}
```

`Approve` / `Reject` are separate RPCs, not field updates. `status` is **never** writable through the generic `Update` path.

### 2.6 Open redirect

The only request handler that accepts a redirect URL is the CMS OAuth callback (CMS_CONTENT_MODEL.md §8.3). It:

- Validates the redirect target against an exact allowlist: `https://cms.numun.org` only.
- Validates `state` against the HMAC signature stored in the short-lived `cms-oauth-state` cookie.

The portal sign-in flow has no `redirect_url` parameter — after sign-in the SPA navigates to a hardcoded route.

### 2.7 Server-side request forgery (SSRF)

The only outbound HTTP call to a user-supplied URL is **Google Sheets fetch** in BULK_IMPORT.md §7.2. The fetcher:

- Allowlists `docs.google.com` and `*.googleusercontent.com` only.
- Resolves the hostname server-side, refuses if it resolves to any private IPv4 (`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `169.254.0.0/16`, `127.0.0.0/8`, `0.0.0.0/8`) or any private IPv6 (`::1`, `fc00::/7`, `fe80::/10`).
- Sets a hard 5 s connect timeout and 10 s overall timeout.
- Refuses redirects across origins.

These rules live in `/api/internal/httpclient/safe.go` as a hardened wrapper. No raw `net/http.Client` calls are permitted in handler code (lint-enforced).

### 2.8 Path traversal

Server-controlled S3 keys (BULK_IMPORT.md §7.1) eliminate the direct traversal vector. As a belt-and-suspenders check, the presign handler asserts the generated key does not contain `..` before returning the presigned URL.

### 2.9 Insecure deserialization

JSON and Protobuf binary deserialization are handled by the Connect framework — no `eval`-style danger. CSV parsing uses Go's standard `encoding/csv`. XLSX parsing uses `github.com/xuri/excelize/v2` (or equivalent), pinned to a vetted version. Dependency updates flow through `pnpm audit` (for JS) and `govulncheck` (for Go) in CI.

### 2.10 Rate limiting & DoS

| Layer | Limit | Mechanism |
|---|---|---|
| Cognito sign-in | 5 failed attempts/min/user, plus per-IP global limits | Built-in |
| Per-IP (all API requests) | 60 req/min | Lambda middleware + DDB token-bucket on `RATELIMIT#ip#<ip>` |
| Per-user (all API requests) | 300 req/min | Lambda middleware + DDB token-bucket on `RATELIMIT#user#<userId>` |
| `PublicService.*` | 60 req/min per IP | Same mechanism, distinct key namespace |
| Bulk import | 10 previews + 10 commits per hour per advisor (BULK_IMPORT.md §8.2) | Same mechanism |

Excess returns Connect `resource_exhausted` (HTTP 429). Token-bucket implementation is a counter with TTL; refresh windows in 60 s buckets — simpler than sliding window and sufficient at this scale.

AWS WAF on CloudFront ($5/mo base) is **not enabled in v1** because the Lambda-layer limits suffice for current threat volume and budget. WAF is the natural escalation if real attack traffic appears.

### 2.11 Malicious file upload

- **XLSX with macros:** the parser rejects files whose ZIP archive contains `xl/vbaProject.bin` before any further processing.
- **PDF with JS / external references:** **not scanned in v1.** Background guides are CMS-curated by trusted editors; the risk is bounded. PDFs are served via CloudFront with `Content-Disposition: attachment` so they don't render in-browser by default. Document this risk in the open items.
- **Other file types:** uploads land only in the bulk-import S3 path or as CMS Decap commits. Each path validates content type and extension at parse time.

### 2.12 Dependency security

- **Go:** `govulncheck` runs in CI on every PR. Major dependencies are pinned in `go.mod` with checksums in `go.sum`. Renovate or Dependabot opens PRs for security updates.
- **JS / TS:** `pnpm audit` in CI. Dependabot enabled on the GitHub repo for both ecosystems.
- **Tooling and base images:** none of our infra uses container images; Lambda's managed Go runtime is patched by AWS.

---

## 3. Infrastructure-layer controls

### 3.1 S3 buckets

| Bucket | Visibility | Access mechanism |
|---|---|---|
| `numun-org-site` | Private | CloudFront OAC only |
| `numun-org-portal` | Private | CloudFront OAC only |
| `numun-org-cms` | Private | CloudFront OAC only |
| `numun-org-assets` | Private | CloudFront OAC only (BG guides + CMS images served via `assets.numun.org`) |
| `numun-org-uploads` | Private | Presigned PUT (writes) and presigned GET (reads); no public path |
| `numun-org-artifacts` | Private | Presigned GET |

**Account-level Block Public Access:** all four flags enabled (`BlockPublicAcls`, `IgnorePublicAcls`, `BlockPublicPolicy`, `RestrictPublicBuckets`). No per-bucket exception in v1.

**CloudFront Origin Access Control (OAC)** signs every CloudFront-to-S3 request with SigV4; the bucket policy allows only the specific CloudFront distribution's principal. No `s3:GetObject` to `Principal: *`.

**S3 versioning:**
- `numun-org-site` — **enabled**, 90-day retention. Lets us roll back a bad deploy.
- `numun-org-cms` — **enabled**, 90-day retention. Same.
- All other buckets — **disabled**.

**S3 lifecycle:**
- `numun-org-uploads` — 30-day expiration per INFRASTRUCTURE.md §3.6.
- Versioned buckets — non-current versions expire at 90 days.

### 3.2 IAM principle of least privilege

**Per-Lambda roles.** Each Lambda function has its own IAM role scoped to the actions it needs. Roles defined in SAM templates; no shared "Lambda role for everything."

| Lambda | Allowed actions |
|---|---|
| `api` (the Lambdalith) | DDB R/W on `numun-prod` table prefixes; SES `SendEmail` from the three sender identities; SSM `GetParameter` on `/numun/prod/*` (covers portal/API secrets **and** the GitHub CMS-OAuth client id + secret used by the `/cms-oauth/*` routes per CMS_CONTENT_MODEL.md §8.3); S3 R/W on `numun-org-uploads/<...>` and `numun-org-artifacts/<...>`; Cognito admin actions on the user pool; SNS `Publish` to the email-feedback topic (for testing only) |
| `email-worker` | DDB R/W on email-related entities; SES `SendEmail`; SSM `GetParameter`; S3 read on `numun-org-uploads/<...>` (none in v1 but reserved for attachments later) |
| `email-feedback` | DDB R/W on User + EmailEvent partitions only; SNS `Subscribe` to the feedback topic |
| `cognito-post-confirmation` | DDB `PutItem` on User partition only |

**GitHub Actions OIDC roles.** Per APPLICATION.md §8. One role per workflow, scoped to a specific S3 bucket prefix or CloudFormation stack:

| Workflow | Allowed actions |
|---|---|
| `site-deploy` | `aws s3 sync` on `numun-org-site/*`; CloudFront `CreateInvalidation` on the site distribution; `dynamodb:GetItem` on `Conference` partition (for `GetActiveConference` build-time call equivalent — see CMS_CONTENT_MODEL.md §9.1; uses the public RPC, so this scope is actually unneeded — left here for completeness) |
| `portal-deploy` | `aws s3 sync` on `numun-org-portal/*`; CloudFront `CreateInvalidation` on the portal distribution |
| `api-deploy` | CloudFormation `Create/Update/Delete` on the API stack; Lambda `Update*` on the API functions; IAM `PassRole` on the Lambda roles |
| `cms-deploy` | `aws s3 sync` on `numun-org-cms/*`; CloudFront `CreateInvalidation` on the CMS distribution |

None of the deploy roles can read PII tables. None have wildcards on resources or actions beyond what's listed.

**Break-glass admin.** One IAM user (`numun-break-glass`) with full administrator privileges for emergencies. MFA enforced. No long-lived access keys; access is via console + temporary STS credentials only. Documented in `/docs/runbooks/breakglass-access.md`.

### 3.3 Secrets

| Where | Stored as |
|---|---|
| Application secrets (JWT signing key if used, Sentry DSN, GitHub OAuth client secret + ID, etc.) | SSM Parameter Store SecureString under `/numun/prod/...` |
| Cognito refresh tokens (in DDB Session rows) | DDB-default at-rest encryption (AWS-managed KMS) |
| Long-lived AWS credentials in CI | **None.** OIDC federation only. |
| GitHub Actions `secrets.*` | None except GitHub's own `GITHUB_TOKEN` |
| Local dev | `.env.local` files (in `.gitignore`); never committed |

**No rotation in v1.** Static secrets stay static until manual rotation. AWS Secrets Manager ($0.40/secret/mo) considered overkill at our scale and outside the budget; revisit if compliance requires automatic rotation.

### 3.4 TLS

- All public-facing endpoints (CloudFront distributions, API Gateway custom domain) use ACM certs in `us-east-1` with **`TLSv1.2_2021`** security policy (TLS 1.2 minimum, modern cipher suites).
- HTTP requests are redirected to HTTPS at CloudFront / API Gateway.
- **HSTS:** set on the landing page and portal via CloudFront response-headers policy:
  ```
  Strict-Transport-Security: max-age=31536000; includeSubDomains; preload
  ```
  Submitted to the HSTS preload list once the production deployment is stable for 30 days.

### 3.5 CORS

Per API.md §9 and CMS_CONTENT_MODEL.md:

- `api.numun.org` allows `Origin: https://portal.numun.org` only.
- `api.numun.org/numun.v1.PublicService/*` and `api.numun.org/cms-oauth/*` allow `*` (or a CMS-specific origin for the OAuth proxy).
- S3 / CloudFront for static assets do not require CORS; same-origin reads.

CORS preflight responses are cached for 600 s to avoid extra round-trips on busy pages.

### 3.6 Network controls

- No VPC; everything runs in Lambda's default network. Egress is via public AWS endpoints (DDB, SES, etc.). At NUMUN's cost ceiling, a VPC + NAT Gateway ($32/mo) is unjustifiable.
- Outbound network from Lambda is unrestricted by default; the SSRF allowlist in §2.7 is the application-layer control.

### 3.7 Logging and monitoring

| Surface | Sink | Retention |
|---|---|---|
| Lambda execution logs | CloudWatch Logs | 30 days |
| API Gateway access logs | CloudWatch Logs | 30 days, plus **S3 archive for 1 year** (forensics) |
| `AuthAuditEvent` (DDB) | DDB | 1 year (TTL) |
| `EmailEvent` (DDB) | DDB | 1 year (TTL) |
| Sentry events | Sentry | Sentry default retention |
| SES events (bounce/complaint/delivery) | SNS → DDB `EmailEvent` rows | 1 year (TTL) |
| CloudFront access logs | **Not enabled in v1** | n/a (revisit if abuse appears) |

**Alarms** (CloudWatch):
- Billing alarm at $10/mo.
- Lambda error-rate spike on `api`, `email-worker`, `email-feedback`.
- DLQ depth > 0 on `numun-prod-email-send-dlq`.
- DDB throttling on the main table.
- Failed `AssignmentRun` (status flips to `failed`).
- SNS topic for SES feedback receives a complaint.

All alarms publish to an SNS topic that emails NUMUN staff (currently a single admin distribution list).

**AWS GuardDuty:** **not enabled in v1.** ~$3–5/mo at our usage profile; revisit if the budget allows. Documented as a recommended add.

---

## 4. Identity threats

### 4.1 Account takeover (password compromise)

Mitigations from AUTH.md §6, §11:

- Password policy: 12-char minimum, NIST-style (length over composition).
- Cognito rate-limits (5 failed sign-ins per minute per user).
- `AuthAuditEvent` of kind `sign_in_failed` records the email (not the password) on each failure.

**Post-incident.** A `staff-admin` who suspects a user account is compromised can:

1. Force a password reset via Cognito `AdminResetUserPassword`.
2. Revoke all refresh tokens via Cognito `AdminUserGlobalSignOut`.
3. Manually delete the user's Session rows in DDB.

This is documented in `/docs/runbooks/account-takeover.md`.

### 4.2 Multi-factor authentication

**Available as opt-in TOTP in v1, no UI built.** Cognito supports TOTP MFA at the user-pool level (enabled in the SAM template). The enrollment screen in the portal is **deferred to v1.1**. Users wanting MFA today can self-enroll via the AWS CLI (documented in `/docs/runbooks/mfa-enrollment.md` — staff-only).

`staff-admin` accounts **should** enable MFA. A future PR will enforce MFA for the admin role via a Cognito group / IAM policy.

### 4.3 Session hijacking

Mitigations from AUTH.md §5:

- Opaque session id (not a JWT) in the cookie.
- HttpOnly + Secure + SameSite=Strict + scoped to `.numun.org`.
- Refresh and access tokens stored server-side, never sent to the browser.
- 1-hour access-token TTL bounds the window of a stolen access token.
- 30-day session TTL forces re-authentication eventually.

**No IP- or UA-binding** in v1. Mobile users change IPs constantly; binding would cause excessive re-logins. The cookie attribute set is the primary defense.

### 4.4 Brute force / credential stuffing

Cognito's built-in per-user (5/min) and per-IP (account-wide) limits suffice for NUMUN's profile. **Cognito Advanced Security Features ($0.05/MAU)** is **not** enabled — re-evaluate if `sign_in_failed` audit events show stuffing patterns.

### 4.5 Email-bound flows (verification, reset codes)

- Cognito generates 6-digit codes (1M-possibility space).
- Codes expire in 1 hour (Cognito default).
- Code submission rate-limited by Cognito (10 attempts).
- Per AUTH.md §3.1, the sign-up "we sent you an email" message is uniform regardless of whether the email is new — anti-enumeration.

### 4.6 CMS OAuth flow

Per CMS_CONTENT_MODEL.md §8.3:

- HMAC-signed `state` parameter prevents OAuth-flow CSRF.
- Exact `redirect_uri` validated against the GitHub OAuth app config — GitHub itself rejects mismatches.
- The callback HTML targets `https://cms.numun.org` explicitly as the `postMessage` recipient; cross-origin script can't read the token.
- The GitHub OAuth client secret lives in SSM Parameter Store.

---

## 5. Data protection & privacy

### 5.1 PII inventory

| Field | Source | Sensitivity |
|---|---|---|
| Advisor name | DDB `User` | High |
| Advisor email | DDB `User` + Cognito | High |
| Advisor phone | DDB `User` | High |
| Delegate first/last name | DDB `Delegate` | High |
| Delegate email | DDB `Delegate` (optional) | High |
| Delegation school + address | DDB `Delegation` | Medium |
| IP + User-Agent | DDB `Session`, `AuthAuditEvent` | Medium |
| Cognito `sub` | DDB `User` | Low (opaque) |

**Explicitly not collected:** SSNs, government IDs, payment card data, health info, demographic data.

### 5.2 Retention

| Data | Retention |
|---|---|
| Conferences, Delegations, Delegates, Committees, Positions, Assignments, Awards, PaymentRecords | Indefinite (soft-delete preserves history; no scheduled hard delete in v1) |
| Sessions | 30 days (TTL) |
| AuthAuditEvent | 1 year (TTL) |
| EmailEvent | 1 year (TTL) |
| BulkImportPreview | 30 minutes (TTL) |
| BulkImportJob | 7 days (TTL) |
| NotificationDedupe | 15 minutes (TTL) |
| S3 advisor uploads (`numun-org-uploads`) | 30 days (lifecycle) |
| CloudWatch logs | 30 days |
| API Gateway access logs (S3 archive) | 1 year |
| Sentry events | Sentry default (90 days on free tier) |

### 5.3 Deletion / right-to-be-forgotten

Per AUTH.md §10.3:

1. `staff-admin` calls `AdminDeleteUser` on Cognito.
2. DDB `User` row is soft-deleted.
3. `DelegationAdvisor` link rows for the user are soft-deleted, with a precondition that no delegation is left advisor-less (the admin must reassign first).
4. `AuthAuditEvent` of kind `account_deleted` is written.

**What is NOT scrubbed in v1:**
- Historical audit events that reference the deleted user (`actorUserId`, `userId`).
- Historical `EmailEvent` rows showing past sends to the deleted user's email.
- `AssignmentRun.triggeredBy`, `Approve.approvedBy`, etc. — denormalized references to the userId.

This means NUMUN is **not GDPR right-to-be-forgotten compliant** in v1. Documented as open item §10.

### 5.4 Logging hygiene

- `slog` wrapper in `/api/internal/log/` redacts known-sensitive fields by name before emitting: `password`, `refresh_token`, `access_token`, `client_secret`, `csrf_token`, `Authorization` header.
- Request bodies are **not** logged in full. Logged metadata: RPC name, caller userId, request id, duration, response code.
- Sentry `BeforeSend` hook scrubs the same field set from event payloads and breadcrumbs.

### 5.5 Backups & disaster recovery

- **DDB:** Point-In-Time Recovery enabled (35 days). Per-table PITR cost ~$0.20/GB/mo, negligible at scale.
- **S3 versioned buckets:** site + cms, 90-day retention (§3.1).
- **Git repo:** GitHub holds the canonical history; local clones provide additional resilience.
- **Catastrophic AWS-account-loss recovery:** out of scope. The break-glass admin user is the only account-level recovery path beyond root credentials.

---

## 6. Abuse & operational threats

### 6.1 Spam sign-ups

- Cognito email verification (must own the inbox).
- Cognito rate limiting per IP.
- No CAPTCHA in v1.
- **Escalation path:** if real abuse appears, add **Cloudflare Turnstile** (free, privacy-respecting) on the sign-up form. Documented in `/docs/runbooks/sign-up-abuse.md`.

### 6.2 Stale accounts and delegations

- No automated cleanup in v1.
- `staff-admin` can soft-delete dormant accounts manually.
- After several years, a scheduled review process (informal, per turnover) is the cleanup mechanism.

### 6.3 Conference data tampering

`AuthAuditEvent` captures (per AUTH.md §11):

- Sign-in / sign-out.
- Role changes.
- Scope grants/revokes.
- Account deletion.
- Password reset.

Extended for SECURITY.md to also cover **high-value state transitions**:

- `delegation_approved` / `delegation_rejected` (actor = approving admin).
- `payment_recorded` (actor = staff who recorded; metadata includes `paymentId`, `amount`).
- `assignment_approved` / `assignment_unapproved` / `assignment_manually_edited`.
- `bulk_import_committed` (already in EMAIL.md/BULK_IMPORT.md).
- `award_created` / `award_modified` / `award_deleted`.
- `email_unsuppressed`.

These events are written by the corresponding handlers in addition to the existing data mutations. They share the `AuthAuditEvent` partition pattern (`USER#<actorUserId>`).

### 6.4 GitHub repo compromise

- **Branch protection on `main`** required: 1 approving review, status checks must pass, no force-push.
- **Path-based exception:** the protection rule allows direct commits to `/content/**` because Decap CMS requires direct-to-main commits (CMS_CONTENT_MODEL.md §3). This is documented as an accepted risk; the mitigation is the GitHub audit log (`git log`, GitHub Audit Log API) and the ease of `git revert`.
- **Collaborator membership** to the content repo is reviewed quarterly. Removed editors lose access immediately (GitHub also revokes their Decap session within minutes).
- **Required two-factor auth** for all repo collaborators (GitHub organization setting).

### 6.5 AWS account compromise

- **Root account:** hardware MFA. No access keys on the root user. Root account credentials sealed.
- **Console access:** federated SSO (future via Cognito or AWS IAM Identity Center). Until then, the break-glass IAM user with MFA enforced.
- **No long-lived access keys** in any IAM user or role. CI uses OIDC federation; humans use STS-temporary credentials via the console.
- **Billing alarm at $10/mo** (INFRASTRUCTURE.md §3.9) catches unexpected cost spikes that often indicate compromise.

---

## 7. Incident response

### 7.1 Required runbooks

Living under `/docs/runbooks/`. Each runbook follows a consistent template (detection signal, immediate containment, investigation, remediation, post-mortem).

| Runbook | Covers |
|---|---|
| `account-takeover.md` | Force password reset, revoke sessions, audit-log review |
| `data-breach-suspected.md` | Quarantine, forensic preservation, stakeholder notification |
| `site-defacement.md` | Revert CMS commit; emergency S3 re-sync from prior version |
| `email-reputation-collapse.md` | Pause announcements queue, investigate bounce/complaint trend, contact SES support |
| `ddos-or-api-abuse.md` | Tighten rate limits, optionally enable WAF, IP blocklist via CloudFront |
| `sign-up-abuse.md` | Enable Turnstile, deactivate spam accounts |
| `breakglass-access.md` | How to access the break-glass IAM user; when to do so |
| `mfa-enrollment.md` | (Staff-only) how to self-enroll TOTP via AWS CLI until UI lands in v1.1 |
| `first-admin-bootstrap.md` | One-time procedure to create the first `staff-admin` account in Cognito via the AWS CLI when no admin exists to invite them (referenced in PROCEDURES_ADMIN.md §1.1) |

These are **deliverables** for the v1 launch — not optional.

### 7.2 Detection signals

- CloudWatch alarms (§3.7).
- Sentry error spikes (cross-checked against deploys before assuming attack).
- DLQ depth > 0 on the email queue.
- `AuthAuditEvent` `sign_in_failed` rate spike on any single user.
- AWS billing alarm.
- Support inbox reports.

### 7.3 Communication

NUMUN does not have a dedicated security inbox in v1. Incidents are reported to the staff-admin distribution list. A dedicated `security@numun.org` mailbox is a recommended addition (open item).

---

## 8. Compliance posture

NUMUN is **not** currently certified under any compliance framework. The system handles data that may overlap with several regulatory regimes; we adopt reasonable practices but make no compliance claims in v1.

| Regime | Applicability | v1 posture |
|---|---|---|
| FERPA (US student records) | Delegates are students at high schools and universities | Minimal data collected; no SSN, grade, GPA. NUMUN is not a "school official" handling education records under FERPA's typical reading, but the line is fuzzy. Documented; not certified. |
| GDPR (EU residents) | International delegations may bring EU-resident delegates | Best-effort: data minimization, deletion process, audit logs. **Not** right-to-be-forgotten compliant (§5.3). Not certified. |
| COPPA (under 13) | MUN includes some middle-school programs in the broader ecosystem; NUMUN's hosted conference targets high school + college | Best-effort: no behavioral profiling, no advertising, no third-party trackers. Not certified. |
| PCI DSS | Payment card data | **Not applicable** — payments are handled off-platform (PROJECT.md). NUMUN never sees card data. |
| SOC 2 | Voluntary | Not pursued in v1. |

**Documented limitation:** NUMUN's privacy notice on the landing page must state that the system is not certified compliant with any of the above and describe the data collected, the retention period, and the deletion process.

---

## 9. Decisions log

| Decision | Choice | Reason |
|---|---|---|
| Threat actors in scope | (a) through (g); (h) nation-state explicitly out | Proportional to a university club portal |
| IDOR enforcement | Mandatory `mustHaveScopeOn*` helpers + CI grep check | Documented intent enforced by tooling |
| XSS posture | Plaintext-only announcements; `rehype-sanitize` on CMS Markdown; default-text-rendering in the SPA | Defense in depth across user-supplied text |
| CSRF | SameSite=Strict + double-submit token | Per AUTH.md §9 |
| Mass-assignment | Per-role allowlist of editable fields in handler code | Protobuf can't enforce role-based field filtering |
| SSRF | Allowlist `docs.google.com` + private-IP block on the only external-URL fetcher (Google Sheets) | Bounds the single SSRF vector |
| Rate limits | Lambda + DDB token-bucket; per-IP 60/min, per-user 300/min | Sufficient at scale; WAF deferred |
| WAF | Not enabled in v1 | Cost ceiling; reassess if abuse appears |
| GuardDuty | Not enabled in v1 | Cost; reassess later |
| S3 buckets | All private; CloudFront OAC for reads; presigned URLs for direct uploads/downloads | Standard pattern |
| S3 Block Public Access | All four flags enabled at account level | Anti-mistake floor |
| S3 versioning | On for `numun-org-site` + `numun-org-cms` (90 days); off elsewhere | Cheap deploy-rollback |
| IAM | Per-Lambda least-privilege roles; per-workflow OIDC roles; one break-glass admin | Minimize blast radius |
| Secrets | SSM Parameter Store; no long-lived AWS keys; OIDC for CI | Per INFRASTRUCTURE.md |
| TLS | TLS 1.2+ minimum (`TLSv1.2_2021`); HSTS with preload | Modern baseline |
| Logging hygiene | `slog` redaction wrapper; Sentry `BeforeSend` scrubber | Avoid leaking PII via logs |
| MFA | TOTP available (opt-in); UI deferred to v1.1; CLI enrollment runbook for staff | Free, Cognito-native |
| IP/UA-binding for sessions | Not enforced | Mobile network instability |
| Spam sign-ups | No CAPTCHA in v1; Turnstile as escalation | Friction is fine for v1 volumes |
| GitHub branch protection | Required PR + review on `main`, except `/content/**` (Decap requirement) | Accept the documented residual risk |
| Compliance | No certifications in v1; best-effort PII minimization; privacy notice required | Honest posture |
| Incident runbooks | Listed in §7.1, mandatory v1 deliverable | Operationalizes the response |

---

## 10. Open items

- **Application-layer encryption of refresh tokens** stored in DDB Session rows. DDB at-rest encryption suffices for the threat model; application-layer envelope encryption would help defend against an attacker who somehow gains DDB read access without breaking the encryption boundary. Re-evaluate if compliance requirements emerge.
- **GDPR right-to-be-forgotten** — currently we soft-delete the User row but leave references in audit logs. A "scrub-on-delete" job would replace `userId` references with a `deleted-user-<n>` placeholder. Not v1.
- **CMS branch protection** — see §6.4. A future improvement would be a GitHub Action that polls Decap-originated commits and posts them as PR-style notifications to staff for after-the-fact review.
- **MFA enrollment UI** in the portal. v1.1 likely.
- **MFA enforcement** for `staff-admin` accounts. v1.1.
- **AWS Identity Center / SSO for console access**, replacing the break-glass IAM user as the primary admin path. v1.1.
- **WAF on CloudFront** if abuse appears. ~$5/mo plus rule costs.
- **GuardDuty** for AWS-level threat detection. ~$3–5/mo.
- **CloudFront access logs** for forensic visibility on static assets and the portal SPA. Adds S3 storage cost; defer until needed.
- **`security@numun.org` mailbox** for incident reports.
- **Dedicated PR for sensitive areas:** consider a `CODEOWNERS` file that requires specific reviewers for `/api/internal/handlers/auth/`, `/api/internal/middleware/`, `/infra/`, and similar high-blast-radius paths.
- **Privacy notice authoring** (landing-page CMS content) — not designed yet; required before launch.
- **DMARC tightening** — per EMAIL.md §13. Step from `p=quarantine; pct=10` to `p=quarantine; pct=100` to `p=reject; pct=100` over weeks.
- **Penetration test** — not in v1's budget. Recommended after the first year of operation, before any partner integration or expanded user base.
- **Bug-bounty program** — out of scope for v1.
