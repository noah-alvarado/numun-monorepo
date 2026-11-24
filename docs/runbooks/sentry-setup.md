# Sentry setup

One-time procedure to wire Sentry into the API + portal. Until the DSN is
stored in SSM (per §2 below), deploys succeed but Sentry init no-ops (both
`observability.InitFromEnv` and `initSentry()` return false when no DSN
resolves). Errors flow to CloudWatch Logs as usual; only the Sentry-
specific UX (grouped issues, deploy hashes, breadcrumbs) is absent.

## 1. Create the Sentry projects

Done for org `numun` (https://numun.sentry.io). Test and prod share both
projects; events are separated by the `environment` tag.

| Project          | Platform   | Used by                   | SSM parameter                     |
| ---------------- | ---------- | ------------------------- | --------------------------------- |
| `numun-backend`  | go         | API + all Lambdas         | `/numun/${ENV}/sentry/dsn`        |
| `numun-frontend` | javascript | portal (inlined at build) | `/numun/${ENV}/sentry/dsn_portal` |

To recreate: sign up at https://sentry.io (free tier: 5k errors/mo), create
org `numun`, then with the Sentry CLI (`sentry auth login`):

```bash
sentry project create numun/numun-backend:go
sentry project create numun/numun-frontend:javascript
```

The DSN is technically a non-secret (it only authorizes ingestion, not read
access), but treat it as scoped config to avoid public abuse.

## 2. Populate the SSM parameters

The API reads `/numun/${ENV}/sentry/dsn` at Lambda cold start; the portal
reads `/numun/${ENV}/sentry/dsn_portal` at build time. For each env
(`test`, future `prod`), copy each project's active DSN without printing it:

```bash
ENV=test
for pair in numun-backend:dsn numun-frontend:dsn_portal; do
  DSN=$(sentry dsn list "numun/${pair%%:*}" --json | jq -r '[.data[] | select(.isActive)][0].dsn')
  aws ssm put-parameter --region us-east-2 --overwrite --type SecureString \
    --name "/numun/$ENV/sentry/${pair##*:}" --value "$DSN" --query Version
  unset DSN
done
```

The portal deploy role has `ssm:GetParameter` on
`/numun/${Env}/sentry/*` (granted in `infra/bootstrap/oidc-roles.yaml`).
The API Lambda's wildcard read on `/numun/${Env}/*` already covers it.

Rotation: `aws ssm put-parameter --overwrite` updates the value. The
next deploy picks it up (Lambda reads at cold-start; portal at build).
No code or IAM change.

**Portal tunnel.** Ad blockers and Firefox tracking protection block
`*.ingest.sentry.io`, so the portal sends envelopes to the API at
`POST /v1/diag` (`observability.TunnelPath`; named to dodge blocklists),
which relays them to Sentry. The route is public (no session) and
rate-limited at the anonymous 60/min/IP tier. It only forwards envelopes
whose header DSN matches `dsn_portal`'s host and project, always to the
upstream URL built from `dsn_portal`, through the SSRF-hardened
`httpclient` with that one host allowlisted; bodies over 512 KiB get 413. The API also reads `dsn_portal` at cold start, so rotating it needs
both a portal and an API deploy. Without `dsn_portal` the route isn't
mounted.

## 3. Verify

After the next deploy:

1. **API** — `curl https://api.<apex>/v1/health` (should still 200). To
   trigger a real error, run any RPC that intentionally fails (e.g., POST
   `/numun.v1.DelegationService/GetDelegation` with a malformed body). The
   error should appear in Sentry within a minute, tagged
   `component=api`, `environment=<env>`, with the deploy's commit SHA as
   the `release`.
2. **Portal** — open `https://portal.<apex>` with browser devtools. In the
   console, run `throw new Error("sentry-smoketest")`. Reload — the error
   should land in Sentry tagged `environment=<env>`.

If errors don't show up, check:

- The Lambda log for the request — `observability: init` lines indicate
  whether Sentry was active.
- The `dist/assets/index-*.js` bundle for the literal DSN string. If
  absent, Vite tree-shook Sentry out because the env var was empty at
  build time.

## 4. What the BeforeSend hook scrubs

Both runtimes share an allowlist with the slog redaction wrapper
(`api/internal/log.RedactedFields`):

`password`, `refresh_token`, `access_token`, `id_token`, `client_secret`,
`csrf_token`, `x-csrf-token`, `authorization`, `cookie`, `set-cookie`,
`session_cookie` (matched case-insensitively, with dash/underscore
collapsed).

If a new sensitive field is introduced, add it to **both**:

- `api/internal/log/redact.go` → `RedactedFields`
- `portal/src/lib/sentry.ts` → `REDACTED_KEYS`

Tests in `api/internal/log/redact_test.go` and
`api/internal/observability/sentry_test.go` guard against accidental
shrinkage of the set.

## 5. Cost notes

- Free tier: 5 k errors / mo, 50 k performance units. Tracing is disabled
  (`SENTRY_TRACES_SAMPLE_RATE=0`) — only errors are sent.
- If we exceed the free tier, the next step is Sentry's $26/mo Team plan
  (50 k events). At our expected scale that's unnecessary; the cost
  ceiling in SECURITY.md / INFRASTRUCTURE.md does not budget for it.
- Sentry's data is stored in their cloud; no PII should ever reach it
  thanks to the BeforeSend scrubber. If a Sentry event ever shows a
  suspected leak, follow `data-breach-suspected.md` per the standard
  procedure.
