# Sentry setup

One-time procedure to wire Sentry into the API + portal. Until the DSN is
stored in SSM (per §2 below), deploys succeed but Sentry init no-ops (both
`observability.InitFromEnv` and `initSentry()` return false when no DSN
resolves). Errors flow to CloudWatch Logs as usual; only the Sentry-
specific UX (grouped issues, deploy hashes, breadcrumbs) is absent.

## 1. Create the Sentry project

1. Sign up at https://sentry.io (free tier covers 5 k events / mo, well
   beyond our expected volume).
2. Create an organization (e.g., `numun`).
3. Create two projects:
   - `numun-api` — platform: Go.
   - `numun-portal` — platform: JavaScript → Browser.
4. For each project, copy the DSN from **Settings → Client Keys (DSN)**.
   They look like `https://<key>@oXXX.ingest.sentry.io/<projectId>`.

The DSN is technically a non-secret (it only authorizes ingestion, not read
access), but treat it as scoped config to avoid public abuse.

## 2. Populate the SSM parameter

The Sentry DSN lives in SSM SecureString at `/numun/${ENV}/sentry/dsn`
(M14). Both the API (Lambda cold-start) and the portal (build-time via
`aws ssm get-parameter`) read it from there.

For each environment (`test`, future `prod`):

```bash
ENV=test
DSN='https://<key>@oXXX.ingest.sentry.io/<projectId>'  # from §1
aws ssm put-parameter \
  --name "/numun/$ENV/sentry/dsn" \
  --type SecureString \
  --value "$DSN" \
  --overwrite \
  --region us-east-2
```

The portal deploy role has `ssm:GetParameter` on
`/numun/${Env}/sentry/*` (granted in `infra/bootstrap/oidc-roles.yaml`).
The API Lambda's wildcard read on `/numun/${Env}/*` already covers it.

**One DSN or two?** The recipe above uses a single `dsn` parameter for
both surfaces. If you want separate Sentry projects (one for API errors,
one for portal errors), create a second parameter at
`/numun/$ENV/sentry/dsn_portal` and adjust `portal.yml`'s "load Sentry
DSN from SSM" step to read it. Recommended only if you exceed ~50
errors/mo on either surface.

Rotation: `aws ssm put-parameter --overwrite` updates the value. The
next deploy picks it up (Lambda reads at cold-start; portal at build).
No code or IAM change.

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
